package services

import (
	"github.com/otis-http/otis/internal/settings"
)

// SettingsService is the frontend's only durable storage. The frontend is
// forbidden from touching localStorage or sessionStorage, so pane sizes, open
// tabs and recent collections all round-trip through here to a JSON file in
// the OS config directory.
type SettingsService struct {
	store *settings.Store
}

// NewSettingsService wraps an existing store. The store is shared with
// CollectionService, which updates the recents list when a collection opens,
// so it is constructed once in main.go and passed to both.
func NewSettingsService(store *settings.Store) *SettingsService {
	return &SettingsService{store: store}
}

// Get returns the persisted settings. A missing or unreadable file yields the
// zero value rather than an error; see the settings package.
func (s *SettingsService) Get() (settings.Settings, error) {
	return s.store.Get()
}

// SetPanes records the pane geometry.
//
// There is deliberately no method that writes the whole document. Go and the
// window both write these settings — Go owns the recents list and the last
// collection, the window owns the panes and the tabs — and a whole-document
// write from one side silently discards whatever the other side wrote in the
// meantime. Every setter here is a read-modify-write of its own fields.
func (s *SettingsService) SetPanes(panes settings.Panes) error {
	_, err := s.store.Update(func(v *settings.Settings) { v.Panes = panes })
	return err
}

// SetTabs records the open document tabs and which one is active.
func (s *SettingsService) SetTabs(tabs settings.Tabs) error {
	_, err := s.store.Update(func(v *settings.Settings) { v.Tabs = tabs })
	return err
}

// RemoveRecent drops one entry from the recent-collections list and returns
// the settings as they now stand.
func (s *SettingsService) RemoveRecent(path string) (settings.Settings, error) {
	return s.store.Update(func(v *settings.Settings) { v.RemoveRecent(path) })
}
