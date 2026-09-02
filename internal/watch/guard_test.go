package watch

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the tests move time without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestGuard(grace time.Duration) (*Guard, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return newGuard(grace, clock.now), clock
}

func TestGuardSuppressesDuringAndAfterAWrite(t *testing.T) {
	g, clock := newTestGuard(100 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "orders", "create-order.http")

	if g.Suppressed(path) {
		t.Fatal("an untouched path is suppressed")
	}

	release := g.Writing(path)
	if !g.Suppressed(path) {
		t.Error("a path being written is not suppressed")
	}
	// A long write does not expire on its own.
	clock.advance(time.Hour)
	if !g.Suppressed(path) {
		t.Error("a path still being written expired")
	}

	release()
	if !g.Suppressed(path) {
		t.Error("a path is not suppressed during the grace period")
	}
	clock.advance(99 * time.Millisecond)
	if !g.Suppressed(path) {
		t.Error("the grace period ended early")
	}
	clock.advance(2 * time.Millisecond)
	if g.Suppressed(path) {
		t.Error("the grace period did not end")
	}
}

func TestGuardIsPerPath(t *testing.T) {
	g, _ := newTestGuard(time.Second)
	dir := t.TempDir()
	mine := filepath.Join(dir, "mine.http")
	theirs := filepath.Join(dir, "theirs.http")

	defer g.Writing(mine)()
	if !g.Suppressed(mine) {
		t.Error("the written path is not suppressed")
	}
	if g.Suppressed(theirs) {
		t.Error("an unrelated path is suppressed")
	}
}

func TestGuardNormalisesPaths(t *testing.T) {
	g, _ := newTestGuard(time.Second)
	dir := t.TempDir()
	defer g.Writing(filepath.Join(dir, "a", "..", "b.http"))()
	if !g.Suppressed(filepath.Join(dir, "b.http")) {
		t.Error("the same file spelled differently was not suppressed")
	}
}

func TestGuardNests(t *testing.T) {
	g, clock := newTestGuard(10 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "x.http")

	outer := g.Writing(path)
	inner := g.Writing(path)
	inner()
	if !g.Suppressed(path) {
		t.Fatal("the inner release ended the outer write")
	}
	outer()
	clock.advance(11 * time.Millisecond)
	if g.Suppressed(path) {
		t.Error("still suppressed after the outer release and the grace period")
	}
}

func TestGuardReleaseIsIdempotent(t *testing.T) {
	g, clock := newTestGuard(10 * time.Millisecond)
	path := filepath.Join(t.TempDir(), "x.http")
	first := g.Writing(path)
	first()
	first() // a double release must not un-suppress a later write
	second := g.Writing(path)
	if !g.Suppressed(path) {
		t.Fatal("the second write is not suppressed")
	}
	second()
	clock.advance(11 * time.Millisecond)
	if g.Suppressed(path) {
		t.Error("still suppressed after both writes finished")
	}
}

func TestGuardForgetsExpiredPaths(t *testing.T) {
	g, clock := newTestGuard(time.Millisecond)
	dir := t.TempDir()
	for i := range 50 {
		g.Writing(filepath.Join(dir, string(rune('a'+i%26)), "f.http"))()
	}
	clock.advance(time.Second)
	for i := range 50 {
		g.Suppressed(filepath.Join(dir, string(rune('a'+i%26)), "f.http"))
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.until) != 0 || len(g.held) != 0 {
		t.Errorf("guard kept %d expired and %d held entries", len(g.until), len(g.held))
	}
}
