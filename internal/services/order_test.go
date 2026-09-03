package services

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/settings"
)

// orderFixture is screen 2a's collection: an orders/ folder with a hand
// written .order, a sibling folder to drag between, and a folder with no
// .order at all so alphabetical order can be exercised too.
func orderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "create-order.http"), "POST https://example.test/orders\n")
	write(t, filepath.Join(root, "orders", "get-order.http"), "GET https://example.test/orders/1\n")
	write(t, filepath.Join(root, "orders", "cancel-order.http"), "DELETE https://example.test/orders/1\n")
	write(t, filepath.Join(root, "orders", "fixtures", "seed.http"), "POST https://example.test/orders?seed=1\n")
	// Hand-written, with a comment and a bare line, so an undo can be seen to
	// restore the file rather than Otis' rendering of the same order.
	write(t, filepath.Join(root, "orders", ".order"),
		"# mine, not Otis'\ncreate-order.http\nget-order\nfixtures/\n")
	write(t, filepath.Join(root, "archive", "old-order.http"), "GET https://example.test/orders/0\n")
	return root
}

func newOrderService(t *testing.T) (*OrderService, *CollectionService, string) {
	t.Helper()
	root := orderFixture(t)
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	return NewOrderService(collections), collections, root
}

// order returns a folder's children in display order, by node path.
func order(t *testing.T, collections *CollectionService, folder string) []string {
	t.Helper()
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find(folder)
	if node == nil {
		t.Fatalf("no folder %q", folder)
	}
	var out []string
	for _, child := range node.Children {
		out = append(out, child.ID)
	}
	return out
}

// readBytes is readFile's byte-exact twin: these tests care about the exact
// bytes of a .order file, not its text.
func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// THE invariant of docs/FORMAT.md §2.2: `.order` is never rewritten except by
// an explicit reorder. A file added to a manually ordered folder is unlisted,
// which puts it after the listed entries — and that is the whole mechanism, so
// nothing has to touch the file.
//
// Byte-identical, not "equivalent": a rewrite that happened to produce the
// same order would still put the file in somebody's diff, and would have lost
// their comment and their bare `get-order` line on the way.
func TestAddingARequestDoesNotTouchTheOrderFile(t *testing.T) {
	_, collections, root := newOrderService(t)
	orderPath := filepath.Join(root, "orders", collection.OrderFileName)
	before := readBytes(t, orderPath)

	write(t, filepath.Join(root, "orders", "archive-order.http"), "POST https://example.test/orders/1/archive\n")
	if err := collections.Refresh(); err != nil {
		t.Fatal(err)
	}

	if after := readBytes(t, orderPath); !bytes.Equal(before, after) {
		t.Errorf(".order changed when a request was added\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	// And the new file sorts alphabetically after the listed ones, which is
	// why nothing needed to be written.
	got := strings.Join(order(t, collections, "orders"), ",")
	want := "orders/create-order.http,orders/get-order.http,orders/fixtures,orders/archive-order.http,orders/cancel-order.http"
	if got != want {
		t.Errorf("order = %v, want the listed three then the unlisted two alphabetically", got)
	}
}

// The same for a folder, and for a save: nothing but a reorder writes .order.
func TestOnlyAnExplicitReorderWritesTheOrderFile(t *testing.T) {
	s, collections, root := newOrderService(t)
	orderPath := filepath.Join(root, "orders", collection.OrderFileName)
	before := readBytes(t, orderPath)

	if err := os.MkdirAll(filepath.Join(root, "orders", "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "orders", "get-order.http"), "GET https://example.test/orders/2\n")
	write(t, filepath.Join(root, "orders", "_folder.http"), "Accept: application/json\n")
	if err := collections.Refresh(); err != nil {
		t.Fatal(err)
	}
	if after := readBytes(t, orderPath); !bytes.Equal(before, after) {
		t.Error(".order changed without a reorder")
	}

	// A reorder does write it.
	if _, err := s.Reorder("orders", order(t, collections, "orders")); err != nil {
		t.Fatal(err)
	}
	if after := readBytes(t, orderPath); bytes.Equal(before, after) {
		t.Error("a reorder did not write .order")
	}
}

func TestReorderWritesEveryEntryInTheGivenOrder(t *testing.T) {
	s, collections, root := newOrderService(t)

	// cancel-order to the front, which is the drag screen 2a is mid-way
	// through.
	want := []string{
		"orders/cancel-order.http",
		"orders/create-order.http",
		"orders/get-order.http",
		"orders/fixtures",
	}
	res, err := s.Reorder("orders", want)
	if err != nil {
		t.Fatal(err)
	}
	// The strip's two lines: the phrase, then the file (screen 2a). The path
	// is not in the sentence, because the design sets it in mono below.
	if res.Summary != "Order saved to" {
		t.Errorf("summary = %q, want the phrase without the path", res.Summary)
	}
	if strings.Join(res.Files, ",") != "orders/.order" {
		t.Errorf("files = %v, want the one file it wrote", res.Files)
	}
	if !res.CanUndo {
		t.Error("CanUndo = false after a reorder")
	}

	if got := order(t, collections, "orders"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}

	// Exact names, one per line, every entry listed once — a folder keeps its
	// slash so it cannot be confused with a request of the same name.
	content := string(readBytes(t, filepath.Join(root, "orders", collection.OrderFileName)))
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	wantLines := []string{"cancel-order.http", "create-order.http", "get-order.http", "fixtures/"}
	if strings.Join(lines, ",") != strings.Join(wantLines, ",") {
		t.Errorf("lines = %v, want %v", lines, wantLines)
	}

	// The written file has to be one the reader accepts without warnings, or
	// the round trip is broken.
	loaded, err := collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range loaded.Warnings {
		if strings.HasSuffix(w.Path, collection.OrderFileName) {
			t.Errorf("warning on a file we wrote: %s: %s", w.Path, w.Msg)
		}
	}
}

// A partial list would leave the rest to sort alphabetically after, silently
// moving rows the drag never touched.
func TestReorderRefusesAPartialOrForeignList(t *testing.T) {
	s, collections, _ := newOrderService(t)

	if _, err := s.Reorder("orders", []string{"orders/get-order.http"}); err == nil {
		t.Error("a one-entry reorder of a four-entry folder was accepted")
	}
	if _, err := s.Reorder("orders", []string{"archive/old-order.http"}); !errors.Is(err, ErrNotAChild) {
		t.Errorf("err = %v, want ErrNotAChild", err)
	}
	full := order(t, collections, "orders")
	if _, err := s.Reorder("orders", append(full[:len(full)-1:len(full)-1], full[0])); err == nil {
		t.Error("a list repeating an entry was accepted")
	}
	if _, err := s.Reorder("orders/get-order.http", nil); !errors.Is(err, ErrNotAFolder) {
		t.Error("a reorder aimed at a request was accepted")
	}
}

// Folders take part in ordering: one may sit between two requests
// (docs/FORMAT.md §2.2).
func TestAFolderCanSitBetweenTwoRequests(t *testing.T) {
	s, collections, _ := newOrderService(t)
	want := []string{
		"orders/create-order.http",
		"orders/fixtures",
		"orders/get-order.http",
		"orders/cancel-order.http",
	}
	if _, err := s.Reorder("orders", want); err != nil {
		t.Fatal(err)
	}
	if got := order(t, collections, "orders"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want the folder in the middle", got)
	}
}

func TestMoveAcrossFoldersWritesBothOrderFiles(t *testing.T) {
	s, collections, root := newOrderService(t)

	// archive/ has no .order, so the arrival needs one written there; orders/
	// has one, and the departure has to leave it.
	res, err := s.Move("orders/get-order.http", "archive", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "archive/") {
		t.Errorf("summary = %q, want it to name the destination", res.Summary)
	}

	if _, err := os.Lstat(filepath.Join(root, "orders", "get-order.http")); !os.IsNotExist(err) {
		t.Error("the file is still in orders/")
	}
	if _, err := os.Lstat(filepath.Join(root, "archive", "get-order.http")); err != nil {
		t.Errorf("the file is not in archive/: %v", err)
	}

	if got := order(t, collections, "archive"); strings.Join(got, ",") != "archive/get-order.http,archive/old-order.http" {
		t.Errorf("archive order = %v, want the arrival first", got)
	}
	if got := order(t, collections, "orders"); strings.Contains(strings.Join(got, ","), "get-order") {
		t.Errorf("orders order = %v, still lists the departure", got)
	}
	// The source's .order lost the entry rather than keeping a line that
	// matches nothing.
	source := string(readBytes(t, filepath.Join(root, "orders", collection.OrderFileName)))
	if strings.Contains(source, "get-order") {
		t.Errorf("orders/.order still lists get-order:\n%s", source)
	}
}

// A folder nobody ordered must not acquire a `.order` because a row left it:
// its remaining entries are alphabetical, which is what they already were.
func TestMoveOutOfAnUnorderedFolderWritesNoOrderFileThere(t *testing.T) {
	s, _, root := newOrderService(t)
	if _, err := s.Move("archive/old-order.http", "orders", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "archive", collection.OrderFileName)); !os.IsNotExist(err) {
		t.Error("archive/.order was created by a row leaving")
	}
}

func TestMoveRefusesTheImpossible(t *testing.T) {
	s, _, root := newOrderService(t)

	if _, err := s.Move("orders/fixtures", "orders/fixtures", 0); err == nil {
		t.Error("a folder was moved into itself")
	}
	if _, err := s.Move("orders/get-order.http", "orders", 0); err == nil {
		t.Error("a cross-folder move into the row's own folder was accepted")
	}
	// A name collision would overwrite somebody's file.
	write(t, filepath.Join(root, "archive", "get-order.http"), "GET https://example.test/x\n")
	if _, err := s.Move("orders/get-order.http", "archive", 0); err == nil {
		t.Error("a move onto an existing file was accepted")
	}
}

func TestUndoRestoresTheFileByteForByte(t *testing.T) {
	s, collections, root := newOrderService(t)
	orderPath := filepath.Join(root, "orders", collection.OrderFileName)
	before := readBytes(t, orderPath)
	beforeOrder := strings.Join(order(t, collections, "orders"), ",")

	if _, err := s.Reorder("orders", []string{
		"orders/cancel-order.http", "orders/fixtures",
		"orders/create-order.http", "orders/get-order.http",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if res.CanUndo {
		t.Error("CanUndo = true with an empty stack")
	}
	// Named the way a diff names it, not as the absolute directory the frame
	// happens to hold: the strip shows this to somebody who may go and look.
	if strings.Join(res.Files, ",") != "orders/.order" {
		t.Errorf("files = %v, want the collection-relative .order path", res.Files)
	}
	// The hand-written file comes back as written — comment, bare line and
	// all — not as Otis' rendering of the same order.
	if after := readBytes(t, orderPath); !bytes.Equal(before, after) {
		t.Errorf("undo did not restore the bytes\n--- want ---\n%s\n--- got ---\n%s", before, after)
	}
	if got := strings.Join(order(t, collections, "orders"), ","); got != beforeOrder {
		t.Errorf("order = %v, want %v", got, beforeOrder)
	}
	if _, err := s.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoOfAMovePutsTheFileBack(t *testing.T) {
	s, collections, root := newOrderService(t)
	before := readBytes(t, filepath.Join(root, "orders", collection.OrderFileName))

	if _, err := s.Move("orders/get-order.http", "archive", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(root, "orders", "get-order.http")); err != nil {
		t.Errorf("the file did not come back: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "archive", "get-order.http")); !os.IsNotExist(err) {
		t.Error("the file is still in the destination")
	}
	if after := readBytes(t, filepath.Join(root, "orders", collection.OrderFileName)); !bytes.Equal(before, after) {
		t.Error("orders/.order was not restored")
	}
	// The destination got a .order it did not have; undoing removes it again.
	if _, err := os.Lstat(filepath.Join(root, "archive", collection.OrderFileName)); !os.IsNotExist(err) {
		t.Error("archive/.order survived the undo")
	}
	if got := order(t, collections, "orders"); !strings.Contains(strings.Join(got, ","), "get-order") {
		t.Errorf("orders order = %v, want get-order back", got)
	}
}

// ⌘Z means "take back what I just did", not "restore this file to a state it
// has since left". An .order edited by hand — or by a git checkout, or by
// another window — is not ours to overwrite.
func TestUndoRefusesWhenTheFileChangedSince(t *testing.T) {
	s, _, root := newOrderService(t)
	orderPath := filepath.Join(root, "orders", collection.OrderFileName)

	if _, err := s.Reorder("orders", []string{
		"orders/cancel-order.http", "orders/fixtures",
		"orders/create-order.http", "orders/get-order.http",
	}); err != nil {
		t.Fatal(err)
	}
	write(t, orderPath, "cancel-order.http\n")

	if _, err := s.Undo(); !errors.Is(err, ErrChangedOnDisk) {
		t.Fatalf("err = %v, want ErrChangedOnDisk", err)
	}
	// And it refused without touching anything.
	if got := string(readBytes(t, orderPath)); got != "cancel-order.http\n" {
		t.Errorf("the refused undo wrote anyway:\n%s", got)
	}
}

func TestSetModeManualThenAlphabetical(t *testing.T) {
	s, collections, root := newOrderService(t)
	archiveOrder := filepath.Join(root, "archive", collection.OrderFileName)

	if mode, err := s.Mode("archive"); err != nil || mode != OrderAlphabetical {
		t.Fatalf("mode = %q, %v; want alphabetical", mode, err)
	}
	// Turning Manual on writes the arrangement the window is already showing,
	// so nothing jumps.
	shown := order(t, collections, "archive")
	if _, err := s.SetMode("archive", OrderManual); err != nil {
		t.Fatal(err)
	}
	if mode, _ := s.Mode("archive"); mode != OrderManual {
		t.Errorf("mode = %q, want manual", mode)
	}
	if got := order(t, collections, "archive"); strings.Join(got, ",") != strings.Join(shown, ",") {
		t.Errorf("order = %v, want it unchanged at %v", got, shown)
	}

	// Alphabetical deletes the file, which is what restores alphabetical
	// order (docs/FORMAT.md §2.2).
	res, err := s.SetMode("archive", OrderAlphabetical)
	if err != nil {
		t.Fatal(err)
	}
	// Short enough to fit a 302px sidebar beside the Undo control: a summary
	// that truncates to "…deleti…" has stopped being a summary.
	if len(res.Summary) > 30 {
		t.Errorf("summary = %q, too long for the strip", res.Summary)
	}
	if !strings.Contains(res.Summary, "deleted") || strings.Join(res.Files, ",") != "archive/.order" {
		t.Errorf("summary = %q, files = %v; want it to say what was deleted", res.Summary, res.Files)
	}
	if _, err := os.Lstat(archiveOrder); !os.IsNotExist(err) {
		t.Error("archive/.order survived a switch to alphabetical")
	}
	// And it is undoable, which is why there is no confirmation in front of it.
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(archiveOrder); err != nil {
		t.Errorf("undo did not bring archive/.order back: %v", err)
	}
}

// Switching an already alphabetical folder to alphabetical is not a change,
// and must not put a no-op on the undo stack for ⌘Z to consume.
func TestAlphabeticalOnAnAlphabeticalFolderIsNotUndoable(t *testing.T) {
	s, _, _ := newOrderService(t)
	if _, err := s.SetMode("archive", OrderAlphabetical); err != nil {
		t.Fatal(err)
	}
	if s.CanUndo() {
		t.Error("CanUndo = true after a no-op")
	}
}

// The tree says which folders are manually ordered, because the sidebar draws
// a glyph for them (screen 2a) and the menu offers the other mode.
func TestTreeReportsWhichFoldersAreManuallyOrdered(t *testing.T) {
	_, collections, _ := newOrderService(t)
	tree, err := collections.Tree()
	if err != nil {
		t.Fatal(err)
	}
	orders := find(&tree.Root, "orders")
	if orders == nil || !orders.Ordered {
		t.Error("orders/ has a .order but is not reported as ordered")
	}
	if archive := find(&tree.Root, "archive"); archive == nil || archive.Ordered {
		t.Error("archive/ has no .order but is reported as ordered")
	}
}

// A reorder holds the write guard, so the watcher does not report it as
// somebody else's change — and therefore has to announce itself, which is
// what the Refresh inside done() is for.
func TestReorderIsGuardedAndAnnouncesItself(t *testing.T) {
	s, collections, root := newOrderService(t)
	orderPath := filepath.Join(root, "orders", collection.OrderFileName)

	announced := false
	collections.OnDiskChange(func() { announced = true })

	if _, err := s.Reorder("orders", []string{
		"orders/cancel-order.http", "orders/create-order.http",
		"orders/get-order.http", "orders/fixtures",
	}); err != nil {
		t.Fatal(err)
	}
	if !collections.Guard().Suppressed(orderPath) {
		t.Error("the write was not suppressed; the watcher will report Otis' own save")
	}
	if announced {
		t.Error("a guarded reorder fired OnDiskChange, which is for files outside the tree")
	}
	// The window sees the new order without waiting for the watcher.
	if got := order(t, collections, "orders")[0]; got != "orders/cancel-order.http" {
		t.Errorf("first row = %q, want the cached walk to be up to date", got)
	}
}
