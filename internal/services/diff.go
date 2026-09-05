package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/diff"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/git"
)

// DiffService is the git diff view (screen 1b).
//
// It is the only service that writes to a repository, and only the writes a
// review needs: the index, and a commit on the current branch. GitService
// stays read-only and answers "what does git think" for the tree dots and the
// status bar.
//
// Every mutating method emits events.GitChanged, so the tree, the status bar
// and this view are never out of step with each other — and Discard, which
// writes a file in the collection, also refreshes the collection so the
// window sees the file it now is.
type DiffService struct {
	app         *application.App
	collections *CollectionService
}

// NewDiffService constructs the service around the open collection.
func NewDiffService(collections *CollectionService) *DiffService {
	return &DiffService{collections: collections}
}

// ServiceStartup resolves the application.
func (s *DiffService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// Overview lists every change under the collection root.
//
// A collection outside a repository yields Overview{Repository: false} and no
// error: that is a normal state and one of the view's empty cases, not a
// failure.
func (s *DiffService) Overview() (diff.Overview, error) {
	repo, err := s.open()
	if err != nil {
		return diff.Overview{}, err
	}
	if repo == nil {
		return diff.Overview{}, nil
	}
	return repo.Overview()
}

// File returns one file's diff, working tree against HEAD. nodePath is
// collection-relative.
func (s *DiffService) File(nodePath string) (diff.FileDiff, error) {
	repo, err := s.repo()
	if err != nil {
		return diff.FileDiff{}, err
	}
	return repo.File(nodePath)
}

// Stage stages a whole file.
func (s *DiffService) Stage(nodePath string) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error { return repo.Stage(nodePath) })
}

// Unstage returns a whole file's index entry to HEAD, leaving the working
// tree alone.
func (s *DiffService) Unstage(nodePath string) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error { return repo.Unstage(nodePath) })
}

// StageAll stages every change under the collection root.
func (s *DiffService) StageAll() (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error { return repo.StageAll() })
}

// StageHunks stages the named hunks of a file. The numbers index the FileDiff
// the window is showing; the diff is recomputed here and the numbers checked
// against it, so a stale view cannot stage something else.
func (s *DiffService) StageHunks(nodePath string, hunks []int) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error { return repo.StageHunks(nodePath, hunks) })
}

// UnstageHunks unstages the named hunks of a file.
func (s *DiffService) UnstageHunks(nodePath string, hunks []int) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error { return repo.UnstageHunks(nodePath, hunks) })
}

// DiscardFile throws away a whole file's uncommitted changes: the working
// tree returns to HEAD, and an untracked file is deleted.
//
// **Destructive.** confirm must be true or the call is refused with
// diff.ErrNoConfirm. The distinct name and the flag are deliberate: this is
// the only operation in Otis that destroys work git cannot get back, and a
// second caller — the CLI, the MCP server — must not be able to reach it by
// picking the wrong method off this service. The dialog in front of it is a
// courtesy on top, not the safety.
func (s *DiffService) DiscardFile(nodePath string, confirm bool) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error {
		return repo.Discard(nodePath, confirm, s.writer())
	})
}

// DiscardHunks throws away the named hunks in the working tree, leaving the
// rest.
//
// **Destructive**, on the same terms as DiscardFile: confirm must be true.
func (s *DiffService) DiscardHunks(nodePath string, hunks []int, confirm bool) (diff.Overview, error) {
	return s.mutate(func(repo *diff.Repo) error {
		return repo.DiscardHunks(nodePath, hunks, confirm, s.writer())
	})
}

// Commit records everything staged with message and returns the new commit
// along with the view as it now is.
//
// Everything staged, which is the repository's index and not only the
// collection's part of it: a commit made here includes anything staged
// outside the collection too. That is what git does, and the view says so
// beside the button rather than pretending otherwise.
func (s *DiffService) Commit(message string) (CommitResult, error) {
	repo, err := s.repo()
	if err != nil {
		return CommitResult{}, err
	}
	commit, err := repo.Commit(message)
	if err != nil {
		return CommitResult{}, err
	}
	over, err := s.announce(repo)
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{Commit: *commit, Overview: over}, nil
}

// CommitResult is the new commit and the view after it.
type CommitResult struct {
	Commit   diff.Commit   `json:"commit"`
	Overview diff.Overview `json:"overview"`
}

// mutate runs an operation and returns the view as it now is, having told the
// rest of the window.
func (s *DiffService) mutate(fn func(*diff.Repo) error) (diff.Overview, error) {
	repo, err := s.repo()
	if err != nil {
		return diff.Overview{}, err
	}
	if err := fn(repo); err != nil {
		return diff.Overview{}, err
	}
	return s.announce(repo)
}

// announce re-reads the view, emits events.GitChanged so the tree dots and
// the status bar follow, and re-walks the collection when a file on disk may
// have changed.
//
// The write guard means the watcher will not report a discard as an external
// change, which is precisely why the writer has to announce it itself
// (CLAUDE.md).
func (s *DiffService) announce(repo *diff.Repo) (diff.Overview, error) {
	over, err := repo.Overview()
	if err != nil {
		return diff.Overview{}, err
	}
	if root := s.collections.Current().Path; root != "" {
		if state, err := git.Read(root); err == nil {
			s.emit(events.GitChanged, state)
		}
		// Re-walk so a discarded, restored or deleted file is the file the
		// sidebar and the open documents see. Refresh emits
		// events.CollectionChanged itself.
		if err := s.collections.Refresh(); err != nil {
			s.logError("re-reading the collection after a git operation", err)
		}
	}
	return over, nil
}

// writer hands the diff package the collection's write guard, so a discard is
// not reported back to the window as somebody else's change.
func (s *DiffService) writer() diff.Writer {
	guard := s.collections.Guard()
	return func(paths ...string) func() { return guard.Writing(paths...) }
}

// open returns the repository around the collection, or nil when there is
// none.
func (s *DiffService) open() (*diff.Repo, error) {
	root := s.collections.Current().Path
	if root == "" {
		return nil, errors.New("no collection is open")
	}
	return diff.Open(root)
}

// repo is open, but with "not a repository" as an error: every mutating
// method needs one, and there is nothing sensible to do without it.
func (s *DiffService) repo() (*diff.Repo, error) {
	repo, err := s.open()
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("this collection is not in a git repository")
	}
	return repo, nil
}

func (s *DiffService) emit(name string, data any) {
	if s.app == nil {
		return // not running under Wails, as in tests
	}
	s.app.Event.Emit(name, data)
}

func (s *DiffService) logError(msg string, err error) {
	recordError(s.app, "diff", msg, err)
}
