package services

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/diff"
	"github.com/otis-http/otis/internal/settings"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newDiffService opens a collection inside a real repository and returns the
// diff service over it.
func newDiffService(t *testing.T) (*DiffService, *CollectionService, string, string) {
	t.Helper()
	work := t.TempDir()
	root := filepath.Join(work, ".requests")
	gitIn(t, work, "init", "-q", "-b", "main")
	gitIn(t, work, "config", "user.name", "Test")
	gitIn(t, work, "config", "user.email", "test@example.test")

	write(t, filepath.Join(root, "orders", "_folder.http"), "Accept: application/json\n")
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nPOST https://example.test/orders\n")
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "add expand param")

	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	return NewDiffService(collections), collections, work, root
}

func TestDiffServiceOverview(t *testing.T) {
	svc, _, _, root := newDiffService(t)
	write(t, filepath.Join(root, "orders", "create-order.http"),
		"# @name Create order\nPOST https://example.test/orders?expand=items\n")

	over, err := svc.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if !over.Repository || over.Branch != "main" {
		t.Errorf("overview = %+v", over)
	}
	if len(over.Changes) != 1 || over.Changes[0].Path != "orders/create-order.http" {
		t.Fatalf("changes = %+v", over.Changes)
	}
	if over.LastCommit == nil || over.LastCommit.Subject != "add expand param" {
		t.Errorf("last commit = %+v", over.LastCommit)
	}
}

// A collection outside a repository is a normal empty state, not an error.
func TestDiffServiceOutsideARepository(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ping.http"), "GET https://example.test/\n")
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	svc := NewDiffService(collections)

	over, err := svc.Overview()
	if err != nil {
		t.Fatalf("Overview = %v, want no error", err)
	}
	if over.Repository {
		t.Error("Repository = true outside a repository")
	}
	// A mutating call has to say what is wrong, rather than pretend.
	if _, err := svc.StageAll(); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("StageAll = %v, want it to name the reason", err)
	}
}

func TestDiffServiceStageAndCommit(t *testing.T) {
	svc, _, work, root := newDiffService(t)
	write(t, filepath.Join(root, "orders", "create-order.http"),
		"# @name Create order\nPOST https://example.test/orders?expand=items\n")

	over, err := svc.Stage("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 1 || !over.Changes[0].Staged || over.Changes[0].Unstaged {
		t.Errorf("change = %+v, want staged and not unstaged", over.Changes[0])
	}

	result, err := svc.Commit("expand line items")
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit.Subject != "expand line items" {
		t.Errorf("commit = %+v", result.Commit)
	}
	if len(result.Overview.Changes) != 0 {
		t.Errorf("changes after commit = %+v, want none", result.Overview.Changes)
	}
	if got := strings.TrimSpace(gitIn(t, work, "status", "--porcelain")); got != "" {
		t.Errorf("git status = %q, want clean", got)
	}
}

// The distinct method name and the confirm flag are the safety, not the
// dialog: a caller that has not said "yes, destroy this" gets refused.
func TestDiffServiceDiscardNeedsConfirmation(t *testing.T) {
	svc, _, _, root := newDiffService(t)
	file := filepath.Join(root, "orders", "create-order.http")
	write(t, file, "GET https://example.test/nonsense\n")

	if _, err := svc.DiscardFile("orders/create-order.http", false); !errors.Is(err, diff.ErrNoConfirm) {
		t.Errorf("DiscardFile without confirm = %v, want ErrNoConfirm", err)
	}
	if _, err := svc.DiscardHunks("orders/create-order.http", []int{0}, false); !errors.Is(err, diff.ErrNoConfirm) {
		t.Errorf("DiscardHunks without confirm = %v, want ErrNoConfirm", err)
	}
	if got := readFile(t, file); got != "GET https://example.test/nonsense\n" {
		t.Errorf("a refused discard changed the file: %q", got)
	}

	over, err := svc.DiscardFile("orders/create-order.http", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 0 {
		t.Errorf("changes after discard = %+v, want none", over.Changes)
	}
	if got := readFile(t, file); got != "# @name Create order\nPOST https://example.test/orders\n" {
		t.Errorf("file after discard = %q", got)
	}
}

// Discarding writes a collection file, so it must hold the write guard — or
// the watcher reports Otis' own write as somebody else's change.
func TestDiffServiceDiscardHoldsTheWriteGuard(t *testing.T) {
	svc, collections, _, root := newDiffService(t)
	file := filepath.Join(root, "orders", "create-order.http")
	write(t, file, "GET https://example.test/nonsense\n")

	if _, err := svc.DiscardFile("orders/create-order.http", true); err != nil {
		t.Fatal(err)
	}
	// The guard keeps a path suppressed for a grace period after the write,
	// which is what makes this checkable without racing it.
	if !collections.Guard().Suppressed(file) {
		t.Error("the discard wrote the file without holding the write guard")
	}
}

func TestDiffServiceHunkOperations(t *testing.T) {
	svc, _, work, root := newDiffService(t)
	file := filepath.Join(root, "orders", "big.http")
	// Two changes far enough apart to stay two hunks: with three lines of
	// context each side, anything closer merges.
	base := "POST https://example.test/x\nContent-Type: application/json\n\n{\n" +
		"  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3,\n  \"d\": 4,\n  \"e\": 5,\n" +
		"  \"f\": 6,\n  \"g\": 7,\n  \"h\": 8,\n  \"i\": 9,\n  \"j\": 10,\n" +
		"  \"k\": 11,\n  \"l\": 12\n}\n"
	write(t, file, base)
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-q", "-m", "add big")

	changed := strings.Replace(base, `"a": 1,`, `"a": 11,`, 1)
	changed = strings.Replace(changed, `"l": 12`, `"l": 120`, 1)
	write(t, file, changed)

	fd, err := svc.File("orders/big.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(fd.Hunks))
	}

	if _, err := svc.StageHunks("orders/big.http", []int{0}); err != nil {
		t.Fatal(err)
	}
	staged := gitIn(t, work, "diff", "--cached", "--unified=0", "--", ".requests/orders/big.http")
	if !strings.Contains(staged, `+  "a": 11,`) || strings.Contains(staged, `"l": 120`) {
		t.Errorf("staged diff = %s", staged)
	}

	if _, err := svc.UnstageHunks("orders/big.http", []int{0}); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, work, "diff", "--cached", "--", ".requests/orders/big.http"); strings.TrimSpace(got) != "" {
		t.Errorf("staged diff after unstaging = %q, want empty", got)
	}

	// Discarding one hunk leaves the other change on disk.
	if _, err := svc.DiscardHunks("orders/big.http", []int{1}, true); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(base, `"a": 1,`, `"a": 11,`, 1)
	if got := readFile(t, file); got != want {
		t.Errorf("file after discarding the second hunk =\n%s\nwant\n%s", got, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
