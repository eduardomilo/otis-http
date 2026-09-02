package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// collector receives the watcher's reports and lets a test wait for one.
type collector struct{ ch chan Change }

func newCollector() *collector { return &collector{ch: make(chan Change, 16)} }

func (c *collector) report(change Change) { c.ch <- change }

// await returns the next change, or fails if none arrives.
func (c *collector) await(t *testing.T, why string) Change {
	t.Helper()
	select {
	case change := <-c.ch:
		return change
	case <-time.After(3 * time.Second):
		t.Fatalf("no change reported: %s", why)
		return Change{}
	}
}

// quiet fails if any change arrives within d.
func (c *collector) quiet(t *testing.T, d time.Duration, why string) {
	t.Helper()
	select {
	case change := <-c.ch:
		t.Fatalf("unexpected change %+v: %s", change, why)
	case <-time.After(d):
	}
}

func startTest(t *testing.T, root string, opts Options) *collector {
	t.Helper()
	c := newCollector()
	if opts.Debounce == 0 {
		opts.Debounce = 20 * time.Millisecond
	}
	w, err := Start(root, c.report, opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return c
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReportsFileChanges(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.http"), "GET https://example.test/\n")
	c := startTest(t, root, Options{})

	write(t, filepath.Join(root, "a.http"), "GET https://example.test/changed\n")
	if change := c.await(t, "editing a request"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}

	write(t, filepath.Join(root, "b.http"), "GET https://example.test/b\n")
	if change := c.await(t, "adding a request"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}

	if err := os.Remove(filepath.Join(root, "b.http")); err != nil {
		t.Fatal(err)
	}
	if change := c.await(t, "deleting a request"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}
}

// A directory created after the watch started must be watched too, or nothing
// added inside it is ever seen.
func TestWatchesNewSubdirectories(t *testing.T) {
	root := t.TempDir()
	c := startTest(t, root, Options{})

	nested := filepath.Join(root, "orders", "fixtures")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	c.await(t, "creating a directory")

	write(t, filepath.Join(nested, "seed.http"), "POST https://example.test/\n")
	if change := c.await(t, "adding a file in a new directory"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}
}

func TestGitHeadAndIndexAreGitChanges(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	write(t, filepath.Join(git, "HEAD"), "ref: refs/heads/main\n")
	c := startTest(t, root, Options{})

	write(t, filepath.Join(git, "HEAD"), "ref: refs/heads/feat/orders-v2\n")
	change := c.await(t, "switching branch")
	if !change.Git {
		t.Errorf("change = %+v, want Git", change)
	}
	if change.Collection {
		t.Errorf("change = %+v, want Collection false: .git is not the collection", change)
	}

	write(t, filepath.Join(git, "index"), "staged\n")
	if change := c.await(t, "staging"); !change.Git {
		t.Errorf("change = %+v, want Git", change)
	}
}

func TestGitInternalsAreIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	c := startTest(t, root, Options{})

	// The churn a real repository produces constantly.
	write(t, filepath.Join(root, ".git", "objects", "ab", "cdef"), "object\n")
	write(t, filepath.Join(root, ".git", "logs", "HEAD"), "reflog\n")
	write(t, filepath.Join(root, ".git", "COMMIT_EDITMSG"), "wip\n")
	c.quiet(t, 200*time.Millisecond, ".git internals must not wake the watcher")
}

func TestDebouncesABurst(t *testing.T) {
	root := t.TempDir()
	c := startTest(t, root, Options{Debounce: 120 * time.Millisecond})

	for i := range 20 {
		write(t, filepath.Join(root, "f.http"), "GET https://example.test/"+string(rune('a'+i))+"\n")
	}
	if change := c.await(t, "a burst of writes"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}
	c.quiet(t, 250*time.Millisecond, "a burst must produce exactly one report")
}

// The guard is what stops Otis' own writes from coming back as external ones.
func TestGuardSuppressesOtisOwnWrites(t *testing.T) {
	root := t.TempDir()
	guard := NewGuard()
	c := startTest(t, root, Options{Guard: guard})

	own := filepath.Join(root, "written-by-otis.http")
	release := guard.Writing(own)
	write(t, own, "POST https://example.test/\n")
	release()
	c.quiet(t, 300*time.Millisecond, "Otis' own write came back as an external change")

	// Once the guard has expired, the same file is external again.
	time.Sleep(DefaultGrace)
	write(t, own, "POST https://example.test/edited\n")
	if change := c.await(t, "an external edit after the guard expired"); !change.Collection {
		t.Errorf("change = %+v, want Collection", change)
	}
}

func TestCloseStopsReports(t *testing.T) {
	root := t.TempDir()
	c := newCollector()
	w, err := Start(root, c.report, Options{Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	write(t, filepath.Join(root, "after.http"), "GET https://example.test/\n")
	c.quiet(t, 200*time.Millisecond, "a closed watcher must not report")
}

func TestStartRejectsAMissingRoot(t *testing.T) {
	_, err := Start(filepath.Join(t.TempDir(), "nope"), func(Change) {}, Options{})
	if err == nil {
		t.Fatal("Start on a missing directory returned no error")
	}
}

// The common layout puts the collection in a subdirectory of the repository —
// ".requests" beside the code — so the git directory to watch is above the
// directory being watched, not inside it.
func TestGitDirAboveTheCollection(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	collection := filepath.Join(repo, ".requests")
	write(t, filepath.Join(collection, "a.http"), "GET https://example.test/\n")

	c := startTest(t, collection, Options{GitDir: gitDir})

	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat/orders-v2\n")
	change := c.await(t, "a branch switch in the repository above the collection")
	if !change.Git {
		t.Errorf("change = %+v, want Git", change)
	}

	// Its internals are still noise.
	write(t, filepath.Join(gitDir, "ORIG_HEAD"), "abc\n")
	c.quiet(t, 200*time.Millisecond, "an unrelated git file woke the watcher")
}
