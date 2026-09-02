package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// run executes a git command in dir, failing the test if it does not succeed.
// The tests drive the real git binary so what they assert is what git says,
// not what go-git and the test agree on.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Otis", "GIT_AUTHOR_EMAIL=otis@example.test",
		"GIT_COMMITTER_NAME=Otis", "GIT_COMMITTER_EMAIL=otis@example.test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
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

// newRepo makes a repository with one commit and returns its root.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "README.md"), "# repo\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "first")
	return dir
}

func TestNotARepositoryIsNormal(t *testing.T) {
	state, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read outside a repository returned an error: %v", err)
	}
	if state.Repository {
		t.Error("Repository is true outside a repository")
	}
	if state.Branch != "" || len(state.Statuses) != 0 {
		t.Errorf("state = %+v, want the zero value", state)
	}
}

func TestBranchAndHead(t *testing.T) {
	dir := newRepo(t)
	state, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Repository {
		t.Fatal("Repository is false inside a repository")
	}
	if state.Branch != "main" {
		t.Errorf("Branch = %q, want main", state.Branch)
	}
	if len(state.Head) != 7 {
		t.Errorf("Head = %q, want a 7-character short hash", state.Head)
	}
	if state.HasUpstream {
		t.Error("HasUpstream is true for a branch that was never pushed")
	}

	run(t, dir, "checkout", "-qb", "feat/orders-v2")
	state, err = Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Branch != "feat/orders-v2" {
		t.Errorf("Branch = %q after checkout, want feat/orders-v2", state.Branch)
	}
}

func TestDetachedHead(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "checkout", "-q", "--detach")
	state, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Detached {
		t.Error("Detached is false on a detached HEAD")
	}
	if state.Branch != "" {
		t.Errorf("Branch = %q on a detached HEAD, want empty", state.Branch)
	}
}

func TestStatuses(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "orders", "create-order.http"), "POST https://example.test/\n")
	run(t, dir, "add", "orders")
	run(t, dir, "commit", "-qm", "add orders")

	write(t, filepath.Join(dir, "orders", "create-order.http"), "POST https://example.test/changed\n")
	write(t, filepath.Join(dir, "orders", "fixtures", "seed.http"), "POST https://example.test/seed\n")
	write(t, filepath.Join(dir, "orders", "staged.http"), "GET https://example.test/\n")
	run(t, dir, "add", "orders/staged.http")

	state, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{
		"orders/create-order.http":  StatusModified,
		"orders/fixtures/seed.http": StatusUntracked,
		"orders/staged.http":        StatusAdded,
	}
	for path, status := range want {
		if got := state.Statuses[path]; got != status {
			t.Errorf("status of %s = %q, want %q (all: %+v)", path, got, status, state.Statuses)
		}
	}
	if _, ok := state.Statuses["README.md"]; ok {
		t.Error("an unchanged file has a status")
	}
}

// The statuses are relative to the collection, which is often a subdirectory
// of the repository (the ".requests beside the code" layout).
func TestStatusesAreRelativeToTheCollectionNotTheRepository(t *testing.T) {
	repo := newRepo(t)
	collection := filepath.Join(repo, ".requests")
	write(t, filepath.Join(collection, "orders", "create.http"), "POST https://example.test/\n")
	write(t, filepath.Join(repo, "main.go"), "package main\n")

	state, err := Read(collection)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Statuses["orders/create.http"]; got != StatusUntracked {
		t.Errorf("status = %q, want U; all: %+v", got, state.Statuses)
	}
	for path := range state.Statuses {
		if path == "main.go" || path == "../main.go" {
			t.Errorf("a file outside the collection appears in the statuses: %q", path)
		}
	}
	if state.Branch != "main" {
		t.Errorf("Branch = %q, want main: the repository is above the collection", state.Branch)
	}
}

func TestAheadBehind(t *testing.T) {
	origin := newRepo(t)
	run(t, origin, "config", "receive.denyCurrentBranch", "ignore")

	clone := t.TempDir()
	run(t, clone, "clone", "-q", origin, ".")
	state, err := Read(clone)
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasUpstream {
		t.Fatal("HasUpstream is false for a fresh clone")
	}
	if state.Ahead != 0 || state.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d for a fresh clone, want 0/0", state.Ahead, state.Behind)
	}

	// Two local commits: ahead 2.
	write(t, filepath.Join(clone, "a.http"), "GET https://example.test/a\n")
	run(t, clone, "add", ".")
	run(t, clone, "commit", "-qm", "a")
	write(t, filepath.Join(clone, "b.http"), "GET https://example.test/b\n")
	run(t, clone, "add", ".")
	run(t, clone, "commit", "-qm", "b")

	state, err = Read(clone)
	if err != nil {
		t.Fatal(err)
	}
	if state.Ahead != 2 || state.Behind != 0 {
		t.Errorf("ahead/behind = %d/%d after two local commits, want 2/0", state.Ahead, state.Behind)
	}

	// One commit upstream that we have not fetched into our branch: behind 1.
	write(t, filepath.Join(origin, "c.http"), "GET https://example.test/c\n")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-qm", "c")
	run(t, clone, "fetch", "-q")

	state, err = Read(clone)
	if err != nil {
		t.Fatal(err)
	}
	if state.Ahead != 2 || state.Behind != 1 {
		t.Errorf("ahead/behind = %d/%d after fetching one upstream commit, want 2/1", state.Ahead, state.Behind)
	}
}

func TestIsRepository(t *testing.T) {
	repo := newRepo(t)
	nested := filepath.Join(repo, ".requests", "orders")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsRepository(nested) {
		t.Error("IsRepository = false inside a repository")
	}
	if IsRepository(t.TempDir()) {
		t.Error("IsRepository = true outside a repository")
	}
}

func TestEmptyRepositoryHasNoCommits(t *testing.T) {
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skip()
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	state, err := Read(dir)
	if err != nil {
		t.Fatalf("Read on a repository with no commits: %v", err)
	}
	if !state.Repository {
		t.Error("Repository is false for an initialised repository")
	}
	if state.Head != "" {
		t.Errorf("Head = %q with no commits, want empty", state.Head)
	}
}

func TestDirFindsTheRepositoryAboveTheCollection(t *testing.T) {
	repo := newRepo(t)
	collection := filepath.Join(repo, ".requests", "orders")
	if err := os.MkdirAll(collection, 0o755); err != nil {
		t.Fatal(err)
	}
	got := Dir(collection)
	want, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(got); err != nil || real != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if Dir(t.TempDir()) != "" {
		t.Error("Dir found a repository where there is none")
	}
}

// A worktree's .git is a file pointing at the real directory.
func TestDirFollowsAGitFile(t *testing.T) {
	repo := newRepo(t)
	elsewhere := t.TempDir()
	real := filepath.Join(elsewhere, "realgit")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	write(t, filepath.Join(linked, ".git"), "gitdir: "+real+"\n")
	if got := Dir(linked); got != real {
		t.Errorf("Dir = %q, want %q", got, real)
	}
	_ = repo
}
