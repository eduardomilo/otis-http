package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

// Account is the account name every Otis secret is stored under.
//
// An OS keyring entry is addressed by a (service, account) pair. The design
// fixes the service string: screen 1c states the keychain service is
// "acme-api/staging/apiKey", which is exactly Key(collection, env, name). That
// leaves the account free, so it is a constant — the whole address is already
// in the service, and a second varying part would only make the same secret
// findable under two names.
const Account = "otis"

// ErrUnavailable means the machine has no keyring Otis can reach: a Linux
// session with no Secret Service on the bus, a locked keychain the user
// declined to unlock, an unsupported platform.
//
// It is a normal state, not a failure of Otis. A collection whose environments
// reference secrets is still perfectly readable — the references are in the
// committed file — so the window says the keychain is unavailable and shows
// the references, rather than refusing to open anything.
var ErrUnavailable = errors.New("no OS keychain is available on this machine")

// Keyring is a Store backed by the operating system's credential store: the
// macOS keychain, the Secret Service on Linux, the Windows credential manager.
// go-keyring is pure Go on all three (macOS shells out to /usr/bin/security),
// so CGO_ENABLED=0 still builds.
//
// # Why there is an index
//
// No keyring enumerates. Given a service string they answer with a value;
// there is no "list everything Otis stored". But the environment editor has to
// be able to say which references have a value on this machine and which do
// not, and a request that is about to be sent has to be able to fail early and
// name the key. So Keyring keeps an index: a JSON file beside settings.json
// holding **the keys only**.
//
// Nothing else. Not a value, not a length, not a hash, not a fingerprint. The
// file is a list of strings of the form <collection>/<env>/<name>, every one
// of which is already committed in plain sight in an environment file — the
// index tells an attacker who reads it nothing the repository did not. That is
// the whole reason it is allowed to exist, and it is why Set writes the value
// to the keyring and only then records the key.
type Keyring struct {
	mu sync.Mutex
	// indexPath is the JSON file holding the known keys. Empty disables the
	// index, which makes List empty and is what tests without a temp dir get.
	indexPath string
	// index is the loaded key set, nil until first use.
	index map[string]bool

	// get, set, del are the keyring operations, indirected for tests.
	get func(service, user string) (string, error)
	set func(service, user, password string) error
	del func(service, user string) error
}

// NewKeyring returns a Store backed by the OS keyring, keeping its key index
// at indexPath. An empty indexPath disables the index (List returns nothing).
func NewKeyring(indexPath string) *Keyring {
	return &Keyring{
		indexPath: indexPath,
		get:       keyring.Get,
		set:       keyring.Set,
		del:       keyring.Delete,
	}
}

// DefaultIndexPath is the key index's location, beside the settings file:
// os.UserConfigDir()/otis/secret-keys.json.
func DefaultIndexPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("secrets: locating the config directory: %w", err)
	}
	return filepath.Join(dir, "otis", "secret-keys.json"), nil
}

// Available reports whether this machine has a keyring Otis can use.
//
// It answers by writing and deleting a probe entry under a reserved key, which
// is the only honest test: go-keyring reports an unsupported platform up front
// but cannot tell in advance that the D-Bus Secret Service is missing or that
// the user will decline to unlock the keychain.
func (s *Keyring) Available() error {
	const probe = "$otis/$probe/$probe"
	if err := s.set(probe, Account, "probe"); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// A probe that cannot be removed is still a working keyring; leaving one
	// stale reserved entry behind is better than reporting failure.
	_ = s.del(probe, Account)
	return nil
}

func (s *Keyring) Get(key string) (string, error) {
	value, err := s.get(key, Account)
	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, keyring.ErrNotFound):
		// The index and the keyring disagreeing is normal: the user removed
		// the entry in Keychain Access, or the index came from another
		// machine's config. The keyring is the truth, so forget the key.
		s.forget(key)
		return "", ErrNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	default:
		// The error may name the key, which is public, but never the value:
		// there is no value to name on a failed read.
		return "", fmt.Errorf("reading the secret %s from the keychain: %w", key, err)
	}
}

func (s *Keyring) Set(key, value string) error {
	if err := s.set(key, Account, value); err != nil {
		if errors.Is(err, keyring.ErrUnsupportedPlatform) {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		// Deliberately not %w on anything carrying value: this error is
		// shown to the user and must name the key only.
		return fmt.Errorf("storing the secret %s in the keychain: %w", key, err)
	}
	s.remember(key)
	return nil
}

func (s *Keyring) Delete(key string) error {
	err := s.del(key, Account)
	// The key leaves the index whichever way this went: if the entry is gone
	// the index is wrong to claim it, and if the entry was never there the
	// index was wrong already.
	s.forget(key)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	default:
		return fmt.Errorf("removing the secret %s from the keychain: %w", key, err)
	}
}

// List returns the indexed keys with the given prefix, sorted.
//
// These are keys Otis has stored, which is not the same as keys that are
// present: another program may have removed one. Get is what settles that, and
// it repairs the index when it does.
func (s *Keyring) List(prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadIndex()
	var keys []string
	for k := range s.index {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// remember adds a key to the index and writes it out.
func (s *Keyring) remember(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadIndex()
	if s.index[key] {
		return
	}
	s.index[key] = true
	s.saveIndex()
}

// forget removes a key from the index and writes it out.
func (s *Keyring) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadIndex()
	if !s.index[key] {
		return
	}
	delete(s.index, key)
	s.saveIndex()
}

// loadIndex reads the index file once. The caller holds the lock.
//
// A missing, unreadable or malformed index is treated as empty, exactly as the
// settings file is: it is a cache of public strings, and refusing to open a
// collection because it is corrupt would be absurd. It repairs itself on the
// next Set.
func (s *Keyring) loadIndex() {
	if s.index != nil {
		return
	}
	s.index = map[string]bool{}
	if s.indexPath == "" {
		return
	}
	data, err := os.ReadFile(s.indexPath)
	if err != nil {
		return
	}
	var keys []string
	if json.Unmarshal(data, &keys) != nil {
		return
	}
	for _, k := range keys {
		s.index[k] = true
	}
}

// saveIndex writes the index out atomically. The caller holds the lock.
func (s *Keyring) saveIndex() {
	if s.indexPath == "" {
		return
	}
	keys := make([]string, 0, len(s.index))
	for k := range s.index {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.indexPath), 0o700); err != nil {
		return
	}
	// Same atomic write as the settings store: a temp file in the same
	// directory, renamed over the target, so a crash cannot truncate it.
	tmp, err := os.CreateTemp(filepath.Dir(s.indexPath), ".secret-keys-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	_ = os.Rename(tmp.Name(), s.indexPath)
}

// Fallback is a Store that reads through a list of stores in order and writes
// to the first one that accepts.
//
// It is how the CLI supplies secrets: OTIS_SECRET_* environment variables come
// first so CI keeps working with no keychain at all, and the OS keyring comes
// second so a developer running `otis run` locally gets the same values the
// window uses (docs/FORMAT.md §5).
type Fallback []Store

func (f Fallback) Get(key string) (string, error) {
	var firstErr error
	for _, s := range f {
		value, err := s.Get(key)
		if err == nil {
			return value, nil
		}
		// ErrNotFound means "ask the next one". Anything else — an
		// unavailable keychain, a locked one — is worth reporting if nothing
		// later answers, because it is the difference between "no such
		// secret" and "could not look".
		if !errors.Is(err, ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return "", ErrNotFound
}

func (f Fallback) Set(key, value string) error {
	var firstErr error
	for _, s := range f {
		err := s.Set(key, value)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return ErrReadOnly
}

func (f Fallback) Delete(key string) error {
	deleted := false
	var firstErr error
	for _, s := range f {
		err := s.Delete(key)
		switch {
		case err == nil:
			deleted = true
		case errors.Is(err, ErrNotFound):
			// nothing to remove here
		case firstErr == nil:
			firstErr = err
		}
	}
	if deleted {
		return nil
	}
	if firstErr != nil {
		return firstErr
	}
	return ErrNotFound
}

func (f Fallback) List(prefix string) ([]string, error) {
	seen := map[string]bool{}
	var keys []string
	var firstErr error
	for _, s := range f {
		found, err := s.List(prefix)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, k := range found {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	if keys == nil && firstErr != nil {
		return nil, firstErr
	}
	sort.Strings(keys)
	return keys, nil
}
