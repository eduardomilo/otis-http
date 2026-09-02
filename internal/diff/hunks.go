package diff

import (
	"sort"
	"strings"

	gitdiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ContextLines is how many unchanged lines are shown either side of a change,
// and therefore how far apart two changes must be to become separate hunks.
// Three is what git uses and what the design draws.
const ContextLines = 3

// LineKind is what a diff line is.
type LineKind string

const (
	// Context is a line both sides share.
	Context LineKind = " "
	// Added is a line only the new side has.
	Added LineKind = "+"
	// Removed is a line only the old side has.
	Removed LineKind = "-"
)

// Line is one line of a hunk.
//
// It carries both line numbers because the design draws both gutters, in
// unified *and* split view (DESIGN-NOTES §4.6: two 44px gutters). The number
// that does not apply is zero, which is what lets the split view leave a cell
// blank rather than guess.
type Line struct {
	Kind LineKind `json:"kind"`
	Text string   `json:"text"`
	// Old is the 1-based line number on the old side, 0 for an added line.
	Old int `json:"old"`
	// New is the 1-based line number on the new side, 0 for a removed line.
	New int `json:"new"`
}

// Hunk is a run of changes with its surrounding context.
type Hunk struct {
	OldStart int `json:"oldStart"`
	OldLines int `json:"oldLines"`
	NewStart int `json:"newStart"`
	NewLines int `json:"newLines"`
	// Label is the semantic header, when one can be derived: the request
	// line for a hunk in the body, "tests" for one in a post-response
	// script (see label.go). Empty means the view falls back to the raw
	// "@@ -a,b +c,d @@" offsets.
	Label string `json:"label,omitempty"`
	Lines []Line `json:"lines"`
	Adds  int    `json:"adds"`
	Dels  int    `json:"dels"`
	// Staged reports that this hunk is already in the index: it came from
	// the HEAD-to-index diff rather than the index-to-worktree one.
	Staged bool `json:"staged"`
}

// Hunks computes the hunks turning old into new.
//
// Both are whole file contents. A trailing newline is significant: a file that
// gains one has a changed last line, which is what git reports and what the
// "\ No newline at end of file" marker exists to explain. Rather than draw
// that marker, the newline is kept on the line's text so a reader sees the
// change as the line it is.
func Hunks(old, new string) []Hunk {
	if old == new {
		return nil
	}
	return group(lines(old, new))
}

// lines flattens a line-oriented diff into one entry per line, numbered on
// both sides.
func lines(old, new string) []Line {
	var out []Line
	oldNo, newNo := 0, 0
	for _, d := range gitdiff.Do(old, new) {
		for _, text := range split(d.Text) {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				oldNo++
				newNo++
				out = append(out, Line{Kind: Context, Text: text, Old: oldNo, New: newNo})
			case diffmatchpatch.DiffInsert:
				newNo++
				out = append(out, Line{Kind: Added, Text: text, New: newNo})
			case diffmatchpatch.DiffDelete:
				oldNo++
				out = append(out, Line{Kind: Removed, Text: text, Old: oldNo})
			}
		}
	}
	return out
}

// split breaks a chunk of text into its lines, dropping the terminator but
// remembering whether the final line had one.
//
// A chunk from the line differ is one or more whole lines and normally ends in
// "\n". A final chunk without one is the file's last line, unterminated; it
// still counts as a line, and "" is not a line at all.
func split(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	// A trailing "\n" leaves an empty final element that is not a line.
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// group collects changed lines into hunks, each padded with up to
// ContextLines of unchanged lines and merged with its neighbour when their
// context would overlap.
func group(all []Line) []Hunk {
	// Where the changes are.
	var changed []int
	for i, l := range all {
		if l.Kind != Context {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}

	// Spans of [first-context, last+context], merged where they touch.
	type span struct{ from, to int }
	var spans []span
	for _, i := range changed {
		from, to := max(0, i-ContextLines), min(len(all)-1, i+ContextLines)
		if n := len(spans); n > 0 && from <= spans[n-1].to+1 {
			spans[n-1].to = max(spans[n-1].to, to)
			continue
		}
		spans = append(spans, span{from, to})
	}

	hunks := make([]Hunk, 0, len(spans))
	for _, s := range spans {
		h := Hunk{Lines: all[s.from : s.to+1]}
		for _, l := range h.Lines {
			switch l.Kind {
			case Added:
				h.Adds++
				h.NewLines++
				if h.NewStart == 0 {
					h.NewStart = l.New
				}
			case Removed:
				h.Dels++
				h.OldLines++
				if h.OldStart == 0 {
					h.OldStart = l.Old
				}
			case Context:
				h.OldLines++
				h.NewLines++
				if h.OldStart == 0 {
					h.OldStart = l.Old
				}
				if h.NewStart == 0 {
					h.NewStart = l.New
				}
			}
		}
		hunks = append(hunks, h)
	}
	return hunks
}

// Apply returns text with the given hunks applied in the forward direction:
// removed lines dropped, added lines inserted. The hunks must come from a diff
// whose old side is text.
//
// This is what staging a hunk does — the selected hunks move onto the index's
// copy — and reversing it is what discarding does. It works on line numbers
// rather than by searching for context, because the hunks were computed from
// this exact text a moment ago; a fuzzy patcher would be the wrong tool and a
// worse failure mode.
func Apply(text string, hunks []Hunk) string {
	return patch(text, hunks, false)
}

// Reverse returns text with the given hunks undone: added lines dropped,
// removed lines restored. The hunks must come from a diff whose *new* side is
// text.
func Reverse(text string, hunks []Hunk) string {
	return patch(text, hunks, true)
}

// patch rebuilds text with hunks applied forward or backward.
//
// A hunk describes both sides of one region exactly: its lines with kind
// Context or Added are the new side, and its lines with kind Context or
// Removed are the old side. So applying it is a splice — replace the source
// region with the other side's lines — and reversing it is the same splice
// with the two sides swapped. Both directions are therefore one function, and
// a bug fixed in staging is fixed in discarding.
//
// The splice is by line number, not by searching for context: the hunks were
// computed from this exact text a moment ago, so a fuzzy patcher would be the
// wrong tool and a much worse failure mode.
func patch(text string, hunks []Hunk, reverse bool) string {
	src := split(text)

	// Order the regions so one walk covers them, and so two hunks handed over
	// in either order still splice correctly.
	ordered := make([]Hunk, len(hunks))
	copy(ordered, hunks)
	sort.SliceStable(ordered, func(i, j int) bool {
		return regionStart(ordered[i], reverse) < regionStart(ordered[j], reverse)
	})

	var out []string
	at := 1 // the next source line not yet copied, 1-based
	for _, h := range ordered {
		from, count := regionStart(h, reverse), regionLen(h, reverse)
		// A region before what has already been copied would mean two hunks
		// overlap, which a diff never produces. Skipping it is the safe
		// reading: better to leave the text alone than to corrupt it.
		if from < at {
			continue
		}
		for ; at < from && at <= len(src); at++ {
			out = append(out, src[at-1])
		}
		for _, l := range h.Lines {
			if survives(l.Kind, reverse) {
				out = append(out, l.Text)
			}
		}
		at = from + count
	}
	for ; at <= len(src); at++ {
		out = append(out, src[at-1])
	}

	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// regionStart is the first source line a hunk replaces. Zero — a hunk with no
// source side at all, which only happens for a file that was empty — means
// "insert before line 1".
func regionStart(h Hunk, reverse bool) int {
	start := h.OldStart
	if reverse {
		start = h.NewStart
	}
	if start == 0 {
		return 1
	}
	return start
}

// regionLen is how many source lines a hunk replaces.
func regionLen(h Hunk, reverse bool) int {
	if reverse {
		return h.NewLines
	}
	return h.OldLines
}

// survives reports whether a line of this kind belongs to the side being
// written: the new side going forward, the old side in reverse.
func survives(kind LineKind, reverse bool) bool {
	if kind == Context {
		return true
	}
	if reverse {
		return kind == Removed
	}
	return kind == Added
}
