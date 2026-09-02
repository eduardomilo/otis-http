package httpclient

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// prep writes a small collection, resolves the request at id and prepares it.
func prep(t *testing.T, files map[string]string, id string, opts resolve.Options) (*Request, []Warning, error) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := collection.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	node := c.Find(id)
	if node == nil {
		t.Fatalf("%s not found", id)
	}
	res, err := resolve.InCollection(c, node, opts)
	if err != nil {
		t.Fatal(err)
	}
	return Prepare(res, node.Request, filepath.Dir(node.Path))
}

func TestPrepareAuth(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []Header
	}{
		{"bearer", map[string]string{"r.http": "# @auth bearer tok\nGET https://x\nAccept: */*\n"},
			[]Header{{"Accept", "*/*"}, {"Authorization", "Bearer tok"}}},
		{"basic", map[string]string{"r.http": "# @auth basic alice pa ss\nGET https://x\n"},
			[]Header{{"Authorization", "Basic YWxpY2U6cGEgc3M="}}},
		{"basic no password", map[string]string{"r.http": "# @auth basic alice\nGET https://x\n"},
			[]Header{{"Authorization", "Basic YWxpY2U6"}}},
		{"none sends nothing", map[string]string{"_folder.http": "# @auth bearer tok\n", "r.http": "# @auth none\nGET https://x\n"},
			nil},
		{"explicit header wins over @auth", map[string]string{"_folder.http": "# @auth bearer tok\n", "r.http": "GET https://x\nauthorization: Custom 1\n"},
			[]Header{{"authorization", "Custom 1"}}},
		{"inherited header wins over request @auth", map[string]string{"_folder.http": "Authorization: Inherited\n", "r.http": "# @auth bearer tok\nGET https://x\n"},
			[]Header{{"Authorization", "Inherited"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, warns, err := prep(t, tt.files, "r.http", resolve.Options{})
			if err != nil || len(warns) != 0 {
				t.Fatal(err, warns)
			}
			if !reflect.DeepEqual(req.Headers, tt.want) {
				t.Errorf("headers = %v, want %v", req.Headers, tt.want)
			}
		})
	}
}

func TestPrepareBody(t *testing.T) {
	t.Run("raw", func(t *testing.T) {
		req, _, err := prep(t, map[string]string{"r.http": "@v = 1\nPOST https://x\n\n{\"v\": {{v}}}\n"}, "r.http", resolve.Options{})
		if err != nil || string(req.Body) != `{"v": 1}` {
			t.Errorf("body = %q, %v", req.Body, err)
		}
	})
	t.Run("file relative to the request file, no substitution", func(t *testing.T) {
		req, warns, err := prep(t, map[string]string{
			"api/r.http":          "POST https://x\n\n< ./payloads/a.json\n",
			"api/payloads/a.json": `{"raw": "{{v}}"}`,
		}, "api/r.http", resolve.Options{})
		if err != nil || len(warns) != 0 || string(req.Body) != `{"raw": "{{v}}"}` {
			t.Errorf("body = %q, %v, %v", req.Body, warns, err)
		}
	})
	t.Run("file with substitution and secrets", func(t *testing.T) {
		store := secrets.NewMemory()
		env := &resolve.Environment{Name: "e", Path: "env/e.json", Values: map[string]resolve.EnvValue{"s": {Secret: true}}}
		files := map[string]string{
			"api/r.http":    "@v = 1\nPOST https://x\n\n<@ ../shared/a.json\n",
			"shared/a.json": `{"v": {{v}}, "s": "{{s}}"}`,
		}
		// Collection key is the temp dir's base name; set the secret after
		// learning it via a first resolve.
		dir := t.TempDir()
		for rel, content := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(content), 0o644)
		}
		c, _ := collection.Load(dir)
		_ = store.Set(secrets.Key(resolve.CollectionKey(c), "e", "s"), "hidden")
		node := c.Find("api/r.http")
		res, err := resolve.InCollection(c, node, resolve.Options{Env: env, Secrets: store})
		if err != nil {
			t.Fatal(err)
		}
		req, _, err := Prepare(res, node.Request, filepath.Dir(node.Path))
		if err != nil || string(req.Body) != `{"v": 1, "s": "hidden"}` {
			t.Errorf("body = %q, %v", req.Body, err)
		}
		if res.Mask(string(req.Body)) != `{"v": 1, "s": "•••••"}` {
			t.Error("secret used by the file body is not masked")
		}
	})
	t.Run("file with unresolved variable", func(t *testing.T) {
		_, _, err := prep(t, map[string]string{"r.http": "POST https://x\n\n<@ ./a.json\n", "a.json": `{{nope}}`}, "r.http", resolve.Options{})
		if err == nil || err.Error() != "body file ./a.json: unresolved variables: nope" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("charset warns", func(t *testing.T) {
		req, warns, err := prep(t, map[string]string{"r.http": "POST https://x\n\n<@latin1 ./a.txt\n", "a.txt": "caf\xe9"}, "r.http", resolve.Options{})
		if err != nil || string(req.Body) != "caf\xe9" || len(warns) != 1 || !strings.Contains(string(warns[0]), `charset "latin1" is not converted`) {
			t.Errorf("req = %+v warns = %v err = %v", req, warns, err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, _, err := prep(t, map[string]string{"r.http": "POST https://x\n\n< ./missing.json\n"}, "r.http", resolve.Options{})
		if err == nil || !strings.HasPrefix(err.Error(), "body file ./missing.json: ") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestPrepareDirectives(t *testing.T) {
	req, _, err := prep(t, map[string]string{"r.http": "# @no-redirect\n// @no-cookie-jar\n# @timeout 2.5\nGET https://x\n"}, "r.http", resolve.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := Options{Timeout: 2500 * time.Millisecond, NoRedirect: true, NoCookieJar: true}
	if req.Options != want {
		t.Errorf("options = %+v, want %+v", req.Options, want)
	}
	for _, bad := range []string{"# @timeout abc\n", "# @timeout 0\n", "# @timeout -1\n", "# @timeout\n"} {
		_, _, err := prep(t, map[string]string{"r.http": bad + "GET https://x\n"}, "r.http", resolve.Options{})
		if err == nil || !strings.Contains(err.Error(), "@timeout") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	// nil source request: no directives, no crash.
	if _, _, err := Prepare(&resolve.Resolved{Method: "GET", URL: "https://x"}, (*httpfile.Request)(nil), "."); err != nil {
		t.Error(err)
	}
}
