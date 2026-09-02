package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/git"
	"github.com/otis-http/otis/internal/settings"
	"github.com/otis-http/otis/internal/watch"
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

// Opened is what the window gets back when a collection is opened: which
// collection it is, and everything needed to draw it.
type Opened struct {
	Collection CollectionInfo `json:"collection"`
	Tree       Tree           `json:"tree"`
}

// CollectionService owns the one piece of app-level state everything else
// hangs off: which collection is open, its tree, and the watcher keeping that
// tree true.
//
// Changing the collection emits events.CollectionOpened; a change on disk
// emits events.CollectionChanged with the new tree; a commit, stage or branch
// switch emits events.GitChanged. No part of the frontend polls.
type CollectionService struct {
	app      *application.App
	settings *settings.Store
	guard    *watch.Guard

	mu      sync.RWMutex
	current CollectionInfo
	watcher *watch.Watcher
	// generation increments on every open and close, so a watcher callback
	// that was already in flight when the collection changed can tell that
	// its work is stale and drop it.
	generation uint64
}

// NewCollectionService constructs the service around the shared settings
// store, which it updates with the recents list and the last collection.
func NewCollectionService(store *settings.Store) *CollectionService {
	return &CollectionService{settings: store, guard: watch.NewGuard()}
}

// Guard is the write guard every writer to a collection must hold, so Otis'
// own writes are not mistaken for someone else's. Nothing writes yet; the
// first writer arrives in Phase C.
func (s *CollectionService) Guard() *watch.Guard { return s.guard }

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

// ServiceShutdown stops the watcher so its goroutine does not outlive the app.
func (s *CollectionService) ServiceShutdown() error {
	s.stopWatching()
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
				s.logError("opening a dropped directory", err)
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

// Open makes dir the current collection: it walks the tree, reads the
// repository, starts watching for changes, records dir in the recents list and
// as the collection to reopen next launch, then emits events.CollectionOpened.
func (s *CollectionService) Open(dir string) (Opened, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Opened{}, fmt.Errorf("opening %s: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Opened{}, fmt.Errorf("opening %s: %w", abs, err)
	}
	if !info.IsDir() {
		return Opened{}, fmt.Errorf("opening %s: not a directory", abs)
	}

	tree, err := readTree(abs)
	if err != nil {
		return Opened{}, err
	}

	opened := CollectionInfo{Path: abs, Name: collection.DisplayName(abs)}
	s.stopWatching()

	s.mu.Lock()
	s.current = opened
	s.generation++
	generation := s.generation
	s.mu.Unlock()

	if err := s.startWatching(abs, generation); err != nil {
		// A collection that cannot be watched still opens; it just will not
		// update itself. Refusing to open it would be worse.
		s.logError("watching the collection", err)
	}

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
	return Opened{Collection: opened, Tree: tree}, nil
}

// Close forgets the current collection and emits events.CollectionOpened with
// an empty path, which returns the window to the empty state.
func (s *CollectionService) Close() error {
	s.stopWatching()
	s.mu.Lock()
	s.current = CollectionInfo{}
	s.generation++
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

// Tree re-walks the open collection and returns it. The window does not need
// to call this in the normal course of things — changes arrive as
// events.CollectionChanged — but it is how a view recovers after an error.
func (s *CollectionService) Tree() (Tree, error) {
	root := s.Current().Path
	if root == "" {
		return Tree{}, fmt.Errorf("no collection is open")
	}
	return readTree(root)
}

// readTree walks the collection and reads the repository around it.
func readTree(root string) (Tree, error) {
	loaded, err := collection.Load(root)
	if err != nil {
		return Tree{}, fmt.Errorf("reading the collection at %s: %w", root, err)
	}
	// A git failure must not stop the tree from loading: a collection outside
	// a repository, or in a repository git itself cannot read, is still a
	// perfectly good collection.
	state, err := git.Read(root)
	if err != nil {
		state = git.State{}
	}
	return buildTree(loaded, state), nil
}

// startWatching begins reporting changes under root. The caller holds no lock.
func (s *CollectionService) startWatching(root string, generation uint64) error {
	watcher, err := watch.Start(root, func(change watch.Change) {
		s.onChange(root, generation, change)
	}, watch.Options{
		Guard:   s.guard,
		GitDir:  git.Dir(root),
		OnError: func(err error) { s.logError("watching the collection", err) },
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	// The collection may have changed again while the watcher was starting.
	if s.generation != generation {
		s.mu.Unlock()
		watcher.Close()
		return nil
	}
	s.watcher = watcher
	s.mu.Unlock()
	return nil
}

func (s *CollectionService) stopWatching() {
	s.mu.Lock()
	watcher := s.watcher
	s.watcher = nil
	s.mu.Unlock()
	if watcher != nil {
		watcher.Close()
	}
}

// onChange re-reads what changed and tells the window. It runs on the
// watcher's goroutine.
func (s *CollectionService) onChange(root string, generation uint64, change watch.Change) {
	if !s.isCurrent(root, generation) {
		return
	}
	if change.Collection {
		tree, err := readTree(root)
		if err != nil {
			s.logError("re-reading the collection", err)
		} else if s.isCurrent(root, generation) {
			s.emit(events.CollectionChanged, tree)
		}
	}
	if change.Git {
		state, err := git.Read(root)
		if err != nil {
			s.logError("re-reading the repository", err)
			return
		}
		if s.isCurrent(root, generation) {
			s.emit(events.GitChanged, state)
		}
	}
}

// isCurrent reports whether a watcher callback still refers to the open
// collection.
func (s *CollectionService) isCurrent(root string, generation uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation == generation && s.current.Path == root
}

// Reveal shows a node in the OS file manager. nodePath is collection-relative.
func (s *CollectionService) Reveal(nodePath string) error {
	target, err := s.resolve(nodePath)
	if err != nil {
		return err
	}
	return reveal(target)
}

// AbsolutePath resolves a collection-relative node path to an absolute one.
func (s *CollectionService) AbsolutePath(nodePath string) (string, error) {
	return s.resolve(nodePath)
}

// CopyPath puts a node's absolute path on the clipboard and returns what it
// copied.
//
// The absolute path, not the collection-relative one: the relative path is
// already on screen next to the row, and what you want on the clipboard is
// the thing you can paste into a terminal or another editor. Copying goes
// through Go rather than navigator.clipboard because the window is served
// from a custom scheme, where the browser clipboard API is not reliably
// available.
func (s *CollectionService) CopyPath(nodePath string) (string, error) {
	target, err := s.resolve(nodePath)
	if err != nil {
		return "", err
	}
	if s.app == nil {
		return target, nil // not running under Wails, as in tests
	}
	if !s.app.Clipboard.SetText(target) {
		return "", fmt.Errorf("copying %s: the clipboard refused the text", nodePath)
	}
	return target, nil
}

// resolve turns a collection-relative node path into an absolute path inside
// the open collection.
func (s *CollectionService) resolve(nodePath string) (string, error) {
	root := s.Current().Path
	if root == "" {
		return "", fmt.Errorf("no collection is open")
	}
	target := absolutePath(root, nodePath)
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("%s: %w", nodePath, err)
	}
	return target, nil
}

// reveal opens the platform's file manager with target selected.
func reveal(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", target)
	case "windows":
		// explorer always exits non-zero here, so its status is ignored below.
		cmd = exec.Command("explorer", "/select,"+target)
	default:
		// No portable "select this file" on Linux; open the containing
		// directory, which every file manager understands.
		cmd = exec.Command("xdg-open", filepath.Dir(target))
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("revealing %s: %w", target, err)
	}
	go cmd.Wait() // reap the child; its exit status is not meaningful
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
