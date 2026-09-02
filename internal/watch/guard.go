// Package watch keeps the window in step with the collection directory: a
// recursive filesystem watcher that re-walks after a change, and a guard that
// stops Otis' own writes from coming back as external ones.
package watch

import (
	"path/filepath"
	"sync"
	"time"
)

// DefaultGrace is how long a path stays suppressed after Otis finishes
// writing it. Filesystem events arrive after the write, not during it, so
// releasing the guard the instant the file is closed would let the app's own
// change through as an external one.
const DefaultGrace = 400 * time.Millisecond

// Guard suppresses filesystem events for the paths Otis is writing itself.
//
// Every write the app makes to a collection goes inside Writing:
//
//	release := guard.Writing(path)
//	defer release()
//	... write the file ...
//
// The watcher then drops events for that path until the grace period after
// release has passed. Without this, saving a request would be indistinguishable
// from someone editing it in another editor, and the app would re-walk and
// re-render on every keystroke it had just persisted.
//
// Nothing in Phase B writes to a collection. The guard exists now because the
// watcher has to consult it from the start; the first writer arrives in
// Phase C.
type Guard struct {
	grace time.Duration
	now   func() time.Time

	mu sync.Mutex
	// held counts the open Writing calls per path, and until is when a path
	// stops being suppressed once the last of them has been released.
	held  map[string]int
	until map[string]time.Time
}

// NewGuard returns a guard using DefaultGrace.
func NewGuard() *Guard { return newGuard(DefaultGrace, time.Now) }

// newGuard is the injectable constructor tests use to control the clock.
func newGuard(grace time.Duration, now func() time.Time) *Guard {
	return &Guard{
		grace: grace,
		now:   now,
		held:  make(map[string]int),
		until: make(map[string]time.Time),
	}
}

// Writing marks paths as being written by Otis and returns the function that
// releases them. Calls nest: a path stays suppressed until the outermost
// release, plus the grace period. Calling the returned function more than once
// is a no-op.
func (g *Guard) Writing(paths ...string) (release func()) {
	keys := make([]string, 0, len(paths))
	g.mu.Lock()
	for _, p := range paths {
		key := g.key(p)
		keys = append(keys, key)
		g.held[key]++
		// A path being written is suppressed regardless of the clock; the
		// deadline below only matters once the write is done.
		delete(g.until, key)
	}
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			deadline := g.now().Add(g.grace)
			g.mu.Lock()
			defer g.mu.Unlock()
			for _, key := range keys {
				if g.held[key] > 1 {
					g.held[key]--
					continue
				}
				delete(g.held, key)
				g.until[key] = deadline
			}
		})
	}
}

// Suppressed reports whether an event for path should be ignored because Otis
// wrote it. Expired entries are dropped as they are found, so a long-running
// session does not accumulate one per file it has ever written.
func (g *Guard) Suppressed(path string) bool {
	key := g.key(path)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held[key] > 0 {
		return true
	}
	deadline, ok := g.until[key]
	if !ok {
		return false
	}
	if g.now().Before(deadline) {
		return true
	}
	delete(g.until, key)
	return false
}

// key normalises a path so a write and the event it produces agree, whatever
// mixture of relative and absolute paths the callers use.
func (g *Guard) key(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}
