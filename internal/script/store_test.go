package script

import "fmt"

// mapStore is a VarStore backed by maps, so the runtime can be tested without
// the send pipeline behind it.
type mapStore struct {
	scopes map[VarScope]map[string]string
}

func newMapStore() *mapStore {
	return &mapStore{scopes: map[VarScope]map[string]string{
		ScopeRequest: {}, ScopeSession: {}, ScopeEnv: {},
	}}
}

// Resolve is the fall-through of docs/FORMAT.md §4.2, in miniature: request,
// then session, then environment.
func (s *mapStore) Resolve(name string) (string, bool) {
	for _, scope := range []VarScope{ScopeRequest, ScopeSession, ScopeEnv} {
		if value, ok := s.scopes[scope][name]; ok {
			return value, true
		}
	}
	return "", false
}

func (s *mapStore) ReadScope(scope VarScope, name string) (string, bool) {
	value, ok := s.scopes[scope][name]
	return value, ok
}

func (s *mapStore) WriteScope(scope VarScope, name, value string) error {
	if s.scopes[scope] == nil {
		return fmt.Errorf("no %s scope", scope)
	}
	s.scopes[scope][name] = value
	return nil
}
