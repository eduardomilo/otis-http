// Package secrets defines how Otis stores secret values outside the
// collection. Committed environment files hold only a reference
// ({"$secret": "keychain"}); the value lives in a Store on the user's
// machine and is looked up by a key of the form <collection>/<env>/<name>.
//
// Only an in-memory Store ships today. The interface is shaped so that an OS
// keychain backend (zalando/go-keyring: Get/Set/Delete keyed by service and
// user) drops in later; List will need a small index there because keyrings
// cannot enumerate, which is why it is part of the interface now.
package secrets

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound is returned by Get and Delete for an unknown key.
var ErrNotFound = errors.New("secret not found")

// Store holds secret values by key. Implementations must be safe for
// concurrent use. Values must never appear in error messages.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
	// List returns every key with the given prefix, sorted. An empty prefix
	// lists everything.
	List(prefix string) ([]string, error)
}

// Key builds the canonical key for a secret: <collection>/<env>/<name>.
func Key(collection, env, name string) string {
	return collection + "/" + env + "/" + name
}

// Memory is an in-memory Store for tests and for sessions that have not
// unlocked a real backend. It is never persisted.
type Memory struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return ErrNotFound
	}
	delete(s.m, key)
	return nil
}

func (s *Memory) List(prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var keys []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
