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
