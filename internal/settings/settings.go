// Package settings persists the small amount of state the Otis window needs
// across restarts: pane sizes, the open tabs, the recent collections and the
// last collection opened.
//
// The frontend is forbidden from using localStorage or sessionStorage, so
// this is the only place that state lives. The file is JSON in the OS config
// directory (os.UserConfigDir()/otis/settings.json) and is written
// atomically: a temp file in the same directory, fsynced, then renamed over
// the target, so a crash mid-write can never leave a truncated file.
//
// Settings are conveniences, never data the user would miss. A file that
// cannot be read or parsed is therefore treated as absent and replaced on the
// next write, rather than surfaced as an error the window has to handle.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxRecents is how many recent collections are remembered. Older entries
// fall off the end.
const MaxRecents = 10

// Settings is the whole persisted document. Every field is optional; the zero
// value is a valid, empty settings file.
type Settings struct {
	// Panes holds the widths of the resizable panes, in CSS pixels. Zero
	// means "never set" — the frontend then applies the design defaults from
	// docs/design/DESIGN-NOTES.md §4.1 rather than a width of nothing.
	Panes Panes `json:"panes"`
	// Tabs is the open document tabs. Only paths are kept; tab contents and
	// dirty state are rebuilt from disk on launch.
	Tabs Tabs `json:"tabs"`
	// Recents lists recently opened collections, most recent first.
	Recents []Recent `json:"recents"`
	// LastCollection is the absolute path of the collection to reopen on
	// launch, or "" for none.
	LastCollection string `json:"lastCollection"`
	// ActiveEnv is the environment selected in LastCollection, or "" for
	// none. Which environment is active is a per-machine choice, not a
	// property of the collection — one person works against staging while
	// another is on local, from the same branch — so it lives here rather
	// than in a committed file. It is cleared when the collection changes,
	// for the same reason the tabs are: an environment names a file inside
	// one collection.
	//
	// Only the *name* is ever written here. A secret value never reaches this
	// file (docs/FORMAT.md §5).
	ActiveEnv string `json:"activeEnv"`
}

// Panes is the geometry of the three-pane layout.
type Panes struct {
	SidebarWidth      float64 `json:"sidebarWidth"`
	ResponseWidth     float64 `json:"responseWidth"`
	SidebarCollapsed  bool    `json:"sidebarCollapsed"`
	ResponseCollapsed bool    `json:"responseCollapsed"`
}

// Tabs is the open document tabs, as collection-relative node IDs
// (docs/FORMAT.md §2.1).
type Tabs struct {
	Open   []string `json:"open"`
	Active string   `json:"active"`
}

// Recent is one entry in the recent-collections list.
type Recent struct {
	// Name is the collection directory's base name.
	Name string `json:"name"`
	// Path is the absolute path of the collection directory.
	Path string `json:"path"`
	// LastOpened is when the collection was last opened.
	LastOpened time.Time `json:"lastOpened"`
	// Missing reports that Path no longer resolves to a directory. It is
	// computed on every read and never written to disk, so a directory that
	// comes back stops being missing on its own.
	Missing bool `json:"missing"`
}

// Store reads and writes one settings file. It is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
}

// DefaultPath is the settings file's location, os.UserConfigDir()/otis/settings.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("settings: locating the config directory: %w", err)
	}
	return filepath.Join(dir, "otis", "settings.json"), nil
}

// NewStore returns a Store backed by the file at path. The file and its
// directory are created on the first write.
func NewStore(path string) *Store { return &Store{path: path} }

// Path is the settings file this Store reads and writes.
func (s *Store) Path() string { return s.path }

// Get reads the settings. A missing, unreadable or malformed file yields the
// zero Settings and no error: settings are a convenience, and refusing to
// open the window over a corrupt cache file would be worse than losing it.
func (s *Store) Get() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(), nil
}

// Set replaces the settings and writes them out.
func (s *Store) Set(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(v)
}

// Update applies fn to the current settings and writes the result. It is the
// only safe way to change one field without racing another writer.
func (s *Store) Update(fn func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.load()
	fn(&v)
	if err := s.save(v); err != nil {
		return v, err
	}
	return v, nil
}

// load reads and normalises the file. The caller holds the mutex.
func (s *Store) load() Settings {
	var v Settings
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// A readable-but-broken file is discarded, like a missing one.
			fmt.Fprintf(os.Stderr, "otis: settings: %v (using defaults)\n", err)
		}
		return v
	}
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Fprintf(os.Stderr, "otis: settings: %s: %v (using defaults)\n", s.path, err)
		return Settings{}
	}
	v.stampMissing()
	return v
}

// save normalises and atomically writes v. The caller holds the mutex.
func (s *Store) save(v Settings) error {
	v.normalise()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encoding: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("settings: creating %s: %w", dir, err)
	}
	// Write to a sibling temp file and rename over the target: rename is
	// atomic within a directory, so readers see either the old file or the
	// new one and never a half-written mix.
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("settings: creating a temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below has succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("settings: writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("settings: syncing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: closing %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("settings: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("settings: replacing %s: %w", s.path, err)
	}
	return nil
}

// normalise trims the settings to what is worth persisting.
func (v *Settings) normalise() {
	if len(v.Recents) > MaxRecents {
		v.Recents = v.Recents[:MaxRecents]
	}
	for i := range v.Recents {
		// Missing is derived from the filesystem on read; persisting it
		// would make a directory look gone after it came back.
		v.Recents[i].Missing = false
	}
}

// stampMissing marks recents whose directory no longer exists.
func (v *Settings) stampMissing() {
	for i := range v.Recents {
		info, err := os.Stat(v.Recents[i].Path)
		v.Recents[i].Missing = err != nil || !info.IsDir()
	}
}

// AddRecent moves path to the front of the recent list, creating the entry if
// it is new. It is the mutation Open performs, kept here so the ordering rule
// lives with the type. The display name is passed in because deriving it from
// the path is the collection package's business, not this one's.
func (v *Settings) AddRecent(name, path string, now time.Time) {
	kept := make([]Recent, 0, len(v.Recents)+1)
	kept = append(kept, Recent{Name: name, Path: path, LastOpened: now})
	for _, r := range v.Recents {
		if r.Path != path {
			kept = append(kept, r)
		}
	}
	if len(kept) > MaxRecents {
		kept = kept[:MaxRecents]
	}
	v.Recents = kept
}

// RemoveRecent drops path from the recent list. It reports whether anything
// was removed.
func (v *Settings) RemoveRecent(path string) bool {
	kept := v.Recents[:0]
	for _, r := range v.Recents {
		if r.Path != path {
			kept = append(kept, r)
		}
	}
	removed := len(kept) != len(v.Recents)
	v.Recents = kept
	return removed
}
