package resolve

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionScope is the level a session variable belongs to.
type SessionScope string

const (
	// SessionFolder is a value set for one folder, visible to every request
	// in it and below it. It is what the design's vars.folder.set writes.
	SessionFolder SessionScope = "folder"
	// SessionEnv is a value set for one environment, visible to every request
	// resolved against it.
	SessionEnv SessionScope = "env"
)

// SessionValue is one session variable, with where it came from.
//
// Provenance is not decoration here. A session variable is the one value in
// the system that is in no file, so "who set this and when" is the only
// account of it there is — and the reason the design shows both beside every
// row (screen 3a: "orderId · set by create-order · 2h ago").
type SessionValue struct {
	Scope SessionScope `json:"scope"`
	// Owner is the folder's node ID for SessionFolder, or the environment
	// name for SessionEnv.
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Value string `json:"value"`
	// Origin is the node ID of the request whose run set the value, or "" if
	// it was set outside a request.
	Origin string `json:"origin,omitempty"`
	// At is when it was set.
	At time.Time `json:"at"`
}

// Session holds the variables a run sets, for the life of one collection being
// open.
//
// In memory only, and there is no code path that writes it anywhere: not to
// the collection, not to settings.json, not to a log. That is the whole
// contract (docs/FORMAT.md §4.5). A teammate pulling the branch sees no trace
// of it, which is what makes it safe for the id of an order somebody created
// against staging five minutes ago.
type Session struct {
	mu     sync.RWMutex
	values map[sessionKey]SessionValue
}

type sessionKey struct {
	scope SessionScope
	owner string
	name  string
}

// NewSession returns an empty session store.
func NewSession() *Session { return &Session{values: map[sessionKey]SessionValue{}} }

// Set records a value, replacing any earlier one with the same scope, owner
// and name.
func (s *Session) Set(v SessionValue) {
	if s == nil {
		return
	}
	if v.At.IsZero() {
		v.At = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[sessionKey{v.Scope, v.Owner, v.Name}] = v
}

// Get returns one value.
func (s *Session) Get(scope SessionScope, owner, name string) (SessionValue, bool) {
	if s == nil {
		return SessionValue{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[sessionKey{scope, owner, name}]
	return v, ok
}

// List returns every value, ordered by scope, then owner, then name, so the
// UI's grouping is stable between reads.
func (s *Session) List() []SessionValue {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionValue, 0, len(s.values))
	for _, v := range s.values {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Name < b.Name
	})
	return out
}

// Delete removes one value. It reports whether anything was removed.
func (s *Session) Delete(scope SessionScope, owner, name string) bool {
	if s == nil {
		return false
	}
	key := sessionKey{scope, owner, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[key]
	delete(s.values, key)
	return ok
}

// Clear forgets everything. It is what the design's Clear button does, and
// what closing the collection does.
func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = map[sessionKey]SessionValue{}
}

// ClearScope forgets one scope's values: one folder's, or one environment's.
func (s *Session) ClearScope(scope SessionScope, owner string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key := range s.values {
		if key.scope == scope && key.owner == owner {
			delete(s.values, key)
			removed++
		}
	}
	return removed
}

// FolderChain is the folder IDs a request inherits from, nearest first, ending
// with the collection root (""). It is what a scope needs to look session
// values up in the right order, and it cannot be derived from the inheritance
// levels alone: a folder with no _folder.http contributes no level but can
// still hold a session value.
func FolderChain(requestID string) []string {
	dir := requestID
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[:i]
	} else {
		dir = ""
	}
	chain := []string{dir}
	for dir != "" {
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			dir = dir[:i]
		} else {
			dir = ""
		}
		chain = append(chain, dir)
	}
	return chain
}
