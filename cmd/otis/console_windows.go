//go:build windows

package cli

import (
	"io"
	"os"
	"syscall"
)

// Console returns the streams the CLI writes to, attaching to the console the
// process was started from if it has one.
//
// The packaged otis.exe is linked with -H windowsgui so that double-clicking
// it — or a .http file associated with it — opens the window without a console
// flashing up behind it. The cost is that such a binary starts with no
// standard handles at all, so `otis.exe ls` would print into nothing.
// AttachConsole(ATTACH_PARENT_PROCESS) borrows the parent shell's console and
// gives the output somewhere to go.
//
// The handles the process was *given* win when it has them, which is what
// keeps `otis ls > requests.txt` and `otis ls | more` working: cmd.exe passes
// a redirected or piped handle in STARTUPINFO even to a GUI-subsystem child,
// and writing to CONOUT$ instead would put the output on the screen and leave
// the file empty. CONOUT$ is the fallback for the plain interactive case,
// where there is a console but no handle pointing at it.
//
// What this cannot fix: a GUI-subsystem process is not waited for. cmd.exe
// returns to the prompt immediately and never sees the exit code, so `otis
// run` cannot gate a Windows CI step. That is why the release ships a
// console-subsystem build for command-line use — see docs/BUILDING.md, "The
// Windows CLI".
func Console() (stdout, stderr io.Writer) {
	attachParentConsole()
	return os.Stdout, os.Stderr
}

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
)

// attachParentProcess is ATTACH_PARENT_PROCESS, (DWORD)-1.
const attachParentProcess = ^uintptr(0)

// attachParentConsole attaches to the parent's console and points os.Stdout
// and os.Stderr at it, unless they already point somewhere valid.
//
// Every failure is silent and leaves the streams as they were. There is
// nowhere to report it to — the reason we are here is that there is no
// console — and a binary that refused to run because it could not find one
// would be worse than one that prints nothing.
func attachParentConsole() {
	// A process launched with a console already has one; AttachConsole then
	// fails with ERROR_ACCESS_DENIED, which is not a problem.
	ret, _, _ := procAttachConsole.Call(attachParentProcess)
	attached := ret != 0

	os.Stdout = consoleStream(os.Stdout, syscall.STD_OUTPUT_HANDLE, attached)
	os.Stderr = consoleStream(os.Stderr, syscall.STD_ERROR_HANDLE, attached)
}

// consoleStream picks the file the CLI should write to for one stream: the
// handle the process was given if it has one, else the attached console.
func consoleStream(current *os.File, stdHandle int, attached bool) *os.File {
	if h, err := syscall.GetStdHandle(stdHandle); err == nil && h != 0 && h != syscall.InvalidHandle {
		// Redirected, piped, or a console handle we already hold. Whatever it
		// is, it is what the shell asked for.
		return current
	}
	if !attached {
		return current
	}
	// CONOUT$ is the current console's active screen buffer, and is the same
	// device for stdout and stderr — as it is for any console program whose
	// streams the user did not redirect.
	f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return current
	}
	return f
}
