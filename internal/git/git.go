// Package git reads the state of the repository a collection lives in. It
// never writes: Otis shows you what git thinks, and leaves committing to git.
//
// Not being in a repository is a normal state, not an error — a collection is
// a directory of files and works perfectly well outside version control. Every
// function here says so by returning a zero value rather than failing.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Status is a file's state in the working tree, as the one letter the status
// bar and the tree dots show (DESIGN-NOTES §2.6).
type Status string

const (
	// StatusClean means the file matches HEAD and the index.
	StatusClean Status = ""
	// StatusModified is git's M: tracked and changed.
	StatusModified Status = "M"
	// StatusUntracked is git's U: not in the index at all.
	StatusUntracked Status = "U"
	// StatusAdded is git's A: newly staged.
	StatusAdded Status = "A"
	// StatusDeleted is git's D. The tree cannot show a file that is gone, but
	// the status bar can still name it.
	StatusDeleted Status = "D"
)

// State is everything the window knows about the repository at one moment.
type State struct {
	// Repository is false when the collection is not inside a git repository.
	// Every other field is then zero and nothing git-related is shown.
	Repository bool `json:"repository"`
	// Branch is the checked-out branch, or "" on a detached HEAD.
	Branch string `json:"branch"`
	// Detached reports a checkout that is not on a branch; Head then names
	// the commit.
	Detached bool `json:"detached"`
	// Head is the short hash of the checked-out commit, "" on an empty
	// repository with no commits yet.
	Head string `json:"head"`
	// Ahead and Behind count commits relative to the branch's upstream. Both
	// are zero when there is no upstream to compare against.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// HasUpstream distinguishes "0 ahead, 0 behind" from "nothing to
	// compare": a branch never pushed has no upstream, and showing it as
	// level with one would be a lie.
	HasUpstream bool `json:"hasUpstream"`
	// Statuses maps a path relative to the collection root, "/"-separated, to
	// its status. Clean files are absent.
	Statuses map[string]Status `json:"statuses"`
}

// Read returns the state of the repository containing dir, restricting the
// per-path statuses to files under dir.
//
// dir not being in a repository yields State{Repository: false} and no error.
func Read(dir string) (State, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return State{}, fmt.Errorf("git: %w", err)
	}
	// go-git reports the worktree root with its symlinks resolved (on macOS
	// /var is /private/var), so resolve ours too or every path looks like it
	// is outside the collection.
	abs = resolve(abs)
	repo, err := gogit.PlainOpenWithOptions(abs, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("git: opening the repository above %s: %w", abs, err)
	}

	state := State{Repository: true, Statuses: map[string]Status{}}
	readHead(repo, &state)
	if err := readStatuses(repo, abs, &state); err != nil {
		return state, err
	}
	return state, nil
}

// readHead fills in the branch, the head commit and the ahead/behind counts.
// A repository with no commits is normal (a fresh `git init`), so a missing
// HEAD is not an error.
func readHead(repo *gogit.Repository, state *State) {
	head, err := repo.Head()
	if err != nil {
		return // no commits yet
	}
	state.Head = head.Hash().String()[:7]
	if !head.Name().IsBranch() {
		state.Detached = true
		return
	}
	state.Branch = head.Name().Short()

	upstream, err := upstreamRef(repo, state.Branch)
	if err != nil || upstream == nil {
		return
	}
	state.HasUpstream = true
	ahead, behind, err := countAheadBehind(repo, head.Hash(), upstream.Hash())
	if err != nil {
		return
	}
	state.Ahead, state.Behind = ahead, behind
}

// upstreamRef resolves the remote-tracking ref a branch is configured to
// track, or nil when it tracks nothing.
func upstreamRef(repo *gogit.Repository, branch string) (*plumbing.Reference, error) {
	cfg, err := repo.Config()
	if err != nil {
		return nil, err
	}
	b, ok := cfg.Branches[branch]
	if !ok || b.Remote == "" || b.Merge == "" {
		return nil, nil
	}
	name := plumbing.NewRemoteReferenceName(b.Remote, b.Merge.Short())
	ref, err := repo.Reference(name, true)
	if err != nil {
		// Configured but not fetched yet. Not an error, just nothing to
		// compare against.
		return nil, nil
	}
	return ref, nil
}

// countAheadBehind counts the commits reachable from one side and not the
// other, which is what `git status` means by "ahead 2, behind 1".
func countAheadBehind(repo *gogit.Repository, local, remote plumbing.Hash) (ahead, behind int, err error) {
	localSet, err := reachable(repo, local)
	if err != nil {
		return 0, 0, err
	}
	remoteSet, err := reachable(repo, remote)
	if err != nil {
		return 0, 0, err
	}
	for hash := range localSet {
		if _, shared := remoteSet[hash]; !shared {
			ahead++
		}
	}
	for hash := range remoteSet {
		if _, shared := localSet[hash]; !shared {
			behind++
		}
	}
	return ahead, behind, nil
}

// reachable returns every commit reachable from start.
//
// This walks the whole history on both sides, which is fine for the repos a
// collection lives in and is called only when HEAD or the index changes. A
// merge-base walk would be the answer if it ever became a cost.
func reachable(repo *gogit.Repository, start plumbing.Hash) (map[plumbing.Hash]struct{}, error) {
	commit, err := repo.CommitObject(start)
	if err != nil {
		return nil, err
	}
	seen := map[plumbing.Hash]struct{}{}
	iter := object.NewCommitPreorderIter(commit, nil, nil)
	err = iter.ForEach(func(c *object.Commit) error {
		seen[c.Hash] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seen, nil
}

// readStatuses fills in the per-path statuses for files under dir.
func readStatuses(repo *gogit.Repository, dir string, state *State) error {
	tree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("git: reading the worktree: %w", err)
	}
	status, err := tree.Status()
	if err != nil {
		return fmt.Errorf("git: reading the status: %w", err)
	}
	root := resolve(tree.Filesystem.Root())
	for repoPath, entry := range status {
		abs := filepath.Join(root, filepath.FromSlash(repoPath))
		rel, err := filepath.Rel(dir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // outside the collection
		}
		if s := classify(entry); s != StatusClean {
			state.Statuses[filepath.ToSlash(rel)] = s
		}
	}
	return nil
}

// classify reduces git's two-axis status (staging area, worktree) to the one
// letter the design shows. The worktree wins when the two disagree, because
// that is the state of the file you are looking at.
func classify(entry *gogit.FileStatus) Status {
	if entry.Worktree == gogit.Untracked && entry.Staging == gogit.Untracked {
		return StatusUntracked
	}
	for _, code := range []gogit.StatusCode{entry.Worktree, entry.Staging} {
		switch code {
		case gogit.Modified, gogit.Renamed, gogit.Copied, gogit.UpdatedButUnmerged:
			return StatusModified
		case gogit.Deleted:
			return StatusDeleted
		case gogit.Added:
			return StatusAdded
		}
	}
	return StatusClean
}

// resolve returns path with its symlinks evaluated, or path itself when it
// cannot be resolved (it may not exist).
func resolve(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// IsRepository reports whether dir is inside a git repository.
func IsRepository(dir string) bool { return Dir(dir) != "" }

// Dir returns the absolute path of the git directory for the repository
// containing dir, or "" when dir is not in one.
//
// It is what the watcher watches for HEAD and index. The search walks upwards
// because the common layout puts the collection below the repository root —
// a ".requests" directory beside the code it exercises — so the git directory
// is usually *above* the directory being watched, not inside it.
func Dir(dir string) string {
	current, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(current, ".git")
		info, err := os.Stat(candidate)
		switch {
		case err != nil:
			// keep walking up
		case info.IsDir():
			return candidate
		case info.Mode().IsRegular():
			// A worktree or submodule: .git is a file naming the real
			// directory. Following it is what makes those layouts work.
			if real := readGitFile(candidate); real != "" {
				return real
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// readGitFile resolves a ".git" file of the form "gitdir: <path>".
func readGitFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	target, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return ""
	}
	return target
}
