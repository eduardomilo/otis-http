// Package scriptrun wires the script runtime to a collection and a send.
//
// It exists so the window and the CLI run scripts the same way. `otis run` in
// CI and the request editor in the window are the same binary, and a request
// whose scripts ran in one and not the other would make the CLI a different
// product with the same name — so the plan, the module loader, the variable
// store and the substitution of a secret handle all live here, once, and both
// callers use them.
//
// internal/script stays free of all of this: it takes interfaces, so the
// interpreter never touches disk and cannot be handed something that does.
package scriptrun

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/script"
)

// MaxScriptBytes bounds a script file. A .js in a collection is a hook or a
// small helper; anything larger is not that, and reading it into a send's
// budget would be a way to stall one.
const MaxScriptBytes = 1 << 20

// Plan is every script one send will run, in the order of docs/FORMAT.md
// §9.1.
type Plan struct {
	Pre  []script.Source
	Post []script.Source
}

// PlanFor collects the scripts for a request.
//
// Pre-request, outermost first: the root's `_pre.js`, then each folder's on
// the way down, then `<name>.pre.js`, then the request's own `< {% %}` blocks.
// Post-response, the exact reverse. That is the order that lets a folder set
// up what its requests need before any of them run and conclude after them.
func PlanFor(loaded *collection.Collection, node *collection.Node) Plan {
	var plan Plan
	if node == nil || node.Request == nil {
		return plan
	}

	// The folder chain, root-most first.
	ancestors := node.Ancestors() // nearest first
	folders := make([]*collection.Node, 0, len(ancestors))
	for i := len(ancestors) - 1; i >= 0; i-- {
		folders = append(folders, ancestors[i])
	}

	// Pre: folders outermost first.
	for _, folder := range folders {
		if src, ok := readHook(folder, collection.PreHookName); ok {
			plan.Pre = append(plan.Pre, src)
		}
	}
	// Then the request's own file hook, then its inline blocks.
	if src, ok := requestHook(node, collection.PreHookSuffix); ok {
		plan.Pre = append(plan.Pre, src)
	}
	for _, block := range node.Request.PreScripts {
		if src, ok := inlineBlock(node, block); ok {
			plan.Pre = append(plan.Pre, src)
		}
	}

	// Post: the exact reverse — inline blocks, the file hook, then folders
	// from the request's own outwards to the root.
	for _, block := range node.Request.PostScripts {
		if src, ok := inlineBlock(node, block); ok {
			plan.Post = append(plan.Post, src)
		}
	}
	if src, ok := requestHook(node, collection.PostHookSuffix); ok {
		plan.Post = append(plan.Post, src)
	}
	for i := len(folders) - 1; i >= 0; i-- {
		if src, ok := readHook(folders[i], collection.PostHookName); ok {
			plan.Post = append(plan.Post, src)
		}
	}
	return plan
}

// readHook reads a folder's _pre.js or _post.js, if it has one.
func readHook(folder *collection.Node, name string) (script.Source, bool) {
	for _, child := range folder.Children {
		if child.Kind != collection.KindScript || child.Name != name {
			continue
		}
		return readScriptFile(child.ID, child.Path)
	}
	return script.Source{}, false
}

// requestHook reads a request's own <name>.pre.js or <name>.post.js.
func requestHook(node *collection.Node, suffix string) (script.Source, bool) {
	if node.Parent == nil {
		return script.Source{}, false
	}
	base := strings.TrimSuffix(node.Name, collection.RequestExt)
	// The node's display name may be an @name rather than the file name, so
	// the file name is taken from the path.
	base = strings.TrimSuffix(path.Base(node.ID), collection.RequestExt)
	want := base + suffix
	for _, sibling := range node.Parent.Children {
		if sibling.Kind == collection.KindScript && sibling.Name == want {
			return readScriptFile(sibling.ID, sibling.Path)
		}
	}
	return script.Source{}, false
}

// inlineBlock turns a `< {% %}` or `> {% %}` block into a Source.
//
// An external `> ./handler.js` is read from disk relative to the request
// file, which is the form docs/FORMAT.md §1.10 also allows.
func inlineBlock(node *collection.Node, block httpfile.Script) (script.Source, bool) {
	if block.FilePath != "" {
		target := filepath.Join(filepath.Dir(node.Path), filepath.FromSlash(block.FilePath))
		rel := path.Join(path.Dir(node.ID), block.FilePath)
		return readScriptFile(rel, target)
	}
	if strings.TrimSpace(block.Text) == "" {
		return script.Source{}, false
	}
	// Line is the `{%` marker's line, and the block's text starts after it,
	// so an error inside reports the request file's own line.
	return script.Source{Path: node.ID, Line: block.Line, Code: block.Text}, true
}

// readScriptFile reads a script, refusing one too large to be a hook.
func readScriptFile(nodePath, absPath string) (script.Source, bool) {
	info, err := os.Stat(absPath)
	if err != nil || info.Size() > MaxScriptBytes {
		return script.Source{}, false
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return script.Source{}, false
	}
	return script.Source{Path: nodePath, Code: string(data)}, true
}

// moduleLoader resolves and reads modules from one collection, and nowhere
// else.
//
// This is where the confinement of docs/FORMAT.md §9.8 is enforced: the
// script package never touches disk, and this is the only thing that reads a
// module — so a specifier that escapes the root cannot be read whatever the
// interpreter is asked to do.
type ModuleLoader struct {
	// Root is the collection root, absolute.
	Root string
}

// Resolve applies §9.8's path rule and then checks the result is really
// inside the root on disk, which is the belt to the rule's braces: a symlink
// inside the collection pointing out of it would otherwise pass.
func (l ModuleLoader) Resolve(from, specifier string) (string, error) {
	resolved, err := script.ResolveSpecifier(from, specifier)
	if err != nil {
		return "", err
	}
	target := filepath.Join(l.Root, filepath.FromSlash(resolved))
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		// It may not exist, which Read reports more usefully than this can.
		return resolved, nil
	}
	rootReal, err := filepath.EvalSymlinks(l.Root)
	if err != nil {
		rootReal = l.Root
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s resolves outside the collection", resolved)
	}
	return resolved, nil
}

// Read returns a module's source.
func (l ModuleLoader) Read(nodePath string) (script.Source, error) {
	target := filepath.Join(l.Root, filepath.FromSlash(nodePath))
	info, err := os.Stat(target)
	if err != nil {
		return script.Source{}, fmt.Errorf("%s is not in this collection", nodePath)
	}
	if info.Size() > MaxScriptBytes {
		return script.Source{}, fmt.Errorf("%s is larger than %d bytes", nodePath, MaxScriptBytes)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return script.Source{}, fmt.Errorf("reading %s: %w", nodePath, err)
	}
	return script.Source{Path: nodePath, Code: string(data)}, nil
}

// Vars is the VarStore a script writes through.
//
// Reads go to the resolver's own scope, so `vars.get` and a `{{reference}}`
// are the same lookup (docs/FORMAT.md §9.4). Writes go to three different
// places, which is the whole point of the three scopes:
//
//   - request: a map that lives for this send, layered over the scope
//   - session: the in-memory session store, keyed by the request's folder
//   - env:     the active environment file, which is committed
type Vars struct {
	// scope resolves a name the way the file does. Rebuilt after a write, so
	// a script that sets a value can read it back.
	Scope func() *resolve.Scope
	// request holds this send's scratch values.
	Request map[string]string
	// folder is the request's folder node ID, which the session scope is
	// keyed by.
	Folder string
	// session is the collection's session store.
	Session *resolve.Session
	// origin is the request whose run is writing, for a session value's
	// provenance (§4.5).
	Origin string
	// writeEnv persists an environment value. Nil when no environment is
	// active, which makes vars.env.set an error rather than a silent no-op.
	WriteEnv func(name, value string) error
	// readEnv reads the active environment's own value for a name.
	ReadEnv func(name string) (string, bool)
	// envName is the active environment, for the error message.
	EnvName string
}

// Resolve is vars.get: the full §4.2 fall-through, plus this send's scratch
// values, which sit above everything because they are the nearest scope there
// is.
func (v *Vars) Resolve(name string) (string, bool) {
	if value, ok := v.Request[name]; ok {
		return value, true
	}
	scope := v.Scope()
	if scope == nil {
		return "", false
	}
	x := resolve.NewExpander(scope)
	value, err := x.Expand("{{" + name + "}}")
	if err != nil {
		return "", false
	}
	// A secret resolves to the placeholder on this path, never a value: the
	// scope a script reads through is built with secrets.Placeholder.
	for _, use := range x.Uses() {
		if use.Name == name && use.Secret {
			return "", false
		}
	}
	return value, true
}

// ReadScope is vars.<scope>.get: one scope, no fall-through.
func (v *Vars) ReadScope(scope script.VarScope, name string) (string, bool) {
	switch scope {
	case script.ScopeRequest:
		value, ok := v.Request[name]
		return value, ok
	case script.ScopeSession:
		if v.Session == nil {
			return "", false
		}
		got, ok := v.Session.Get(resolve.SessionFolder, v.Folder, name)
		return got.Value, ok
	case script.ScopeEnv:
		// The environment's own value, not what would resolve: this is the
		// scope-specific read. readEnv is supplied alongside writeEnv, so a
		// send with no active environment reads nothing here.
		if v.ReadEnv == nil {
			return "", false
		}
		return v.ReadEnv(name)
	}
	return "", false
}

// WriteScope is vars.<scope>.set.
func (v *Vars) WriteScope(scope script.VarScope, name, value string) error {
	if !resolve.ValidReferenceName(name) {
		return fmt.Errorf("%q is not a name a {{reference}} can use", name)
	}
	switch scope {
	case script.ScopeRequest:
		if v.Request == nil {
			v.Request = map[string]string{}
		}
		v.Request[name] = value
		return nil
	case script.ScopeSession:
		if v.Session == nil {
			return fmt.Errorf("this collection has no session store")
		}
		v.Session.Set(resolve.SessionValue{
			Scope: resolve.SessionFolder, Owner: v.Folder,
			Name: name, Value: value, Origin: v.Origin, At: time.Now(),
		})
		return nil
	case script.ScopeEnv:
		if v.WriteEnv == nil {
			return fmt.Errorf("no environment is active, so there is no file to write")
		}
		return v.WriteEnv(name, value)
	}
	return fmt.Errorf("unknown scope %q", scope)
}
