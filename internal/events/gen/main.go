// Command gen writes the TypeScript mirror of the Go event-name registry.
// Run it with `go generate ./internal/events`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/otis-http/otis/internal/events"
)

func main() {
	// go:generate runs with the package directory as the working directory.
	out := filepath.Join("..", "..", "frontend", "src", "lib", "events.gen.ts")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(out, events.TypeScript(), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "events/gen:", err)
	os.Exit(1)
}
