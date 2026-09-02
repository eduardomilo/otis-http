package diff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hunkLabels is what the view would print above each hunk, for a readable
// failure message.
func hunkLabels(hunks []Hunk) []string {
	out := make([]string, 0, len(hunks))
	for _, h := range hunks {
		out = append(out, h.Label)
	}
	return out
}

func TestStageAndUnstageAWholeFile(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "list-orders.http")
	writeFile(t, file, "GET {{baseUrl}}/orders?limit=10\n")

	if got := porcelain(t, work); !strings.Contains(got, " M .requests/orders/list-orders.http") {
		t.Fatalf("git status before = %q", got)
	}
	if err := repo.Stage("orders/list-orders.http"); err != nil {
		t.Fatal(err)
	}
	// git itself has to agree the change is staged.
	if got := porcelain(t, work); !strings.Contains(got, "M  .requests/orders/list-orders.http") {
		t.Errorf("git status after staging = %q", got)
	}

	if err := repo.Unstage("orders/list-orders.http"); err != nil {
		t.Fatal(err)
	}
	if got := porcelain(t, work); !strings.Contains(got, " M .requests/orders/list-orders.http") {
		t.Errorf("git status after unstaging = %q", got)
	}
	// Unstaging never touches the working tree.
	if read(t, file) != "GET {{baseUrl}}/orders?limit=10\n" {
		t.Error("unstaging changed the file on disk")
	}
}

func TestStageAnUntrackedFileAndUnstageItBack(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "cancel-order.http"), "DELETE {{baseUrl}}/orders/{{id}}\n")

	if err := repo.Stage("orders/cancel-order.http"); err != nil {
		t.Fatal(err)
	}
	if got := porcelain(t, work); !strings.Contains(got, "A  .requests/orders/cancel-order.http") {
		t.Errorf("git status = %q, want the file added", got)
	}
	// Back to untracked: out of the index, still on disk.
	if err := repo.Unstage("orders/cancel-order.http"); err != nil {
		t.Fatal(err)
	}
	if got := porcelain(t, work); !strings.Contains(got, "?? .requests/orders/cancel-order.http") {
		t.Errorf("git status = %q, want the file untracked again", got)
	}
	if _, err := os.Stat(filepath.Join(root, "orders", "cancel-order.http")); err != nil {
		t.Error("unstaging deleted the file")
	}
}

func TestStageADeletion(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	if err := os.Remove(filepath.Join(root, "orders", "list-orders.http")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage("orders/list-orders.http"); err != nil {
		t.Fatal(err)
	}
	if got := porcelain(t, work); !strings.Contains(got, "D  .requests/orders/list-orders.http") {
		t.Errorf("git status = %q, want the deletion staged", got)
	}
}

// The increment's own verification: stage one hunk of three, and git must
// report exactly that hunk staged and the other two not.
func TestStageOneHunkOfSeveral(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "create-order.http")
	changed := strings.Replace(bodyV1, "Content-Type: application/json\n",
		"Content-Type: application/json\nIdempotency-Key: {{$uuid}}\n", 1)
	changed = strings.Replace(changed, `"items": 1,`, `"items": 2,`, 1)
	changed = strings.Replace(changed,
		`  test("created", () => expect(response.status).toBe(201));`,
		"  test(\"created\", () => expect(response.status).toBe(201));\n  test(\"has an id\", () => expect(response.body.id).toBeDefined());", 1)
	writeFile(t, file, changed)

	fd, err := repo.File("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 3 {
		t.Fatalf("hunks = %v, want 3", hunkLabels(fd.Hunks))
	}

	// Stage the body hunk only.
	if err := repo.StageHunks("orders/create-order.http", []int{1}); err != nil {
		t.Fatal(err)
	}

	// git's own view: the staged diff is the one line, and the unstaged diff
	// is the other two changes.
	staged := git(t, work, "diff", "--cached", "--unified=0", "--", ".requests/orders/create-order.http")
	if !strings.Contains(staged, `+  "items": 2,`) {
		t.Errorf("staged diff should hold the body change:\n%s", staged)
	}
	if strings.Contains(staged, "Idempotency-Key") || strings.Contains(staged, "has an id") {
		t.Errorf("staged diff holds a hunk that was not staged:\n%s", staged)
	}
	unstaged := git(t, work, "diff", "--unified=0", "--", ".requests/orders/create-order.http")
	if !strings.Contains(unstaged, "+Idempotency-Key") || !strings.Contains(unstaged, "has an id") {
		t.Errorf("unstaged diff should hold the other two hunks:\n%s", unstaged)
	}
	if strings.Contains(unstaged, `"items"`) {
		t.Errorf("unstaged diff still holds the staged hunk:\n%s", unstaged)
	}

	// The file on disk is untouched by staging.
	if read(t, file) != changed {
		t.Error("staging a hunk changed the working tree")
	}

	// The view now says that hunk is staged and the others are not.
	fd, err = repo.File("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 3 {
		t.Fatalf("hunks = %v, want 3 still", hunkLabels(fd.Hunks))
	}
	if fd.Hunks[0].Staged || !fd.Hunks[1].Staged || fd.Hunks[2].Staged {
		t.Errorf("staged flags = %v %v %v, want false true false",
			fd.Hunks[0].Staged, fd.Hunks[1].Staged, fd.Hunks[2].Staged)
	}

	// Unstaging it puts the index back to HEAD.
	if err := repo.UnstageHunks("orders/create-order.http", []int{1}); err != nil {
		t.Fatal(err)
	}
	if got := git(t, work, "diff", "--cached", "--", ".requests/orders/create-order.http"); strings.TrimSpace(got) != "" {
		t.Errorf("staged diff after unstaging = %q, want empty", got)
	}
	if got := porcelain(t, work); !strings.Contains(got, " M .requests/orders/create-order.http") {
		t.Errorf("git status = %q, want the file modified and unstaged", got)
	}
}

// Staging two hunks then a third accumulates rather than replacing, because
// the index is rebuilt from HEAD plus everything staged.
func TestStageHunksAccumulate(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "create-order.http")
	changed := strings.Replace(bodyV1, "Content-Type: application/json\n",
		"Content-Type: application/json\nIdempotency-Key: {{$uuid}}\n", 1)
	changed = strings.Replace(changed, `"items": 1,`, `"items": 2,`, 1)
	writeFile(t, file, changed)

	if err := repo.StageHunks("orders/create-order.http", []int{0}); err != nil {
		t.Fatal(err)
	}
	if err := repo.StageHunks("orders/create-order.http", []int{1}); err != nil {
		t.Fatal(err)
	}

	staged := git(t, work, "diff", "--cached", "--", ".requests/orders/create-order.http")
	if !strings.Contains(staged, "Idempotency-Key") || !strings.Contains(staged, `"items": 2`) {
		t.Errorf("both hunks should be staged:\n%s", staged)
	}
	// Nothing left unstaged, so the file is fully staged.
	if got := porcelain(t, work); !strings.Contains(got, "M  .requests/orders/create-order.http") {
		t.Errorf("git status = %q, want the file fully staged", got)
	}
}

// Discard is destructive, so it refuses without an explicit confirmation from
// the caller. This is the safety, not the dialog in front of it.
func TestDiscardRefusesWithoutConfirmation(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "list-orders.http")
	writeFile(t, file, "GET {{baseUrl}}/orders?limit=10\n")

	if err := repo.Discard("orders/list-orders.http", false, nil); !errors.Is(err, ErrNoConfirm) {
		t.Errorf("Discard without confirm = %v, want ErrNoConfirm", err)
	}
	if err := repo.DiscardHunks("orders/list-orders.http", []int{0}, false, nil); !errors.Is(err, ErrNoConfirm) {
		t.Errorf("DiscardHunks without confirm = %v, want ErrNoConfirm", err)
	}
	// And the file is untouched by the refusal.
	if read(t, file) != "GET {{baseUrl}}/orders?limit=10\n" {
		t.Error("a refused discard changed the file")
	}
}

func TestDiscardAWholeFileRestoresIt(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "create-order.http")
	writeFile(t, file, "GET {{baseUrl}}/nonsense\n")

	if err := repo.Discard("orders/create-order.http", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, file); got != bodyV1 {
		t.Errorf("file after discard =\n%s\nwant the committed copy", got)
	}
	if got := strings.TrimSpace(porcelain(t, work)); got != "" {
		t.Errorf("git status after discard = %q, want clean", got)
	}
}

// Discarding an untracked file deletes it: there is no committed copy to
// return to. It is the most destructive thing in the app, which is why the
// confirmation exists.
func TestDiscardAnUntrackedFileDeletesIt(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "cancel-order.http")
	writeFile(t, file, "DELETE {{baseUrl}}/orders/{{id}}\n")

	if err := repo.Discard("orders/cancel-order.http", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the untracked file is still there")
	}
	if got := strings.TrimSpace(porcelain(t, work)); got != "" {
		t.Errorf("git status = %q, want clean", got)
	}
}

// The increment's own verification: discard a hunk and the file on disk
// matches — the discarded hunk is gone and the others are still there.
func TestDiscardOneHunkLeavesTheOthers(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "create-order.http")
	changed := strings.Replace(bodyV1, "Content-Type: application/json\n",
		"Content-Type: application/json\nIdempotency-Key: {{$uuid}}\n", 1)
	changed = strings.Replace(changed, `"items": 1,`, `"items": 2,`, 1)
	writeFile(t, file, changed)

	fd, err := repo.File("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 2 {
		t.Fatalf("hunks = %v, want 2", hunkLabels(fd.Hunks))
	}

	// Throw away the header hunk, keep the body change.
	if err := repo.DiscardHunks("orders/create-order.http", []int{0}, true, nil); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(bodyV1, `"items": 1,`, `"items": 2,`, 1)
	if got := read(t, file); got != want {
		t.Errorf("file after discarding the header hunk =\n%s\nwant\n%s", got, want)
	}
	if got := porcelain(t, work); !strings.Contains(got, " M .requests/orders/create-order.http") {
		t.Errorf("git status = %q, want still modified", got)
	}

	// And the remaining change is still one reviewable hunk.
	fd, err = repo.File("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 1 || fd.Hunks[0].Label != "POST {{baseUrl}}/orders" {
		t.Errorf("hunks = %v, want just the body hunk", hunkLabels(fd.Hunks))
	}
}

// A stale view must not be able to stage or discard the wrong thing.
func TestHunkOperationsRejectAnOutOfRangeIndex(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders?limit=10\n")

	for _, err := range []error{
		repo.StageHunks("orders/list-orders.http", []int{7}),
		repo.UnstageHunks("orders/list-orders.http", []int{7}),
		repo.DiscardHunks("orders/list-orders.http", []int{7}, true, nil),
	} {
		if err == nil {
			t.Error("an out-of-range hunk should be refused")
		} else if !strings.Contains(err.Error(), "out of date") {
			t.Errorf("error = %v, want it to say the view is out of date", err)
		}
	}
}

// The write guard: discarding writes a collection file, so the caller's hook
// has to be given the path before the write and released after it.
func TestDiscardAsksTheCallerToGuardItsWrites(t *testing.T) {
	repo, _, root := repoWithCollection(t)
	file := filepath.Join(root, "orders", "create-order.http")
	writeFile(t, file, "GET {{baseUrl}}/nonsense\n")

	var guarded []string
	released := 0
	write := Writer(func(paths ...string) func() {
		guarded = append(guarded, paths...)
		return func() { released++ }
	})

	if err := repo.Discard("orders/create-order.http", true, write); err != nil {
		t.Fatal(err)
	}
	// The guarded path is the resolved one: Open evaluates symlinks so that
	// on macOS /var and /private/var are the same directory, and the guard
	// has to be told the path the watcher will report.
	if len(guarded) != 1 || guarded[0] != resolvePath(file) {
		t.Errorf("guarded = %v, want %q", guarded, resolvePath(file))
	}
	if released != 1 {
		t.Errorf("released %d times, want 1", released)
	}
}

func TestCommitRecordsWhatIsStaged(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders?limit=10\n")

	if _, err := repo.Commit("nothing staged yet"); !errors.Is(err, ErrNothingToDo) {
		t.Errorf("Commit with an empty index = %v, want ErrNothingToDo", err)
	}
	if _, err := repo.Commit(""); err == nil {
		t.Error("a commit with no message should be refused")
	}

	if err := repo.Stage("orders/list-orders.http"); err != nil {
		t.Fatal(err)
	}
	commit, err := repo.Commit("add a limit")
	if err != nil {
		t.Fatal(err)
	}
	if commit.Subject != "add a limit" || commit.Author != "Test" {
		t.Errorf("commit = %+v", commit)
	}
	if got := strings.TrimSpace(porcelain(t, work)); got != "" {
		t.Errorf("git status after commit = %q, want clean", got)
	}
	// git's own log has to agree.
	if got := git(t, work, "log", "-1", "--pretty=%s"); strings.TrimSpace(got) != "add a limit" {
		t.Errorf("git log = %q", got)
	}
	// And the view's last-commit line follows.
	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if over.LastCommit == nil || over.LastCommit.Subject != "add a limit" {
		t.Errorf("last commit = %+v", over.LastCommit)
	}
	if len(over.Changes) != 0 {
		t.Errorf("changes = %+v, want none", over.Changes)
	}
}

// Stage all stages the collection's changes and nothing else: the diff view
// shows the collection, and sweeping up unrelated source edits would stage
// things it never showed.
func TestStageAllIsScopedToTheCollection(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	writeFile(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders?limit=10\n")
	writeFile(t, filepath.Join(root, "orders", "cancel-order.http"), "DELETE {{baseUrl}}/orders/{{id}}\n")
	writeFile(t, filepath.Join(work, "README.md"), "# changed outside the collection\n")

	if err := repo.StageAll(); err != nil {
		t.Fatal(err)
	}
	got := porcelain(t, work)
	if !strings.Contains(got, "M  .requests/orders/list-orders.http") {
		t.Errorf("the modified request is not staged:\n%s", got)
	}
	if !strings.Contains(got, "A  .requests/orders/cancel-order.http") {
		t.Errorf("the new request is not staged:\n%s", got)
	}
	if !strings.Contains(got, " M README.md") {
		t.Errorf("the file outside the collection should be left alone:\n%s", got)
	}
}

// Staging a rename has to move both halves, or the index keeps the file at
// its old path and the commit shows a delete plus an add.
func TestStageAllStagesARename(t *testing.T) {
	repo, work, root := repoWithCollection(t)
	from := filepath.Join(root, "orders", "list-orders.http")
	to := filepath.Join(root, "orders", "fixtures", "list-orders.http")
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}

	if err := repo.StageAll(); err != nil {
		t.Fatal(err)
	}
	// git detects the rename itself once both halves are staged.
	if got := git(t, work, "status", "--porcelain"); !strings.Contains(got, "R  .requests/orders/list-orders.http -> .requests/orders/fixtures/list-orders.http") {
		t.Errorf("git status = %q, want a staged rename", got)
	}
	if _, err := repo.Commit("move list-orders into fixtures"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(porcelain(t, work)); got != "" {
		t.Errorf("git status after commit = %q, want clean", got)
	}
}

// A repository with no commits at all is a normal state (a fresh git init),
// and every file in the collection is an addition.
func TestOverviewInARepositoryWithNoCommits(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, ".requests")
	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "config", "user.name", "Test")
	git(t, work, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(root, "ping.http"), "GET https://example.test/\n")

	repo, err := Open(root)
	if err != nil || repo == nil {
		t.Fatalf("Open = %v, %v", repo, err)
	}
	over, err := repo.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if !over.Repository {
		t.Error("Repository = false")
	}
	if over.LastCommit != nil {
		t.Errorf("LastCommit = %+v, want none", over.LastCommit)
	}
	if len(over.Changes) != 1 || over.Changes[0].Status != StatusUntracked {
		t.Fatalf("changes = %+v", over.Changes)
	}
	if over.Adds != 1 {
		t.Errorf("+%d, want +1", over.Adds)
	}

	// And it can be staged and committed from nothing.
	if err := repo.StageAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit("first commit"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(porcelain(t, work)); got != "" {
		t.Errorf("git status = %q, want clean", got)
	}
}
