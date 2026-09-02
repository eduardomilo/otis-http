package collection

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGolden loads every tree under testdata/collections and compares the
// JSON view (tree shape + warnings) with <name>.golden.json.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "collections", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixture trees: %v", err)
	}
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			c, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.MarshalIndent(c, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join("testdata", "collections", name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func TestBasicTree(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "collections", "basic"))
	if err != nil {
		t.Fatal(err)
	}
	// Listed entries first in .order sequence, then unlisted alphabetically.
	// "users/" is listed twice (duplicate warning), "missing.http" does not
	// exist, "bare" matches bare.http by convenience.
	wantIDs := []string{"users", "auth", "zeta.http", "bare.http", "alpha.http", "beta"}
	if got := childIDs(c.Root); !reflect.DeepEqual(got, wantIDs) {
		t.Errorf("root children = %v, want %v", got, wantIDs)
	}
	// No .order in users/: case-insensitive alphabetical.
	wantUsers := []string{"users/broken.http", "users/Create.http", "users/delete.http", "users/list.http", "users/multi.http"}
	if got := childIDs(c.Find("users")); !reflect.DeepEqual(got, wantUsers) {
		t.Errorf("users children = %v, want %v", got, wantUsers)
	}

	// Root settings and parent links.
	if c.Root.Settings == nil || c.Root.Settings.Requests[0].Variables[0].Name != "baseUrl" {
		t.Errorf("root _folder.http not loaded: %+v", c.Root.Settings)
	}
	login := c.Find("auth/login.http")
	if login == nil {
		t.Fatal("auth/login.http not found")
	}
	if login.Name != "Login" || login.Method != "POST" || login.Request == nil || login.Parent.ID != "auth" {
		t.Errorf("login node = %+v", login)
	}
	if anc := login.Ancestors(); len(anc) != 2 || anc[0].ID != "auth" || anc[1] != c.Root {
		t.Errorf("ancestors = %v", anc)
	}
	if c.Find("auth").Settings == nil {
		t.Error("auth/_folder.http not loaded")
	}

	// Names: @name, then ### title, then file name.
	for id, want := range map[string]string{
		"zeta.http":        "Zeta first by .order",
		"bare.http":        "Matched by a bare .order entry",
		"alpha.http":       "alpha",
		"users/multi.http": "First of two",
	} {
		if got := c.Find(id).Name; got != want {
			t.Errorf("%s: name = %q, want %q", id, got, want)
		}
	}

	// Broken file still appears.
	broken := c.Find("users/broken.http")
	if broken == nil || !broken.Broken || broken.Method != "" || !strings.Contains(broken.Error, "line 2") {
		t.Errorf("broken node = %+v", broken)
	}

	// Ignored: non-.http files, hidden entries, env/ at the root.
	for _, id := range []string{"README.md", "env", ".hidden.http", ".hidden-dir"} {
		if c.Find(id) != nil {
			t.Errorf("%s should not be in the tree", id)
		}
	}

	wantWarnings := []Warning{
		{".order", WarnOrderMissing, `line 7: "missing.http" does not exist`},
		{".order", WarnOrderDuplicate, `line 8: "users/" is listed more than once`},
		{"users/broken.http", WarnParseError, `line 2: expected header ("Name: value") or blank line before body, got "this is not a header"`},
		{"users/multi.http", WarnMultipleRequests, "file contains 2 requests; only the first is used"},
	}
	if !reflect.DeepEqual(c.Warnings, wantWarnings) {
		t.Errorf("warnings =\n%v\nwant\n%v", c.Warnings, wantWarnings)
	}

	if got := len(c.Requests()); got != 11 {
		t.Errorf("Requests() = %d nodes, want 11", got)
	}
}

func TestFolderIssues(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "collections", "folder-issues"))
	if err != nil {
		t.Fatal(err)
	}
	wantWarnings := []Warning{
		{"_folder.http", WarnFolderHasRequest, "line 1: GET https://should-not-be-here.test ignored; folder files hold settings only"},
		{"settings-only.http", WarnNoRequestLine, "file contains no request"},
		{"sub/_folder.http", WarnParseError, `line 2: expected header ("Name: value") or blank line before body, got "not a header"`},
	}
	if !reflect.DeepEqual(c.Warnings, wantWarnings) {
		t.Errorf("warnings =\n%v\nwant\n%v", c.Warnings, wantWarnings)
	}
	sub := c.Find("sub")
	if sub.Settings != nil || sub.SettingsPath == "" {
		t.Errorf("unparseable _folder.http: Settings=%v SettingsPath=%q", sub.Settings, sub.SettingsPath)
	}
	so := c.Find("settings-only.http")
	if so.Broken || so.Request != nil || so.File == nil {
		t.Errorf("settings-only node = %+v", so)
	}
}

func TestNoOrderIsCaseInsensitiveAlphabetical(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "collections", "no-order"))
	if err != nil {
		t.Fatal(err)
	}
	// Lower-cased: alpha.http < b-folder < beta.http < delta.http < gamma.
	want := []string{"alpha.http", "b-folder", "Beta.http", "delta.http", "Gamma"}
	if got := childIDs(c.Root); !reflect.DeepEqual(got, want) {
		t.Errorf("children = %v, want %v", got, want)
	}
}

func TestLessName(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"a", "B", true},
		{"B", "a", false},
		{"A", "a", true}, // equal ignoring case: byte order breaks the tie
		{"a", "A", false},
		{"a", "a", false},
		{"a-x", "a.x", true},
	}
	for _, tt := range tests {
		if got := lessName(tt.a, tt.b); got != tt.want {
			t.Errorf("lessName(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestEmptyAndErrors(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "collections", "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Root.Children) != 0 || len(c.Warnings) != 0 || c.Root.Name != "empty" {
		t.Errorf("empty collection = %+v", c)
	}
	if _, err := Load(filepath.Join("testdata", "collections", "does-not-exist")); err == nil {
		t.Error("expected error for missing directory")
	}
	if _, err := Load(filepath.Join("testdata", "collections", "basic", "zeta.http")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory', got %v", err)
	}
}

func TestParseOrder(t *testing.T) {
	o, err := ParseOrder(strings.NewReader("\ufeff# comment\n\n  users/  \nlogin.http\n#another\nbare\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []OrderEntry{{"users/", 3}, {"login.http", 4}, {"bare", 6}}
	if !reflect.DeepEqual(o.Entries, want) {
		t.Errorf("entries = %v, want %v", o.Entries, want)
	}
}

func TestApplyOrder(t *testing.T) {
	listing := []dirEntry{{"a.http", false}, {"b", true}, {"b.http", false}, {"c", true}, {"D.http", false}}
	tests := []struct {
		name       string
		order      []string
		want       []string
		missing    []string
		duplicates []string
	}{
		{"no order", nil, []string{"a.http", "b", "b.http", "c", "D.http"}, nil, nil},
		{"exact keys", []string{"c/", "D.http"}, []string{"c", "D.http", "a.http", "b", "b.http"}, nil, nil},
		{"bare prefers file over dir", []string{"b"}, []string{"b.http", "a.http", "b", "c", "D.http"}, nil, nil},
		{"bare falls back to dir", []string{"c"}, []string{"c", "a.http", "b", "b.http", "D.http"}, nil, nil},
		{"file key does not match dir", []string{"c.http", "b/"}, []string{"b", "a.http", "b.http", "c", "D.http"}, []string{"c.http"}, nil},
		{"dir key does not match file", []string{"a.http/"}, []string{"a.http", "b", "b.http", "c", "D.http"}, []string{"a.http/"}, nil},
		{"duplicate", []string{"a.http", "a", "a.http"}, []string{"a.http", "b", "b.http", "c", "D.http"}, nil, []string{"a", "a.http"}},
		{"missing", []string{"nope", "zzz/"}, []string{"a.http", "b", "b.http", "c", "D.http"}, []string{"nope", "zzz/"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o Order
			for i, n := range tt.order {
				o.Entries = append(o.Entries, OrderEntry{Name: n, Line: i + 1})
			}
			res := applyOrder(listing, o)
			var got []string
			for _, e := range res.entries {
				got = append(got, e.name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("order = %v, want %v", got, tt.want)
			}
			if got := names(res.missing); !reflect.DeepEqual(got, tt.missing) {
				t.Errorf("missing = %v, want %v", got, tt.missing)
			}
			if got := names(res.duplicates); !reflect.DeepEqual(got, tt.duplicates) {
				t.Errorf("duplicates = %v, want %v", got, tt.duplicates)
			}
		})
	}
}

// TestLoadDoesNotWrite guards the "never rewrite .order" rule: loading a
// collection must not create or modify any file.
func TestLoadDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.http"), []byte("GET https://x.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".order"), []byte("missing.http\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("Load modified the directory:\nbefore %v\nafter  %v", before, after)
	}
}

func childIDs(n *Node) []string {
	var ids []string
	for _, c := range n.Children {
		ids = append(ids, c.ID)
	}
	return ids
}

func names(es []OrderEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

// writeTree lays out a collection in a temp directory from a map of
// collection-relative paths to contents.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func snapshot(t *testing.T, dir string) map[string]string {
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"/code/acme-api", "acme-api"},
		{"/code/acme-api/.requests", "acme-api"}, // the design's example
		{"/code/acme-api/http", "http"},
		{"/.requests", ".requests"}, // no useful parent: keep the base
		{"/code/.a/.b", ".a"},
	}
	for _, c := range cases {
		if got := DisplayName(filepath.FromSlash(c.dir)); got != c.want {
			t.Errorf("DisplayName(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
	// This name is also the collection component of a secret's key
	// (docs/FORMAT.md §5), so two projects using the ".requests" convention
	// must not collapse onto one key. That collision is the whole reason the
	// rule walks up out of a dot-directory.
	a := DisplayName(filepath.FromSlash("/code/acme-api/.requests"))
	b := DisplayName(filepath.FromSlash("/code/other-api/.requests"))
	if a == b {
		t.Errorf("two collections share the secrets key %q", a)
	}
}

// Script files are tree rows: a file in the collection that changes what
// requests do must not be invisible (docs/FORMAT.md §2.4).
func TestScriptsAreTreeRows(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"orders/_folder.http":         "Accept: application/json\n",
		"orders/_pre.js":              "vars.request.set(\"idemKey\", 1);\n",
		"orders/_post.js":             "vars.folder.set(\"orderId\", 1);\n",
		"orders/create-order.http":    "POST https://x.test/orders\n",
		"orders/create-order.pre.js":  "// runs around create-order only\n",
		"orders/create-order.post.js": "// and after it\n",
		// A .pre.js naming no request is a module with an unfortunate name.
		"orders/utils.pre.js": "export const x = 1;\n",
		"lib/idempotency.js":  "export function idempotencyKey() {}\n",
		"lib/assert.js":       "export function ok() {}\n",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("warnings = %v", c.Warnings)
	}

	byID := map[string]*Node{}
	c.Walk(func(n *Node) bool {
		byID[n.ID] = n
		return true
	})

	cases := []struct {
		id     string
		hook   bool
		hookOf string
	}{
		{"orders/_pre.js", true, ""},
		{"orders/_post.js", true, ""},
		{"orders/create-order.pre.js", true, "orders/create-order.http"},
		{"orders/create-order.post.js", true, "orders/create-order.http"},
		{"orders/utils.pre.js", false, ""},
		{"lib/idempotency.js", false, ""},
		{"lib/assert.js", false, ""},
	}
	for _, c := range cases {
		node := byID[c.id]
		if node == nil {
			t.Errorf("%s is not in the tree", c.id)
			continue
		}
		if node.Kind != KindScript {
			t.Errorf("%s kind = %q, want script", c.id, node.Kind)
		}
		if node.Hook != c.hook {
			t.Errorf("%s hook = %v, want %v", c.id, node.Hook, c.hook)
		}
		if node.HookOf != c.hookOf {
			t.Errorf("%s hookOf = %q, want %q", c.id, node.HookOf, c.hookOf)
		}
	}

	// A script is not a request, so Requests() does not list one.
	for _, n := range c.Requests() {
		if strings.HasSuffix(n.ID, ScriptExt) {
			t.Errorf("Requests() includes the script %s", n.ID)
		}
	}
	// _folder.http is still settings rather than a row (§2.1).
	if _, ok := byID["orders/_folder.http"]; ok {
		t.Error("_folder.http became a tree row")
	}
}

// .order names scripts like anything else, and unlisted ones still sort
// alphabetically after the listed entries (§2.2).
func TestScriptsParticipateInOrdering(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"orders/.order":            "_pre.js\ncreate-order.http\n",
		"orders/_pre.js":           "// first\n",
		"orders/create-order.http": "POST https://x.test/orders\n",
		"orders/aaa.js":            "// unlisted, sorts after\n",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	folder := c.Find("orders")
	if folder == nil {
		t.Fatal("no orders folder")
	}
	var names []string
	for _, ch := range folder.Children {
		names = append(names, ch.Name)
	}
	// A request's display name drops the .http (§2.1); a script keeps its
	// extension, which is what screen 3a shows and what tells a reader at a
	// glance that the row is not a request.
	want := []string{"_pre.js", "create-order", "aaa.js"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("children = %v, want %v", names, want)
	}
}
