//go:build otis_cli

package main

import (
	"fmt"
	"os"
)

// The `otis_cli` build tag leaves the desktop app out of the binary.
//
// It exists so that the command line can be installed with nothing but a Go
// toolchain:
//
//	go install -tags otis_cli github.com/otis-http/otis@latest
//
// Without the tag the build needs the frontend bundle — which is generated,
// so a module fetched from the proxy does not carry it — and, on macOS and
// Linux, cgo plus a platform toolkit: Cocoa, or GTK4 and WebKitGTK 6.0
// development headers. None of that is anything a person who wants `otis run`
// in a CI job should have to install, and on a Linux runner it is not
// available at all.
//
// What is left out is exactly gui.go. Every command, the parser, the resolver,
// the sender, the script engine and the keychain are in the build as usual,
// because none of them depends on Wails — which is the point of the layering
// CLAUDE.md describes, and this tag is the thing that proves it holds.
func runGUI(string) {
	fmt.Fprintln(os.Stderr, "otis: this build is the command line only (built with -tags otis_cli).")
	fmt.Fprintln(os.Stderr, "      Run `otis --help` for the commands, or install the desktop app:")
	fmt.Fprintln(os.Stderr, "      https://github.com/otis-http/otis/releases")
	os.Exit(2)
}
