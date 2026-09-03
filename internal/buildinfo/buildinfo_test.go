package buildinfo

import (
	"strings"
	"testing"
)

// An unstamped build must still say something true rather than printing
// empty fields: `go install` produces exactly that binary.
func TestUnstampedBuildNamesWhatItDoesNotKnow(t *testing.T) {
	restore(t, "dev", "", "")

	if got := Line(); got != "otis dev" {
		t.Errorf("Line() = %q, want %q", got, "otis dev")
	}
	block := Block()
	for _, want := range []string{"otis dev", "commit    unknown", "built     unknown"} {
		if !strings.Contains(block, want) {
			t.Errorf("Block() = %q, missing %q", block, want)
		}
	}
	// The toolchain and target are always known, stamped or not.
	if !strings.Contains(block, "go        go1.") {
		t.Errorf("Block() = %q, missing the Go version", block)
	}
}

func TestStampedBuildReportsEveryPart(t *testing.T) {
	restore(t, "v0.2.0", "1a2b3c4", "2026-09-03T10:04:00Z")

	if got, want := Line(), "otis v0.2.0 (1a2b3c4, 2026-09-03)"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
	if got := Get(); got.Version != "v0.2.0" || got.Commit != "1a2b3c4" || got.Date != "2026-09-03T10:04:00Z" {
		t.Errorf("Get() = %+v", got)
	}
	if !strings.Contains(Block(), "built     2026-09-03T10:04:00Z") {
		t.Errorf("Block() = %q", Block())
	}
}

// Date is whatever the linker was handed. A value that is not a timestamp is
// dropped from the one-line form rather than printed as a broken date.
func TestALinkerDateThatIsNotATimestampIsDroppedFromTheLine(t *testing.T) {
	for _, date := range []string{"unknown", "2026", "", "xxxx-xx-xx"} {
		restore(t, "v1.0.0", "abc", date)
		if got, want := Line(), "otis v1.0.0 (abc)"; got != want {
			t.Errorf("Date=%q: Line() = %q, want %q", date, got, want)
		}
	}
}

func restore(t *testing.T, version, commit, date string) {
	t.Helper()
	oldV, oldC, oldD := Version, Commit, Date
	Version, Commit, Date = version, commit, date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })
}
