package resolve

import (
	"testing"
	"time"
)

func TestFolderChain(t *testing.T) {
	cases := []struct {
		id   string
		want []string
	}{
		{"get.http", []string{""}},
		{"orders/create.http", []string{"orders", ""}},
		{"orders/fixtures/seed.http", []string{"orders/fixtures", "orders", ""}},
	}
	for _, tc := range cases {
		if got := FolderChain(tc.id); !equalStrings(got, tc.want) {
			t.Errorf("FolderChain(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestSessionStore(t *testing.T) {
	s := NewSession()
	at := time.Date(2026, 9, 2, 14, 32, 7, 0, time.UTC)
	s.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "orderId", Value: "ord_1", Origin: "orders/create.http", At: at})
	s.Set(SessionValue{Scope: SessionEnv, Owner: "staging", Name: "cursor", Value: "abc"})

	v, ok := s.Get(SessionFolder, "orders", "orderId")
	if !ok || v.Value != "ord_1" || v.Origin != "orders/create.http" || !v.At.Equal(at) {
		t.Errorf("Get = %+v, %v; want the value with its origin and time", v, ok)
	}
	// A value with no time gets one, so "when" is always answerable.
	if cursor, _ := s.Get(SessionEnv, "staging", "cursor"); cursor.At.IsZero() {
		t.Error("Set left At zero; a session value with no time has no account of itself")
	}
	// Setting again replaces rather than accumulating.
	s.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "orderId", Value: "ord_2"})
	if v, _ := s.Get(SessionFolder, "orders", "orderId"); v.Value != "ord_2" {
		t.Errorf("value = %q after a second Set, want ord_2", v.Value)
	}
	if got := len(s.List()); got != 2 {
		t.Errorf("List has %d values, want 2", got)
	}

	if !s.Delete(SessionFolder, "orders", "orderId") {
		t.Error("Delete reported nothing removed")
	}
	if s.Delete(SessionFolder, "orders", "orderId") {
		t.Error("Delete removed the same value twice")
	}
	s.Clear()
	if got := s.List(); len(got) != 0 {
		t.Errorf("Clear left %d values", len(got))
	}
}

func TestSessionClearScope(t *testing.T) {
	s := NewSession()
	s.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "a", Value: "1"})
	s.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "b", Value: "2"})
	s.Set(SessionValue{Scope: SessionFolder, Owner: "customers", Name: "a", Value: "3"})
	s.Set(SessionValue{Scope: SessionEnv, Owner: "staging", Name: "a", Value: "4"})

	if removed := s.ClearScope(SessionFolder, "orders"); removed != 2 {
		t.Errorf("ClearScope removed %d, want 2", removed)
	}
	if got := len(s.List()); got != 2 {
		t.Errorf("%d values left, want 2", got)
	}
	if _, ok := s.Get(SessionFolder, "customers", "a"); !ok {
		t.Error("ClearScope took another folder's value")
	}
}

// The nil store is usable: a collection open with no run yet has no session.
func TestNilSessionIsUsable(t *testing.T) {
	var s *Session
	s.Set(SessionValue{Name: "a"})
	if _, ok := s.Get(SessionFolder, "", "a"); ok {
		t.Error("a nil session returned a value")
	}
	if got := s.List(); got != nil {
		t.Errorf("List = %v, want nil", got)
	}
	s.Clear()
}

// A session value shadows the committed declaration at the same level, and a
// nearer folder's beats a further one's — the format's own "nearest definition
// wins", applied to the new layer.
func TestSessionValueShadowsTheCommittedOne(t *testing.T) {
	files := map[string]string{
		"_folder.http":              "@currency = eur\n@region = eu\n",
		"orders/_folder.http":       "@currency = usd\n",
		"orders/fixtures/seed.http": "POST https://example.test/orders\n",
	}
	levels := levelsFor(t, files, "orders/fixtures/seed.http")
	session := NewSession()
	session.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "currency", Value: "gbp"})
	session.Set(SessionValue{Scope: SessionFolder, Owner: "", Name: "region", Value: "us"})

	scope := NewScope(levels, nil, nil, "c").
		WithSession(session, FolderChain("orders/fixtures/seed.http"))
	x := NewExpander(scope)
	got, err := x.Expand("{{currency}} {{region}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gbp us" {
		t.Errorf("Expand = %q, want %q", got, "gbp us")
	}
	for _, u := range x.Uses() {
		if u.Origin != OriginSession {
			t.Errorf("%s came from %q, want session", u.Name, u.Origin)
		}
		if u.Source.Path == "" {
			t.Errorf("%s has no source; a session value is the one value in no file", u.Name)
		}
	}
}

// A nearer folder's committed value still beats a further folder's session
// value: the layers interleave per level rather than stacking.
func TestNearerFileBeatsFurtherSession(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"_folder.http":        "@currency = eur\n",
		"orders/_folder.http": "@currency = usd\n",
		"orders/create.http":  "POST https://example.test/orders\n",
	}, "orders/create.http")
	session := NewSession()
	session.Set(SessionValue{Scope: SessionFolder, Owner: "", Name: "currency", Value: "gbp"})

	scope := NewScope(levels, nil, nil, "c").WithSession(session, FolderChain("orders/create.http"))
	got, err := NewExpander(scope).Expand("{{currency}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "usd" {
		t.Errorf("Expand = %q, want usd: orders/_folder.http is nearer than the root's session value", got)
	}
}

// The request file's own @var beats every session value: it is the nearest
// scope there is.
func TestRequestDeclarationBeatsSession(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"orders/create.http": "@currency = jpy\nPOST https://example.test/orders\n",
	}, "orders/create.http")
	session := NewSession()
	session.Set(SessionValue{Scope: SessionFolder, Owner: "orders", Name: "currency", Value: "gbp"})

	scope := NewScope(levels, nil, nil, "c").WithSession(session, FolderChain("orders/create.http"))
	got, err := NewExpander(scope).Expand("{{currency}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "jpy" {
		t.Errorf("Expand = %q, want jpy", got)
	}
}

// An environment-scope session value beats the committed environment file.
func TestSessionEnvValueShadowsTheEnvironmentFile(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"get.http": "GET https://example.test/\n",
	}, "get.http")
	env := &Environment{Name: "staging", Path: "env/staging.json", Values: map[string]EnvValue{
		"cursor": {Value: "page1"},
	}}
	session := NewSession()
	session.Set(SessionValue{Scope: SessionEnv, Owner: "staging", Name: "cursor", Value: "page9"})

	scope := NewScope(levels, env, nil, "c").WithSession(session, FolderChain("get.http"))
	got, err := NewExpander(scope).Expand("{{cursor}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "page9" {
		t.Errorf("Expand = %q, want page9", got)
	}
}

// A session value is data a run produced, not a template. "{{" arriving in a
// response must not reach into the variable scope.
func TestSessionValueIsNotExpanded(t *testing.T) {
	levels := levelsFor(t, map[string]string{
		"get.http": "@secretish = do-not-leak\nGET https://example.test/\n",
	}, "get.http")
	session := NewSession()
	session.Set(SessionValue{Scope: SessionFolder, Owner: "", Name: "echo", Value: "{{secretish}}"})

	scope := NewScope(levels, nil, nil, "c").WithSession(session, FolderChain("get.http"))
	got, err := NewExpander(scope).Expand("{{echo}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "{{secretish}}" {
		t.Errorf("Expand = %q, want the value verbatim", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
