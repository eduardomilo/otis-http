package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/collection"
)

// Ordering modes a folder can be in (screen 2a's Folder options).
const (
	// OrderManual means the folder has a .order file and the sidebar's
	// arrangement is the one somebody chose.
	OrderManual = "manual"
	// OrderAlphabetical means there is no .order file. It is the default, and
	// switching back to it deletes the file (docs/FORMAT.md §2.2).
	OrderAlphabetical = "alphabetical"
)

// Errors a reorder can refuse with.
var (
	// ErrNotAFolder is a reorder aimed at a request.
	ErrNotAFolder = errors.New("not a folder")
	// ErrNotAChild is a reorder listing something that does not live in the
	// folder being reordered.
	ErrNotAChild = errors.New("not a child of that folder")
	// ErrNothingToUndo is ⌘Z with an empty stack.
	ErrNothingToUndo = errors.New("nothing to undo")
	// ErrChangedOnDisk is an undo whose starting point is gone: the folder was
	// edited outside Otis since the reorder, so reverting to the remembered
	// bytes would throw that edit away.
	ErrChangedOnDisk = errors.New("the folder changed on disk since then")
)

// OrderResult is what the window shows in the strip under the tree after a
// reorder (screen 2a): a sentence naming the file, and whether ⌘Z has
// anything left to revert.
type OrderResult struct {
	// Summary is the strip's first line: what happened, in words, with no
	// path in it. The design sets the path on a second line in mono
	// (screen 2a: "Order saved to" / `orders/.order`), which is also the only
	// way it fits a 302px sidebar — so the two are separate fields rather
	// than one sentence the window has to take apart.
	Summary string `json:"summary"`
	// Files is every .order file written or deleted, collection-relative. It
	// is the strip's second line, and what the diff view can be pointed at.
	Files []string `json:"files"`
	// CanUndo is false once the stack is empty, which hides the Undo control
	// rather than offering one that would refuse.
	CanUndo bool `json:"canUndo"`
}

// OrderService writes `.order` files: the one thing that reorders a
// collection, and the only writer of that file.
//
// It is separate from CollectionService because CollectionService reads. The
// invariant this service exists to hold is FORMAT.md §2.2's: **`.order` is
// never rewritten except by an explicit reorder.** Adding a request, importing
// a collection, saving a file, running a folder — none of them touch it, and
// keeping the writer in one small file with no other callers is what makes
// that checkable rather than hoped for.
type OrderService struct {
	app         *application.App
	collections *CollectionService

	mu   sync.Mutex
	undo []undoFrame
}

// undoFrame is what one reorder needs in order to be reverted.
//
// The previous *bytes* of each affected .order file, not a description of the
// previous order: a hand-written file with comments and a bare `create` line
// should come back exactly as it was, not as Otis' canonical rendering of the
// same order. `existed` is false for a file that was not there, which reverts
// by deleting.
type undoFrame struct {
	// label is what the strip says after the undo, e.g. "Order restored".
	label string
	// files is each affected directory's absolute path and its prior state.
	files []undoFile
	// moved is the file or directory a cross-folder drag moved, so undoing
	// puts it back. Empty for a plain reorder.
	movedFrom string
	movedTo   string
}

type undoFile struct {
	dir string
	// rel is the .order file's collection-relative path, so an undo can name
	// the file the way a diff would rather than reporting the absolute
	// directory it happens to hold in memory.
	rel string
	// existed and content are the file before the reorder — what an undo puts
	// back. existed false means there was none, and reverting deletes.
	existed bool
	content []byte
	// after is what the reorder left behind, and what Undo requires to still
	// be there. nil means the reorder left no file. This is the staleness
	// check: an .order somebody edited by hand since is not ours to overwrite.
	after []byte
}

// NewOrderService constructs the service.
func NewOrderService(collections *CollectionService) *OrderService {
	s := &OrderService{collections: collections}
	// A stored revert describes one collection; opening another makes every
	// frame meaningless. A change *within* the collection does not clear the
	// stack — Undo checks at the time whether the file still holds what the
	// reorder wrote, which is exact where forgetting on any disk event would
	// throw the stack away because a script touched an environment file.
	collections.OnClose(s.forget)
	return s
}

// ServiceStartup resolves the application.
func (s *OrderService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

func (s *OrderService) forget() {
	s.mu.Lock()
	s.undo = nil
	s.mu.Unlock()
}

// Mode reports whether a folder is manually ordered. nodePath is
// collection-relative; "" is the root.
func (s *OrderService) Mode(nodePath string) (string, error) {
	_, folder, err := s.folder(nodePath)
	if err != nil {
		return "", err
	}
	if folder.Ordered {
		return OrderManual, nil
	}
	return OrderAlphabetical, nil
}

// Reorder writes folder's `.order` to list nodePaths in that order.
//
// nodePaths must be exactly the folder's direct children, each once: a
// reorder writes the whole list (FORMAT.md §2.2), so a partial list would
// leave the rest to sort alphabetically after and silently move rows the drag
// never touched.
func (s *OrderService) Reorder(nodePath string, nodePaths []string) (OrderResult, error) {
	_, folder, err := s.folder(nodePath)
	if err != nil {
		return OrderResult{}, err
	}
	names, err := orderKeys(folder, nodePaths)
	if err != nil {
		return OrderResult{}, err
	}

	frame := undoFrame{label: "Order restored"}
	prior, err := captureOrder(folder)
	if err != nil {
		return OrderResult{}, err
	}
	frame.files = append(frame.files, prior)

	release := s.collections.Guard().Writing(folder.Path, filepath.Join(folder.Path, collection.OrderFileName))
	err = collection.WriteOrder(folder.Path, names)
	release()
	if err != nil {
		return OrderResult{}, err
	}
	frame.files[0].after = collection.FormatOrder(names)

	s.push(frame)
	return s.done("Order saved to", []string{orderRel(folder)})
}

// Move moves a request, folder or script into another folder at a position,
// then writes both folders' `.order` files.
//
// Both, because the entry has to leave one list and join the other: leaving it
// out of the source's list would only put it back where alphabetical order
// says, which is not where the drag left it. index is the position among the
// destination's children after the move; a negative or oversized index appends.
func (s *OrderService) Move(nodePath, toPath string, index int) (OrderResult, error) {
	loaded, dest, err := s.folder(toPath)
	if err != nil {
		return OrderResult{}, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return OrderResult{}, fmt.Errorf("%s: %w", nodePath, os.ErrNotExist)
	}
	if node.Parent == nil {
		return OrderResult{}, errors.New("the collection root cannot be moved")
	}
	source := node.Parent
	if source.Path == dest.Path {
		return OrderResult{}, errors.New("use Reorder to move a row inside its own folder")
	}
	// A folder cannot be dropped inside itself; the move would take the
	// destination with it.
	if node.Kind == collection.KindFolder && strings.HasPrefix(dest.Path+string(filepath.Separator), node.Path+string(filepath.Separator)) {
		return OrderResult{}, errors.New("a folder cannot be moved into itself")
	}

	base := filepath.Base(node.Path)
	target := filepath.Join(dest.Path, base)
	if _, err := os.Lstat(target); err == nil {
		return OrderResult{}, fmt.Errorf("%s already holds %s", dest.ID, base)
	}

	frame := undoFrame{label: "Move undone", movedFrom: node.Path, movedTo: target}
	for _, folder := range []*collection.Node{source, dest} {
		prior, err := captureOrder(folder)
		if err != nil {
			return OrderResult{}, err
		}
		frame.files = append(frame.files, prior)
	}

	// The destination's new list: its current children, with the arrival
	// spliced in. The source's: its children minus the departure. Both are
	// computed from the tree before the move, which is the order the window
	// was showing.
	destNames := childKeys(dest)
	arrival := entryKey(node)
	if index < 0 || index > len(destNames) {
		index = len(destNames)
	}
	destNames = append(destNames[:index:index], append([]string{arrival}, destNames[index:]...)...)
	sourceNames := childKeys(source)
	sourceNames = withoutKey(sourceNames, arrival)

	release := s.collections.Guard().Writing(
		node.Path, target, source.Path, dest.Path,
		filepath.Join(source.Path, collection.OrderFileName),
		filepath.Join(dest.Path, collection.OrderFileName),
	)
	err = os.Rename(node.Path, target)
	if err == nil {
		err = collection.WriteOrder(dest.Path, destNames)
	}
	if err == nil {
		// The source keeps a .order only if it had one: a folder somebody
		// never ordered must not acquire one because a row left it.
		if frame.files[0].existed {
			err = collection.WriteOrder(source.Path, sourceNames)
		}
	}
	release()
	if err != nil {
		return OrderResult{}, err
	}
	frame.files[1].after = collection.FormatOrder(destNames)
	if frame.files[0].existed {
		frame.files[0].after = collection.FormatOrder(sourceNames)
	}

	s.push(frame)
	files := []string{orderRel(dest)}
	if frame.files[0].existed {
		files = append(files, orderRel(source))
	}
	return s.done(fmt.Sprintf("%s moved to %s", node.Name, folderLabel(dest)), files)
}

// SetMode switches a folder between a manual order and alphabetical.
//
// Manual writes the order the window is currently showing, so the arrangement
// does not jump when it is turned on. Alphabetical deletes the file, which is
// what FORMAT.md §2.2 says restores alphabetical order — and is undoable, so
// there is no confirmation dialog in front of it: one mechanism for taking
// back an ordering change is better than two.
func (s *OrderService) SetMode(nodePath, mode string) (OrderResult, error) {
	_, folder, err := s.folder(nodePath)
	if err != nil {
		return OrderResult{}, err
	}

	frame := undoFrame{label: "Ordering restored"}
	prior, err := captureOrder(folder)
	if err != nil {
		return OrderResult{}, err
	}
	frame.files = append(frame.files, prior)

	var summary string
	var after []byte
	release := s.collections.Guard().Writing(folder.Path, filepath.Join(folder.Path, collection.OrderFileName))
	switch mode {
	case OrderManual:
		names := childKeys(folder)
		err = collection.WriteOrder(folder.Path, names)
		after = collection.FormatOrder(names)
		summary = "Manual order saved to"
	case OrderAlphabetical:
		err = collection.RemoveOrder(folder.Path)
		summary = "Alphabetical order · deleted"
	default:
		err = fmt.Errorf("unknown ordering mode %q", mode)
	}
	release()
	if err != nil {
		return OrderResult{}, err
	}
	frame.files[0].after = after
	// Nothing was there and nothing is there: alphabetical on an already
	// alphabetical folder is not a change to remember.
	if prior.existed || mode == OrderManual {
		s.push(frame)
	}
	return s.done(summary, []string{orderRel(folder)})
}

// CanUndo reports whether ⌘Z has anything to revert, so the shell can leave
// the shortcut alone rather than offering one that refuses.
func (s *OrderService) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.undo) > 0
}

// Undo reverts the last ordering change.
//
// It restores the previous bytes rather than re-deriving the previous order,
// so a hand-written `.order` comes back as it was written. It refuses when the
// affected files no longer hold what the reorder left behind: something edited
// them outside Otis, and overwriting that is not what ⌘Z means.
func (s *OrderService) Undo() (OrderResult, error) {
	s.mu.Lock()
	if len(s.undo) == 0 {
		s.mu.Unlock()
		return OrderResult{}, ErrNothingToUndo
	}
	frame := s.undo[len(s.undo)-1]
	s.mu.Unlock()

	var paths []string
	for _, f := range frame.files {
		paths = append(paths, f.dir, filepath.Join(f.dir, collection.OrderFileName))
	}
	if frame.movedFrom != "" {
		paths = append(paths, frame.movedFrom, frame.movedTo)
	}

	release := s.collections.Guard().Writing(paths...)
	err := revert(frame)
	release()
	if err != nil {
		return OrderResult{}, err
	}

	s.mu.Lock()
	if n := len(s.undo); n > 0 {
		s.undo = s.undo[:n-1]
	}
	s.mu.Unlock()

	// The .order files it put back, named the way a diff names them.
	var files []string
	for _, f := range frame.files {
		files = append(files, f.rel)
	}
	return s.done(frame.label, files)
}

// revert puts a frame's files back, moved entry first so the .order files it
// writes describe a directory that already holds what they list.
func revert(frame undoFrame) error {
	if frame.movedFrom != "" {
		if _, err := os.Lstat(frame.movedTo); err != nil {
			return fmt.Errorf("%w: %s is gone", ErrChangedOnDisk, filepath.Base(frame.movedTo))
		}
		if _, err := os.Lstat(frame.movedFrom); err == nil {
			return fmt.Errorf("%w: %s is back already", ErrChangedOnDisk, filepath.Base(frame.movedFrom))
		}
		if err := os.Rename(frame.movedTo, frame.movedFrom); err != nil {
			return err
		}
	}
	// Check every file before touching any, so a refusal leaves the reorder
	// intact rather than half undone.
	for _, f := range frame.files {
		if err := f.unchanged(); err != nil {
			return err
		}
	}
	for _, f := range frame.files {
		var err error
		if f.existed {
			err = os.WriteFile(filepath.Join(f.dir, collection.OrderFileName), f.content, 0o644)
		} else {
			err = collection.RemoveOrder(f.dir)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// unchanged reports whether the file still holds what the reorder wrote.
//
// An .order edited by hand since — or by a git checkout, or by another Otis
// window — is not ours to overwrite: ⌘Z means "take back what I just did",
// not "restore this to a state it has since left".
func (f undoFile) unchanged() error {
	current, err := os.ReadFile(filepath.Join(f.dir, collection.OrderFileName))
	if os.IsNotExist(err) {
		if f.after == nil {
			return nil
		}
		return fmt.Errorf("%w: %s is gone", ErrChangedOnDisk, collection.OrderFileName)
	}
	if err != nil {
		return err
	}
	if f.after == nil || !bytes.Equal(current, f.after) {
		return fmt.Errorf("%w: %s was edited", ErrChangedOnDisk, collection.OrderFileName)
	}
	return nil
}

// done re-walks so the window sees the new order, and builds the strip.
//
// The re-walk is this service's job because it held the write guard: the
// watcher deliberately did not report the write, so the save has to announce
// itself (see CLAUDE.md).
func (s *OrderService) done(summary string, files []string) (OrderResult, error) {
	if err := s.collections.Refresh(); err != nil {
		return OrderResult{}, err
	}
	return OrderResult{Summary: summary, Files: files, CanUndo: s.CanUndo()}, nil
}

// push adds a frame, capped. The cap is generous rather than exact: the point
// is that the stack cannot grow without bound in a long session, not that
// twenty is the right number of reorders to remember.
func (s *OrderService) push(frame undoFrame) {
	const maxUndo = 20
	s.mu.Lock()
	s.undo = append(s.undo, frame)
	if len(s.undo) > maxUndo {
		s.undo = s.undo[len(s.undo)-maxUndo:]
	}
	s.mu.Unlock()
}

// captureOrder reads a directory's .order file as it stands, for the undo
// stack. A missing file is recorded as missing, not as empty.
func captureOrder(folder *collection.Node) (undoFile, error) {
	dir, rel := folder.Path, orderRel(folder)
	content, err := os.ReadFile(filepath.Join(dir, collection.OrderFileName))
	switch {
	case os.IsNotExist(err):
		return undoFile{dir: dir, rel: rel}, nil
	case err != nil:
		return undoFile{}, err
	}
	return undoFile{dir: dir, rel: rel, existed: true, content: content}, nil
}

// folder resolves a node path to a folder node in the cached walk.
func (s *OrderService) folder(nodePath string) (*collection.Collection, *collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return nil, nil, fmt.Errorf("%s: %w", nodePath, os.ErrNotExist)
	}
	if node.Kind != collection.KindFolder {
		return nil, nil, fmt.Errorf("%s: %w", nodePath, ErrNotAFolder)
	}
	return loaded, node, nil
}

// entryKey is a node's exact .order spelling: its base name, with a trailing
// slash for a folder (docs/FORMAT.md §2.2).
func entryKey(node *collection.Node) string {
	name := filepath.Base(node.Path)
	if node.Kind == collection.KindFolder {
		return name + "/"
	}
	return name
}

// childKeys is a folder's direct children in the order the tree is showing
// them, as .order entries.
func childKeys(folder *collection.Node) []string {
	names := make([]string, 0, len(folder.Children))
	for _, child := range folder.Children {
		names = append(names, entryKey(child))
	}
	return names
}

// orderKeys turns the window's requested order into .order entries, checking
// that it names the folder's children and all of them.
func orderKeys(folder *collection.Node, nodePaths []string) ([]string, error) {
	byID := make(map[string]*collection.Node, len(folder.Children))
	for _, child := range folder.Children {
		byID[child.ID] = child
	}
	names := make([]string, 0, len(nodePaths))
	seen := make(map[string]bool, len(nodePaths))
	for _, id := range nodePaths {
		child, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%s: %w", id, ErrNotAChild)
		}
		if seen[id] {
			return nil, fmt.Errorf("%s: listed twice", id)
		}
		seen[id] = true
		names = append(names, entryKey(child))
	}
	if len(names) != len(folder.Children) {
		return nil, fmt.Errorf("a reorder must list all %d entries, got %d", len(folder.Children), len(names))
	}
	return names, nil
}

// withoutKey drops one entry from a list of .order keys.
func withoutKey(names []string, drop string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name != drop {
			out = append(out, name)
		}
	}
	return out
}

// orderRel names a folder's .order file the way a diff would.
func orderRel(folder *collection.Node) string {
	return path.Join(folder.ID, collection.OrderFileName)
}

// folderLabel is how a folder is named in a sentence: its path with a trailing
// slash, or "the collection root" for the root, which has no name to use.
func folderLabel(folder *collection.Node) string {
	if folder.ID == "" {
		return "the collection root"
	}
	return folder.ID + "/"
}

// renameInOrder and dropFromOrder keep a folder's `.order` in step when
// something in it is renamed or deleted.
//
// They live here, unexported, because this file is the only writer of a
// `.order` and that is a property worth being able to check by reading one
// file. They are not an exception to docs/FORMAT.md §2.2's "never rewritten
// except by an explicit reorder": neither writes an order, and neither brings
// a `.order` into being. They edit the single line that named the thing the
// user just renamed or deleted, in a file that already existed and already
// listed it — and only that line, so every comment and every other entry's
// spelling survives (collection.EditOrderLine).
//
// The alternative was to leave the line alone, and it is worse in both
// directions: a rename would drop the request to the bottom of the folder
// because its new name is unlisted, and a delete would leave a line naming
// nothing, which warns on every walk from then on. Neither is something the
// user did.

// renameInOrder rewrites the line naming `from` to name `to`.
func renameInOrder(folder *collection.Node, from, to string) error {
	_, err := collection.EditOrderLine(folder.Path, from, to)
	return err
}

// dropFromOrder removes the line naming `name`, if there is one.
func dropFromOrder(folder *collection.Node, name string) error {
	_, err := collection.EditOrderLine(folder.Path, name, "")
	return err
}
