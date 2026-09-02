package response

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// bigJSON builds a minified JSON body of roughly the requested size: a long
// array of records, which is what a large API response actually looks like.
func bigJSON(target int) []byte {
	var b strings.Builder
	b.Grow(target + 1024)
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < target; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"ord_%08d","sku":"SKU-%04d","qty":%d,"amount":%d.%02d,`+
			`"customer":{"id":"cus_%08d","email":"user%d@example.com"},`+
			`"tags":["a","b"],"captured":%t,"note":null}`,
			i, i%9999, i%7+1, i*13%9999, i%100, i, i, i%2 == 0)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// The increment's requirement: a 40 MB JSON response must pretty-print without
// blocking the UI. It cannot block the UI at all from here — this runs in Go —
// so what is measured is whether it is fast enough to be worth doing eagerly.
func TestFortyMegabyteTimings(t *testing.T) {
	body := bigJSON(40 << 20)
	t.Logf("body: %.1f MB", float64(len(body))/(1<<20))

	start := time.Now()
	d := New(body, "application/json")
	classify := time.Since(start)

	start = time.Now()
	pretty, err := d.View(Pretty)
	if err != nil {
		t.Fatal(err)
	}
	format := time.Since(start)

	start = time.Now()
	raw, err := d.View(Raw)
	if err != nil {
		t.Fatal(err)
	}
	rawIndex := time.Since(start)

	// A viewport's worth of lines, which is what a scroll step costs.
	start = time.Now()
	const viewport = 60
	from := pretty.Lines() / 2
	for i := from; i < from+viewport; i++ {
		_ = pretty.Line(i)
	}
	folds := pretty.Folds(from, from+viewport)
	page := time.Since(start)

	t.Logf("classify:        %v", classify.Round(time.Microsecond))
	t.Logf("pretty format:   %v  -> %d lines, %.1f MB, %d folds",
		format.Round(time.Millisecond), pretty.Lines(),
		float64(pretty.Bytes)/(1<<20), len(pretty.Folds(0, pretty.Lines())))
	t.Logf("raw index:       %v  -> %d lines", rawIndex.Round(time.Millisecond), raw.Lines())
	t.Logf("one viewport:    %v  (%d lines + %d folds)", page.Round(time.Microsecond), viewport, len(folds))

	// A scroll step has to be far under a frame, or scrolling stutters however
	// good the virtualizer is. This is the assertion that matters, and it is
	// the one that holds under load: fetching a viewport is two binary
	// searches and sixty slices, so it does not depend on the body's size.
	if page > 2*time.Millisecond {
		t.Errorf("fetching a viewport took %v, which is over a frame budget", page)
	}
	// Formatting has a deliberately loose bound. It is a smoke check that the
	// pass is still linear, not a performance target: `go test ./...` runs
	// packages in parallel, and a 40 MB format that takes 220 ms alone takes
	// well over a second while eight other packages are competing for the
	// machine. The number to look at is BenchmarkIndentJSON40MB.
	if format > 10*time.Second {
		t.Errorf("formatting 40 MB took %v, which is not linear any more", format)
	}
}

func BenchmarkIndentJSON40MB(b *testing.B) {
	body := bigJSON(40 << 20)
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if _, err := indentJSON(body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkViewportOf40MB(b *testing.B) {
	v, err := indentJSON(bigJSON(40 << 20))
	if err != nil {
		b.Fatal(err)
	}
	at := 0
	for b.Loop() {
		at = (at + 60) % (v.Lines() - 60)
		for i := at; i < at+60; i++ {
			_ = v.Line(i)
		}
		_ = v.Folds(at, at+60)
	}
}

// The index must not cost more than the body it indexes. A 40 MB response
// formats to nearly four million lines, and the naive index for that — two
// int64 slices — is larger than the response, held for as long as the tab is
// open.
func TestFootprintOfAFortyMegabyteResponse(t *testing.T) {
	body := bigJSON(40 << 20)
	d := New(body, "application/json")
	pretty, err := d.View(Pretty)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.View(Raw)
	if err != nil {
		t.Fatal(err)
	}
	mb := func(n int64) float64 { return float64(n) / (1 << 20) }
	t.Logf("body:        %6.1f MB", mb(d.Size()))
	t.Logf("pretty view: %6.1f MB (%.1f MB text + %.1f MB index)",
		mb(pretty.Footprint()), mb(pretty.Bytes), mb(pretty.Footprint()-pretty.Bytes))
	t.Logf("raw view:    %6.1f MB", mb(raw.Footprint()))
	t.Logf("total:       %6.1f MB for one response", mb(d.Footprint()))

	// The index for the formatted view — everything but its text — has to
	// stay under the size of the body itself.
	index := pretty.Footprint() - pretty.Bytes
	if index > d.Size() {
		t.Errorf("the line index is %.1f MB for a %.1f MB body", mb(index), mb(d.Size()))
	}
}
