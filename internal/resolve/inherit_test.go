package resolve

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/collection"
)

// tree writes files (relative path → content) into a temp collection and
// loads it.
func tree(t *testing.T, files map[string]string) *collection.Collection {
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
	return c
}

func resolveReq(t *testing.T, files map[string]string, id string) (*Effective, error) {
	t.Helper()
	c := tree(t, files)
	n := c.Find(id)
	if n == nil {
		t.Fatalf("node %q not found", id)
	}
	return Inheritance(n)
}

func TestInheritance(t *testing.T) {
	const req = "a/b/c/req.http"
	tests := []struct {
		name         string
		files        map[string]string
		wantHeaders  []Header
		wantDisabled []Disabled
		wantAuth     *Auth
	}{
		{
			name: "no folder file",
			files: map[string]string{
				req: "GET https://x.test\nAccept: */*\n",
			},
			wantHeaders: []Header{{"Accept", "*/*", Source{req, 2}}},
		},
		{
			name: "one level: folder header inherited, request header appended",
			files: map[string]string{
				"a/b/c/_folder.http": "X-Folder: c\n",
				req:                  "GET https://x.test\nAccept: */*\n",
			},
			wantHeaders: []Header{
				{"X-Folder", "c", Source{"a/b/c/_folder.http", 1}},
				{"Accept", "*/*", Source{req, 2}},
			},
		},
		{
			name: "three levels: nearest wins, case-insensitive, order root→request",
			files: map[string]string{
				"_folder.http":       "X-Root: 1\nAccept: application/json\nX-Trace: root\n",
				"a/_folder.http":     "X-A: 1\naccept: text/plain\n",
				"a/b/c/_folder.http": "X-C: 1\n",
				req:                  "GET https://x.test\nx-trace: req\n",
			},
			wantHeaders: []Header{
				{"X-Root", "1", Source{"_folder.http", 1}},
				{"X-A", "1", Source{"a/_folder.http", 1}},
				{"accept", "text/plain", Source{"a/_folder.http", 2}},
				{"X-C", "1", Source{"a/b/c/_folder.http", 1}},
				{"x-trace", "req", Source{req, 2}},
			},
		},
		{
			name: "!inherit at request removes folder header and is not sent",
			files: map[string]string{
				"_folder.http": "X-Root: 1\nX-Keep: yes\n",
				req:            "GET https://x.test\nX-Root: !inherit\n",
			},
			wantHeaders: []Header{{"X-Keep", "yes", Source{"_folder.http", 2}}},
			wantDisabled: []Disabled{{
				Name: "X-Root", Source: Source{req, 2},
				Removed: []Header{{"X-Root", "1", Source{"_folder.http", 1}}},
			}},
		},
		{
			name: "!inherit at middle folder; request below can redefine",
			files: map[string]string{
				"_folder.http":   "X-Root: 1\n",
				"a/_folder.http": "x-root: !inherit\n",
				"a/b/req.http":   "GET https://x.test\n",
				"a/c/req.http":   "GET https://x.test\nX-Root: again\n",
			},
			wantHeaders: nil, // checked separately below
		},
		{
			name: "!inherit at root with nothing above is a no-op but recorded",
			files: map[string]string{
				"_folder.http": "X-Nothing: !inherit\n",
				req:            "GET https://x.test\n",
			},
			wantHeaders:  []Header{},
			wantDisabled: []Disabled{{Name: "X-Nothing", Source: Source{"_folder.http", 1}}},
		},
		{
			name: "duplicates within one level are all kept; nearer level replaces all",
			files: map[string]string{
				"_folder.http": "Set-Cookie: a=1\nSet-Cookie: b=2\n",
				"a/req.http":   "GET https://x.test\nCookie: x=1\nCookie: y=2\n",
				req:            "GET https://x.test\nSet-Cookie: c=3\n",
			},
			wantHeaders: []Header{{"Set-Cookie", "c=3", Source{req, 2}}},
		},
		{
			name: "auth: folder bearer inherited",
			files: map[string]string{
				"_folder.http": "# @auth bearer {{token}}\n",
				req:            "GET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthBearer, Token: "{{token}}", Source: Source{"_folder.http", 1}},
		},
		{
			name: "auth: none at request over bearer at folder",
			files: map[string]string{
				"_folder.http": "# @auth bearer abc\n",
				req:            "# @auth none\nGET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthNone, Source: Source{req, 1}},
		},
		{
			name: "auth: request beats folder, basic with spaces in password",
			files: map[string]string{
				"_folder.http":   "# @auth none\n",
				"a/_folder.http": "// @auth basic alice s3cret with spaces \n",
				req:              "GET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthBasic, Username: "alice", Password: "s3cret with spaces", Source: Source{"a/_folder.http", 1}},
		},
		{
			name: "auth: basic without password",
			files: map[string]string{
				req: "# @auth basic {{user}}\nGET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthBasic, Username: "{{user}}", Source: Source{req, 1}},
		},
		{
			name: "auth: last directive in a level wins, scheme case-insensitive",
			files: map[string]string{
				req: "# @auth bearer one\n# @auth Bearer two\nGET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthBearer, Token: "two", Source: Source{req, 2}},
		},
		{
			name: "auth: unparseable _folder.http contributes nothing",
			files: map[string]string{
				"_folder.http":   "# @auth bearer root\n",
				"a/_folder.http": "GET https://x.test\nnot a header\n",
				req:              "GET https://x.test\n",
			},
			wantHeaders: []Header{},
			wantAuth:    &Auth{Kind: AuthBearer, Token: "root", Source: Source{"_folder.http", 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "!inherit at middle folder; request below can redefine" {
				c := tree(t, tt.files)
				b, err := Inheritance(c.Find("a/b/req.http"))
				if err != nil {
					t.Fatal(err)
				}
				if len(b.Headers) != 0 {
					t.Errorf("a/b: headers = %v, want none", b.Headers)
				}
				wantDis := []Disabled{{Name: "x-root", Source: Source{"a/_folder.http", 1}, Removed: []Header{{"X-Root", "1", Source{"_folder.http", 1}}}}}
				if !reflect.DeepEqual(b.Disabled, wantDis) {
					t.Errorf("a/b: disabled = %+v, want %+v", b.Disabled, wantDis)
				}
				cEff, err := Inheritance(c.Find("a/c/req.http"))
				if err != nil {
					t.Fatal(err)
				}
				wantH := []Header{{"X-Root", "again", Source{"a/c/req.http", 2}}}
				if !reflect.DeepEqual(cEff.Headers, wantH) {
					t.Errorf("a/c: headers = %v, want %v", cEff.Headers, wantH)
				}
				return
			}
			eff, err := resolveReq(t, tt.files, req)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(eff.Headers, tt.wantHeaders) {
				t.Errorf("headers =\n%+v\nwant\n%+v", eff.Headers, tt.wantHeaders)
			}
			if !reflect.DeepEqual(eff.Disabled, tt.wantDisabled) {
				t.Errorf("disabled =\n%+v\nwant\n%+v", eff.Disabled, tt.wantDisabled)
			}
			if !reflect.DeepEqual(eff.Auth, tt.wantAuth) {
				t.Errorf("auth = %+v, want %+v", eff.Auth, tt.wantAuth)
			}
		})
	}
}

func TestAuthErrors(t *testing.T) {
	tests := []struct {
		name, folder, wantMsg string
	}{
		{"empty", "# @auth\n", "needs a scheme"},
		{"unknown scheme", "# @auth digest x\n", `unknown @auth scheme "digest"`},
		{"bearer without token", "# @auth bearer\n", "exactly one token, got 0"},
		{"bearer with two tokens", "# @auth bearer a b\n", "exactly one token, got 2"},
		{"basic without user", "# @auth basic\n", "needs a username"},
		{"none with args", "# @auth none please\n", "takes no arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveReq(t, map[string]string{
				"a/_folder.http": tt.folder,
				"a/req.http":     "GET https://x.test\n",
			}, "a/req.http")
			if err == nil {
				t.Fatal("expected error")
			}
			rerr, ok := err.(*Error)
			if !ok {
				t.Fatalf("error type %T", err)
			}
			if rerr.Source != (Source{"a/_folder.http", 1}) {
				t.Errorf("source = %v", rerr.Source)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) || !strings.HasPrefix(err.Error(), "a/_folder.http:1: ") {
				t.Errorf("error = %q, want prefix %q and %q", err, "a/_folder.http:1: ", tt.wantMsg)
			}
		})
	}
}

func TestInheritanceRejectsNonRequests(t *testing.T) {
	c := tree(t, map[string]string{
		"a/req.http":  "GET https://x.test\n",
		"a/none.http": "X-Only: header\n",
	})
	if _, err := Inheritance(c.Find("a")); err == nil {
		t.Error("folder node should be rejected")
	}
	if _, err := Inheritance(c.Find("a/none.http")); err == nil {
		t.Error("request-less file should be rejected")
	}
	if _, err := Inheritance(nil); err == nil {
		t.Error("nil should be rejected")
	}
}

func TestEffectiveHeaderLookup(t *testing.T) {
	eff, err := resolveReq(t, map[string]string{
		"_folder.http": "Content-Type: application/json\n",
		"r.http":       "GET https://x.test\n",
	}, "r.http")
	if err != nil {
		t.Fatal(err)
	}
	h, ok := eff.Header("content-type")
	if !ok || h.Value != "application/json" || h.Source.String() != "_folder.http:1" {
		t.Errorf("lookup = %+v %v", h, ok)
	}
	if _, ok := eff.Header("nope"); ok {
		t.Error("unexpected header")
	}
}
