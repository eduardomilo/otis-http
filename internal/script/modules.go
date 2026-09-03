package script

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/dop251/goja"
)

// importFn is the global the import transform compiles down to. It is
// deliberately unspeakable in source: a script that reaches for it directly
// gets the same "not available" treatment as `require`, because the module
// graph is Otis' to resolve (docs/FORMAT.md §9.8).
const importFn = "__otis_import"

// exportsVar is the object a module's exports are collected on.
const exportsVar = "__otis_exports"

// ModuleLoader resolves an import specifier to a module's source.
//
// An interface so this package does not read the filesystem itself: the
// sandbox rule is that nothing here touches disk, and a loader the sender
// supplies is what keeps that true. It is also what confines resolution to
// the collection (§9.8) — the loader refuses anything outside it, so no
// amount of "../" in a specifier can escape.
type ModuleLoader interface {
	// Resolve turns a specifier into a collection-relative path, refusing
	// anything that is not a .js file inside the collection. from is the
	// importing file's collection-relative path.
	Resolve(from, specifier string) (string, error)
	// Read returns the module's source. It is called once per module per
	// send: resolution is cheap and the cache is keyed on the resolved path,
	// so a module imported by three hooks is read once.
	Read(path string) (Source, error)
}

// importModule is the Go behind a transformed `import`.
//
// A module is evaluated once per send whatever imports it, and its exports are
// shared: two hooks importing the same helper get the same object, which is
// what makes a module a sensible place to keep state for one send.
func (r *Runtime) importModule(from, specifier string) goja.Value {
	if r.opts.Modules == nil {
		panic(r.vm.NewTypeError("import %q: this collection has no module loader", specifier))
	}
	resolved, err := r.opts.Modules.Resolve(from, specifier)
	if err != nil {
		panic(r.vm.NewTypeError("import %q from %s: %s", specifier, from, err.Error()))
	}
	// The cache is checked before the read, so a module three hooks import is
	// read once and evaluated once.
	if cached, ok := r.modules[resolved]; ok {
		return cached
	}
	// A cycle would otherwise be an infinite descent that the phase timeout
	// eventually kills with a much less useful message.
	for i, loading := range r.loading {
		if loading == resolved {
			panic(r.vm.NewTypeError("import %q: cycle: %s -> %s",
				specifier, strings.Join(r.loading[i:], " -> "), resolved))
		}
	}
	r.loading = append(r.loading, resolved)
	defer func() { r.loading = r.loading[:len(r.loading)-1] }()

	src, err := r.opts.Modules.Read(resolved)
	if err != nil {
		panic(r.vm.NewTypeError("import %q from %s: %s", specifier, from, err.Error()))
	}

	code, err := transform(src)
	if err != nil {
		panic(r.vm.NewTypeError("%s", err.Error()))
	}
	// The module body runs in its own function so its declarations do not
	// leak into the realm's globals, and returns the exports object. The
	// prologue shares the code's first line so reported line numbers are the
	// module's own.
	wrapped := fmt.Sprintf("(function(){const %s = {};%s\nreturn %s;})()",
		exportsVar, code, exportsVar)
	program, compileErr := goja.Compile(src.Path, wrapped, false)
	if compileErr != nil {
		panic(r.vm.NewTypeError("%s: %s", src.Path, compileErr.Error()))
	}
	value, runErr := r.vm.RunProgram(program)
	if runErr != nil {
		panic(runErr)
	}
	exports := value.ToObject(r.vm)
	r.modules[src.Path] = exports
	return exports
}

// The import and export forms of docs/FORMAT.md §9.8. Anchored to the start
// of a line: a declaration is a statement, and matching one mid-expression
// would rewrite a string that happens to contain the word.
var (
	reImportNamed   = regexp.MustCompile(`^\s*import\s*\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]\s*;?\s*$`)
	reImportStar    = regexp.MustCompile(`^\s*import\s+\*\s+as\s+([A-Za-z_$][\w$]*)\s+from\s*['"]([^'"]+)['"]\s*;?\s*$`)
	reImportDefault = regexp.MustCompile(`^\s*import\s+([A-Za-z_$][\w$]*)\s+from\s*['"]([^'"]+)['"]\s*;?\s*$`)
	reImportBare    = regexp.MustCompile(`^\s*import\s*['"]([^'"]+)['"]\s*;?\s*$`)
	reExportDecl    = regexp.MustCompile(`^(\s*)export\s+(function\s*\*?|class|const|let|var)\s+([A-Za-z_$][\w$]*)`)
	reExportDefault = regexp.MustCompile(`^(\s*)export\s+default\s+`)
	reExportList    = regexp.MustCompile(`^\s*export\s*\{([^}]*)\}\s*;?\s*$`)
	reAnyImport     = regexp.MustCompile(`^\s*(import|export)\b`)
)

// transform rewrites the module syntax of §9.8 into something goja can
// compile, which has no module support of its own.
//
// A source transform rather than a real module system, and the subset is
// documented rather than guessed at: a form outside it is an error naming the
// file and line, because a declaration silently misread is far worse than one
// refused. Declarations must start a line, which the spec says.
func transform(src Source) (string, error) {
	lines := strings.Split(src.Code, "\n")
	out := make([]string, 0, len(lines))
	// Names to publish at the end, so `export function f` does not have to be
	// rewritten into something that changes hoisting.
	var exported []string

	for i, line := range lines {
		at := func() string {
			line := i + 1
			if src.Line > 0 {
				line += src.Line
			}
			return fmt.Sprintf("%s:%d", src.Path, line)
		}

		if m := reImportNamed.FindStringSubmatch(line); m != nil {
			bindings, err := destructure(m[1])
			if err != nil {
				return "", fmt.Errorf("%s: %w", at(), err)
			}
			out = append(out, fmt.Sprintf("const {%s} = %s(%q, %q);",
				bindings, importFn, src.Path, m[2]))
			continue
		}
		if m := reImportStar.FindStringSubmatch(line); m != nil {
			out = append(out, fmt.Sprintf("const %s = %s(%q, %q);", m[1], importFn, src.Path, m[2]))
			continue
		}
		if m := reImportDefault.FindStringSubmatch(line); m != nil {
			out = append(out, fmt.Sprintf("const %s = %s(%q, %q).default;", m[1], importFn, src.Path, m[2]))
			continue
		}
		if m := reImportBare.FindStringSubmatch(line); m != nil {
			out = append(out, fmt.Sprintf("%s(%q, %q);", importFn, src.Path, m[1]))
			continue
		}
		if m := reExportDefault.FindStringSubmatch(line); m != nil {
			out = append(out, m[1]+exportsVar+".default = "+strings.TrimSuffix(strings.TrimSpace(line[len(m[0]):]), ";")+";")
			continue
		}
		if m := reExportDecl.FindStringSubmatch(line); m != nil {
			// Keep the declaration as written so hoisting and `const` are
			// unchanged, and publish the name afterwards.
			out = append(out, m[1]+strings.TrimSpace(line[len(m[1])+len("export "):]))
			exported = append(exported, m[3])
			continue
		}
		if m := reExportList.FindStringSubmatch(line); m != nil {
			assignments, err := republish(m[1])
			if err != nil {
				return "", fmt.Errorf("%s: %w", at(), err)
			}
			out = append(out, assignments)
			continue
		}
		if reAnyImport.MatchString(line) {
			return "", fmt.Errorf("%s: %s is not one of the import or export forms Otis supports (docs/FORMAT.md §9.8)",
				at(), strings.TrimSpace(line))
		}
		out = append(out, line)
	}

	for _, name := range exported {
		out = append(out, fmt.Sprintf("%s.%s = %s;", exportsVar, name, name))
	}
	return strings.Join(out, "\n"), nil
}

// destructure turns `a, b as c` into the destructuring `a, b: c`.
func destructure(list string) (string, error) {
	var parts []string
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, alias, renamed := strings.Cut(item, " as ")
		name = strings.TrimSpace(name)
		if !isIdentifier(name) {
			return "", fmt.Errorf("%q is not a name that can be imported", item)
		}
		if !renamed {
			parts = append(parts, name)
			continue
		}
		alias = strings.TrimSpace(alias)
		if !isIdentifier(alias) {
			return "", fmt.Errorf("%q is not a name that can be imported", item)
		}
		parts = append(parts, name+": "+alias)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("the import list is empty")
	}
	return strings.Join(parts, ", "), nil
}

// republish turns `export { a, b as c }` into assignments on the exports.
func republish(list string) (string, error) {
	var parts []string
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, alias, renamed := strings.Cut(item, " as ")
		name = strings.TrimSpace(name)
		if !isIdentifier(name) {
			return "", fmt.Errorf("%q is not a name that can be exported", item)
		}
		as := name
		if renamed {
			as = strings.TrimSpace(alias)
			if !isIdentifier(as) {
				return "", fmt.Errorf("%q is not a name that can be exported", item)
			}
		}
		parts = append(parts, fmt.Sprintf("%s.%s = %s;", exportsVar, as, name))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("the export list is empty")
	}
	return strings.Join(parts, " "), nil
}

var reIdentifier = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)

func isIdentifier(name string) bool { return reIdentifier.MatchString(name) }

// ResolveSpecifier is the path rule of §9.8, exposed so a loader and a test
// agree about it: a relative path from the importing file, ending in .js, that
// stays inside the collection.
func ResolveSpecifier(from, specifier string) (string, error) {
	switch {
	case specifier == "":
		return "", fmt.Errorf("the specifier is empty")
	case strings.Contains(specifier, "://"):
		return "", fmt.Errorf("a script cannot import from a URL; imports resolve inside the collection")
	case strings.HasPrefix(specifier, "/"):
		return "", fmt.Errorf("an import must be a relative path, not an absolute one")
	case !strings.HasPrefix(specifier, "./") && !strings.HasPrefix(specifier, "../"):
		return "", fmt.Errorf("%q is not a relative path: there is no package registry, only files in the collection", specifier)
	case !strings.HasSuffix(specifier, ".js"):
		return "", fmt.Errorf("an import must name a .js file")
	}
	resolved := path.Join(path.Dir(from), specifier)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("%q resolves outside the collection", specifier)
	}
	return resolved, nil
}
