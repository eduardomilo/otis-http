package gitclone

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// No test here touches the network. A clone from a local directory exercises
// the same code path — argument construction, the stderr split, the cleanup
// on failure — and is the only kind of clone a test is allowed to make.

func TestNameFor(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"git@github.com:org/api-requests.git", "api-requests"},
		{"git@github.com:org/api-requests", "api-requests"},
		{"https://github.com/org/api-requests.git", "api-requests"},
		{"https://github.com/org/api-requests/", "api-requests"},
		{"ssh://git@host:2222/org/repo.git", "repo"},
		{"/srv/git/repo.git", "repo"},
		{"", ""},
		{"   ", ""},
	} {
		if got := NameFor(tt.url); got != tt.want {
			t.Errorf("NameFor(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// ext:: runs a command the URL names. Nothing else in Otis lets a string
// decide what to execute, and a clone URL is exactly the sort of thing that
// arrives by paste.
func TestCheckURLRefusesATransportThatRunsACommand(t *testing.T) {
	for _, url := range []string{
		"ext::sh -c 'touch /tmp/pwned'",
		"EXT::sh -c whoami",
		"--upload-pack=touch /tmp/pwned",
		"",
	} {
		if err := CheckURL(url); err == nil {
			t.Errorf("CheckURL(%q) allowed it", url)
		}
	}
	for _, url := range []string{
		"git@github.com:org/repo.git",
		"https://github.com/org/repo.git",
		"file:///srv/git/repo.git",
	} {
		if err := CheckURL(url); err != nil {
			t.Errorf("CheckURL(%q) = %v", url, err)
		}
	}
}

func TestCloneCopiesARepositoryAndReportsProgress(t *testing.T) {
	source := makeRepo(t)
	dest := filepath.Join(t.TempDir(), "clone", "here")

	var lines []string
	if err := Clone(t.Context(), source, dest, func(line string) {
		lines = append(lines, line)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "example.http")); err != nil {
		t.Errorf("the clone has no content: %v", err)
	}
	if len(lines) == 0 {
		t.Error("no progress was reported")
	}
	// git announces itself first; the assertion is that stderr arrived at
	// all, not what it said.
	if !strings.Contains(strings.Join(lines, "\n"), "Cloning into") {
		t.Errorf("progress = %q", lines)
	}
}

func TestCloneRefusesAnExistingDestination(t *testing.T) {
	source := makeRepo(t)
	dest := t.TempDir()
	if err := Clone(t.Context(), source, dest, nil); err == nil {
		t.Fatal("cloned over an existing directory")
	}
}

// A failed clone must not leave a half-written directory behind, because the
// next thing the person does is try again with the same name — and the second
// attempt would then fail for a different reason than the first.
func TestAFailedCloneLeavesNothingBehind(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nope")
	err := Clone(t.Context(), filepath.Join(t.TempDir(), "not-a-repo"), dest, nil)
	if err == nil {
		t.Fatal("cloning a non-repository succeeded")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("%s survived a failed clone", dest)
	}
}

func TestCloneStopsWhenTheContextIsCancelled(t *testing.T) {
	source := makeRepo(t)
	dest := filepath.Join(t.TempDir(), "cancelled")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Clone(ctx, source, dest, nil)
	if err == nil {
		t.Fatal("a cancelled clone succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("%s survived a cancelled clone", dest)
	}
}

// makeRepo builds a one-commit repository in a temp directory and returns its
// path, skipping the test if git is not installed.
func makeRepo(t *testing.T) string {
	t.Helper()
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "example.http"), "GET https://example.test/\n")
	run(t, dir, "init", "-q")
	run(t, dir, "config", "user.email", "test@example.test")
	run(t, dir, "config", "user.name", "Test")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// A developer's own git config must not decide whether this passes.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
