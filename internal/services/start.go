package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/gitclone"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// exampleFileName is the request the scaffold writes, and the one a new
// collection opens on. It is named here as well as in collection.Scaffold
// because this is the caller that has to know which of the three files is
// worth showing.
const exampleFileName = "example.http"

// StartService is the two ways a collection comes into being that are not
// opening one that already exists: screen 2b's "Start fresh" and "Clone
// repository" cards, which shipped as `soon` because neither was in the A–E
// plan (DESIGN-NOTES §9.9).
//
// Both end the same way — a directory exists, and it is now the open
// collection — which is why they are one service rather than two, and why
// neither of them opens it themselves: `CollectionService` is the only thing
// that decides what "open" means, and this hands it a path exactly as the
// Postman import does.
//
// What it deliberately does not do is **`git init`**. Screen 2b's example line
// for Start fresh reads `mkdir .requests && git init`, and the first half of
// that is this service. The second half is not: `internal/git` is read-only
// and `internal/diff` is the only thing in Otis that writes to a repository,
// and creating one would be a third writer with no review in front of it. A
// collection made inside a repository is already versioned, which is the
// common case; one made outside is a directory of files, which docs/VISION.md
// says is a perfectly good thing to be until somebody runs `git init`
// themselves.
type StartService struct {
	collections  *CollectionService
	dialogs      *DialogService
	environments *EnvironmentService
	app          *application.App

	mu sync.Mutex
	// cancel stops the clone in flight. One at a time: a second clone while
	// one is running is a mistake, not a feature, and the dialog is modal.
	cancel context.CancelFunc
}

// NewStartService constructs the service.
func NewStartService(
	collections *CollectionService,
	dialogs *DialogService,
	environments *EnvironmentService,
) *StartService {
	return &StartService{collections: collections, dialogs: dialogs, environments: environments}
}

// ServiceStartup resolves the application.
func (s *StartService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// StartResult is where a new collection landed.
type StartResult struct {
	// Root is the collection that is now open.
	Root string `json:"root"`
	// NodePath is a node worth showing, relative to the root, or "" when
	// there is nothing in particular to open.
	NodePath string `json:"nodePath"`
	// Note is a sentence for the window when the outcome needs one — a clone
	// that worked but held no `.http` files, say. Empty on the ordinary path.
	Note string `json:"note"`
}

// StartDefaults is what the two dialogs need before they can be drawn.
type StartDefaults struct {
	// Parent is the directory the dialogs start pointing at: the last
	// collection's own parent, so the second collection lands beside the
	// first, and the home directory before there is one.
	Parent string `json:"parent"`
	// Name is the conventional collection directory name.
	Name string `json:"name"`
	// CloneBlocked is why cloning is not possible on this machine, or "".
	// The card is disabled and says this rather than failing on click.
	CloneBlocked string `json:"cloneBlocked"`
}

// Defaults answers what the dialogs need to open.
func (s *StartService) Defaults() StartDefaults {
	d := StartDefaults{Name: collection.DefaultCollectionName}
	if current := s.collections.Current().Path; current != "" {
		d.Parent = filepath.Dir(current)
	} else if home, err := os.UserHomeDir(); err == nil {
		d.Parent = home
	}
	if err := gitclone.Available(); err != nil {
		d.CloneBlocked = err.Error()
	}
	return d
}

// ChooseLocation shows the native directory picker for where a new collection
// should be created or cloned.
//
// Cancelling returns "" with no error, like every other picker here.
func (s *StartService) ChooseLocation() (string, error) {
	return s.dialogs.OpenCollectionParent()
}

// SuggestName is the folder name a clone URL implies — git's own rule, so the
// dialog can show the path before anything runs.
func (s *StartService) SuggestName(url string) string {
	return gitclone.NameFor(url)
}

// Create writes a new collection into parent/name and opens it.
func (s *StartService) Create(parent, name string) (StartResult, error) {
	dest, err := destination(parent, name)
	if err != nil {
		return StartResult{}, err
	}
	if err := collection.WriteScaffold(dest); err != nil {
		return StartResult{}, err
	}
	// The example request, not the folder: a scaffold whose whole point is
	// "here is a request" should open on it.
	example := filepath.Join(dest, exampleFileName)
	if err := s.open(example); err != nil {
		return StartResult{}, err
	}
	// The environment it just wrote becomes the active one. `{{baseUrl}}` in
	// the example resolves to nothing otherwise, so the first thing a new
	// collection does would be to fail — with the fix sitting unselected in a
	// menu the person has not found yet.
	if _, err := s.environments.Activate(collection.DefaultEnvironmentName); err != nil {
		return StartResult{}, err
	}
	return StartResult{Root: dest, NodePath: exampleFileName}, nil
}

// Clone clones url into parent/name and opens the collection inside it.
//
// Progress is emitted as events.CloneProgress, one event per line git writes,
// because a clone of anything real takes long enough that a dialog with no
// output in it looks hung. Nothing else is done with git's output: it is not
// logged, not kept, and not parsed except to find the sentence that explains a
// failure.
func (s *StartService) Clone(url, parent, name string) (StartResult, error) {
	if err := gitclone.CheckURL(url); err != nil {
		return StartResult{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = gitclone.NameFor(url)
	}
	dest, err := destination(parent, name)
	if err != nil {
		return StartResult{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return StartResult{}, fmt.Errorf("a clone is already running")
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}()

	err = gitclone.Clone(ctx, url, dest, func(line string) {
		s.emit(events.CloneProgress, line)
	})
	if err != nil {
		return StartResult{}, err
	}

	// The clone is a repository; the collection is somewhere inside it, and
	// usually is it. Opening the repository root when the collection is in a
	// subdirectory would show an empty tree beside a `.requests` folder, so
	// the search is worth the twenty lines it takes.
	root := collection.FindWithin(dest)
	result := StartResult{Root: root}
	if root == "" {
		result.Root = dest
		result.Note = "Cloned, but nothing in it looks like a collection — no .http files and no env/ directory."
	}
	if err := s.open(result.Root); err != nil {
		return StartResult{}, err
	}
	return result, nil
}

// CancelClone stops the clone in flight, if there is one. Doing nothing is a
// normal outcome: the dialog's Cancel does not know whether git has already
// finished.
func (s *StartService) CancelClone() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// open makes path's collection the current one, and leaves the window looking
// at path.
//
// `OpenPath` and not `Open`, for the second half of that: it is the one entry
// point for a path that came from outside the window (CLAUDE.md), and the
// thing it adds is the pending open the window *pulls* once it has mounted.
// Without it a new collection arrives with an empty centre pane, which is the
// same bug a double-clicked `.http` file had.
//
// Refresh follows because Go asked rather than the window: Open emits
// CollectionOpened, which carries the collection and not the tree, and Refresh
// is the mechanism a write Otis makes uses to announce itself.
func (s *StartService) open(path string) error {
	if err := s.collections.OpenPath(path); err != nil {
		return err
	}
	return s.collections.Refresh()
}

func (s *StartService) emit(name string, data any) {
	if s.app == nil {
		return // not running under Wails, as in tests
	}
	s.app.Event.Emit(name, data)
}

// destination validates parent and name and joins them.
//
// The name is one directory, not a path: a dialog that quietly accepted
// `../../etc` or an absolute path would be writing somewhere the person
// reading the preview line did not agree to. Leading dots are fine — the
// convention is `.requests` — which is why this is not `collection.Slug`,
// whose job is naming a *request* file.
func destination(parent, name string) (string, error) {
	parent = strings.TrimSpace(parent)
	name = strings.TrimSpace(name)
	if parent == "" {
		return "", fmt.Errorf("choose where to put it")
	}
	if !filepath.IsAbs(parent) {
		return "", fmt.Errorf("the location must be a full path")
	}
	if name == "" {
		return "", fmt.Errorf("give the folder a name")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%q is not a folder name", name)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("%s is not there", parent)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", parent)
	}
	return filepath.Join(parent, name), nil
}
