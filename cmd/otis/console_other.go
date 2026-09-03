//go:build !windows

package cli

import (
	"io"
	"os"
)

// Console returns the streams the CLI writes to.
//
// Everywhere but Windows that is simply the process's own stdout and stderr:
// the binary is an ordinary executable, the shell waits for it, and redirects
// and pipes work as they always do. console_windows.go has the interesting
// version and the explanation.
func Console() (stdout, stderr io.Writer) {
	return os.Stdout, os.Stderr
}
