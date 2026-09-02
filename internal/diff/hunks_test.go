package diff

import (
	"strings"
	"testing"
)

// render draws a hunk the way a unified view does, so a test can assert on
// what a reader would actually see.
func render(hunks []Hunk) string {
	var b strings.Builder
	for _, h := range hunks {
		b.WriteString("@@\n")
		for _, l := range h.Lines {
			b.WriteString(string(l.Kind) + l.Text + "\n")
		}
	}
	return b.String()
}

func TestHunksOfNoChange(t *testing.T) {
	if got := Hunks("a\nb\n", "a\nb\n"); got != nil {
		t.Errorf("Hunks = %v, want none", got)
	}
}

func TestHunksNumbersBothSides(t *testing.T) {
	old := "one\ntwo\nthree\n"
	new := "one\nTWO\nthree\n"
	hunks := Hunks(old, new)
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	h := hunks[0]
	if h.Adds != 1 || h.Dels != 1 {
		t.Errorf("+%d -%d, want +1 -1", h.Adds, h.Dels)
	}
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 3 {
		t.Errorf("hunk = @@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	}
	// A removed line has no new number and an added line has no old one,
	// which is what lets the split view leave a cell blank.
	for _, l := range h.Lines {
		switch l.Kind {
		case Added:
			if l.Old != 0 || l.New == 0 {
				t.Errorf("added line = %+v", l)
			}
		case Removed:
			if l.New != 0 || l.Old == 0 {
				t.Errorf("removed line = %+v", l)
			}
		case Context:
			if l.Old == 0 || l.New == 0 {
				t.Errorf("context line = %+v", l)
			}
		}
	}
}

// Two changes far apart are two hunks; two close together are one, because
// their context overlaps. This is what the footer's hunk count counts.
func TestHunksSplitOnDistanceAndMergeOnOverlap(t *testing.T) {
	var old, new []string
	for i := 1; i <= 40; i++ {
		old = append(old, "line "+string(rune('a'+i%26)))
		new = append(new, "line "+string(rune('a'+i%26)))
	}
	new[0] = "CHANGED first"
	new[39] = "CHANGED last"
	far := Hunks(strings.Join(old, "\n")+"\n", strings.Join(new, "\n")+"\n")
	if len(far) != 2 {
		t.Errorf("changes 39 lines apart = %d hunks, want 2", len(far))
	}

	near := append([]string(nil), old...)
	near[10] = "CHANGED"
	near[12] = "ALSO CHANGED"
	got := Hunks(strings.Join(old, "\n")+"\n", strings.Join(near, "\n")+"\n")
	if len(got) != 1 {
		t.Errorf("changes 2 lines apart = %d hunks, want 1", len(got))
	}
}

func TestHunksOfAWholeNewFile(t *testing.T) {
	hunks := Hunks("", "alpha\nbeta\n")
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	if hunks[0].Adds != 2 || hunks[0].Dels != 0 {
		t.Errorf("+%d -%d, want +2 -0", hunks[0].Adds, hunks[0].Dels)
	}
	want := "@@\n+alpha\n+beta\n"
	if got := render(hunks); got != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
}

// Apply and Reverse are the operations behind Stage and Discard, so the
// round trip is the property that matters: applying every hunk to the old
// side must reproduce the new side exactly, and reversing them from the new
// side must reproduce the old.
func TestApplyAndReverseRoundTrip(t *testing.T) {
	cases := []struct{ name, old, new string }{
		{"one line changed", "a\nb\nc\n", "a\nB\nc\n"},
		{"line added", "a\nc\n", "a\nb\nc\n"},
		{"line removed", "a\nb\nc\n", "a\nc\n"},
		{"prepended", "a\n", "z\na\n"},
		{"appended", "a\n", "a\nz\n"},
		{"emptied", "a\nb\n", ""},
		{"created", "", "a\nb\n"},
		{"replaced wholesale", "a\nb\nc\n", "x\ny\nz\n"},
		{"two distant changes", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n",
			"1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\nFOURTEEN\n15\n"},
		{"duplicate lines", "x\nx\nx\n", "x\ny\nx\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hunks := Hunks(c.old, c.new)
			if got := Apply(c.old, hunks); got != c.new {
				t.Errorf("Apply = %q, want %q", got, c.new)
			}
			if got := Reverse(c.new, hunks); got != c.old {
				t.Errorf("Reverse = %q, want %q", got, c.old)
			}
		})
	}
}

// Staging or discarding *one* hunk of several is the whole point of the
// per-hunk controls, so applying a subset must move only that hunk.
func TestApplyOneHunkOfSeveral(t *testing.T) {
	old := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n"
	new := "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\nFOURTEEN\n15\n"
	hunks := Hunks(old, new)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}

	// The first hunk only: line 2 changes, line 14 does not.
	firstOnly := Apply(old, hunks[:1])
	if want := "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n"; firstOnly != want {
		t.Errorf("first hunk applied = %q, want %q", firstOnly, want)
	}
	// The second hunk only.
	secondOnly := Apply(old, hunks[1:])
	if want := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\nFOURTEEN\n15\n"; secondOnly != want {
		t.Errorf("second hunk applied = %q, want %q", secondOnly, want)
	}
	// Discarding one hunk from the new side leaves the other in place.
	if got := Reverse(new, hunks[1:]); got != firstOnly {
		t.Errorf("Reverse of the second hunk = %q, want %q", got, firstOnly)
	}
	// Order handed over must not matter.
	if got := Apply(old, []Hunk{hunks[1], hunks[0]}); got != new {
		t.Errorf("out-of-order Apply = %q, want %q", got, new)
	}
}

// A file whose last line has no terminator is a real case (an editor that
// does not add one), and losing or inventing that newline would show up as a
// spurious one-line diff forever.
func TestHunksAndApplyWithNoTrailingNewline(t *testing.T) {
	hunks := Hunks("a\nb", "a\nB")
	if len(hunks) != 1 || hunks[0].Adds != 1 || hunks[0].Dels != 1 {
		t.Fatalf("hunks = %+v", hunks)
	}
	// Apply normalises to a terminated file, which is what Otis writes
	// (docs/FORMAT.md §1.1) — the point of the test is that the *content*
	// round-trips rather than the terminator.
	if got := Apply("a\nb", hunks); got != "a\nB\n" {
		t.Errorf("Apply = %q", got)
	}
}
