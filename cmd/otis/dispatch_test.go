package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The rule main.go dispatches on. Every clause of it has a case here,
// because getting it wrong costs either the file association (a .http file
// answered with "unknown command") or the CLI (`otis run` opening a window).
func TestWindowPath(t *testing.T) {
	dir := t.TempDir()
	request := filepath.Join(dir, "create-order.http")
	if err := os.WriteFile(request, []byte("GET https://x.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory named after a command, in the working directory, must not
	// shadow the command.
	if err := os.Mkdir(filepath.Join(dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	opens := []struct {
		name string
		args []string
	}{
		{"a request file, as a file association passes it", []string{request}},
		{"a request file by relative path", []string{"create-order.http"}},
		{"the working directory", []string{"."}},
		{"a collection directory", []string{dir}},
	}
	for _, tc := range opens {
		got, ok := WindowPath(tc.args)
		if !ok {
			t.Errorf("%s: WindowPath(%q) = _, false; want a path", tc.name, tc.args)
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("%s: WindowPath(%q) = %q, want an absolute path", tc.name, tc.args, got)
		}
	}

	commands := []struct {
		name string
		args []string
	}{
		{"no arguments at all", nil},
		{"a command", []string{"ls"}},
		{"a command shadowed by a directory of the same name", []string{"run"}},
		{"the synthesised help command", []string{"help"}},
		{"a version flag", []string{"--version"}},
		{"a short flag", []string{"-h"}},
		{"a mistyped command", []string{"lss"}},
		{"a path that does not exist", []string{"nope.http"}},
		{"a command with its argument", []string{"ls", dir}},
		{"an empty argument", []string{""}},
	}
	for _, tc := range commands {
		if got, ok := WindowPath(tc.args); ok {
			t.Errorf("%s: WindowPath(%q) = %q, true; want the CLI", tc.name, tc.args, got)
		}
	}
}

// A second launch forwards its command line *and* its working directory, and
// a relative path in it means the file beside that terminal — not beside the
// running instance, whose own directory is wherever it happened to be started.
// Resolving with the wrong base fails silently: the path does not exist, so
// the rule decides it was never a path, and the file is dropped.
func TestWindowPathInResolvesAgainstTheSendersDirectory(t *testing.T) {
	sender := t.TempDir()
	request := filepath.Join(sender, "create-order.http")
	if err := os.WriteFile(request, []byte("GET https://x.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The running instance sits somewhere else entirely, as it does when it
	// was launched from Finder.
	chdir(t, t.TempDir())

	got, ok := WindowPathIn(sender, []string{"create-order.http"})
	if !ok {
		t.Fatal("WindowPathIn did not resolve a relative path against the sender's directory")
	}
	if got != request {
		t.Errorf("WindowPathIn = %q, want %q", got, request)
	}

	// The same argument without the sender's directory is not a path at all,
	// which is the bug this function exists to avoid.
	if _, ok := WindowPath([]string{"create-order.http"}); ok {
		t.Error("WindowPath resolved a path relative to the wrong directory")
	}

	// An absolute argument ignores the sender's directory.
	if got, ok := WindowPathIn(sender, []string{request}); !ok || got != request {
		t.Errorf("WindowPathIn(abs) = %q, %v; want %q, true", got, ok, request)
	}

	// A command name is still a command, whichever directory it came from.
	if _, ok := WindowPathIn(sender, []string{"ls"}); ok {
		t.Error("WindowPathIn treated a command name as a path")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
