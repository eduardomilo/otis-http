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
		"baseUrl": {Value: "https://dev.example.com"},
		"port":    {Value: "8443"},
		"ratio":   {Value: "0.50"},
		"debug":   {Value: "true"},
		"token":   {Secret: true},
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
