package resolve

import (
	"testing"

	"github.com/otis-http/otis/internal/secrets"
)

// levelsFor loads a collection and returns the inheritance chain of one node.
func levelsFor(t *testing.T, files map[string]string, id string) []Level {
	t.Helper()
	c := tree(t, files)
	n := c.Find(id)
	if n == nil {
		t.Fatalf("node %q not found", id)
	}
	return LevelsOf(n)
}

func TestInheritedHeaders(t *testing.T) {
	// Three levels, so every fate appears: X-Tenant is redefined halfway down
	// and then disabled at the request, Accept is overridden twice, and
	// X-Trace comes all the way through.
	levels := levelsFor(t, map[string]string{
		"_folder.http": "X-Tenant: root\n" +
			"Accept: text/plain\n" +
			"X-Trace: on\n",
		"orders/_folder.http": "X-Tenant: orders\n" +
			"Accept: application/json\n",
		"orders/create.http": "POST https://example.test/orders\n" +
			"Accept: application/vnd.acme+json\n" +
			"X-Tenant: !inherit\n",
	}, "orders/create.http")

	got := InheritedHeaders(levels)
	type row struct {
		name, value, source string
		state               HeaderState
		by                  string
	}
	var rows []row
	for _, in := range got {
		by := ""
		if in.By != nil {
			by = in.By.Path
		}
		rows = append(rows, row{in.Name, in.Value, in.Source.Path, in.State, by})
	}

	want := []row{
		// Root level, in file order.
		{"X-Tenant", "root", "_folder.http", HeaderOverridden, "orders/_folder.http"},
		{"Accept", "text/plain", "_folder.http", HeaderOverridden, "orders/_folder.http"},
		{"X-Trace", "on", "_folder.http", HeaderSent, ""},
		// orders/, whose own X-Tenant is then disabled by the request and
		// whose Accept is overridden by it.
		{"X-Tenant", "orders", "orders/_folder.http", HeaderOff, "orders/create.http"},
		{"Accept", "application/json", "orders/_folder.http", HeaderOverridden, "orders/create.http"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d inherited rows, want %d:\n%+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}

	// Only X-Trace survives to the wire from above, so what Effective says and
	// what these states say must agree.
	eff, err := Levels(levels)
	if err != nil {
		t.Fatal(err)
	}
	sent := map[string]bool{}
	for _, h := range eff.Headers {
		if h.Source.Path != "orders/create.http" {
			sent[h.Name] = true
		}
	}
	for _, in := range got {
		if (in.State == HeaderSent) != sent[in.Name] {
			t.Errorf("%s: state %q disagrees with the effective set", in.Name, in.State)
		}
	}
}

// A request's own headers are local, never inherited, even though they decide
// the fate of the ones above them.
func TestInheritedHeadersExcludesTheRequestsOwn(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"get.http": "GET https://example.test/\nX-Local: 1\n",
	}, "get.http")
	if got := InheritedHeaders(levels); len(got) != 0 {
		t.Errorf("InheritedHeaders = %+v, want none: nothing above declares a header", got)
	}
}

func TestAncestorAuthIgnoresTheRequestsOwn(t *testing.T) {
	files := map[string]string{
		"_folder.http":        "# @auth basic root secret\n",
		"orders/_folder.http": "# @auth bearer {{apiKey}}\n",
		"orders/create.http":  "# @auth none\nPOST https://example.test/orders\n",
	}
	levels := levelsFor(t, files, "orders/create.http")

	// The request opts out, so the effective auth is none...
	eff, err := Levels(levels)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Auth == nil || eff.Auth.Kind != AuthNone {
		t.Fatalf("effective auth = %+v, want none", eff.Auth)
	}
	// ...but the nearest folder's auth is still what the Auth tab shows under
	// "Inherit from folder" and what Override prefills from.
	ancestor := AncestorAuth(levels)
	if ancestor == nil || ancestor.Kind != AuthBearer || ancestor.Token != "{{apiKey}}" {
		t.Errorf("AncestorAuth = %+v, want the orders/ bearer", ancestor)
	}
	if ancestor.Source.Path != "orders/_folder.http" {
		t.Errorf("AncestorAuth source = %q, want orders/_folder.http", ancestor.Source.Path)
	}
}

func TestAncestorAuthIsNilWithNoFoldersAbove(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"get.http": "# @auth bearer abc\nGET https://example.test/\n",
	}, "get.http")
	if got := AncestorAuth(levels); got != nil {
		t.Errorf("AncestorAuth = %+v, want nil", got)
	}
}

// A file variable that expands to a secret is that secret under another name,
// so its Use must carry no value: Use is serialized to the frontend and to
// `otis run --json`.
func TestUseOfAVariableResolvingToASecretCarriesNoValue(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"get.http": "@token = {{apiKey}}\n" +
			"@header = Bearer {{token}}\n" +
			"GET https://example.test/\n",
	}, "get.http")

	store := stubStore{"c/staging/apiKey": "sk-live-do-not-print"}
	env := &Environment{Name: "staging", Path: "env/staging.json", Values: map[string]EnvValue{
		"apiKey": {Secret: true},
	}}
	x := NewExpander(NewScope(levels, env, store, "c"))
	out, err := x.Expand("{{header}}")
	if err != nil {
		t.Fatal(err)
	}
	// The expansion itself is the real value: that is what goes on the wire.
	if out != "Bearer sk-live-do-not-print" {
		t.Fatalf("Expand = %q, want the resolved header", out)
	}
	for _, u := range x.Uses() {
		if !u.Secret {
			t.Errorf("%s = %+v, want Secret", u.Name, u)
		}
		if u.Value != "" {
			t.Errorf("%s carries the value %q", u.Name, u.Value)
		}
	}
	// A plain variable is unaffected: its value is already in the repo.
	x2 := NewExpander(NewScope(levelsFor(t, map[string]string{
		"get.http": "@plain = hello\nGET https://example.test/\n",
	}, "get.http"), nil, nil, "c"))
	if _, err := x2.Expand("{{plain}}"); err != nil {
		t.Fatal(err)
	}
	if got := x2.Uses(); len(got) != 1 || got[0].Value != "hello" || got[0].Secret {
		t.Errorf("uses = %+v, want one plain use with its value", got)
	}
}

// stubStore is a secrets.Store with fixed contents.
type stubStore map[string]string

func (s stubStore) Get(key string) (string, error) {
	v, ok := s[key]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (s stubStore) Set(key, value string) error { s[key] = value; return nil }
func (s stubStore) Delete(key string) error     { delete(s, key); return nil }
func (s stubStore) List(string) ([]string, error) {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	return keys, nil
}
