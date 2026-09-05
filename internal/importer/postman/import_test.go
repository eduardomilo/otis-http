package postman

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
)

var update = flag.Bool("update", false, "rewrite golden files")

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "postman", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// render flattens a planned output into one reviewable text: every file
// in path order, then the report.
func render(out *Output) string {
	var b strings.Builder
	for _, rel := range sortedKeys(out.Files) {
		b.WriteString("=== " + rel + " ===\n")
		b.WriteString(out.Files[rel])
		if !strings.HasSuffix(out.Files[rel], "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("=== REPORT ===\n")
	b.WriteString(out.Report.String())
	return b.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", "postman", name+".golden.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestPlanPetstore(t *testing.T) {
	out, err := Plan(read(t, "petstore.postman_collection.json"), read(t, "dev.postman_environment.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "petstore", render(out))

	r := out.Report
	if r.Requests != 13 || r.Folders != 3 || r.Environments != 1 {
		t.Errorf("counts = %d requests, %d folders, %d envs", r.Requests, r.Folders, r.Environments)
	}
	// Secret values from the environment must not appear anywhere.
	for rel, content := range out.Files {
		for _, leak := range []string{"dev-token-should-not-appear", "dev-api-key-should-not-appear", "wJalrXUtnFEMI", "FQoGZXIvYXdz"} {
			if strings.Contains(content, leak) {
				t.Errorf("%s leaks %q", rel, leak)
			}
		}
	}
	if strings.Contains(r.String(), "wJalrXUtnFEMI") {
		t.Error("report leaks the AWS secret key")
	}
}

func TestPlanLegacyShapes(t *testing.T) {
	out, err := Plan(read(t, "legacy-v2.postman_collection.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "legacy-v2", render(out))
}

// TestOutputLoadsAsCollection writes the plan to disk and loads it back with
// the real walker and parser: every .http must parse, the tree must follow
// the .order files, and there must be no collection warnings.
func TestOutputLoadsAsCollection(t *testing.T) {
	dir := t.TempDir()
	report, err := Import(filepath.Join("testdata", "postman", "petstore.postman_collection.json"), Options{
		OutDir:   dir,
		EnvFiles: []string{filepath.Join("testdata", "postman", "dev.postman_environment.json")},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := collection.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("collection warnings: %v", c.Warnings)
	}
	var ids []string
	c.Walk(func(n *collection.Node) bool {
		if n.Kind == collection.KindRequest {
			ids = append(ids, n.ID)
		}
		return true
	})
	want := []string{
		"pets/list-pets.http", "pets/get-pet.http", "pets/create-pet.http", "pets/update-pet-form.http",
		"pets/upload-photo.http", "pets/delete-pet.http",
		"search/graphql-search.http", "search/search.http", "search/search-2.http",
		"infra-aws/invoke-internal-api.http", "infra-aws/s3-object-literal-keys.http",
		"health-check.http", "pets-2.http",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("request order =\n%v\nwant\n%v", ids, want)
	}
	// A request slugged "pets" collides with the "pets" folder only in
	// .order namespace terms; the file is pets-2.http and .order lists both.
	order, err := os.ReadFile(filepath.Join(dir, ".order"))
	if err != nil || string(order) != "pets/\nsearch/\ninfra-aws/\nhealth-check.http\npets-2.http\n" {
		t.Errorf(".order = %q, %v", order, err)
	}
	if report.Requests != 13 {
		t.Errorf("requests = %d", report.Requests)
	}
	for _, f := range report.Files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("reported file missing: %s", f)
		}
	}
	// Inheritance and variables resolve on the generated tree.
	n := c.Find("pets/get-pet.http")
	if n == nil || n.Name != "Get pet" || n.Method != "GET" {
		t.Fatalf("node = %+v", n)
	}
	if v, ok := n.Request.Header("Accept"); ok {
		t.Errorf("unexpected header Accept=%q", v)
	}
	if got := c.Find("pets").Settings.Requests[0].Directives; len(got) != 1 || got[0].Value != "bearer {{token}}" {
		t.Errorf("folder auth = %+v", got)
	}
}

func TestLegacyOutputLoadsCleanly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(filepath.Join("testdata", "postman", "legacy-v2.postman_collection.json"), Options{OutDir: dir}); err != nil {
		t.Fatal(err)
	}
	c, err := collection.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("collection warnings: %v", c.Warnings)
	}
	if n := c.Find("request.http"); n == nil || n.Name != "request" {
		t.Errorf("unnamed request node = %+v", n)
	}
	if n := c.Find("empty-folder"); n == nil || n.Kind != collection.KindFolder || n.Settings == nil {
		t.Errorf("empty folder node = %+v", n)
	}
}

func TestWriteRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.http"), []byte("GET https://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &Output{Files: map[string]string{"a.http": "GET https://a\n"}, Report: &Report{}}
	err := Write(out, dir, false)
	if err == nil || !strings.Contains(err.Error(), "not empty (existing.http)") {
		t.Errorf("err = %v", err)
	}
	if err := Write(out, dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "existing.http")); err != nil {
		t.Error("force removed an unrelated file")
	}
	// Hidden files (.git, .DS_Store) do not count as content.
	dir2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir2, ".git"), 0o755)
	if err := Write(out, dir2, false); err != nil {
		t.Errorf("hidden entries should not block: %v", err)
	}
	if err := Write(out, "", false); err == nil {
		t.Error("empty dir accepted")
	}
}

// docs/FORMAT.md §2.2: an import never rewrites somebody's `.order`.
//
// An import into a fresh directory writes one, because Postman's arrangement
// is the only order information there is and there is nothing to preserve. An
// import into a directory that already holds one is refused — `.order` is the
// one hidden file that counts as content for exactly this reason — so the only
// way to overwrite it is --force, which is a wholesale "write over this
// collection" and says so.
func TestImportDoesNotRewriteAnExistingOrderFile(t *testing.T) {
	dir := t.TempDir()
	mine := []byte("# mine\nb.http\na.http\n")
	if err := os.WriteFile(filepath.Join(dir, ".order"), mine, 0o644); err != nil {
		t.Fatal(err)
	}
	out := &Output{Files: map[string]string{".order": "a.http\nb.http\n"}, Report: &Report{}}

	err := Write(out, dir, false)
	if err == nil || !strings.Contains(err.Error(), "not empty (.order)") {
		t.Fatalf("err = %v, want a refusal naming .order", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ".order")); string(got) != string(mine) {
		t.Errorf(".order changed:\n%s", got)
	}
}

func TestPlanErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"not json", `{`, "invalid JSON"},
		{"not a collection", `{"foo": 1}`, "not a Postman collection export"},
		{"v1 schema", `{"info":{"name":"x","schema":"https://schema.getpostman.com/json/collection/v1.0.0/collection.json"},"item":[]}`, "unsupported schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Plan([]byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
	_, err := Plan([]byte(`{"info":{"name":"x"},"item":[]}`), []byte(`{"nope":1}`))
	if err == nil || !strings.Contains(err.Error(), "not a Postman environment export") {
		t.Errorf("env err = %v", err)
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Get pet":                      "get-pet",
		"  Weird / Name: (chars) & Ü ": "weird-name-chars",
		"UPPER_case_123":               "upper-case-123",
		"---":                          "",
		"":                             "",
		"日本語":                          "",
		"a--b":                         "a-b",
	}
	for in, want := range tests {
		if got := collection.Slug(in); got != want {
			t.Errorf("collection.Slug(%q) = %q, want %q", in, got, want)
		}
	}
	used := map[string]bool{}
	got := []string{collection.UniqueName(used, "", "request"), collection.UniqueName(used, "", "request"), collection.UniqueName(used, "a", "x"), collection.UniqueName(used, "a", "x"), collection.UniqueName(used, "a", "x"), collection.UniqueName(used, "env", "x")}
	want := []string{"request", "request-2", "a", "a-2", "a-3", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uniqueSlug = %v, want %v", got, want)
	}
}

func TestFormEscapeKeepsReferences(t *testing.T) {
	tests := map[string]string{
		"Rex & Co":            "Rex+%26+Co",
		"{{ownerId}}":         "{{ownerId}}",
		"a b {{x}} c&d {{y}}": "a+b+{{x}}+c%26d+{{y}}",
		"broken {{ref":        "broken+%7B%7Bref",
	}
	for in, want := range tests {
		if got := formEscape(in); got != want {
			t.Errorf("formEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommentLinesWrap(t *testing.T) {
	long := strings.Repeat("word ", 30)
	cs := commentLines("Para one.\n\n\n" + long + "\nlast")
	var texts []string
	for _, c := range cs {
		texts = append(texts, c.Text)
	}
	if texts[0] != " Para one." || texts[1] != "" || len(texts) < 4 {
		t.Errorf("comments = %q", texts)
	}
	for _, tx := range texts {
		if len(tx) > 79 {
			t.Errorf("line too long: %q", tx)
		}
	}
	// Round-trips through the parser as comments.
	f, err := httpfile.ParseString((&httpfile.File{Requests: []*httpfile.Request{{Comments: cs, Method: "GET", URL: "https://x"}}}).String())
	if err != nil || len(f.Requests[0].Comments) != len(cs) {
		t.Errorf("round trip: %v", err)
	}
}

func TestSortedKeysDeterministic(t *testing.T) {
	m := map[string]int{"b": 1, "a": 2, "c": 3}
	got := sortedKeys(m)
	want := []string{"a", "b", "c"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedKeys = %v", got)
	}
}

// A script is a hook only if it was translated completely.
//
// This is the invariant the whole import-scripts feature turns on, and it
// replaced a blunter one. Every imported script used to land as `<slug>.post.js`
// beside `<slug>.http` — a hook (docs/FORMAT.md §2.4) — so all of them ran on
// the first send and threw at the first `pm.` they reached, while the header
// said "NOT executed". The fix then was to make no script a hook; the fix now
// is to make a script a hook exactly when running it is safe.
//
// It walks the imported collection with the real classifier rather than
// matching names, because the classifier is the thing that decides, and then
// reads every hook to prove nothing untranslated is in one.
func TestOnlyATranslatedScriptBecomesAHook(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(filepath.Join("testdata", "postman", "petstore.postman_collection.json"), Options{OutDir: dir}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	loaded, err := collection.Load(dir)
	if err != nil {
		t.Fatalf("walking the imported collection: %v", err)
	}

	hooks, modules := 0, 0
	var walk func(*collection.Node)
	walk = func(node *collection.Node) {
		if node.Kind == collection.KindScript {
			text := string(mustRead(t, node.Path))
			if node.Hook {
				hooks++
				// The header is prose about Postman, so only the code below
				// it is checked — everything after the last header line.
				code := text[strings.LastIndex(text, "//")+1:]
				for _, forbidden := range []string{"pm.", "postman.", "require(", "setTimeout", "tests["} {
					if strings.Contains(code, forbidden) {
						t.Errorf("%s runs and still contains %q:\n%s", node.ID, forbidden, text)
					}
				}
			} else {
				modules++
				if !strings.Contains(text, "Nothing runs this file") {
					t.Errorf("%s is a module and does not say so:\n%s", node.ID, text)
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(loaded.Root)

	// Both branches, or the test proves only half of the rule.
	if hooks == 0 {
		t.Error("nothing translated completely, so the hook branch is untested")
	}
	if modules == 0 {
		t.Error("everything translated, so the module branch is untested")
	}
}

// A translated hook says what was substituted, and names the one substitution
// that was a decision rather than a rename.
func TestATranslatedHookSaysWhatChanged(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(filepath.Join("testdata", "postman", "petstore.postman_collection.json"), Options{OutDir: dir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	text := string(mustRead(t, filepath.Join(dir, "pets", "create-pet.post.js")))
	for _, want := range []string{
		"**This runs.**",
		"vars.session.set",
		"response.json",
		// The scope decision, in the file, where the person who has to live
		// with it will read it.
		"vars.env.set",
		"written to no file",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the header does not mention %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "pm.") {
		t.Errorf("a translated hook still contains pm.:\n%s", text)
	}
}

// And a module names the line that stopped it, since that is the step
// somebody has to take.
func TestAnUntranslatedModuleNamesWhatStoppedIt(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(filepath.Join("testdata", "postman", "petstore.postman_collection.json"), Options{OutDir: dir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	text := string(mustRead(t, filepath.Join(dir, "pets", "get-pet.postman-post.js")))
	for _, want := range []string{
		"Nothing runs this file",
		// The blocker, with its line and its reason.
		"line 2, pm.expect",
		"that is chai",
		// And where the finished version goes.
		"get-pet.post.js",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the header does not mention %q:\n%s", want, text)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
