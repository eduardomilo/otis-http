package diff

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// ErrNoConfirm is returned by every destructive operation called without an
// explicit confirmation from the caller.
//
// Discarding is the only thing in Otis that destroys work git cannot get
// back: a staged change survives in the object database, a commit can be
// reset, but an unstaged edit thrown away is gone. So the confirmation is a
// parameter of the operation rather than a UI convention — a second caller
// (the CLI, the MCP server) cannot reach the destructive path by accident, and
// the dialog in front of it is a courtesy on top, not the safety.
var ErrNoConfirm = errors.New("this operation destroys uncommitted work and needs an explicit confirmation")

// ErrNothingToDo means the operation would change nothing.
var ErrNothingToDo = errors.New("nothing to do")

// Writer is the hook a caller uses to hold its own write guard around the
// worktree changes this package makes.
//
// Discarding writes a collection file, and every write Otis makes has to be
// announced by the writer rather than discovered by the watcher (CLAUDE.md).
// This package does not know about the guard, so the caller passes the
// wrapper: it is handed the absolute paths about to change and returns the
// release. A nil Writer means no guarding, which is what tests use.
type Writer func(paths ...string) (release func())

// Stage stages a whole file: the index gets the working tree's copy, or the
// deletion when the file is gone.
func (r *Repo) Stage(nodePath string) error {
	repoPath, err := r.repoPath(nodePath)
	if err != nil {
		return err
	}
	tree, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("diff: reading the worktree: %w", err)
	}
	if _, err := os.Stat(r.absPath(nodePath)); os.IsNotExist(err) {
		// A deleted file is staged by removing it from the index. Worktree.Add
		// on a missing path fails, and "stage this deletion" is what the row
		// means.
		if _, err := tree.Remove(repoPath); err != nil && !errors.Is(err, index.ErrEntryNotFound) {
			return fmt.Errorf("staging the deletion of %s: %w", nodePath, err)
		}
		return nil
	}
	if _, err := tree.Add(repoPath); err != nil {
		return fmt.Errorf("staging %s: %w", nodePath, err)
	}
	return nil
}

// Unstage returns a whole file's index entry to HEAD, leaving the working
// tree alone. A file that is not in HEAD leaves the index entirely, which
// makes it untracked again.
func (r *Repo) Unstage(nodePath string) error {
	repoPath, err := r.repoPath(nodePath)
	if err != nil {
		return err
	}
	_, headTree, err := r.head()
	if err != nil {
		return err
	}
	if headTree != nil {
		if entry, err := headTree.File(repoPath); err == nil {
			content, _, _, _ := blobOf(headTree, repoPath)
			return r.setIndex(repoPath, entry.Hash, []byte(content))
		}
	}
	return r.removeIndex(repoPath)
}

// StageHunks sets the index to HEAD plus the hunks already staged plus the
// ones named here.
//
// The hunk numbers index the FileDiff the window is showing. That the caller
// hands back positions in a diff it was given, rather than a patch it built,
// is what keeps this honest: the diff is recomputed here and the positions
// checked against it, so a stale view cannot stage something else.
func (r *Repo) StageHunks(nodePath string, hunks []int) error {
	return r.restage(nodePath, hunks, true)
}

// UnstageHunks sets the index to HEAD plus the staged hunks except the ones
// named here.
func (r *Repo) UnstageHunks(nodePath string, hunks []int) error {
	return r.restage(nodePath, hunks, false)
}

// restage rebuilds the index entry from HEAD plus a recomputed set of hunks.
func (r *Repo) restage(nodePath string, want []int, add bool) error {
	repoPath, err := r.repoPath(nodePath)
	if err != nil {
		return err
	}
	fd, st, err := r.diffOf(nodePath)
	if err != nil {
		return err
	}
	if fd.Binary || fd.Note != "" {
		return fmt.Errorf("%s has no hunks to stage; stage the whole file instead", nodePath)
	}
	chosen, err := pick(fd.Hunks, want)
	if err != nil {
		return err
	}

	// The set to leave in the index: what is staged now, plus or minus the
	// chosen hunks. Recomputed from the diff rather than trusted from the
	// caller, so two windows disagreeing cannot corrupt the index.
	var keep []Hunk
	for _, h := range fd.Hunks {
		staged := h.Staged
		if in(chosen, h) {
			staged = add
		}
		if staged {
			keep = append(keep, h)
		}
	}

	content := Apply(st.head, keep)
	if content == st.head && !st.hasIndex {
		return nil
	}
	if len(keep) == 0 && !st.hasHead {
		// Nothing staged and nothing in HEAD: the file goes back to untracked.
		return r.removeIndex(repoPath)
	}
	hash, err := r.writeBlob([]byte(content))
	if err != nil {
		return err
	}
	return r.setIndex(repoPath, hash, []byte(content))
}

// Discard throws away a whole file's uncommitted changes: the working tree
// returns to HEAD, and an untracked file is deleted.
//
// Destructive, and it needs confirm to be true. write, when not nil, is asked
// to guard the paths this touches.
func (r *Repo) Discard(nodePath string, confirm bool, write Writer) error {
	if !confirm {
		return ErrNoConfirm
	}
	repoPath, err := r.repoPath(nodePath)
	if err != nil {
		return err
	}
	target := r.absPath(nodePath)
	release := guard(write, target)
	defer release()

	_, headTree, err := r.head()
	if err != nil {
		return err
	}
	if headTree != nil {
		if content, _, _, ok := blobOf(headTree, repoPath); ok {
			if err := writeWorktreeFile(target, []byte(content)); err != nil {
				return err
			}
			// The index goes back too, so the file is clean rather than
			// "staged, then reverted in the worktree".
			return r.Stage(nodePath)
		}
	}
	// Not in HEAD: the file is new, so discarding it means deleting it.
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discarding %s: %w", nodePath, err)
	}
	return r.removeIndex(repoPath)
}

// DiscardHunks undoes the named hunks in the working tree, leaving the rest.
//
// Destructive, and it needs confirm to be true.
func (r *Repo) DiscardHunks(nodePath string, hunks []int, confirm bool, write Writer) error {
	if !confirm {
		return ErrNoConfirm
	}
	fd, st, err := r.diffOf(nodePath)
	if err != nil {
		return err
	}
	if fd.Binary || fd.Note != "" {
		return fmt.Errorf("%s has no hunks to discard; discard the whole file instead", nodePath)
	}
	chosen, err := pick(fd.Hunks, hunks)
	if err != nil {
		return err
	}

	target := r.absPath(nodePath)
	release := guard(write, target)
	defer release()

	content := Reverse(st.work, chosen)
	if err := writeWorktreeFile(target, []byte(content)); err != nil {
		return err
	}
	// A hunk that was staged and is now gone from the worktree would leave
	// the index claiming a change the file no longer has, so the index is
	// rebuilt from what is left.
	var keep []Hunk
	for _, h := range fd.Hunks {
		if h.Staged && !in(chosen, h) {
			keep = append(keep, h)
		}
	}
	repoPath, err := r.repoPath(nodePath)
	if err != nil {
		return err
	}
	if len(keep) == 0 && !st.hasHead {
		return r.removeIndex(repoPath)
	}
	if len(keep) == 0 && st.hasIndex && st.index != st.head {
		hash, err := r.writeBlob([]byte(st.head))
		if err != nil {
			return err
		}
		return r.setIndex(repoPath, hash, []byte(st.head))
	}
	if len(keep) > 0 {
		staged := Apply(st.head, keep)
		hash, err := r.writeBlob([]byte(staged))
		if err != nil {
			return err
		}
		return r.setIndex(repoPath, hash, []byte(staged))
	}
	return nil
}

// Commit records everything staged, with message, and returns the new commit.
//
// Everything staged: the index is the repository's, not the collection's, so
// a commit made here includes anything staged outside the collection too.
// That is git's behaviour and pretending otherwise would be a lie; the view
// says so beside the button.
func (r *Repo) Commit(message string) (*Commit, error) {
	if len(message) == 0 {
		return nil, errors.New("a commit needs a message")
	}
	if ok, reason := r.commitIdentity(); !ok {
		return nil, errors.New(reason)
	}
	tree, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the worktree: %w", err)
	}
	status, err := tree.Status()
	if err != nil {
		return nil, fmt.Errorf("diff: reading the status: %w", err)
	}
	staged := false
	for _, entry := range status {
		if entry.Staging != gogit.Unmodified && entry.Staging != gogit.Untracked {
			staged = true
			break
		}
	}
	if !staged {
		return nil, fmt.Errorf("%w: nothing is staged", ErrNothingToDo)
	}

	hash, err := tree.Commit(message, &gogit.CommitOptions{})
	if err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	commit, err := r.repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("reading the new commit: %w", err)
	}
	return describe(commit), nil
}

// StageAll stages every change under the collection root.
//
// Under the collection root, deliberately: the diff view shows the
// collection, and a "Stage all" that also swept up the caller's unrelated
// source edits would be staging things it never showed them.
func (r *Repo) StageAll() error {
	over, err := r.Overview()
	if err != nil {
		return err
	}
	for _, c := range over.Changes {
		if !c.Unstaged {
			continue
		}
		if c.Status == StatusRenamed && c.OldPath != "" {
			// Both halves, or the index keeps the file at its old path.
			if err := r.Stage(c.OldPath); err != nil {
				return err
			}
		}
		if err := r.Stage(c.Path); err != nil {
			return err
		}
	}
	return nil
}

// diffOf recomputes one file's diff and the three contents behind it.
func (r *Repo) diffOf(nodePath string) (FileDiff, state, error) {
	_, headTree, err := r.head()
	if err != nil {
		return FileDiff{}, state{}, err
	}
	states, err := r.states(headTree)
	if err != nil {
		return FileDiff{}, state{}, err
	}
	st, ok := states[nodePath]
	if !ok {
		return FileDiff{}, state{}, fmt.Errorf("%s has no changes", nodePath)
	}
	return r.fileDiff(st), st, nil
}

// pick resolves hunk positions against the diff they came from.
func pick(hunks []Hunk, want []int) ([]Hunk, error) {
	if len(want) == 0 {
		return nil, fmt.Errorf("%w: no hunks were named", ErrNothingToDo)
	}
	var out []Hunk
	for _, at := range want {
		if at < 0 || at >= len(hunks) {
			return nil, fmt.Errorf("hunk %d is not in this diff, which has %d; the view is out of date", at, len(hunks))
		}
		out = append(out, hunks[at])
	}
	return out, nil
}

// in reports whether a hunk is in the list.
func in(list []Hunk, h Hunk) bool { return containsHunk(list, h) }

// writeBlob stores content as a blob and returns its hash.
func (r *Repo) writeBlob(content []byte) (plumbing.Hash, error) {
	obj := r.repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(content)))
	writer, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writing a blob: %w", err)
	}
	if _, err := writer.Write(content); err != nil {
		writer.Close()
		return plumbing.ZeroHash, fmt.Errorf("writing a blob: %w", err)
	}
	if err := writer.Close(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writing a blob: %w", err)
	}
	hash, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("storing a blob: %w", err)
	}
	return hash, nil
}

// setIndex points a path's index entry at a blob holding content, adding the
// entry when the file was not in the index.
//
// The entry's stat data is not decoration. git compares an index entry's size
// against the file before it will spend anything on hashing, so an entry left
// at size zero reads as "modified in the working tree" forever — a file whose
// every hunk had been staged would still show up as unstaged, which is
// go-git's own warning in doUpdateFileToIndex.
//
// The times are the other half of the same care, in the other direction. They
// are filled only when the staged content *is* what is on disk: git then
// trusts its stat cache and skips the hash. When the two differ — which is
// exactly what partial staging means — they are left zero, so git notices the
// mismatch and re-reads the file rather than believing a cache that would say
// the file is clean when it is not.
func (r *Repo) setIndex(repoPath string, hash plumbing.Hash, content []byte) error {
	idx, err := r.repo.Storer.Index()
	if err != nil {
		return fmt.Errorf("reading the index: %w", err)
	}
	entry, err := idx.Entry(repoPath)
	if err != nil {
		entry = idx.Add(repoPath)
	}
	entry.Hash = hash
	entry.Mode = blobMode
	entry.Size = uint32(len(content))
	entry.ModifiedAt = time.Time{}
	entry.CreatedAt = time.Time{}
	entry.Dev, entry.Inode, entry.UID, entry.GID = 0, 0, 0, 0

	onDisk := filepath.Join(r.worktree, filepath.FromSlash(repoPath))
	if current, err := os.ReadFile(onDisk); err == nil && bytes.Equal(current, content) {
		if info, err := os.Lstat(onDisk); err == nil {
			entry.ModifiedAt = info.ModTime()
			entry.Size = uint32(info.Size())
		}
	}

	if err := r.repo.Storer.SetIndex(idx); err != nil {
		return fmt.Errorf("writing the index: %w", err)
	}
	return nil
}

// removeIndex drops a path's index entry, which is what makes a file
// untracked again.
func (r *Repo) removeIndex(repoPath string) error {
	idx, err := r.repo.Storer.Index()
	if err != nil {
		return fmt.Errorf("reading the index: %w", err)
	}
	kept := idx.Entries[:0]
	for _, e := range idx.Entries {
		if e.Name != repoPath {
			kept = append(kept, e)
		}
	}
	idx.Entries = kept
	if err := r.repo.Storer.SetIndex(idx); err != nil {
		return fmt.Errorf("writing the index: %w", err)
	}
	return nil
}

// writeWorktreeFile replaces a file on disk, creating its directory.
func writeWorktreeFile(target string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}

// guard runs the caller's write hook, or returns a no-op release.
func guard(write Writer, paths ...string) func() {
	if write == nil {
		return func() {}
	}
	return write(paths...)
}
