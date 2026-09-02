package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// newEnvService opens the shared fixture and returns the environment service
// over it, with an in-memory secret store standing in for the keychain. Tests
// must never touch the real one: on macOS it would prompt, and a prompt in a
// test run is a hang rather than a failure.
func newEnvService(t *testing.T) (*EnvironmentService, *CollectionService, *secrets.Memory, string) {
	t.Helper()
	root := fixture(t)
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	vault := secrets.NewMemory()
	return NewEnvironmentService(collections, store, vault), collections, vault, root
}

func TestEnvironmentListAndActivate(t *testing.T) {
	env, _, _, root := newEnvService(t)
	write(t, filepath.Join(root, "env", "prod.json"),
		`{"$otis": {"confirmBeforeSend": true}, "baseUrl": "https://prod.example.test", "token": {"$secret": "keychain"}}`)

	list, err := env.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %+v, want prod and staging", list.Items)
	}
	if list.Active != "" {
		t.Errorf("Active = %q, want none before one is chosen", list.Active)
	}
	prod := list.Items[0]
	if prod.Name != "prod" || prod.Path != "env/prod.json" {
		t.Errorf("prod = %+v", prod)
	}
	if prod.Variables != 2 || prod.Secrets != 1 {
		t.Errorf("prod counts = %d variables, %d secrets, want 2 and 1", prod.Variables, prod.Secrets)
	}
	if !prod.ConfirmBeforeSend {
		t.Error("prod should confirm before send")
	}

	after, err := env.Activate("prod")
	if err != nil {
		t.Fatal(err)
	}
	if after.Active != "prod" {
		t.Errorf("Active = %q, want prod", after.Active)
	}
	if !after.Items[0].Active {
		t.Error("the prod row should be marked active")
	}

	// An environment that is not there must be refused, or every request
	// would resolve against a file that does not exist.
	if _, err := env.Activate("nope"); err == nil {
		t.Error("activating a missing environment should fail")
	}
	if list, _ := env.List(); list.Active != "prod" {
		t.Errorf("Active = %q, want prod to survive a refused switch", list.Active)
	}

	if _, err := env.Activate(""); err != nil {
		t.Fatal(err)
	}
	if list, _ := env.List(); list.Active != "" {
		t.Error("Activate(\"\") should deactivate")
	}
}

// A collection without env/ is normal, not an error.
func TestEnvironmentListWithNoEnvironments(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ping.http"), "GET https://example.test/\n")
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	list, err := NewEnvironmentService(collections, store, secrets.NewMemory()).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("items = %+v, want none", list.Items)
	}
}

// A file that does not parse still gets a row: it is exactly the environment
// you need to see in order to fix it.
func TestEnvironmentListShowsABrokenFile(t *testing.T) {
	env, _, _, root := newEnvService(t)
	write(t, filepath.Join(root, "env", "bad.json"), `{"oops": }`)

	list, err := env.List()
	if err != nil {
		t.Fatal(err)
	}
	var bad *EnvironmentSummary
	for i := range list.Items {
		if list.Items[i].Name == "bad" {
			bad = &list.Items[i]
		}
	}
	if bad == nil {
		t.Fatal("the broken environment has no row")
	}
	if bad.Error == "" {
		t.Error("the broken row carries no error")
	}
}

// THE RULE. A resolved secret value never crosses this boundary — not in a
// row, not in a document, not in an error. This is the test that says so.
func TestEnvironmentNeverReturnsASecretValue(t *testing.T) {
	env, _, vault, root := newEnvService(t)
	const value = "correct-horse-battery-staple"
	write(t, filepath.Join(root, "env", "staging.json"),
		`{"baseUrl": "https://example.test", "apiKey": {"$secret": "keychain"}}`)
	if err := vault.Set(secrets.Key(collection.DisplayName(root), "staging", "apiKey"), value); err != nil {
		t.Fatal(err)
	}

	doc, err := env.Load("staging")
	if err != nil {
		t.Fatal(err)
	}
	if dump := render(doc); strings.Contains(dump, value) {
		t.Fatalf("the document carries the secret value: %s", dump)
	}

	var secret *EnvironmentRow
	for i := range doc.Rows {
		if doc.Rows[i].Name == "apiKey" {
			secret = &doc.Rows[i]
		}
	}
	if secret == nil {
		t.Fatal("no apiKey row")
	}
	if !secret.Secret {
		t.Error("the apiKey row is not marked secret")
	}
	if secret.Value != "" {
		t.Errorf("Value = %q, want empty for a secret", secret.Value)
	}
	if !secret.Present {
		t.Error("Present should be true: the value is in the store")
	}
	if secret.Key != collection.DisplayName(root)+"/staging/apiKey" {
		t.Errorf("Key = %q", secret.Key)
	}

	// Every mutating path, too. Any of these leaking a value would be the
	// same bug arriving by another door.
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"SetSecretValue", func() error { _, err := env.SetSecretValue("staging", "apiKey", value); return err }},
		{"MakeSecret", func() error { _, err := env.MakeSecret("staging", "another", value); return err }},
		{"SetConfirmBeforeSend", func() error { _, err := env.SetConfirmBeforeSend("staging", true); return err }},
		{"Load", func() error { _, err := env.Load("staging"); return err }},
	} {
		if err := call.run(); err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		doc, err := env.Load("staging")
		if err != nil {
			t.Fatal(err)
		}
		if dump := render(doc); strings.Contains(dump, value) {
			t.Errorf("after %s the document carries the value: %s", call.name, dump)
		}
	}

	// And the committed file itself, which is the other half of the promise.
	data, err := os.ReadFile(filepath.Join(root, "env", "staging.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), value) {
		t.Errorf("the environment file carries the value:\n%s", data)
	}
	if !strings.Contains(string(data), `{"$secret": "keychain"}`) {
		t.Errorf("the file should hold a reference:\n%s", data)
	}
}

// A reference a teammate committed with no value on this machine is a state
// the editor must be able to describe: it is the difference between "ready"
// and "this request will fail".
func TestEnvironmentReportsAMissingSecretValue(t *testing.T) {
	env, _, _, root := newEnvService(t)
	write(t, filepath.Join(root, "env", "staging.json"), `{"apiKey": {"$secret": "keychain"}}`)

	doc, err := env.Load("staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 1 || !doc.Rows[0].Secret {
		t.Fatalf("rows = %+v", doc.Rows)
	}
	if doc.Rows[0].Present {
		t.Error("Present should be false: nothing is stored for it here")
	}
	if doc.Secrets != 1 || doc.Variables != 1 {
		t.Errorf("counts = %d variables, %d secrets", doc.Variables, doc.Secrets)
	}
}

func TestEnvironmentVariableEditing(t *testing.T) {
	env, _, _, root := newEnvService(t)
	file := filepath.Join(root, "env", "staging.json")
	write(t, file, "{\n  \"baseUrl\": \"https://example.test\",\n  \"port\": 8443\n}\n")

	// Add.
	doc, err := env.SetVariable("staging", "region", "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	if names := rowNames(doc); strings.Join(names, ",") != "baseUrl,port,region" {
		t.Errorf("rows = %v, want the new one last", names)
	}

	// A number stays a number in the file (docs/FORMAT.md §4.3).
	if _, err := env.SetVariable("staging", "port", "9000"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `"port": 9000`) {
		t.Errorf("port should stay a JSON number:\n%s", data)
	}

	// Rename keeps the row's place, which is what keeps the diff to one line.
	if _, err := env.RenameVariable("staging", "baseUrl", "apiBase"); err != nil {
		t.Fatal(err)
	}
	doc, _ = env.Load("staging")
	if names := rowNames(doc); strings.Join(names, ",") != "apiBase,port,region" {
		t.Errorf("rows after rename = %v", names)
	}

	// Remove.
	if _, err := env.RemoveVariable("staging", "region"); err != nil {
		t.Fatal(err)
	}
	doc, _ = env.Load("staging")
	if names := rowNames(doc); strings.Join(names, ",") != "apiBase,port" {
		t.Errorf("rows after remove = %v", names)
	}

	// A name no {{reference}} could match would be a row that silently never
	// applies, so it is refused rather than written.
	for _, bad := range []string{"", "has space", "1leading", resolve.MetaKey} {
		if _, err := env.SetVariable("staging", bad, "x"); err == nil {
			t.Errorf("SetVariable(%q) should fail", bad)
		}
	}
	if _, err := env.RenameVariable("staging", "port", "apiBase"); err == nil {
		t.Error("renaming onto an existing name should fail")
	}
}

// Turning a variable into a secret moves the value off disk; turning it back
// puts a literal on disk and forgets the stored one, so the file and the
// keychain never disagree about where the value is.
func TestEnvironmentSecretLifecycle(t *testing.T) {
	env, _, vault, root := newEnvService(t)
	file := filepath.Join(root, "env", "staging.json")
	write(t, file, `{"apiKey": "committed-by-mistake"}`)
	key := secrets.Key(collection.DisplayName(root), "staging", "apiKey")

	// Plain -> secret.
	if _, err := env.MakeSecret("staging", "apiKey", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if got, err := vault.Get(key); err != nil || got != "s3cret" {
		t.Fatalf("keychain = %q, %v", got, err)
	}
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), "committed-by-mistake") || strings.Contains(string(data), "s3cret") {
		t.Errorf("the file still holds a value:\n%s", data)
	}

	// Replace.
	if _, err := env.SetSecretValue("staging", "apiKey", "rotated"); err != nil {
		t.Fatal(err)
	}
	if got, _ := vault.Get(key); got != "rotated" {
		t.Errorf("keychain = %q, want rotated", got)
	}

	// Rename moves the stored value with the reference.
	if _, err := env.RenameVariable("staging", "apiKey", "apiToken"); err != nil {
		t.Fatal(err)
	}
	moved := secrets.Key(collection.DisplayName(root), "staging", "apiToken")
	if got, err := vault.Get(moved); err != nil || got != "rotated" {
		t.Errorf("moved value = %q, %v", got, err)
	}
	if _, err := vault.Get(key); err == nil {
		t.Error("the old key should be gone")
	}

	// Forget keeps the committed reference and gives up only this machine's
	// value, which is what "Remove from keychain" means.
	doc, err := env.ForgetSecret("staging", "apiToken")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 1 || !doc.Rows[0].Secret || doc.Rows[0].Present {
		t.Errorf("rows = %+v, want the reference kept and no value", doc.Rows)
	}
	if _, err := vault.Get(moved); err == nil {
		t.Error("the value should be gone from the keychain")
	}

	// Secret -> plain drops the stored value too: the file would otherwise
	// claim the value is committed while the real one sat unused.
	if _, err := env.MakeSecret("staging", "apiToken", "again"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.SetVariable("staging", "apiToken", "now-public"); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(moved); err == nil {
		t.Error("the stored value should be forgotten when the row goes plain")
	}
	data, _ = os.ReadFile(file)
	if !strings.Contains(string(data), `"apiToken": "now-public"`) {
		t.Errorf("file =\n%s", data)
	}

	// Removing a secret row forgets its value: a value nothing references and
	// nobody can see is the worst of the three outcomes.
	if _, err := env.MakeSecret("staging", "apiToken", "final"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.RemoveVariable("staging", "apiToken"); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Get(moved); err == nil {
		t.Error("removing the reference should forget the value")
	}

	if _, err := env.MakeSecret("staging", "empty", ""); err == nil {
		t.Error("a secret with no value should be refused")
	}
}

func TestEnvironmentCreateAndDelete(t *testing.T) {
	env, _, _, root := newEnvService(t)

	doc, err := env.Create("local")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "local" || doc.Path != "env/local.json" || len(doc.Rows) != 0 {
		t.Errorf("doc = %+v", doc)
	}
	if data, err := os.ReadFile(filepath.Join(root, "env", "local.json")); err != nil {
		t.Fatal(err)
	} else if string(data) != "{\n}\n" {
		t.Errorf("a new environment = %q", data)
	}

	if _, err := env.Create("local"); err == nil {
		t.Error("creating an existing environment should fail")
	}
	for _, bad := range []string{"", "a/b", "..", ".hidden"} {
		if _, err := env.Create(bad); err == nil {
			t.Errorf("Create(%q) should fail", bad)
		}
	}

	// Deleting the active environment must clear the selection, or every send
	// would resolve against a file that is not there.
	if _, err := env.Activate("local"); err != nil {
		t.Fatal(err)
	}
	list, err := env.Delete("local")
	if err != nil {
		t.Fatal(err)
	}
	if list.Active != "" {
		t.Errorf("Active = %q, want cleared", list.Active)
	}
	if _, err := os.Stat(filepath.Join(root, "env", "local.json")); !os.IsNotExist(err) {
		t.Error("the file should be gone")
	}
}

// Every write Otis makes to a collection holds the write guard, or the
// watcher reports the save as somebody else's change and the window re-walks
// on every keystroke it just persisted.
//
// The guard keeps a path suppressed for a grace period after the write
// releases it, which is what makes this checkable without racing the write:
// a path that was never guarded is not suppressed a moment later.
func TestEnvironmentWritesHoldTheWriteGuard(t *testing.T) {
	env, collections, _, root := newEnvService(t)
	guard := collections.Guard()

	for _, call := range []struct {
		name   string
		target string
		run    func() error
	}{
		{"SetVariable", "env/staging.json", func() error {
			_, err := env.SetVariable("staging", "probe", "1")
			return err
		}},
		{"MakeSecret", "env/staging.json", func() error {
			_, err := env.MakeSecret("staging", "probeSecret", "v")
			return err
		}},
		{"SetConfirmBeforeSend", "env/staging.json", func() error {
			_, err := env.SetConfirmBeforeSend("staging", true)
			return err
		}},
		{"Create", "env/fresh.json", func() error {
			_, err := env.Create("fresh")
			return err
		}},
		{"Delete", "env/fresh.json", func() error {
			_, err := env.Delete("fresh")
			return err
		}},
	} {
		target := filepath.Join(root, filepath.FromSlash(call.target))
		if err := call.run(); err != nil {
			t.Fatalf("%s: %v", call.name, err)
		}
		if !guard.Suppressed(target) {
			t.Errorf("%s wrote %s without holding the write guard", call.name, call.target)
		}
	}

	data, _ := os.ReadFile(filepath.Join(root, "env", "staging.json"))
	if !strings.Contains(string(data), `"probe": "1"`) {
		t.Errorf("file =\n%s", data)
	}
}

// An unavailable keychain is a normal state: the committed references are
// still readable and the editor says the values cannot be reached.
func TestEnvironmentReportsAnUnavailableKeychain(t *testing.T) {
	root := fixture(t)
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	env := NewEnvironmentService(collections, store, unavailableStore{})

	list, err := env.List()
	if err != nil {
		t.Fatalf("List should still work: %v", err)
	}
	if list.Keychain.Available {
		t.Error("Keychain.Available should be false")
	}
	if list.Keychain.Reason == "" {
		t.Error("an unavailable keychain should say why")
	}
	if len(list.Items) != 1 {
		t.Errorf("items = %+v, want the environments to still list", list.Items)
	}
}

// unavailableStore stands in for a machine with no reachable credential store.
type unavailableStore struct{}

func (unavailableStore) Available() error              { return secrets.ErrUnavailable }
func (unavailableStore) Get(string) (string, error)    { return "", secrets.ErrUnavailable }
func (unavailableStore) Set(string, string) error      { return secrets.ErrUnavailable }
func (unavailableStore) Delete(string) error           { return secrets.ErrUnavailable }
func (unavailableStore) List(string) ([]string, error) { return nil, secrets.ErrUnavailable }

func rowNames(doc EnvironmentDocument) []string {
	names := make([]string, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		names = append(names, r.Name)
	}
	return names
}

// render is every string in a document, so a test can assert that a value is
// nowhere in it rather than checking the fields it happens to think of.
func render(doc EnvironmentDocument) string {
	var b strings.Builder
	b.WriteString(doc.Name + doc.Path + doc.Description + doc.Keychain.Reason)
	for _, r := range doc.Rows {
		b.WriteString(r.Name + r.Value + r.Key + string(r.Kind))
	}
	return b.String()
}

// "Referenced by N requests" (screen 1c). A folder header carrying a
// reference makes every request below it a reference too, which is the whole
// reason the count uses effective headers rather than each file's own.
func TestEnvironmentReferencedByCount(t *testing.T) {
	env, _, _, root := newEnvService(t)

	// The fixture's orders/_folder.http declares "@auth bearer {{apiKey}}",
	// inherited by create-order, list-orders and fixtures/seed-order.
	write(t, filepath.Join(root, "env", "staging.json"), `{"apiKey": {"$secret": "keychain"}}`)
	doc, err := env.Load("staging")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ReferencedBy != 3 {
		t.Errorf("ReferencedBy = %d, want the 3 requests under orders/", doc.ReferencedBy)
	}

	// A name nothing mentions is referenced by nothing.
	write(t, filepath.Join(root, "env", "staging.json"), `{"unused": "1"}`)
	doc, err = env.Load("staging")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ReferencedBy != 0 {
		t.Errorf("ReferencedBy = %d, want 0", doc.ReferencedBy)
	}

	// An environment with no variables short-circuits rather than walking.
	write(t, filepath.Join(root, "env", "staging.json"), `{}`)
	doc, err = env.Load("staging")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ReferencedBy != 0 {
		t.Errorf("ReferencedBy = %d, want 0", doc.ReferencedBy)
	}
}
