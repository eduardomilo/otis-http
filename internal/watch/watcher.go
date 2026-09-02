package watch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is how long the watcher waits for quiet before reporting a
// change. Saving one file in an editor commonly produces three or four events
// (a temp file, a rename, a chmod), and a checkout produces hundreds; batching
// them into one re-walk is the difference between a redraw and a stutter.
const DefaultDebounce = 150 * time.Millisecond

// Change says what kind of change was seen since the last report. Both flags
// can be set by one batch.
type Change struct {
	// Collection is set when something in the collection tree changed.
	Collection bool
	// Git is set when .git/HEAD or .git/index changed — a commit, a stage,
	// or a branch switch.
	Git bool
}

// Watcher watches a collection directory and its subdirectories, and reports
// debounced changes.
//
// fsnotify watches a single directory, so every subdirectory gets its own
// watch and new ones are picked up as they appear. The .git directory is
// watched but not descended into: its internals churn constantly and none of
// it is the collection, but HEAD and index are how a branch switch or a stage
// becomes visible.
type Watcher struct {
	root     string
	gitDir   string
	guard    *Guard
	debounce time.Duration
	report   func(Change)

	fsw *fsnotify.Watcher

	mu      sync.Mutex
	pending Change
	timer   *time.Timer

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	// onError, when set, receives watcher errors. Tests use it; the service
	// logs them.
	onError func(error)
}

// Options configures a Watcher. The zero value uses the defaults.
type Options struct {
	// Guard suppresses events for paths Otis wrote itself. May be nil.
	Guard *Guard
	// GitDir is the repository's git directory, watched for HEAD and index.
	// It is usually *not* inside root: the common layout puts the collection
	// in a subdirectory of the repository. Empty when there is no repository.
	GitDir string
	// Debounce overrides DefaultDebounce.
	Debounce time.Duration
	// OnError receives non-fatal watcher errors.
	OnError func(error)
}

// Start begins watching root. report is called from a background goroutine,
// never more often than once per debounce interval, and never after Close
// returns.
func Start(root string, report func(Change), opts Options) (*Watcher, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}
	// The root has to exist: a watch on nothing would silently never fire,
	// which is far worse than refusing to start.
	if info, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("watch %s: %w", abs, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("watch %s: not a directory", abs)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}
	w := &Watcher{
		root:     abs,
		gitDir:   cleanDir(opts.GitDir),
		guard:    opts.Guard,
		debounce: cmpOr(opts.Debounce, DefaultDebounce),
		report:   report,
		fsw:      fsw,
		done:     make(chan struct{}),
		onError:  opts.OnError,
	}
	if err := w.addTree(abs); err != nil {
		fsw.Close()
		return nil, err
	}
	// Watched shallowly: only HEAD and index matter, and the rest of a git
	// directory churns constantly.
	if w.gitDir != "" && !w.insideRoot(w.gitDir) {
		if err := fsw.Add(w.gitDir); err != nil {
			w.fail(fmt.Errorf("watch %s: %w", w.gitDir, err))
		}
	}
	w.wg.Add(1)
	go w.loop()
	return w, nil
}

// Close stops the watcher and waits for its goroutine to finish, so no report
// can arrive after it returns.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		err = w.fsw.Close()
		w.wg.Wait()
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		w.mu.Unlock()
	})
	return err
}

// addTree adds a watch for dir and every directory below it.
func (w *Watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished between the listing and the walk is
			// not an error; there is simply nothing left to watch.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if err := w.fsw.Add(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			w.fail(fmt.Errorf("watch %s: %w", path, err))
		}
		if path != dir && d.Name() == gitDirName {
			// Watch .git itself so HEAD and index are seen; skip its
			// internals, which change constantly and mean nothing here.
			return fs.SkipDir
		}
		return nil
	})
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(event)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.fail(err)
		}
	}
}

func (w *Watcher) handle(event fsnotify.Event) {
	// A new directory needs its own watch, and may already contain files.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() && !w.insideGitDir(event.Name) {
			_ = w.addTree(event.Name)
		}
	}

	kind, interesting := w.classify(event.Name)
	if !interesting {
		return
	}
	if w.guard != nil && w.guard.Suppressed(event.Name) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if kind == changeGit {
		w.pending.Git = true
	} else {
		w.pending.Collection = true
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(w.debounce, w.flush)
	} else {
		w.timer.Reset(w.debounce)
	}
}

type changeKind int

const (
	changeCollection changeKind = iota
	changeGit
)

const gitDirName = ".git"

// classify decides whether an event matters and which kind it is.
func (w *Watcher) classify(name string) (changeKind, bool) {
	// The repository's git directory, wherever it is.
	// Resolve the parent directory rather than the file: a deleted file
	// cannot be resolved, but its directory still can.
	if w.gitDir != "" && cleanDir(filepath.Dir(name)) == w.gitDir {
		if base := filepath.Base(name); base == "HEAD" || base == "index" {
			return changeGit, true
		}
		return changeCollection, false
	}
	rel, err := filepath.Rel(w.root, name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return changeCollection, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if parts[0] == gitDirName {
		// Only the two files that say which commit is checked out and what is
		// staged. Everything else under .git is noise.
		if len(parts) == 2 && (parts[1] == "HEAD" || parts[1] == "index") {
			return changeGit, true
		}
		return changeCollection, false
	}
	return changeCollection, true
}

func (w *Watcher) insideGitDir(name string) bool {
	rel, err := filepath.Rel(w.root, name)
	if err != nil {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(rel), gitDirName)
}

func (w *Watcher) flush() {
	w.mu.Lock()
	change := w.pending
	w.pending = Change{}
	w.timer = nil
	w.mu.Unlock()

	if change == (Change{}) {
		return
	}
	select {
	case <-w.done:
		return // closed while the timer was pending
	default:
	}
	w.report(change)
}

func (w *Watcher) fail(err error) {
	if w.onError != nil {
		w.onError(err)
	}
}

// insideRoot reports whether path is at or below the watched root.
func (w *Watcher) insideRoot(path string) bool {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cleanDir normalises a path for comparison, resolving symlinks where it can
// (on macOS /var is /private/var, and the two must not look like different
// directories).
func cleanDir(path string) string {
	if path == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func cmpOr[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}
