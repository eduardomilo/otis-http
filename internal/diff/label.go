package diff

import (
	"path"
	"sort"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
)

// Labels for the parts of a request file. They are what a hunk header says
// instead of "@@ -12,7 +12,9 @@".
const (
	LabelComments = "comments"
	LabelVars     = "variables"
	LabelHeaders  = "headers"
	LabelPreHook  = "pre-request script"
	// LabelTests is what the design calls a post-response script block
	// (screen 1b draws "@@ tests @@"), and from Increment 15 it is literally
	// where test(name, fn) lives.
	LabelTests    = "tests"
	LabelRedirect = "response redirect"
	LabelBody     = "body"
	LabelSettings = "folder settings"
)

// anchor marks the line at which a labelled region begins.
type anchor struct {
	line  int
	label string
}

// Label returns the semantic header for a hunk in the file at path, or "" when
// nothing better than the raw offsets can be said.
//
// The design asks for the request line above a hunk in the body and a tests
// marker above one in a script block, rather than "@@ -12,7 +12,9 @@" — the
// point the whole product is arguing is that a `.http` file is ordinary text
// somebody can review, and "@@ tests @@" is what makes a diff of one readable
// at a glance.
//
// content is the *new* side of the file, which is what the line numbers in a
// hunk header describe. A file that does not parse gets no labels: its line
// map cannot be trusted, and a confidently wrong header is worse than an
// honest offset.
func Label(filePath, content string, h Hunk) string {
	anchors := anchorsOf(filePath, content)
	if len(anchors) == 0 {
		return ""
	}
	line := firstChanged(h)
	if line == 0 {
		return ""
	}
	// The label of the nearest region beginning at or before the change.
	// Carrying the previous label forward is what covers the lines a parser
	// records only the start of — the rest of a body, the inside of a script
	// block — without this having to recompute either extent.
	label := ""
	for _, a := range anchors {
		if a.line > line {
			break
		}
		label = a.label
	}
	return label
}

// firstChanged is the new-side line number of the hunk's first added line, or
// the old-side number of its first removed line when the hunk only deletes.
//
// The new side is what the labels were built from. A pure deletion has no new
// line to point at, and the old number is the closest honest answer: the label
// then names the region the lines were taken *out of*.
func firstChanged(h Hunk) int {
	for _, l := range h.Lines {
		if l.Kind == Added {
			return l.New
		}
	}
	for _, l := range h.Lines {
		if l.Kind == Removed {
			return l.Old
		}
	}
	return 0
}

// anchorsOf builds the line-to-label map for a file, or nil for a file whose
// format Otis has nothing to say about — a README, an environment JSON, an
// `.order` list. Those keep the raw offsets.
func anchorsOf(filePath, content string) []anchor {
	if !strings.HasSuffix(filePath, collection.RequestExt) {
		return nil
	}
	file, err := httpfile.ParseString(content)
	if err != nil || file == nil {
		return nil
	}

	settings := path.Base(filePath) == collection.FolderFileName
	var anchors []anchor
	add := func(line int, label string) {
		if line > 0 && label != "" {
			anchors = append(anchors, anchor{line, label})
		}
	}

	for _, entry := range file.Requests {
		// The separator names the entry, so a file with several requests says
		// which one a hunk is in before it says which part.
		if entry.SeparatorLine > 0 {
			add(entry.SeparatorLine, entryLabel(entry, settings))
		}
		for _, c := range entry.Comments {
			add(c.Line, LabelComments)
		}
		for _, v := range entry.Variables {
			add(v.Line, LabelVars)
		}
		for _, d := range entry.Directives {
			add(d.Line, "@"+d.Name)
		}
		for _, s := range entry.PreScripts {
			add(s.Line, LabelPreHook)
		}
		// The request line labels itself and, per the design, the body below
		// it: "the request line for the body hunk".
		requestLine := requestLabel(entry)
		add(entry.Line, requestLine)
		if len(entry.Headers) > 0 {
			add(entry.Headers[0].Line, LabelHeaders)
		}
		body := requestLine
		if body == "" {
			body = LabelBody
		}
		add(entry.Body.Line, body)
		for _, s := range entry.PostScripts {
			add(s.Line, LabelTests)
		}
		if entry.Redirect != nil {
			add(entry.Redirect.Line, LabelRedirect)
		}
	}

	sort.SliceStable(anchors, func(i, j int) bool { return anchors[i].line < anchors[j].line })
	return anchors
}

// entryLabel names an entry: its @name or "###" title, else what kind of
// entry it is.
func entryLabel(entry *httpfile.Request, settings bool) string {
	if name := entry.Name(); name != "" {
		return name
	}
	if settings {
		return LabelSettings
	}
	if line := requestLabel(entry); line != "" {
		return line
	}
	return LabelSettings
}

// requestLabel is the request line as written, which is what the design puts
// above a body hunk. An entry with no request line — the shape `_folder.http`
// uses (docs/FORMAT.md §1.9) — has none.
func requestLabel(entry *httpfile.Request) string {
	if !entry.HasRequestLine() {
		return ""
	}
	label := entry.Method + " " + entry.URL
	if entry.Version != "" {
		label += " " + entry.Version
	}
	return label
}
