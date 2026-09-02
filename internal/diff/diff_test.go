package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here drive a real repository through the real git binary for
// setup and assertions, and Otis' own code for the operations. Asserting with
// `git status --porcelain` rather than with this package's own reader is the
// point: it is git that has to agree the index and worktree are what Otis
// says they are, not this package agreeing with itself.

func git(t *testing.T, dir string, args ...string) string {
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

func porcelain(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "status", "--porcelain")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The layout the design uses: a repository whose collection sits in a
// subdirectory, so every path conversion is exercised.
const bodyV1 = `# @name Create order
POST {{baseUrl}}/orders
Content-Type: application/json

{
  "currency": "{{currency}}",
  "customer": "{{customerId}}",
  "line_items": [
    {"sku": "SKU-1", "qty": 1},
    {"sku": "SKU-2", "qty": 2},
    {"sku": "SKU-3", "qty": 3},
    {"sku": "SKU-4", "qty": 4}
  ],
  "items": 1,
  "metadata": {
    "source": "otis",
    "version": 2,
    "channel": "web",
    "note": "created by a test"
  }
}

> {%
  test("created", () => expect(response.status).toBe(201));
%}
`

func repoWithCollection(t *testing.T) (*Repo, string, string) {
	t.Helper()
	work := t.TempDir()
	root := filepath.Join(work, ".requests")

	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "config", "user.name", "Test")
	git(t, work, "config", "user.email", "test@example.test")

	writeFile(t, filepath.Join(root, "orders", "_folder.http"), "# @auth bearer {{apiKey}}\nAccept: application/json\n")
	writeFile(t, filepath.Join(root, "orders", "create-order.http"), bodyV1)
	writeFile(t, filepath.Join(root, "orders", ".order"), "create-order.http\nlist-orders.http\n")
	writeFile(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders\n")
	writeFile(t, filepath.Join(work, "README.md"), "# the app itself, outside the collection\n")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "add expand param")

	repo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("Open returned no repository")
	}
	return repo, work, root
}

// Not being in a repository is a normal state, never an error: a collection is
// a directory of files and works perfectly well outside version control.
func TestOpenOutsideARepository(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open = %v, want no error", err)
	}
	if repo != nil {
		t.Error("Open found a repository where there is none")
	}
}

func TestOverviewOfACleanCollection(t *testing.T) {
	repo, _, _ := repoWithCollection(t)
	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if !over.Repository || over.Branch != "main" {
		t.Errorf("overview = %+v", over)
	}
	if len(over.Changes) != 0 {
		t.Errorf("changes = %+v, want none", over.Changes)
	}
	if over.LastCommit == nil || over.LastCommit.Subject != "add expand param" {
		t.Errorf("last commit = %+v", over.LastCommit)
	}
	if over.LastCommit.Author != "Test" {
		t.Errorf("author = %q", over.LastCommit.Author)
	}
	if !over.CanCommit {
		t.Errorf("CanCommit = false, reason %q", over.Reason)
	}
}

// The increment's own verification: a body change, a header change and a
// script change all appear as legible hunks, each with the semantic header
// the format allows rather than a bare "@@ -12,7 +12,9 @@".
//
// The three changes are far enough apart to stay three hunks, which is what
// screen 1b draws. Changes closer than twice the context merge into one, and
// that is correct — the label then names whichever region the hunk starts in.
func TestDiffOfABodyAHeaderAndAScript(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	changed := bodyV1
	changed = strings.Replace(changed, "Content-Type: application/json\n",
		"Content-Type: application/json\nIdempotency-Key: {{$uuid}}\n", 1)
	changed = strings.Replace(changed, `"items": 1`, `"items": 2`, 1)
	changed = strings.Replace(changed,
		`  test("created", () => expect(response.status).toBe(201));`,
		`  test("created", () => expect(response.status).toBe(201));
  test("has an id", () => expect(response.body.id).toBeDefined());`, 1)
	writeFile(t, filepath.Join(root, "orders", "create-order.http"), changed)

	fd, err := repo.File("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != StatusModified {
		t.Errorf("status = %q, want M", fd.Status)
	}
	if len(fd.Hunks) != 3 {
		t.Fatalf("hunks = %d, want 3 (header, body, script)", len(fd.Hunks))
	}

	// Each hunk is headed by the part of the format it falls in: the header
	// block, the request line above the body, and the tests marker.
	want := []string{LabelHeaders, "POST {{baseUrl}}/orders", LabelTests}
	for i, h := range fd.Hunks {
		if h.Label != want[i] {
			t.Errorf("hunk %d headed %q, want %q", i, h.Label, want[i])
		}
		if h.Adds == 0 && h.Dels == 0 {
			t.Errorf("hunk %d has no changes", i)
		}
	}
	if fd.Adds != 3 || fd.Dels != 1 {
		t.Errorf("+%d -%d, want +3 -1", fd.Adds, fd.Dels)
	}
}

// The footer's hunk count must match what is rendered, so the count in the
// changes list and the count of hunks in the file diff are the same number.
func TestChangeHunkCountMatchesTheFileDiff(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders?limit=10\n")

	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	var row *Change
	for i := range over.Changes {
		if over.Changes[i].Path == "orders/list-orders.http" {
			row = &over.Changes[i]
		}
	}
	if row == nil {
		t.Fatal("no row for the changed file")
	}
	fd, err := repo.File(row.Path)
	if err != nil {
		t.Fatal(err)
	}
	if row.Hunks != len(fd.Hunks) {
		t.Errorf("row says %d hunks, the diff has %d", row.Hunks, len(fd.Hunks))
	}
	if row.Adds != fd.Adds || row.Dels != fd.Dels {
		t.Errorf("row says +%d -%d, the diff has +%d -%d", row.Adds, row.Dels, fd.Adds, fd.Dels)
	}
	if over.Hunks != len(fd.Hunks) {
		t.Errorf("overview totals %d hunks, the one change has %d", over.Hunks, len(fd.Hunks))
	}
}

// Changes outside the collection are none of the diff view's business: it
// shows the collection, and the repository may hold a whole application.
func TestDiffIgnoresChangesOutsideTheCollection(t *testing.T) {
	repo, work, _ := repoWithCollection(t)
	writeFile(t, filepath.Join(work, "README.md"), "# changed outside\n")
	writeFile(t, filepath.Join(work, "main.go"), "package main\n")

	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 0 {
		t.Errorf("changes = %+v, want none inside the collection", over.Changes)
	}
}

// An untracked file shows its whole content as additions.
func TestUntrackedFileIsAllAdditions(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "cancel-order.http"), "DELETE {{baseUrl}}/orders/{{id}}\n")

	fd, err := repo.File("orders/cancel-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != StatusUntracked {
		t.Errorf("status = %q, want U", fd.Status)
	}
	if fd.Adds != 1 || fd.Dels != 0 {
		t.Errorf("+%d -%d, want +1 -0", fd.Adds, fd.Dels)
	}
	if len(fd.Hunks) != 1 || fd.Hunks[0].Lines[0].Kind != Added {
		t.Errorf("hunks = %+v", fd.Hunks)
	}
}

// A file moved outside Otis is reported by git as a delete plus an untracked
// file. The changes list shows one rename, because that is what happened.
func TestRenameIsOneChangeNotTwo(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	from := filepath.Join(root, "orders", "list-orders.http")
	to := filepath.Join(root, "orders", "fixtures", "list-orders.http")
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}

	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 1 {
		t.Fatalf("changes = %+v, want one rename", over.Changes)
	}
	c := over.Changes[0]
	if c.Status != StatusRenamed {
		t.Errorf("status = %q, want R", c.Status)
	}
	if c.Path != "orders/fixtures/list-orders.http" || c.OldPath != "orders/list-orders.http" {
		t.Errorf("rename = %q from %q", c.Path, c.OldPath)
	}
	if c.Adds != 0 || c.Dels != 0 {
		t.Errorf("a pure move should be +0 -0, got +%d -%d", c.Adds, c.Dels)
	}
}

// A move that also edited the file is still a rename, and its diff is the
// edit rather than a whole new file.
func TestRenameWithAnEditShowsOnlyTheEdit(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	from := filepath.Join(root, "orders", "create-order.http")
	to := filepath.Join(root, "orders", "fixtures", "create-order.http")
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	writeFile(t, to, strings.Replace(bodyV1, `"items": 1`, `"items": 9`, 1))

	fd, err := repo.File("orders/fixtures/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Status != StatusRenamed || fd.OldPath != "orders/create-order.http" {
		t.Errorf("diff = %+v", fd)
	}
	if fd.Adds != 1 || fd.Dels != 1 {
		t.Errorf("+%d -%d, want the one edited line", fd.Adds, fd.Dels)
	}
}

// Reordering a folder is one line moved in one file, which is the claim
// screen 2a makes to a reviewer.
func TestReorderIsOneLineInOneFile(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", ".order"), "list-orders.http\ncreate-order.http\n")

	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 1 || over.Changes[0].Path != "orders/.order" {
		t.Fatalf("changes = %+v, want only the order file", over.Changes)
	}
	if over.Adds != 1 || over.Dels != 1 {
		t.Errorf("+%d -%d, want +1 -1", over.Adds, over.Dels)
	}
	if over.Hunks != 1 {
		t.Errorf("hunks = %d, want 1", over.Hunks)
	}
}

// A rename is not staged until both halves are: until then git still has the
// file at its old path. Inferring the flag from the three contents gets this
// wrong, because the paired state's HEAD side comes from the old path while
// its index side is the new path's.
func TestRenameIsNotStagedUntilBothHalvesAre(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	from := filepath.Join(root, "orders", "list-orders.http")
	to := filepath.Join(root, "orders", "fixtures", "list-orders.http")
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}

	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 1 {
		t.Fatalf("changes = %+v", over.Changes)
	}
	if over.Changes[0].Staged {
		t.Error("Staged = true, but neither half of the move is staged")
	}
	if !over.Changes[0].Unstaged {
		t.Error("Unstaged = false, but the whole move is unstaged")
	}

	if err := repo.StageAll(); err != nil {
		t.Fatal(err)
	}
	over, err = repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if len(over.Changes) != 1 {
		t.Fatalf("changes after staging = %+v", over.Changes)
	}
	if !over.Changes[0].Staged || over.Changes[0].Unstaged {
		t.Errorf("change = %+v, want staged and not unstaged", over.Changes[0])
	}
	_ = work
}
