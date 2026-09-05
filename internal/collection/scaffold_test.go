// Tests for the scaffold a new collection is created with (scaffold.go).

package collection

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/otis-http/otis/internal/httpfile"
)

func TestScaffoldRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".requests")
	if err := WriteScaffold(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"_folder.http", "env/local.json", "example.http"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// Every .http the scaffold writes must parse, and re-serialize byte for
	// byte: it is Otis' own canonical form or it is a diff on somebody's
	// first save.
	for _, name := range []string{"_folder.http", "example.http"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := httpfile.ParseString(string(raw))
		if err != nil {
			t.Fatalf("%s does not parse: %v\n%s", name, err, raw)
		}
		if got := f.String(); got != string(raw) {
			t.Errorf("%s is not canonical:\nwrote %q\nround  %q", name, raw, got)
		}
	}
	// The collection it wrote is one the walker recognises.
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Root.Children) != 1 || tree.Root.Children[0].Name != "Example" {
		t.Errorf("tree = %+v", tree.Root.Children)
	}
	if FindRoot(filepath.Join(dir, "example.http")) != dir {
		t.Errorf("FindRoot did not find the scaffold's root")
	}
	if FindWithin(filepath.Dir(dir)) != dir {
		t.Errorf("FindWithin did not find .requests one level down")
	}
	if FindWithin(dir) != dir {
		t.Errorf("FindWithin did not recognise the collection itself")
	}
	if err := WriteScaffold(dir); err == nil {
		t.Error("WriteScaffold overwrote a non-empty directory")
	}
}

// The scaffold's _folder.http must declare *nothing*. A comment whose text
// begins with "@" is a directive, not an example, so a commented-out
// "# @auth bearer {{apiToken}}" would put live auth on every request of every
// new collection — which is what the first draft of scaffold.go did.
func TestScaffoldDeclaresNothing(t *testing.T) {
	f, err := httpfile.ParseString(rootFolderFile())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range f.Requests {
		if len(r.Directives) != 0 {
			t.Errorf("_folder.http declares directives: %+v", r.Directives)
		}
		if len(r.Variables) != 0 {
			t.Errorf("_folder.http declares variables: %+v", r.Variables)
		}
		if len(r.Headers) != 0 {
			t.Errorf("_folder.http declares headers: %+v", r.Headers)
		}
		if r.HasRequestLine() {
			t.Errorf("_folder.http has a request line: %s %s", r.Method, r.URL)
		}
	}
}
