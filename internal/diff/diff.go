// Package diff is the git diff view (screen 1b): what changed in the
// collection against HEAD, and the four things a review does about it —
// stage, unstage, discard, commit.
//
// # Why this is not internal/git
//
// internal/git is read-only on purpose, and stays that way: it answers "what
// does git think" for the tree dots and the status bar, and never writes.
// This package is the one place Otis writes to a repository, and only the
// writes a review needs: the index, and a commit on the current branch. It
// does not push, pull, fetch, merge, rebase, cherry-pick, or move HEAD. Those
// are git's job and the terminal's.
//
// # What the view shows
//
// One diff, **working tree against HEAD**, which is what the design draws and
// the only numbering a reader can trust — two bases interleaved in one column
// would put the same line on screen twice under different numbers. Each hunk
// also reports whether it is already staged, which is what makes per-hunk
// staging exact: staging a hunk sets the index to HEAD plus every hunk staged
// so far plus this one, and unstaging one sets it to HEAD plus the rest.
package diff

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Status is a file's state, as the one letter the changes list shows.
type Status string

const (
	// StatusModified is git's M: tracked and changed.
	StatusModified Status = "M"
	// StatusAdded is a file staged for addition.
	StatusAdded Status = "A"
	// StatusDeleted is git's D.
	StatusDeleted Status = "D"
	// StatusUntracked is a file git does not know about yet.
	StatusUntracked Status = "U"
	// StatusRenamed is a file that moved. git reports an unstaged move as a
	// delete plus an untracked file; Otis pairs them back up, because "this
	// request moved into orders/" is the change that actually happened.
	StatusRenamed Status = "R"
)

// MaxDiffBytes bounds what will be diffed. Past it the file is reported as
// changed with no hunks: a line diff of a 20 MB file is not something anyone
// reviews in a pane, and computing one would stall the window.
const MaxDiffBytes = 4 << 20

// RenameSimilarity is how alike two files must be for a delete and an
// untracked file to be reported as one rename. Identical content always
// pairs; below this they are two separate changes.
const RenameSimilarity = 0.5

// Commit is a commit as the view names one.
type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	When    time.Time `json:"when"`
}

// Change is one row of the changes list.
type Change struct {
	// Path is collection-relative, "/"-separated (docs/FORMAT.md §2.1).
	Path string `json:"path"`
	// OldPath is where a renamed file came from, else "".
	OldPath string `json:"oldPath,omitempty"`
	Status  Status `json:"status"`
	Adds    int    `json:"adds"`
	Dels    int    `json:"dels"`
	// Staged and Unstaged say which halves exist, so a partly staged file
	// can say so rather than looking like either one.
	Staged   bool `json:"staged"`
	Unstaged bool `json:"unstaged"`
	// Hunks is how many hunks the file has, so the list agrees with the
	// footer before the file is opened.
	Hunks int `json:"hunks"`
	// Binary or oversized: a change with no reviewable diff.
	Binary bool `json:"binary,omitempty"`
}

// Overview is the whole diff view's left-hand side.
type Overview struct {
	// Repository is false when the collection is not in a git repository,
	// which is a normal state and one of the view's empty cases.
	Repository bool     `json:"repository"`
	Branch     string   `json:"branch"`
	Head       string   `json:"head"`
	Detached   bool     `json:"detached"`
	Changes    []Change `json:"changes"`
	// LastCommit is nil in a repository with no commits yet.
	LastCommit *Commit `json:"lastCommit,omitempty"`
	// Adds, Dels and Hunks total the changes, which is what the status bar
	// shows ("+4 −2 · 2 hunks").
	Adds  int `json:"adds"`
	Dels  int `json:"dels"`
	Hunks int `json:"hunks"`
	// CanCommit is false when git has no identity configured, in which case
	// Reason says so: a commit would fail, and saying why up front beats
	// failing on the button.
	CanCommit bool   `json:"canCommit"`
	Reason    string `json:"reason,omitempty"`
}

// FileDiff is one file's diff, working tree against HEAD.
type FileDiff struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  Status `json:"status"`
	Adds    int    `json:"adds"`
	Dels    int    `json:"dels"`
	Hunks   []Hunk `json:"hunks"`
	Binary  bool   `json:"binary,omitempty"`
	// Note explains a file with no hunks that is nonetheless changed.
	Note string `json:"note,omitempty"`
}

// Repo is the repository around one collection.
type Repo struct {
	repo *gogit.Repository
	// root is the collection root, absolute with symlinks resolved.
	root string
	// worktree is the repository's worktree root, likewise resolved. It is
	// usually *above* root: the common layout puts the collection in a
	// subdirectory of the repository.
	worktree string
}

// Open returns the repository containing the collection at root.
//
// Not being in a repository is a normal state, not an error: it yields
// (nil, nil), and every caller renders that as the view's "not a repository"
// empty state.
func Open(root string) (*Repo, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("diff: %w", err)
	}
	// go-git reports the worktree root with symlinks resolved (on macOS /var
	// is /private/var), so resolve ours too or every path looks like it is
	// outside the collection.
	abs = resolvePath(abs)
	repo, err := gogit.PlainOpenWithOptions(abs, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return nil, nil
		}
		return nil, fmt.Errorf("diff: opening the repository above %s: %w", abs, err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the worktree: %w", err)
	}
	return &Repo{repo: repo, root: abs, worktree: resolvePath(tree.Filesystem.Root())}, nil
}

// Overview lists every change under the collection root.
func (r *Repo) Overview() (Overview, error) {
	out := Overview{Repository: true}
	head, headTree, err := r.head()
	if err != nil {
		return out, err
	}
	if head != nil {
		out.Head = head.Hash.String()[:7]
		out.LastCommit = describe(head)
		if ref, err := r.repo.Head(); err == nil {
			if ref.Name().IsBranch() {
				out.Branch = ref.Name().Short()
			} else {
				out.Detached = true
			}
		}
	}
	out.CanCommit, out.Reason = r.commitIdentity()

	changes, err := r.changes(headTree)
	if err != nil {
		return out, err
	}
	for _, c := range changes {
		out.Adds += c.Adds
		out.Dels += c.Dels
		out.Hunks += c.Hunks
	}
	out.Changes = changes
	return out, nil
}

// File returns one file's diff. nodePath is collection-relative.
func (r *Repo) File(nodePath string) (FileDiff, error) {
	_, headTree, err := r.head()
	if err != nil {
		return FileDiff{}, err
	}
	states, err := r.states(headTree)
	if err != nil {
		return FileDiff{}, err
	}
	st, ok := states[nodePath]
	if !ok {
		return FileDiff{}, fmt.Errorf("%s has no changes", nodePath)
	}
	return r.fileDiff(st), nil
}

// state is everything known about one changed path, on all three sides.
type state struct {
	// path and oldPath are collection-relative.
	path    string
	oldPath string
	status  Status
	// head, index and work are the three contents. Missing means the file
	// does not exist on that side.
	head, index, work          string
	hasHead, hasIndex, hasWork bool
	binary                     bool
	oversized                  bool
	// staged and unstaged are git's own two axes, taken from the status
	// rather than inferred from the three contents.
	//
	// Inferring them is what a rename breaks: pairing a delete with an
	// untracked file gives one state whose HEAD side comes from the old path
	// and whose index side is the new path's — which has no index entry — so
	// "in HEAD but not in the index" reads as a staged deletion when nothing
	// is staged at all.
	staged, unstaged bool
}

// changes turns the states into changes-list rows.
func (r *Repo) changes(headTree *object.Tree) ([]Change, error) {
	states, err := r.states(headTree)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(states))
	for p := range states {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	changes := make([]Change, 0, len(paths))
	for _, p := range paths {
		st := states[p]
		fd := r.fileDiff(st)
		changes = append(changes, Change{
			Path:     fd.Path,
			OldPath:  fd.OldPath,
			Status:   fd.Status,
			Adds:     fd.Adds,
			Dels:     fd.Dels,
			Staged:   st.staged,
			Unstaged: st.unstaged,
			Hunks:    len(fd.Hunks),
			Binary:   fd.Binary,
		})
	}
	return changes, nil
}

// fileDiff builds the diff for one state: working tree against HEAD, with
// each hunk told whether the index already has it.
func (r *Repo) fileDiff(st state) FileDiff {
	out := FileDiff{Path: st.path, OldPath: st.oldPath, Status: st.status, Binary: st.binary}
	switch {
	case st.binary:
		out.Note = "This file is binary, so there is nothing to show line by line."
		return out
	case st.oversized:
		out.Note = fmt.Sprintf("This file is larger than %d MB, so it is not diffed here.", MaxDiffBytes>>20)
		return out
	}

	out.Hunks = Hunks(st.head, st.work)
	staged := Hunks(st.head, st.index)
	for i := range out.Hunks {
		out.Hunks[i].Label = Label(st.path, st.work, out.Hunks[i])
		out.Hunks[i].Staged = containsHunk(staged, out.Hunks[i])
		out.Adds += out.Hunks[i].Adds
		out.Dels += out.Hunks[i].Dels
	}
	return out
}

// containsHunk reports whether an identical hunk is in the list.
//
// Identity is the hunk's own lines and offsets, which is what makes "is this
// hunk staged" answerable at all: staging a hunk puts exactly it into the
// index, so it reappears unchanged in the HEAD-to-index diff.
func containsHunk(list []Hunk, h Hunk) bool {
	for _, other := range list {
		if sameHunk(other, h) {
			return true
		}
	}
	return false
}

func sameHunk(a, b Hunk) bool {
	if a.OldStart != b.OldStart || a.OldLines != b.OldLines || len(a.Lines) != len(b.Lines) {
		return false
	}
	for i := range a.Lines {
		if a.Lines[i].Kind != b.Lines[i].Kind || a.Lines[i].Text != b.Lines[i].Text {
			return false
		}
	}
	return true
}

// states reads every changed path under the collection on all three sides,
// pairing deletes with untracked files into renames.
func (r *Repo) states(headTree *object.Tree) (map[string]state, error) {
	tree, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the worktree: %w", err)
	}
	status, err := tree.Status()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the status: %w", err)
	}
	idx, err := r.repo.Storer.Index()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the index: %w", err)
	}

	states := map[string]state{}
	for repoPath, entry := range status {
		nodePath, inside := r.nodePath(repoPath)
		if !inside {
			continue
		}
		if entry.Staging == gogit.Unmodified && entry.Worktree == gogit.Unmodified {
			continue
		}
		st := state{
			path:     nodePath,
			status:   classify(entry),
			staged:   entry.Staging != gogit.Unmodified && entry.Staging != gogit.Untracked,
			unstaged: entry.Worktree != gogit.Unmodified,
		}
		r.fill(&st, repoPath, headTree, idx)
		states[nodePath] = st
	}

	r.pairRenames(states)
	return states, nil
}

// fill reads a path's three contents.
func (r *Repo) fill(st *state, repoPath string, headTree *object.Tree, idx *index.Index) {
	if headTree != nil {
		if content, binary, size, ok := blobOf(headTree, repoPath); ok {
			st.head, st.hasHead, st.binary = content, true, st.binary || binary
			st.oversized = st.oversized || size > MaxDiffBytes
		}
	}
	if entry, err := idx.Entry(repoPath); err == nil {
		if content, binary, size, ok := r.object(entry.Hash); ok {
			st.index, st.hasIndex, st.binary = content, true, st.binary || binary
			st.oversized = st.oversized || size > MaxDiffBytes
		}
	}
	if data, err := os.ReadFile(filepath.Join(r.worktree, filepath.FromSlash(repoPath))); err == nil {
		st.work, st.hasWork = string(data), true
		if isBinary(data) {
			st.binary = true
		}
		if len(data) > MaxDiffBytes {
			st.oversized = true
		}
	}
}

// pairRenames turns a delete plus an untracked file with the same or nearly
// the same content into one renamed change.
//
// git does this at diff time by similarity and reports an *unstaged* move as
// "D old" plus "?? new", which is two rows for one thing that happened. The
// design's changes list shows a rename, and a reviewer reading "seed-order
// moved into fixtures/" understands the change; reading a delete and an
// addition, they have to work it out.
func (r *Repo) pairRenames(states map[string]state) {
	var gone, arrived []string
	for p, st := range states {
		switch st.status {
		case StatusDeleted:
			gone = append(gone, p)
		case StatusUntracked, StatusAdded:
			// Untracked before the move is staged, added after it: go-git's
			// Status does no rename detection either way, so both halves have
			// to be paired back up here.
			arrived = append(arrived, p)
		}
	}
	sort.Strings(gone)
	sort.Strings(arrived)

	taken := map[string]bool{}
	for _, from := range gone {
		best, bestScore := "", 0.0
		for _, to := range arrived {
			if taken[to] {
				continue
			}
			score := similarity(states[from].head, states[to].work)
			if score > bestScore {
				best, bestScore = to, score
			}
		}
		if best == "" || bestScore < RenameSimilarity {
			continue
		}
		taken[best] = true
		moved := states[best]
		moved.status = StatusRenamed
		moved.oldPath = from
		// The old side of a rename is where the file came from, so the diff
		// reads as the edit that came with the move rather than as a whole
		// new file.
		moved.head, moved.hasHead = states[from].head, states[from].hasHead
		moved.binary = moved.binary || states[from].binary
		// A move is staged only when both halves are: until then git still
		// has the file at its old path.
		moved.staged = moved.staged && states[from].staged
		moved.unstaged = moved.unstaged || states[from].unstaged
		states[best] = moved
		delete(states, from)
	}
}

// similarity is the fraction of lines the two texts share, 1 for identical
// content and 0 for nothing in common.
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	counts := map[string]int{}
	for _, line := range strings.Split(a, "\n") {
		counts[line]++
	}
	shared := 0
	bLines := strings.Split(b, "\n")
	for _, line := range bLines {
		if counts[line] > 0 {
			counts[line]--
			shared++
		}
	}
	longer := max(len(strings.Split(a, "\n")), len(bLines))
	if longer == 0 {
		return 0
	}
	return float64(shared) / float64(longer)
}

// classify reduces git's two-axis status to the one letter the list shows.
func classify(entry *gogit.FileStatus) Status {
	if entry.Worktree == gogit.Untracked && entry.Staging == gogit.Untracked {
		return StatusUntracked
	}
	if entry.Worktree == gogit.Deleted || entry.Staging == gogit.Deleted {
		return StatusDeleted
	}
	if entry.Staging == gogit.Added && entry.Worktree != gogit.Modified {
		return StatusAdded
	}
	if entry.Staging == gogit.Renamed || entry.Worktree == gogit.Renamed {
		return StatusRenamed
	}
	return StatusModified
}

// head returns the HEAD commit and its tree, both nil in a repository with no
// commits yet — which is a normal state, not an error.
func (r *Repo) head() (*object.Commit, *object.Tree, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return nil, nil, nil // no commits yet
	}
	commit, err := r.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, nil, fmt.Errorf("diff: reading HEAD: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, nil, fmt.Errorf("diff: reading the HEAD tree: %w", err)
	}
	return commit, tree, nil
}

// describe summarises a commit for the view's "Last commit" line.
func describe(c *object.Commit) *Commit {
	subject := c.Message
	if i := strings.IndexByte(subject, '\n'); i >= 0 {
		subject = subject[:i]
	}
	return &Commit{
		Hash:    c.Hash.String(),
		Short:   c.Hash.String()[:7],
		Subject: strings.TrimSpace(subject),
		Author:  c.Author.Name,
		When:    c.Author.When,
	}
}

// commitIdentity reports whether a commit is possible, and why not.
func (r *Repo) commitIdentity() (bool, string) {
	cfg, err := r.repo.ConfigScoped(config.SystemScope)
	if err != nil {
		return false, "git configuration could not be read"
	}
	if strings.TrimSpace(cfg.User.Name) == "" || strings.TrimSpace(cfg.User.Email) == "" {
		return false, "git has no user.name and user.email configured, so a commit would have no author"
	}
	return true, ""
}

// blobOf reads a path out of a tree.
func blobOf(tree *object.Tree, repoPath string) (content string, binary bool, size int64, ok bool) {
	entry, err := tree.File(repoPath)
	if err != nil {
		return "", false, 0, false
	}
	if entry.Size > MaxDiffBytes {
		return "", false, entry.Size, true
	}
	reader, err := entry.Reader()
	if err != nil {
		return "", false, entry.Size, false
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", false, entry.Size, false
	}
	return string(data), isBinary(data), entry.Size, true
}

// object reads a blob by hash, which is how the index's copy is read.
func (r *Repo) object(hash plumbing.Hash) (content string, binary bool, size int64, ok bool) {
	blob, err := r.repo.BlobObject(hash)
	if err != nil {
		return "", false, 0, false
	}
	if blob.Size > MaxDiffBytes {
		return "", false, blob.Size, true
	}
	reader, err := blob.Reader()
	if err != nil {
		return "", false, blob.Size, false
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", false, blob.Size, false
	}
	return string(data), isBinary(data), blob.Size, true
}

// isBinary uses git's own test: a NUL byte in the first 8000 bytes.
func isBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// nodePath converts a repository-relative path to a collection-relative one,
// reporting whether it is inside the collection at all.
func (r *Repo) nodePath(repoPath string) (string, bool) {
	abs := filepath.Join(r.worktree, filepath.FromSlash(repoPath))
	rel, err := filepath.Rel(r.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// repoPath is the inverse: a collection-relative node path as git names it.
func (r *Repo) repoPath(nodePath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(nodePath))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("%s is outside the collection", nodePath)
	}
	abs := filepath.Join(r.root, clean)
	rel, err := filepath.Rel(r.worktree, abs)
	if err != nil {
		return "", fmt.Errorf("%s is outside the repository", nodePath)
	}
	return filepath.ToSlash(rel), nil
}

// absPath is a node path on disk.
func (r *Repo) absPath(nodePath string) string {
	return filepath.Join(r.root, filepath.FromSlash(filepath.Clean(nodePath)))
}

// resolvePath evaluates symlinks, or returns the path unchanged when it
// cannot (it may not exist).
func resolvePath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// blobMode is the file mode a staged regular file gets.
const blobMode = filemode.Regular
