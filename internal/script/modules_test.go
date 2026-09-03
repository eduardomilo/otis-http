package script

import (
	"fmt"
	"strings"
	"testing"
)

// fakeLoader is a ModuleLoader over a map, so the runtime can be tested
// without a filesystem — which is also the sandbox rule: nothing in this
// package reads disk.
type fakeLoader struct {
	files map[string]string
	// loaded counts how many times each module was actually fetched, which is
	// how "evaluated once per send" is checked.
	loaded map[string]int
}

func newFakeLoader(files map[string]string) *fakeLoader {
	return &fakeLoader{files: files, loaded: map[string]int{}}
}

func (l *fakeLoader) Resolve(from, specifier string) (string, error) {
	return ResolveSpecifier(from, specifier)
}

func (l *fakeLoader) Read(path string) (Source, error) {
	code, ok := l.files[path]
	if !ok {
		return Source{}, fmt.Errorf("%s is not in this collection", path)
	}
	l.loaded[path]++
	return Source{Path: path, Code: code}, nil
}

func withModules(t *testing.T, code string, files map[string]string) (Options, *fakeLoader) {
	t.Helper()
	opts := post(t, code)
	loader := newFakeLoader(files)
	opts.Modules = loader
	return opts, loader
}

// The import forms of docs/FORMAT.md §9.8, including the design's own
// lib/idempotency.js shape.
func TestModuleImportForms(t *testing.T) {
	files := map[string]string{
		"lib/idempotency.js": `
			export function idempotencyKey() { return "idem_" + 1; }
			export const version = 2;
		`,
		"lib/assert.js": `
			export function ok(value) { if (!value) throw new Error("not ok"); }
			export function nope() { return "nope"; }
		`,
		"lib/format.js": `export default function (s) { return "<" + s + ">"; };`,
		"lib/renamed.js": `
			const inner = 7;
			function helper() { return "helped"; }
			export { inner, helper as help };
		`,
	}

	cases := map[string]struct{ code, want string }{
		"named": {
			`import { idempotencyKey, version } from "../lib/idempotency.js";
			 console.log(idempotencyKey(), version);`,
			"idem_1 2",
		},
		"named with an alias": {
			`import { idempotencyKey as mint } from "../lib/idempotency.js";
			 console.log(mint());`,
			"idem_1",
		},
		"namespace": {
			`import * as assert from "../lib/assert.js";
			 console.log(assert.nope(), typeof assert.ok);`,
			"nope function",
		},
		"default": {
			`import wrap from "../lib/format.js";
			 console.log(wrap("x"));`,
			"<x>",
		},
		"an export list with an alias": {
			`import { inner, help } from "../lib/renamed.js";
			 console.log(inner, help());`,
			"7 helped",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			opts, _ := withModules(t, c.code, files)
			result, err := Run(opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Console[0].Text; got != c.want {
				t.Errorf("console = %q, want %q", got, c.want)
			}
		})
	}
}

// A module is evaluated once per send whatever imports it, and its exports
// are shared — which is what makes a module a sensible place to keep state
// for one send.
func TestModuleIsEvaluatedOncePerSend(t *testing.T) {
	files := map[string]string{
		"lib/counter.js": `
			let n = 0;
			export function next() { return ++n; }
		`,
	}
	opts, loader := withModules(t, "", files)
	opts.Scripts = []Source{
		{Path: "orders/_post.js", Code: `
			import { next } from "../lib/counter.js";
			console.log("first", next());
		`},
		{Path: "orders/create.http", Line: 10, Code: `
			import { next } from "../lib/counter.js";
			console.log("second", next());
		`},
	}
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if loader.loaded["lib/counter.js"] != 1 {
		t.Errorf("the module was fetched %d times, want 1", loader.loaded["lib/counter.js"])
	}
	// Shared state, so the counter keeps going rather than restarting.
	if result.Console[0].Text != "first 1" || result.Console[1].Text != "second 2" {
		t.Errorf("console = %q, %q", result.Console[0].Text, result.Console[1].Text)
	}
}

// Imports resolve inside the collection and nowhere else (§9.8). There is no
// registry, no network, and no way out of the root.
func TestModuleSpecifiersAreConfinedToTheCollection(t *testing.T) {
	cases := map[string]struct{ from, specifier, wants string }{
		"a bare package":   {"orders/_post.js", "lodash", "not a relative path"},
		"an absolute path": {"orders/_post.js", "/etc/passwd", "relative path"},
		"a URL":            {"orders/_post.js", "https://cdn.test/x.js", "URL"},
		"a file protocol":  {"orders/_post.js", "file:///etc/passwd", "URL"},
		"not a .js file":   {"orders/_post.js", "./notes.md", ".js file"},
		"above the root":   {"orders/_post.js", "../../outside.js", "outside the collection"},
		"far above":        {"_post.js", "../../../etc/passwd.js", "outside the collection"},
		"empty":            {"orders/_post.js", "", "empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveSpecifier(c.from, c.specifier); err == nil {
				t.Fatalf("ResolveSpecifier(%q) should fail", c.specifier)
			} else if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %v, want it to mention %q", err, c.wants)
			}
		})
	}

	// And the same refusals reach a script as errors naming the specifier.
	opts, _ := withModules(t, `import fs from "fs";`, map[string]string{})
	if _, err := Run(opts); err == nil {
		t.Error("importing a bare package should fail")
	} else if !strings.Contains(err.Error(), "fs") {
		t.Errorf("error = %v, want it to name the specifier", err)
	}
}

// A relative path that stays inside the collection resolves, including one
// that climbs and comes back down.
func TestModuleSpecifiersThatResolve(t *testing.T) {
	cases := map[string]struct{ from, specifier, want string }{
		"a sibling":            {"orders/_post.js", "./helper.js", "orders/helper.js"},
		"up and across":        {"orders/_post.js", "../lib/x.js", "lib/x.js"},
		"deeper":               {"orders/_post.js", "./fixtures/seed.js", "orders/fixtures/seed.js"},
		"from a nested folder": {"orders/fixtures/_post.js", "../../lib/x.js", "lib/x.js"},
		"at the root":          {"_post.js", "./lib/x.js", "lib/x.js"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveSpecifier(c.from, c.specifier)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("ResolveSpecifier(%q, %q) = %q, want %q", c.from, c.specifier, got, c.want)
			}
		})
	}
}

// A cycle is an error naming the chain, rather than an infinite descent the
// phase timeout eventually kills with a much less useful message.
func TestModuleCycleIsAnError(t *testing.T) {
	// A declaration is a statement and must be the whole line (§9.8), so the
	// import and the log go on separate lines here.
	opts, _ := withModules(t, "import { a } from \"../lib/a.js\";\nconsole.log(a);", map[string]string{
		"lib/a.js": "import { b } from \"./b.js\";\nexport const a = \"a\" + b;",
		"lib/b.js": "import { a } from \"./a.js\";\nexport const b = \"b\" + a;",
	})
	_, err := Run(opts)
	if err == nil {
		t.Fatal("an import cycle should be an error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want it to say cycle", err)
	}
	if !strings.Contains(err.Error(), "lib/a.js") || !strings.Contains(err.Error(), "lib/b.js") {
		t.Errorf("error = %v, want it to name the chain", err)
	}
}

// A module is a library, not a step: it gets the sandbox minus the things
// that belong to a phase.
func TestModuleHasNoTestOrResponse(t *testing.T) {
	opts, _ := withModules(t, `
		import { what } from "../lib/probe.js";
		console.log(what());
	`, map[string]string{
		"lib/probe.js": `
			export function what() {
				return [typeof vars, typeof secrets, typeof console, typeof crypto].join(" ");
			}
		`,
	})
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "object object object object" {
		t.Errorf("a module should have vars, secrets, console and crypto; got %q", got)
	}
}

// A module's declarations stay in the module: it runs in its own function, so
// nothing leaks into the realm's globals.
func TestModuleDoesNotLeakItsDeclarations(t *testing.T) {
	opts, _ := withModules(t, `
		import { x } from "../lib/private.js";
		console.log(x, typeof secretHelper);
	`, map[string]string{
		"lib/private.js": `
			function secretHelper() { return 1; }
			export const x = secretHelper();
		`,
	})
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "1 undefined" {
		t.Errorf("console = %q, want the helper invisible outside its module", got)
	}
}

// A form outside the supported subset is an error naming the file and line,
// rather than being silently misread.
func TestUnsupportedModuleSyntaxIsRefused(t *testing.T) {
	cases := map[string]string{
		"a side-effect import with a body": `import def, { named } from "../lib/x.js";`,
		"export from":                      `export { a } from "../lib/x.js";`,
		"export star":                      `export * from "../lib/x.js";`,
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := transform(Source{Path: "orders/_post.js", Code: "const a = 1;\n" + code})
			if err == nil {
				t.Fatalf("%s should be refused", code)
			}
			if !strings.Contains(err.Error(), "orders/_post.js:2") {
				t.Errorf("error = %v, want the file and line", err)
			}
			if !strings.Contains(err.Error(), "§9.8") {
				t.Errorf("error = %v, want it to point at the spec", err)
			}
		})
	}
}

// A collection with no loader says so rather than failing obscurely.
func TestImportWithNoLoader(t *testing.T) {
	opts := post(t, `import { x } from "../lib/x.js";`)
	if _, err := Run(opts); err == nil {
		t.Error("an import with no loader should fail")
	} else if !strings.Contains(err.Error(), "module loader") {
		t.Errorf("error = %v", err)
	}
}

// The word `import` inside a string is not a declaration: the forms are
// anchored to the start of a line because a declaration is a statement.
func TestTransformLeavesTheWordImportAloneMidLine(t *testing.T) {
	code := `const message = "import { x } from \"nowhere\"";` + "\n" +
		`console.log(message.length > 0);` + "\n"
	out, err := transform(Source{Path: "orders/_post.js", Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"import { x } from \"nowhere\""`) {
		t.Errorf("the transform rewrote a string:\n%s", out)
	}
}
