package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

// fakeRing is an in-process stand-in for the OS keyring. Tests must never
// touch the real one: on macOS it would prompt, and a prompt in a test run is
// a hang, not a failure.
type fakeRing struct {
	entries map[string]string
	err     error
	sets    int
}

func newFakeRing() *fakeRing { return &fakeRing{entries: map[string]string{}} }

func (f *fakeRing) install(k *Keyring) {
	k.get = func(service, user string) (string, error) {
		if f.err != nil {
			return "", f.err
		}
		if user != Account {
			return "", keyring.ErrNotFound
		}
		v, ok := f.entries[service]
		if !ok {
			return "", keyring.ErrNotFound
		}
		return v, nil
	}
	k.set = func(service, user, password string) error {
		if f.err != nil {
			return f.err
		}
		f.sets++
		f.entries[service] = password
		return nil
	}
	k.del = func(service, user string) error {
		if f.err != nil {
			return f.err
		}
		if _, ok := f.entries[service]; !ok {
			return keyring.ErrNotFound
		}
		delete(f.entries, service)
		return nil
	}
}

func newTestKeyring(t *testing.T) (*Keyring, *fakeRing, string) {
	t.Helper()
	index := filepath.Join(t.TempDir(), "secret-keys.json")
	k := NewKeyring(index)
	ring := newFakeRing()
	ring.install(k)
	return k, ring, index
}

func TestKeyringRoundTrip(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	key := Key("acme-api", "staging", "apiKey")

	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before Set = %v, want ErrNotFound", err)
	}
	if err := k.Set(key, "s3cret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, err := k.Get(key); err != nil || got != "s3cret" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if ring.entries[key] != "s3cret" {
		t.Errorf("the value did not reach the keyring under %q", key)
	}
	if err := k.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := k.Delete(key); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

// The index is allowed to exist only because it holds nothing but keys, every
// one of which is already committed in an environment file. This is the test
// that keeps it that way.
func TestKeyringIndexHoldsKeysAndNeverValues(t *testing.T) {
	k, _, index := newTestKeyring(t)
	const value = "correct-horse-battery-staple"
	keys := []string{
		Key("acme-api", "staging", "apiKey"),
		Key("acme-api", "staging", "webhookSecret"),
		Key("acme-api", "prod", "apiKey"),
	}
	for _, key := range keys {
		if err := k.Set(key, value); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}

	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatalf("reading the index: %v", err)
	}
	if strings.Contains(string(data), value) {
		t.Fatal("the key index contains a secret value")
	}
	for _, key := range keys {
		if !strings.Contains(string(data), key) {
			t.Errorf("the index does not list %s", key)
		}
	}

	// Only the keys, in sorted order, and nothing else.
	want := `[
  "acme-api/prod/apiKey",
  "acme-api/staging/apiKey",
  "acme-api/staging/webhookSecret"
]
`
	if string(data) != want {
		t.Errorf("index =\n%s\nwant\n%s", data, want)
	}

	if info, err := os.Stat(index); err != nil {
		t.Fatalf("stat: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("index mode = %v, want 0600", perm)
	}
}

func TestKeyringListIsPrefixedAndSurvivesRestart(t *testing.T) {
	k, ring, index := newTestKeyring(t)
	for _, key := range []string{
		Key("acme-api", "staging", "apiKey"),
		Key("acme-api", "prod", "apiKey"),
		Key("other", "staging", "apiKey"),
	} {
		if err := k.Set(key, "v"); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}

	// A second Keyring over the same index is what the next launch of Otis
	// sees: the keys are still known without asking the keyring anything.
	next := NewKeyring(index)
	ring.install(next)
	got, err := next.List("acme-api/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"acme-api/prod/apiKey", "acme-api/staging/apiKey"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", got, want)
	}
}

// The keyring is the truth. An index that claims a key the keyring has lost —
// the user removed it in Keychain Access, or the config came from another
// machine — repairs itself on the read that finds out.
func TestKeyringGetRepairsAStaleIndex(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	key := Key("acme-api", "staging", "apiKey")
	if err := k.Set(key, "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	delete(ring.entries, key) // removed behind Otis' back

	if _, err := k.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
	if keys, _ := k.List(""); len(keys) != 0 {
		t.Errorf("List = %v, want the stale key dropped", keys)
	}
}

func TestKeyringReportsAnUnavailableKeyring(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	ring.err = keyring.ErrUnsupportedPlatform

	if err := k.Available(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Available = %v, want ErrUnavailable", err)
	}
	if err := k.Set("a/b/c", "v"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Set = %v, want ErrUnavailable", err)
	}
	if _, err := k.Get("a/b/c"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
}

func TestKeyringAvailableProbesAndCleansUp(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	if err := k.Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if len(ring.entries) != 0 {
		t.Errorf("the probe entry was left behind: %v", ring.entries)
	}
	// The probe is not a secret Otis stored, so it must not be indexed.
	if keys, _ := k.List(""); len(keys) != 0 {
		t.Errorf("List = %v, want the probe unindexed", keys)
	}
}

// An error that reaches the user names the key, which is public, and never the
// value.
func TestKeyringErrorsNeverNameAValue(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	const value = "correct-horse-battery-staple"
	ring.err = errors.New("the keychain is locked")

	key := Key("acme-api", "staging", "apiKey")
	for _, err := range []error{
		k.Set(key, value),
		second(k.Get(key)),
		k.Delete(key),
		k.Available(),
	} {
		if err == nil {
			t.Fatal("want an error")
		}
		if strings.Contains(err.Error(), value) {
			t.Errorf("the error names the value: %v", err)
		}
	}
}

func second(_ string, err error) error { return err }

func TestFallbackReadsInOrderAndWritesToTheFirst(t *testing.T) {
	first, secondStore := NewMemory(), NewMemory()
	store := Fallback{first, secondStore}
	key := Key("acme-api", "staging", "apiKey")

	if _, err := store.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty = %v, want ErrNotFound", err)
	}

	// Only the later store has it: the fallback finds it.
	if err := secondStore.Set(key, "from-keychain"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(key); got != "from-keychain" {
		t.Errorf("Get = %q, want the later store's value", got)
	}

	// The earlier store wins once it has one, which is what makes
	// OTIS_SECRET_* override the keychain in CI.
	if err := first.Set(key, "from-env"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(key); got != "from-env" {
		t.Errorf("Get = %q, want the earlier store's value", got)
	}

	if keys, err := store.List(""); err != nil || len(keys) != 1 || keys[0] != key {
		t.Errorf("List = %v, %v, want one deduplicated key", keys, err)
	}

	if err := store.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// A store that cannot be read at all must not be reported as "no such secret":
// "could not look" and "not there" send the user to different places.
func TestFallbackReportsALookupFailureRatherThanNotFound(t *testing.T) {
	k, ring, _ := newTestKeyring(t)
	ring.err = keyring.ErrUnsupportedPlatform
	store := Fallback{NewMemory(), k}

	_, err := store.Get(Key("acme-api", "staging", "apiKey"))
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
}
