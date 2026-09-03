package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// WindowPath decides which half of the binary an invocation belongs to.
//
// Otis is one binary: with no arguments it opens the window, with arguments it
// runs a command. A file association breaks that rule, because Windows and
// Linux hand a double-clicked file to the app as argv[1] — so `otis
// orders/create-order.http` arrives looking exactly like a command line and
// would otherwise be answered with cobra's "unknown command". (macOS does not
// use argv for this; it goes through the NSApplication delegate, which Wails
// surfaces as events.Common.ApplicationOpenedWithFile.)
//
// The rule: one argument, not a flag, not the name of a command, and it
// names something that exists on disk. Then it is a path to open in the
// window and WindowPath returns it, absolute. Anything else is a command line.
//
// Each clause is load-bearing:
//
//   - One argument, because that is what a file-association launch passes.
//     Two or more is somebody typing.
//   - Not a flag, so `--version` and `--help` stay with the CLI.
//   - Not a command name, so a directory that happens to be called `run`
//     sitting in the working directory cannot shadow `otis run`.
//   - It exists, so a typo (`otis lss`) still reaches cobra and gets the
//     "unknown command" it deserves rather than silently opening a window.
//
// The side effect is that `otis .` and `otis ~/code/acme-api/.requests` open
// the window on a collection, which is what an editor does and is documented
// as such (docs/FORMAT.md §8).
func WindowPath(args []string) (string, bool) {
	return WindowPathIn("", args)
}

// WindowPathIn is WindowPath with an explicit working directory to resolve a
// relative argument against.
//
// It exists for the single-instance handler. A second launch forwards its
// command line to the running instance, and `otis ./create-order.http` typed
// in some other terminal means the file beside *that* terminal — but the
// running instance's own working directory is wherever it happened to be
// started, which for an app launched from Finder is "/". Resolving with the
// wrong base does not fail loudly either: the path simply does not exist, the
// rule below decides this was not a path at all, and the second launch
// silently just focuses the window instead of opening what it was given.
//
// dir must therefore be the *sending* process's working directory, and the
// join has to happen before the existence check rather than after it.
// An empty dir means the current process's own directory, which is what the
// command line wants.
func WindowPathIn(dir string, args []string) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	arg := args[0]
	if arg == "" || strings.HasPrefix(arg, "-") {
		return "", false
	}
	if isCommandName(arg) {
		return "", false
	}
	if dir != "" && !filepath.IsAbs(arg) {
		arg = filepath.Join(dir, arg)
	}
	if _, err := os.Stat(arg); err != nil {
		return "", false
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", false
	}
	return abs, true
}

// isCommandName reports whether s names a command or one of its aliases.
//
// It asks the command tree rather than consulting a list, so a command added
// later cannot be shadowed by a file of the same name without anyone noticing.
// "help" is included by hand: cobra synthesises it and it is not in Commands().
func isCommandName(s string) bool {
	if s == "help" {
		return true
	}
	for _, c := range newRootCmd().Commands() {
		if c.Name() == s {
			return true
		}
		for _, alias := range c.Aliases {
			if alias == s {
				return true
			}
		}
	}
	return false
}
