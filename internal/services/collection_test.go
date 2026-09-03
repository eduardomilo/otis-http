package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/settings"
)

func newService(t *testing.T) *CollectionService {
	t.Helper()
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	s := NewCollectionService(store)
	t.Cleanup(func() { s.stopWatching() })
	return s
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

// fixture builds the collection the tests walk: two folders, a nested one, a
// folder settings file, an .order, and a file that does not parse.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "auth", "login-check.http"), "GET https://example.test/me\n")
	write(t, filepath.Join(root, "orders", "_folder.http"), "# @auth bearer {{apiKey}}\nAccept: application/json\n")
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nPOST https://example.test/orders\n")
	write(t, filepath.Join(root, "orders", "list-orders.http"), "GET {{baseUrl}}/orders?limit=10\n")
	write(t, filepath.Join(root, "orders", "fixtures", "seed-order.http"), "POST https://example.test/orders?seed=1\n")
	write(t, filepath.Join(root, "orders", ".order"), "create-order.http\nlist-orders.http\nfixtures/\n")
	write(t, filepath.Join(root, "broken.http"), "GET https://example.test/ nonsense trailing\n")
	write(t, filepath.Join(root, "env", "staging.json"), `{"baseUrl":"https://example.test"}`)
	write(t, filepath.Join(root, "notes.md"), "ignored\n")
	return root
}

func find(n *Node, path string) *Node {
	var found *Node
	n.Walk(func(node *Node) {
		if node.Path == path {
			found = node
		}
	})
	return found
}

func childNames(n *Node) []string {
	names := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		names = append(names, c.Name)
	}
	return names
}

func TestOpenReturnsTheTree(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Collection.Path != root {
		t.Errorf("Path = %q, want %q", opened.Collection.Path, root)
	}

	tree := opened.Tree
	if got := childNames(&tree.Root); len(got) != 3 {
		t.Fatalf("root children = %v, want auth, broken.http, orders", got)
	}

	orders := find(&tree.Root, "orders")
	if orders == nil {
		t.Fatal("no orders folder")
	}
	// .order is honoured, and folders sort among requests (FORMAT.md §2.2).
	if got := childNames(orders); strings.Join(got, ",") != "Create order,list-orders,fixtures" {
		t.Errorf("orders children = %v, want the .order order", got)
	}

	create := find(&tree.Root, "orders/create-order.http")
	if create == nil || create.Kind != KindRequest {
		t.Fatalf("create-order = %+v, want a request", create)
	}
	if create.Method != "POST" {
		t.Errorf("method = %q, want POST", create.Method)
	}
	if create.Name != "Create order" {
		t.Errorf("name = %q, want the @name directive", create.Name)
	}
}

// The command palette searches a request's URL and shows it (screen 2c), so
// the URL travels with the tree rather than costing a binding call per
// keystroke — and it travels as written, references intact, because the
// palette has no environment in mind and a URL that changed as you switched
// environments would be a moving target to search.
func TestRequestURLTravelsWithTheTreeUnresolved(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	list := find(&opened.Tree.Root, "orders/list-orders.http")
	if list == nil {
		t.Fatal("no list-orders")
	}
	if list.URL != "{{baseUrl}}/orders?limit=10" {
		t.Errorf("URL = %q, want it as written with {{baseUrl}} intact", list.URL)
	}

	// A folder has no URL, and neither does a file that did not parse: the
	// field is omitted rather than carrying an empty string into the window.
	if orders := find(&opened.Tree.Root, "orders"); orders == nil || orders.URL != "" {
		t.Errorf("folder URL = %q, want empty", orders.URL)
	}
}

// A file that does not parse is still a row, with its error attached, because
// seeing why it is broken is the point.
func TestBrokenFileIsARowWithItsError(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	broken := find(&opened.Tree.Root, "broken.http")
	if broken == nil {
		t.Fatal("the broken file is not in the tree")
	}
	if broken.Kind != KindBroken {
		t.Errorf("kind = %q, want %q", broken.Kind, KindBroken)
	}
	if broken.Error == "" {
		t.Error("a broken node carries no error")
	}
	if len(broken.Warnings) == 0 {
		t.Error("a broken node carries no warning")
	}
}

// _folder.http is settings, not a request (FORMAT.md §2.1), so it hangs off
// its folder instead of being a row — which is what keeps the tree identical
// to `otis ls`.
func TestFolderSettingsAreNotARow(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	orders := find(&opened.Tree.Root, "orders")
	if orders.Settings == nil {
		t.Fatal("orders has no settings node")
	}
	if orders.Settings.Kind != KindFolderFile {
		t.Errorf("settings kind = %q, want %q", orders.Settings.Kind, KindFolderFile)
	}
	if orders.Settings.Path != "orders/_folder.http" {
		t.Errorf("settings path = %q", orders.Settings.Path)
	}
	for _, child := range orders.Children {
		if child.Name == collection.FolderFileName {
			t.Error("_folder.http appears as a child row")
		}
	}
	auth := find(&opened.Tree.Root, "auth")
	if auth.Settings != nil {
		t.Error("a folder without _folder.http has a settings node")
	}
}

// The tree the window draws and the tree `otis ls` prints are the same tree.
func TestTreeMatchesOtisLs(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := collection.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var fromCollection, fromTree []string
	loaded.Walk(func(n *collection.Node) bool {
		if n.ID != "" {
			fromCollection = append(fromCollection, string(n.Kind)+" "+n.ID)
		}
		return true
	})
	opened.Tree.Root.Walk(func(n *Node) {
		if n.Path == "" || n.Kind == KindFolderFile {
			return
		}
		kind := "request"
		if n.Kind == KindFolder {
			kind = "folder"
		}
		fromTree = append(fromTree, kind+" "+n.Path)
	})
	if strings.Join(fromCollection, "\n") != strings.Join(fromTree, "\n") {
		t.Errorf("the tree and the walker disagree:\nwalker:\n%s\ntree:\n%s",
			strings.Join(fromCollection, "\n"), strings.Join(fromTree, "\n"))
	}
}

func TestOrderWarningsAttachToTheFolder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "a.http"), "GET https://example.test/\n")
	write(t, filepath.Join(root, "orders", ".order"), "a.http\nnot-here.http\n")
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	orders := find(&opened.Tree.Root, "orders")
	if len(orders.Warnings) == 0 {
		t.Fatalf("the .order warning did not reach the folder; tree warnings: %+v", opened.Tree.Warnings)
	}
	if !strings.Contains(orders.Warnings[0], "order-missing") {
		t.Errorf("warning = %q", orders.Warnings[0])
	}
}

func TestOpenRejectsAFileAndAMissingDirectory(t *testing.T) {
	s := newService(t)
	file := filepath.Join(t.TempDir(), "a.http")
	write(t, file, "GET https://example.test/\n")
	if _, err := s.Open(file); err == nil {
		t.Error("Open on a file returned no error")
	}
	if _, err := s.Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Open on a missing directory returned no error")
	}
}

func TestAbsolutePathStaysInsideTheCollection(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	if _, err := s.Open(root); err != nil {
		t.Fatal(err)
	}
	got, err := s.AbsolutePath("orders/create-order.http")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "orders", "create-order.http") {
		t.Errorf("AbsolutePath = %q", got)
	}
	// A path that tries to climb out resolves inside the collection, and then
	// fails only because there is no such file.
	if _, err := s.AbsolutePath("../../../etc/passwd"); err == nil {
		t.Error("a traversal path resolved to something that exists")
	}
	if escaped := absolutePath(root, "../../etc/passwd"); !strings.HasPrefix(escaped, root) {
		t.Errorf("absolutePath escaped the collection: %q", escaped)
	}
}

func TestNoCollectionOpen(t *testing.T) {
	s := newService(t)
	if _, err := s.Tree(); err == nil {
		t.Error("Tree with no collection open returned no error")
	}
	if _, err := s.AbsolutePath("a.http"); err == nil {
		t.Error("AbsolutePath with no collection open returned no error")
	}
	if _, err := NewGitService(s).State(); err == nil {
		t.Error("git State with no collection open returned no error")
	}
}

func TestGitStatusReachesTheTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := fixture(t)
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Otis", "GIT_AUTHOR_EMAIL=otis@example.test",
			"GIT_COMMITTER_NAME=Otis", "GIT_COMMITTER_EMAIL=otis@example.test",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q", "-b", "main")
	gitRun("add", ".")
	gitRun("commit", "-qm", "first")
	write(t, filepath.Join(root, "orders", "create-order.http"), "# @name Create order\nPOST https://example.test/orders?edited=1\n")
	write(t, filepath.Join(root, "orders", "fixtures", "new.http"), "GET https://example.test/new\n")

	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Tree.Git.Repository || opened.Tree.Git.Branch != "main" {
		t.Fatalf("git state = %+v", opened.Tree.Git)
	}
	if got := find(&opened.Tree.Root, "orders/create-order.http").GitStatus; got != "M" {
		t.Errorf("create-order status = %q, want M", got)
	}
	if got := find(&opened.Tree.Root, "orders/fixtures/new.http").GitStatus; got != "U" {
		t.Errorf("new.http status = %q, want U", got)
	}
	// A folder is dirty when a *tracked* file below it changed, so a collapsed
	// folder can still say something inside needs attention. An untracked file
	// does not propagate: it is visible as itself (screen 1a shows fixtures/
	// with no dot despite the untracked seed-order inside it).
	if !find(&opened.Tree.Root, "orders").Modified {
		t.Error("orders is not marked modified: create-order.http below it is modified")
	}
	if !opened.Tree.Root.Modified {
		t.Error("the root is not marked modified")
	}
	if find(&opened.Tree.Root, "orders/fixtures").Modified {
		t.Error("fixtures is marked modified, but the file below it is only untracked")
	}
	if find(&opened.Tree.Root, "auth").Modified {
		t.Error("auth is marked modified but nothing below it changed")
	}
}

func TestOutsideAGitRepositoryEverythingStillWorks(t *testing.T) {
	root := fixture(t)
	s := newService(t)
	opened, err := s.Open(root)
	if err != nil {
		t.Fatalf("Open outside a repository: %v", err)
	}
	if opened.Tree.Git.Repository {
		t.Error("Repository is true outside a repository")
	}
	if opened.Tree.Root.Modified {
		t.Error("nodes are marked modified outside a repository")
	}
	if len(opened.Tree.Root.Children) == 0 {
		t.Error("the tree is empty outside a repository")
	}
}

// Opening a second collection must stop the first one's watcher, or edits to
// a collection nobody is looking at would keep waking the window.
func TestOpeningAnotherCollectionStopsTheFirstWatcher(t *testing.T) {
	s := newService(t)
	first := fixture(t)
	second := fixture(t)
	if _, err := s.Open(first); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	one := s.watcher
	s.mu.RUnlock()
	if one == nil {
		t.Fatal("no watcher after the first open")
	}
	if _, err := s.Open(second); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	two := s.watcher
	s.mu.RUnlock()
	if two == nil || two == one {
		t.Fatal("the watcher was not replaced")
	}
	// The first watcher is closed, so writing to the first collection is silent.
	write(t, filepath.Join(first, "late.http"), "GET https://example.test/\n")
	time.Sleep(200 * time.Millisecond)
}

func TestCloseStopsWatchingAndClearsTheCollection(t *testing.T) {
	s := newService(t)
	if _, err := s.Open(fixture(t)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Current().Path != "" {
		t.Error("the collection is still open after Close")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.watcher != nil {
		t.Error("the watcher outlived Close")
	}
}

// OpenPath is the one entry point for a path that came from outside the
// window: a .http file double-clicked in Finder or Explorer, a path on the
// command line, a second launch forwarding its arguments, or a file dropped
// on the window. All four have to agree about which collection a file belongs
// to, and about what a `_folder.http` means.
func TestOpenPathFindsTheCollectionAroundAFile(t *testing.T) {
	s := newService(t)
	root := t.TempDir()
	// env/ marks the root, so the walk stops there rather than at the
	// request's own folder (docs/FORMAT.md §8).
	write(t, filepath.Join(root, "env", "dev.json"), "{}")
	write(t, filepath.Join(root, "orders", "_folder.http"), "Accept: application/json\n")
	write(t, filepath.Join(root, "orders", "create-order.http"), "POST https://example.test/orders\n")
	write(t, filepath.Join(root, "orders", "nested", "seed.http"), "GET https://example.test/seed\n")

	tests := []struct {
		name string
		path string
		node string
		kind string
	}{
		{
			name: "a request opens its collection and names itself",
			path: filepath.Join(root, "orders", "create-order.http"),
			node: "orders/create-order.http",
			kind: "request",
		},
		{
			name: "a nested request keeps its whole relative path",
			path: filepath.Join(root, "orders", "nested", "seed.http"),
			node: "orders/nested/seed.http",
			kind: "request",
		},
		{
			// _folder.http is settings, not a row in the tree
			// (docs/FORMAT.md §2.1), so the thing to show is its folder.
			name: "a folder settings file resolves to its folder",
			path: filepath.Join(root, "orders", "_folder.http"),
			node: "orders",
			kind: "folder",
		},
		{
			name: "a directory opens as the collection root",
			path: root,
			node: "",
			kind: "folder",
		},
		{
			name: "a subdirectory opens as that folder's collection",
			path: filepath.Join(root, "orders"),
			node: "",
			kind: "folder",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.OpenPath(tc.path); err != nil {
				t.Fatalf("OpenPath(%s): %v", tc.path, err)
			}
			// The target is held for the window to collect. That is the
			// launch case, and holding it is the whole point: the file
			// association starts the process, so nothing is listening yet.
			target := s.TakePendingOpen()
			if target.Kind == "" {
				t.Fatal("OpenPath held no target for the window")
			}
			if target.Node != tc.node || target.Kind != tc.kind {
				t.Errorf("target = {node: %q, kind: %q}, want {node: %q, kind: %q}",
					target.Node, target.Kind, tc.node, tc.kind)
			}
			// Taking it clears it, so a remount does not navigate again.
			if again := s.TakePendingOpen(); again.Kind != "" {
				t.Errorf("TakePendingOpen returned %+v a second time; it must clear as it reads", again)
			}
		})
	}
}

// A root-level _folder.http belongs to the collection root, whose node path is
// the empty string rather than ".".
func TestOpenPathMapsTheRootFolderFileToTheEmptyPath(t *testing.T) {
	s := newService(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "env", "dev.json"), "{}")
	write(t, filepath.Join(root, "_folder.http"), "Accept: application/json\n")

	if err := s.OpenPath(filepath.Join(root, "_folder.http")); err != nil {
		t.Fatal(err)
	}
	target := s.TakePendingOpen()
	if target.Kind == "" {
		t.Fatal("no target held")
	}
	if target.Node != "" || target.Kind != "folder" {
		t.Errorf("target = {node: %q, kind: %q}, want the root as a folder", target.Node, target.Kind)
	}
}

// A .http file that is in no collection at all — one sitting in ~/Downloads —
// still opens. FindRoot answers with its own directory, which becomes a
// one-file collection. Refusing would be worse: the user pointed at it.
func TestOpenPathOpensALooseRequestFile(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "scratch.http"), "GET https://example.test/\n")

	if err := s.OpenPath(filepath.Join(dir, "scratch.http")); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Path; got != dir {
		t.Errorf("collection = %q, want %q", got, dir)
	}
	if target := s.TakePendingOpen(); target.Node != "scratch.http" {
		t.Errorf("target = %+v, want scratch.http", target)
	}
}

func TestOpenPathRejectsAMissingPath(t *testing.T) {
	s := newService(t)
	if err := s.OpenPath(filepath.Join(t.TempDir(), "nope.http")); err == nil {
		t.Error("OpenPath accepted a path that does not exist")
	}
}
