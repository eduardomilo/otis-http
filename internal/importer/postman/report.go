package postman

import (
	"fmt"
	"sort"
	"strings"
)

// Report says what the import produced, what it skipped, and why. It is
// meant to be read like a PR description.
type Report struct {
	CollectionName string
	Requests       int
	Folders        int
	Environments   int
	// Files written, relative to the output directory, in write order.
	Files []string
	// Skipped lists things that did not make it into the output.
	Skipped []Entry
	// Warnings lists things that were imported but need attention.
	Warnings []Entry
	// Notes lists routine adjustments worth knowing about.
	Notes []Entry
}

// Entry is one report line about a path in the output (or "" for the
// collection as a whole).
type Entry struct {
	Path string
	Msg  string
}

func (e Entry) String() string {
	if e.Path == "" {
		return e.Msg
	}
	return e.Path + ": " + e.Msg
}

func (r *Report) skip(path, format string, args ...any) {
	r.Skipped = append(r.Skipped, Entry{path, fmt.Sprintf(format, args...)})
}

func (r *Report) warn(path, format string, args ...any) {
	r.Warnings = append(r.Warnings, Entry{path, fmt.Sprintf(format, args...)})
}

func (r *Report) note(path, format string, args ...any) {
	r.Notes = append(r.Notes, Entry{path, fmt.Sprintf(format, args...)})
}

// String renders the report as plain text.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported %q: %d requests, %d folders, %d environments, %d files\n",
		r.CollectionName, r.Requests, r.Folders, r.Environments, len(r.Files))
	section := func(title string, entries []Entry) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s (%d)\n", title, len(entries))
		for _, e := range entries {
			fmt.Fprintf(&b, "  - %s\n", e)
		}
	}
	section("Skipped", r.Skipped)
	section("Needs attention", r.Warnings)
	section("Notes", r.Notes)
	return b.String()
}

// sortedKeys returns m's keys sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
