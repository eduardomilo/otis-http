package resolve

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseEnvironment(t *testing.T) {
	env, err := ParseEnvironment("dev", []byte(`{
		"baseUrl": "https://dev.example.com",
		"port": 8443,
		"ratio": 0.50,
		"debug": true,
		"token": {"$secret": "keychain"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]EnvValue{
		"baseUrl": {Value: "https://dev.example.com", Kind: EnvString},
		"port":    {Value: "8443", Kind: EnvNumber},
		"ratio":   {Value: "0.50", Kind: EnvNumber},
		"debug":   {Value: "true", Kind: EnvBool},
		"token":   {Secret: true, Kind: EnvSecret},
	}
	if !reflect.DeepEqual(env.Values, want) {
		t.Errorf("values = %+v\nwant %+v", env.Values, want)
	}
	if env.Path != "env/dev.json" || env.Name != "dev" {
		t.Errorf("env = %+v", env)
	}
	if got := env.Names(); !reflect.DeepEqual(got, []string{"baseUrl", "debug", "port", "ratio", "token"}) {
		t.Errorf("Names = %v", got)
	}
	if got := env.SecretNames(); !reflect.DeepEqual(got, []string{"token"}) {
		t.Errorf("SecretNames = %v", got)
	}
}

func TestParseEnvironmentErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"not json", `{`, "invalid JSON"},
		{"array", `[]`, "invalid JSON"},
		{"trailing content", `{} {}`, "unexpected content"},
		{"null", `{"a": null}`, `key "a": value must be a string, not null`},
		{"array value", `{"a": [1]}`, `key "a": value must be a string, not []interface {}`},
		{"nested object", `{"a": {"b": 1}}`, `key "a": value must be a string or {"$secret": "keychain"}`},
		{"secret with extra keys", `{"a": {"$secret": "keychain", "x": 1}}`, `key "a": value must be a string or`},
		{"unknown backend", `{"a": {"$secret": "vault"}}`, `key "a": unsupported $secret backend vault`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseEnvironment("x", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadAndListEnvironments(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadEnvironment(dir, "dev"); err == nil || !strings.Contains(err.Error(), `environment "dev" not found (expected env/dev.json)`) {
		t.Errorf("missing env error = %v", err)
	}
	if names, err := ListEnvironments(dir); err != nil || names != nil {
		t.Errorf("list without env/ = %v %v", names, err)
	}
	envDir := filepath.Join(dir, "env")
	if err := os.MkdirAll(filepath.Join(envDir, "ignored-dir.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"prod.json": `{"a":"1"}`, "dev.json": `{"a":"2"}`, "notes.txt": "x", ".hidden.json": "{}", "bad.json": "{"} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ListEnvironments(dir)
	if err != nil || !reflect.DeepEqual(names, []string{"bad", "dev", "prod"}) {
		t.Errorf("list = %v %v", names, err)
	}
	env, err := LoadEnvironment(dir, "dev")
	if err != nil || env.Values["a"].Value != "2" {
		t.Errorf("load dev = %+v %v", env, err)
	}
	if _, err := LoadEnvironment(dir, "bad"); err == nil || !strings.HasPrefix(err.Error(), "env/bad.json: invalid JSON") {
		t.Errorf("bad env error = %v", err)
	}
	for _, bad := range []string{"", "../dev", "a/b", `a\b`, "."} {
		if _, err := LoadEnvironment(dir, bad); err == nil || !strings.Contains(err.Error(), "invalid environment name") {
			t.Errorf("%q: error = %v", bad, err)
		}
	}
}

// The round-trip guarantee docs/FORMAT.md §4.3 makes for an environment file,
// which is the same bargain §1.13 makes for a .http file: parsing a canonical
// file and writing it back produces identical bytes. Without it, changing one
// variable reshuffles every key and turns 8443 into "8443".
func TestEnvironmentMarshalRoundTripsCanonicalBytes(t *testing.T) {
	canonical := `{
  "$otis": {"confirmBeforeSend":true,"description":"production"},
  "baseUrl": "https://prod.example.com",
  "port": 8443,
  "ratio": 0.50,
  "debug": true,
  "token": {"$secret": "keychain"},
  "query": "a=1&b=2"
}
`
	env, err := ParseEnvironment("prod", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(env.Marshal()); got != canonical {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, canonical)
	}
	// A second pass must be a fixed point too.
	again, err := ParseEnvironment("prod", env.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(again.Marshal()); got != canonical {
		t.Errorf("second Marshal =\n%s\nwant\n%s", got, canonical)
	}
}

// A URL's & must survive as an &: encoding/json escapes HTML by default, which
// would rewrite every query string in a file people read in review.
func TestEnvironmentMarshalDoesNotEscapeHTML(t *testing.T) {
	env := &Environment{
		Name:   "dev",
		Values: map[string]EnvValue{"url": {Value: "https://x.test/?a=1&b=<2>"}},
		Order:  []string{"url"},
	}
	if got := string(env.Marshal()); !strings.Contains(got, `"https://x.test/?a=1&b=<2>"`) {
		t.Errorf("Marshal = %s", got)
	}
}

// A variable the editor adds goes at the end, where somebody adding one by
// hand would put it — it does not reshuffle the file.
func TestEnvironmentMarshalAppendsNewKeysAfterTheFileOrder(t *testing.T) {
	env, err := ParseEnvironment("dev", []byte("{\n  \"zeta\": \"1\",\n  \"alpha\": \"2\"\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	env.Values["mid"] = EnvValue{Value: "3"}
	env.Values["new"] = EnvValue{Value: "4"}
	want := `{
  "zeta": "1",
  "alpha": "2",
  "mid": "3",
  "new": "4"
}
`
	if got := string(env.Marshal()); got != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, want)
	}
}

// $otis is settings, not a variable: it must not appear as a resolvable name,
// and it must not be written back when it holds nothing.
func TestEnvironmentMetaIsNotAVariable(t *testing.T) {
	env, err := ParseEnvironment("prod", []byte(`{"$otis": {"confirmBeforeSend": true}, "host": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !env.Meta.ConfirmBeforeSend {
		t.Error("ConfirmBeforeSend was not read")
	}
	if _, ok := env.Values[MetaKey]; ok {
		t.Error("$otis leaked into Values")
	}
	if got := env.Names(); !reflect.DeepEqual(got, []string{"host"}) {
		t.Errorf("Names = %v, want just the variable", got)
	}

	env.Meta = EnvMeta{}
	if got := string(env.Marshal()); strings.Contains(got, MetaKey) {
		t.Errorf("an empty $otis was written: %s", got)
	}
}

// The "$" namespace is reserved so a later version can add a second settings
// key without migrating any collection.
func TestEnvironmentRejectsOtherReservedKeys(t *testing.T) {
	for _, key := range []string{"$other", "$", "$secret"} {
		if _, err := ParseEnvironment("dev", []byte(`{"`+key+`": "x"}`)); err == nil {
			t.Errorf("ParseEnvironment with the key %q should fail", key)
		}
	}
}

// A "$otis" field this version does not know is preserved, not dropped: the
// same bargain §1.4 makes for an unknown directive. Otherwise opening a
// collection in an older Otis and changing one variable would silently delete
// a setting a newer one wrote.
func TestEnvironmentPreservesUnknownMetaFields(t *testing.T) {
	env, err := ParseEnvironment("prod", []byte(
		`{"$otis": {"confirmBeforeSend": true, "fromTheFuture": {"a": [1,2]}}, "host": "x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !env.Meta.ConfirmBeforeSend {
		t.Error("the known field was not read")
	}
	if len(env.Meta.Extra) != 1 {
		t.Fatalf("Extra = %v, want the unknown field kept", env.Meta.Extra)
	}

	// Changing a variable must not cost the unknown field.
	env.Values["host"] = EnvValue{Value: "y", Kind: EnvString}
	out := string(env.Marshal())
	if !strings.Contains(out, `"fromTheFuture":{"a": [1,2]}`) {
		t.Errorf("the unknown field was dropped:\n%s", out)
	}
	if !strings.Contains(out, `"confirmBeforeSend":true`) {
		t.Errorf("the known field was dropped:\n%s", out)
	}

	// And it survives a second round trip unchanged.
	again, err := ParseEnvironment("prod", []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Marshal()) != out {
		t.Errorf("not a fixed point:\n%s\n%s", again.Marshal(), out)
	}
}

func TestEnvironmentMarshalIsEmptyObjectForNoValues(t *testing.T) {
	env := &Environment{Name: "dev", Values: map[string]EnvValue{}}
	if got := string(env.Marshal()); got != "{\n}\n" {
		t.Errorf("Marshal = %q", got)
	}
}
