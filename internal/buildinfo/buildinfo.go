// Package buildinfo is the build-time identity of the binary: which version
// it is, which commit it was built from, and when.
//
// Otis has no auto-updater (a deliberate deferral — see docs/RELEASING.md),
// so "which version am I on" is a question the user has to be able to answer
// by hand, from both halves of the binary: `otis --version` on the command
// line and the version Otis shows in the window. Both read this package, so
// they cannot disagree.
//
// The three variables are set by the linker. They live here rather than in
// internal/services because cmd/otis must not import that package — it pulls
// in Wails, and the CLI has to keep building with CGO_ENABLED=0.
//
//	go build -ldflags "
//	  -X github.com/otis-http/otis/internal/buildinfo.Version=v0.2.0
//	  -X github.com/otis-http/otis/internal/buildinfo.Commit=1a2b3c4
//	  -X github.com/otis-http/otis/internal/buildinfo.Date=2026-09-03T10:04:00Z"
//
// The Taskfiles pass all three (see VERSION_LDFLAGS in Taskfile.yml). A
// `go install`ed or `go build`ed binary gets none of them and reports "dev",
// which is the honest answer: nothing stamped it.
package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
)

// Version is the release version, from `git describe --tags --always --dirty`.
// It carries a leading "v" when it came from a tag, and a "-dirty" suffix when
// the tree had uncommitted changes.
var Version = "dev"

// Commit is the abbreviated commit the binary was built from.
var Commit = ""

// Date is the build time, RFC 3339 in UTC.
var Date = ""

// Info is the whole build identity, as one value. It crosses the Wails
// binding so the window can show it (AppService.Build).
type Info struct {
	// Version is the release version, e.g. "v0.2.0" or "dev".
	Version string `json:"version"`
	// Commit is the abbreviated commit, empty in an unstamped build.
	Commit string `json:"commit"`
	// Date is the build time, RFC 3339 in UTC, empty in an unstamped build.
	Date string `json:"date"`
	// Go is the toolchain that built it, e.g. "go1.25.5".
	Go string `json:"go"`
	// Platform is the target, e.g. "darwin/arm64".
	Platform string `json:"platform"`
}

// Get returns the build identity.
func Get() Info {
	return Info{
		Version:  Version,
		Commit:   Commit,
		Date:     Date,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// Line is the one-line form: `otis v0.2.0 (1a2b3c4, 2026-09-03)`.
//
// It is what the window shows and what "Copy version" puts on the clipboard,
// so it has to be short enough for a footer and complete enough to paste into
// a bug report. The parts that were never stamped are left out rather than
// printed empty.
func Line() string {
	s := "otis " + Version
	var parts []string
	if Commit != "" {
		parts = append(parts, Commit)
	}
	if day := day(Date); day != "" {
		parts = append(parts, day)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	return s
}

// Block is the multi-line form `otis --version` prints. Every field is named,
// including the ones that are empty, because "commit unknown" is information:
// it says the binary was not built by the release pipeline.
func Block() string {
	i := Get()
	var b strings.Builder
	fmt.Fprintf(&b, "otis %s\n", i.Version)
	fmt.Fprintf(&b, "commit    %s\n", orUnknown(i.Commit))
	fmt.Fprintf(&b, "built     %s\n", orUnknown(i.Date))
	fmt.Fprintf(&b, "go        %s\n", i.Go)
	fmt.Fprintf(&b, "platform  %s\n", i.Platform)
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// day is the date half of an RFC 3339 timestamp. It is a shape check rather
// than a time.Parse because Date is whatever the linker was given: a value
// that does not look like a date is dropped, not an error.
func day(s string) string {
	const layout = "0000-00-00"
	if len(s) < len(layout) {
		return ""
	}
	d := s[:len(layout)]
	for i := range layout {
		switch layout[i] {
		case '-':
			if d[i] != '-' {
				return ""
			}
		default:
			if d[i] < '0' || d[i] > '9' {
				return ""
			}
		}
	}
	return d
}
