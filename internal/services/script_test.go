package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newScriptService(t *testing.T, root string) *ScriptService {
	t.Helper()
	collections := newService(t)
	if _, err := collections.Open(root); err != nil {
		t.Fatalf("opening the collection: %v", err)
	}
	return NewScriptService(collections)
}

// scriptFixture holds one of each thing a `.js` can be (docs/FORMAT.md §2.4):
// a folder hook, a request hook, and a module that is neither.
func scriptFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nPOST {{baseUrl}}/orders\n")
	write(t, filepath.Join(root, "orders", "_post.js"), "test('ok', () => expect(response.status).toBe(200));\n")
	write(t, filepath.Join(root, "orders", "create-order.post.js"), "vars.folder.set('id', response.json().id);\n")
	write(t, filepath.Join(root, "lib", "assert.js"), "export function ok() {}\n")
	return root
}

// The header says what a file *is*, and what it is comes entirely from its
// name. Getting this wrong would tell somebody a module runs when nothing
// will ever run it.
func TestScriptLoadSaysWhatRunsIt(t *testing.T) {
	root := scriptFixture(t)
	s := newScriptService(t, root)

	folderHook, err := s.Load("orders/_post.js")
	if err != nil {
		t.Fatal(err)
	}
	if !folderHook.Hook || folderHook.Phase != "post" || folderHook.Scope != "orders" || folderHook.HookOf != "" {
		t.Errorf("folder hook = %+v, want a post hook scoped to orders", folderHook)
	}

	requestHook, err := s.Load("orders/create-order.post.js")
	if err != nil {
		t.Fatal(err)
	}
	if !requestHook.Hook || requestHook.Phase != "post" || requestHook.HookOf != "orders/create-order.http" {
		t.Errorf("request hook = %+v, want a post hook of create-order.http", requestHook)
	}
	if requestHook.Scope != "" {
		t.Errorf("a request hook has a folder scope: %+v", requestHook)
	}

	module, err := s.Load("lib/assert.js")
	if err != nil {
		t.Fatal(err)
	}
	if module.Hook || module.Phase != "" || module.HookOf != "" {
		t.Errorf("module = %+v, want nothing that says it runs", module)
	}
	if !strings.Contains(module.Text, "export function ok") {
		t.Errorf("the text did not come with it: %q", module.Text)
	}
}

// Otis has no opinion about JavaScript formatting, and a save must not
// acquire one: what the editor holds is what lands on disk, byte for byte.
func TestScriptSaveWritesTheTextVerbatim(t *testing.T) {
	root := scriptFixture(t)
	s := newScriptService(t, root)

	// Deliberately ragged: tabs, a CRLF, trailing whitespace, no final
	// newline. A serializer would tidy every one of them.
	text := "const a = 1\r\n\tif (a) {\n  console.log( 'x' )   \n}"
	doc, err := s.Save("lib/assert.js", text)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if doc.Text != text {
		t.Errorf("returned text = %q, want %q", doc.Text, text)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "lib", "assert.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != text {
		t.Errorf("bytes on disk = %q, want %q", string(onDisk), text)
	}
}

// The one thing this service must never become is a second way to write a
// request file, or a way to write outside the collection at all.
func TestScriptSaveRefusesAnythingThatIsNotAScript(t *testing.T) {
	root := scriptFixture(t)
	s := newScriptService(t, root)

	for _, path := range []string{
		"orders/create-order.http",
		"../outside.js",
		"orders/_folder.http",
	} {
		if _, err := s.Save(path, "// no"); err == nil {
			t.Errorf("Save accepted %q", path)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.js")); !os.IsNotExist(err) {
		t.Error("a save escaped the collection")
	}
}

func TestScriptLoadRefusesARequest(t *testing.T) {
	s := newScriptService(t, scriptFixture(t))
	if _, err := s.Load("orders/create-order.http"); err == nil {
		t.Error("Load accepted a request")
	}
}

// Saving a script re-walks, because whether a `.pre.js` is a hook at all
// depends on a request file existing beside it.
func TestSavingAScriptRefreshesTheTree(t *testing.T) {
	root := scriptFixture(t)
	s := newScriptService(t, root)

	write(t, filepath.Join(root, "orders", "cancel-order.pre.js"), "// new\n")
	if _, err := s.Save("orders/cancel-order.pre.js", "// still new\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find("orders/cancel-order.pre.js")
	if node == nil {
		t.Fatal("the script is not in the tree")
	}
	// No cancel-order.http beside it, so it is a module with an unfortunate
	// name rather than a hook that will never run.
	if node.Hook {
		t.Error("a .pre.js with no request beside it was called a hook")
	}
}

// Creating a script is entirely a question of what to call the file
// (docs/FORMAT.md §2.4), which is why the window picks a *kind* and Go picks
// the name. This is that table.
func TestCreateScriptNamesTheFileFromItsKind(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nGET {{baseUrl}}/\n")
	svc := newScriptService(t, root)

	for _, tt := range []struct {
		name string
		req  NewScript
		want string
		runs string
	}{
		{
			name: "folder pre-hook",
			req:  NewScript{Kind: ScriptFolderHook, Folder: "orders", Phase: "pre"},
			want: "orders/_pre.js",
			runs: "Runs before every request in orders/ and below.",
		},
		{
			name: "folder post-hook at the root",
			req:  NewScript{Kind: ScriptFolderHook, Folder: "", Phase: "post"},
			want: "_post.js",
			runs: "Runs after every response in the collection root and below.",
		},
		{
			name: "request hook goes beside its request, not in the folder given",
			req:  NewScript{Kind: ScriptRequestHook, Folder: "somewhere-else", Request: "orders/create-order.http", Phase: "post"},
			want: "orders/create-order.post.js",
		},
		{
			name: "module keeps the name as typed",
			req:  NewScript{Kind: ScriptModule, Folder: "orders", Name: "idempotency"},
			want: "orders/idempotency.js",
			runs: "A module: nothing runs this unless a hook imports it.",
		},
		{
			name: "module with a .js the person typed themselves",
			req:  NewScript{Kind: ScriptModule, Folder: "orders", Name: " signing.js "},
			want: "orders/signing.js",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := svc.Plan(tt.req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Problem != "" {
				t.Fatalf("plan refused: %s", plan.Problem)
			}
			if plan.Path != tt.want {
				t.Errorf("path = %q, want %q", plan.Path, tt.want)
			}
			if tt.runs != "" && plan.Runs != tt.runs {
				t.Errorf("runs = %q, want %q", plan.Runs, tt.runs)
			}
			got, err := svc.Create(tt.req)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Create = %q, want %q", got, tt.want)
			}
			// The one comment line, and nothing else: Save is verbatim and
			// this is the only moment Otis writes into a .js at all.
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tt.want)))
			if err != nil {
				t.Fatal(err)
			}
			if want := "// " + plan.Runs + "\n\n"; string(body) != want {
				t.Errorf("body = %q, want %q", body, want)
			}
		})
	}
}

// A created script is the kind the dialog said it was. The name is the whole
// of how §2.4 decides, so this walks back through the classifier rather than
// trusting the name it just wrote.
func TestACreatedScriptIsTheKindItWasAskedFor(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nGET {{baseUrl}}/\n")
	svc := newScriptService(t, root)

	folderHook, err := svc.Create(NewScript{Kind: ScriptFolderHook, Folder: "orders", Phase: "pre"})
	if err != nil {
		t.Fatal(err)
	}
	requestHook, err := svc.Create(NewScript{Kind: ScriptRequestHook, Request: "orders/create-order.http", Phase: "post"})
	if err != nil {
		t.Fatal(err)
	}
	module, err := svc.Create(NewScript{Kind: ScriptModule, Folder: "orders", Name: "helpers"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		path   string
		hook   bool
		phase  string
		hookOf string
		scope  string
	}{
		{folderHook, true, "pre", "", "orders"},
		{requestHook, true, "post", "orders/create-order.http", ""},
		{module, false, "", "", ""},
	} {
		doc, err := svc.Load(tt.path)
		if err != nil {
			t.Fatalf("%s: %v", tt.path, err)
		}
		if doc.Hook != tt.hook || doc.Phase != tt.phase || doc.HookOf != tt.hookOf || doc.Scope != tt.scope {
			t.Errorf("%s: hook=%v phase=%q hookOf=%q scope=%q, want %v/%q/%q/%q",
				tt.path, doc.Hook, doc.Phase, doc.HookOf, doc.Scope, tt.hook, tt.phase, tt.hookOf, tt.scope)
		}
	}
}

// Every one of these produces a file whose *name* says something false about
// what runs it, which is the one thing §2.4 exists to prevent.
func TestCreateScriptRefusesNamesThatWouldLie(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nGET {{baseUrl}}/\n")
	svc := newScriptService(t, root)

	for _, tt := range []struct {
		name string
		req  NewScript
	}{
		{"a module named like a folder hook", NewScript{Kind: ScriptModule, Folder: "orders", Name: "_helpers"}},
		{"a module named like a request hook", NewScript{Kind: ScriptModule, Folder: "orders", Name: "utils.pre"}},
		{"a module with a path in it", NewScript{Kind: ScriptModule, Folder: "orders", Name: "../escape"}},
		{"a module with no name", NewScript{Kind: ScriptModule, Folder: "orders", Name: "  "}},
		{"a hook with no phase", NewScript{Kind: ScriptFolderHook, Folder: "orders"}},
		{"a request hook on a folder", NewScript{Kind: ScriptRequestHook, Request: "orders", Phase: "pre"}},
		{"a folder hook in no folder", NewScript{Kind: ScriptFolderHook, Folder: "nope", Phase: "pre"}},
		{"a kind that is not one", NewScript{Kind: "whatever", Folder: "orders"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := svc.Plan(tt.req)
			if err == nil && plan.Problem == "" {
				t.Fatalf("allowed it: %+v", plan)
			}
			if _, err := svc.Create(tt.req); err == nil {
				t.Error("Create allowed it")
			}
		})
	}
}

// A folder has at most one `_pre.js`, so the second one is a refusal and not
// a `-2`: unlike a request, the name is not free to vary.
func TestCreateScriptRefusesAHookThatAlreadyExists(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nGET {{baseUrl}}/\n")
	svc := newScriptService(t, root)

	req := NewScript{Kind: ScriptFolderHook, Folder: "orders", Phase: "pre"}
	if _, err := svc.Create(req); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Problem, "already exists") {
		t.Errorf("plan.Problem = %q", plan.Problem)
	}
	if _, err := svc.Create(req); err == nil {
		t.Error("created a second _pre.js")
	}
}
