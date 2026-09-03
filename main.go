// Command otis is both halves of Otis: the desktop app and the `otis`
// command line.
//
// Which one runs is decided here and nowhere else. The GUI half lives in
// gui.go, behind the `!otis_cli` build tag, because it needs Wails and
// therefore cgo and a platform toolkit; gui_disabled.go is the other side of
// that tag and explains when you would want it.
package main

import (
	"os"

	cli "github.com/otis-http/otis/cmd/otis"
)

func main() {
	// One binary, three ways in. A path the OS or the shell handed us opens
	// the window on it; any other argument is a command; none opens the
	// window empty. cli.WindowPath owns the rule and documents every clause —
	// it has to, because a file association arrives looking exactly like a
	// command line.
	if path, ok := cli.WindowPath(os.Args[1:]); ok {
		runGUI(path)
		return
	}
	if len(os.Args) > 1 {
		// cli.Console rather than os.Stdout/os.Stderr: the packaged Windows
		// binary is linked -H windowsgui and starts with no standard handles,
		// so the CLI has to borrow the console it was launched from. Every
		// other platform gets exactly os.Stdout and os.Stderr.
		stdout, stderr := cli.Console()
		os.Exit(cli.Execute(os.Args[1:], stdout, stderr))
	}
	runGUI("")
}
