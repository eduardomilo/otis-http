// Package gitclone runs `git clone` in a subprocess, and does nothing else.
//
// # Why a subprocess and not a library
//
// Cloning needs credentials, and Otis' rule about credentials is that it does
// not have them. The user's own `git` already knows how to authenticate:
// their SSH agent, their `~/.gitconfig`, their credential helper — the macOS
// keychain helper, `git-credential-manager`, whatever they set up. Shelling
// out to it means a private repository clones exactly as it does in their
// terminal and **no credential passes through this process**. A Go git
// library would mean the opposite: Otis would have to collect a token or a
// key passphrase and hold it, which is the one thing docs/VISION.md's line
// under secrets forbids.
//
// # Why it must not be able to prompt
//
// A GUI app has no terminal, so a `git` that decides to ask for a password
// blocks forever with nothing on screen. `GIT_TERMINAL_PROMPT=0` and ssh's
// `BatchMode=yes` turn that into an immediate, legible failure, which the
// caller can turn into "clone it in a terminal and use Open folder". A hang
// is the worst outcome available here and it is the default one.
//
// # Why this is not in internal/git
//
// `internal/git` is read-only and says so, and that claim is about the open
// collection: nothing Otis does to the repository you are working in happens
// outside `internal/diff`. A clone is not a write to that repository — it is
// how a checkout comes to exist at all — but folding it into `internal/git`
// would cost the sentence that makes the rest of it easy to trust.
package gitclone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// ErrNoGit means there is no `git` on PATH.
var ErrNoGit = errors.New("git is not installed, or not on PATH")

// ErrNeedsCredentials means git stopped because it wanted to ask for
// something. Otis cannot answer, by design, so this is where the flow ends.
var ErrNeedsCredentials = errors.New(
	"this repository needs credentials Otis will not ask you for. " +
		"Clone it in a terminal, then use Open folder")

// Available reports whether cloning is possible at all.
func Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return ErrNoGit
	}
	return nil
}

// NameFor is the directory name a URL clones into: git's own rule, which is
// the last path segment with any `.git` suffix removed.
//
// It exists so the window can *show* the destination before anything runs.
// Go still decides what is written — the caller passes an explicit
// destination — but a dialog that could not name the folder it was about to
// create would be asking for a blind yes.
func NameFor(url string) string {
	trimmed := strings.TrimSpace(url)
	trimmed = strings.TrimRight(trimmed, "/")
	// scp-style: git@host:org/repo.git
	if at := strings.LastIndex(trimmed, ":"); at >= 0 && !strings.Contains(trimmed[at:], "//") {
		trimmed = trimmed[at+1:]
	}
	base := path.Base(trimmed)
	base = strings.TrimSuffix(base, ".git")
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// CheckURL rejects a URL Otis will not hand to git.
//
// Two of git's transports run a command the URL names: `ext::` runs it
// directly, and a local path can carry `--upload-pack`. A URL is usually
// typed, but it is also the sort of thing that gets pasted out of a chat
// window, and "paste this clone URL" is a plausible way to get somebody to
// run something. The `--` before the arguments below handles a URL that
// starts with a dash; this handles the rest.
func CheckURL(url string) error {
	trimmed := strings.TrimSpace(url)
	if trimmed == "" {
		return errors.New("enter a repository URL")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "ext::") {
		return errors.New("ext:: URLs run a command of their own and are not allowed here")
	}
	if strings.HasPrefix(trimmed, "-") {
		return errors.New("that is not a repository URL")
	}
	return nil
}

// Clone clones url into dest, which must not exist.
//
// progress is called with each line git writes to stderr — its counters
// arrive `\r`-separated rather than as lines, so they are split on both. It
// may be nil.
//
// Cancelling ctx kills git and removes the partial checkout: a half-clone is
// not something to leave behind under a name the user is about to try again.
func Clone(ctx context.Context, url, dest string, progress func(string)) error {
	if err := Available(); err != nil {
		return err
	}
	if err := CheckURL(url); err != nil {
		return err
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--progress", "--", url, dest)
	cmd.Env = append(os.Environ(),
		// No terminal to prompt on, so do not try.
		"GIT_TERMINAL_PROMPT=0",
		// The same for ssh: fail rather than wait for a passphrase nobody
		// can see. A key already in the agent still works, which is the
		// common case.
		"GIT_SSH_COMMAND=ssh -oBatchMode=yes",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running git clone: %w", err)
	}

	// Two lines are remembered for the error message and no more: the last
	// one git wrote, and the last one that announced a failure. git's stderr
	// is mostly counters, so keeping all of it would be keeping noise — and
	// the diagnosis is nearly always in a line starting `fatal:`, which is
	// not always the last one.
	var last, fatal string
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanLinesOrReturns)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}
		last = line
		if l := strings.ToLower(line); strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			fatal = line
		}
		if progress != nil {
			progress(line)
		}
	}
	// A read error on the pipe is not reported separately: git exiting is
	// what closes it, and cmd.Wait below is the authority on how that went.
	_ = scanner.Err()

	waitErr := cmd.Wait()
	if waitErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		os.RemoveAll(dest)
		return ctx.Err()
	}
	os.RemoveAll(dest)
	said := fatal
	if said == "" {
		said = last
	}
	if looksLikeAuth(said) || looksLikeAuth(last) {
		return fmt.Errorf("%w (git said: %s)", ErrNeedsCredentials, said)
	}
	if said != "" {
		return fmt.Errorf("git clone failed: %s", said)
	}
	return fmt.Errorf("git clone failed: %w", waitErr)
}

// looksLikeAuth reports whether git's last words were about credentials.
//
// It is a message match, which is fragile, and it is only used to *improve* an
// error that is reported either way — the clone has already failed and the
// user is already being told. Getting it wrong costs a less helpful sentence,
// not a wrong outcome.
func looksLikeAuth(line string) bool {
	l := strings.ToLower(line)
	for _, needle := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"terminal prompts disabled",
		"permission denied (publickey",
		"host key verification failed",
		"access denied",
	} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// scanLinesOrReturns splits on "\n" and on "\r", because git's progress
// counters overwrite one line with carriage returns and would otherwise
// arrive as a single enormous token at the end.
func scanLinesOrReturns(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
