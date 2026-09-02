package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/settings"
)

// CollectionInfo describes the collection the window is showing. The zero
// value — an empty Path — means no collection is open, which is the state
// screen 2b renders.
type CollectionInfo struct {
	// Path is the absolute path of the collection directory.
	Path string `json:"path"`
	// Name is the collection's display name (collection.DisplayName).
	Name string `json:"name"`
}

// CollectionService owns the one piece of app-level state everything else
// hangs off: which collection is open. Changing it emits
// events.CollectionOpened, so no part of the frontend has to poll or keep its
// own copy.
//
// Increment 9 grows this service into the tree loader and file watcher; for
// now Open only validates the directory and records it.
type CollectionService struct {
	app      *application.App
	settings *settings.Store

	mu      sync.RWMutex
	current CollectionInfo
}

// NewCollectionService constructs the service around the shared settings
// store, which it updates with the recents list and the last collection.
func NewCollectionService(store *settings.Store) *CollectionService {
	return &CollectionService{settings: store}
}

// ServiceStartup resolves the application and wires up drag-and-drop: a
// directory dropped on the window opens exactly as one chosen in the dialog.
func (s *CollectionService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	for _, w := range s.app.Window.GetAll() {
		s.watchForDroppedDirectories(w)
	}
	s.app.Window.OnCreate(s.watchForDroppedDirectories)
	return nil
}

func (s *CollectionService) watchForDroppedDirectories(w application.Window) {
	w.OnWindowEvent(wailsevents.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		for _, p := range e.Context().DroppedFiles() {
			info, err := os.Stat(p)
			if err != nil || !info.IsDir() {
				// Only directories are collections. Dropping a file is
				// ignored rather than guessed at: opening its parent would
				// silently open something the user did not point at.
				continue
			}
			if _, err := s.Open(p); err != nil {
				s.app.Logger.Error("opening a dropped directory", "path", p, "error", err)
			}
			return
		}
	})
}

// Current returns the open collection, or the zero value if there is none.
func (s *CollectionService) Current() CollectionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Open makes dir the current collection. It records dir in the recents list
// and as the collection to reopen on the next launch, then emits
// events.CollectionOpened.
func (s *CollectionService) Open(dir string) (CollectionInfo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return CollectionInfo{}, fmt.Errorf("opening %s: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return CollectionInfo{}, fmt.Errorf("opening %s: %w", abs, err)
	}
	if !info.IsDir() {
		return CollectionInfo{}, fmt.Errorf("opening %s: not a directory", abs)
	}

	opened := CollectionInfo{Path: abs, Name: collection.DisplayName(abs)}
	s.mu.Lock()
	s.current = opened
	s.mu.Unlock()

	if _, err := s.settings.Update(func(v *settings.Settings) {
		if v.LastCollection != abs {
			// Tabs name paths inside a collection, so they do not survive
			// switching to a different one.
			v.Tabs = settings.Tabs{}
		}
		v.LastCollection = abs
		v.AddRecent(opened.Name, abs, time.Now())
	}); err != nil {
		// The collection is open either way; failing to remember it is not a
		// reason to refuse.
		s.logError("recording the recent collection", err)
	} else {
		s.emit(events.SettingsChanged, nil)
	}

	s.emit(events.CollectionOpened, opened)
	return opened, nil
}

// Close forgets the current collection and emits events.CollectionOpened with
// an empty path, which returns the window to the empty state.
func (s *CollectionService) Close() error {
	s.mu.Lock()
	s.current = CollectionInfo{}
	s.mu.Unlock()

	if _, err := s.settings.Update(func(v *settings.Settings) {
		v.LastCollection = ""
		v.Tabs = settings.Tabs{}
	}); err != nil {
		s.logError("clearing the last collection", err)
	} else {
		s.emit(events.SettingsChanged, nil)
	}

	s.emit(events.CollectionOpened, CollectionInfo{})
	return nil
}

func (s *CollectionService) emit(name string, data any) {
	if s.app == nil {
		return // not running under Wails, as in tests
	}
	s.app.Event.Emit(name, data)
}

func (s *CollectionService) logError(msg string, err error) {
	if s.app == nil {
		return
	}
	s.app.Logger.Error(msg, "error", err)
}
