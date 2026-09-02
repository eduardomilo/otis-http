package response

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// lines renders a view as a slice, for readable assertions.
func lines(v *View) []string {
	out := make([]string, v.Lines())
	for i := range out {
		out[i] = v.Line(i)
	}
	return out
}

func mustView(t *testing.T, body, contentType string, kind ViewKind) *View {
	t.Helper()
	v, err := New([]byte(body), contentType).View(kind)
	if err != nil {
		t.Fatalf("View(%s): %v", kind, err)
	}
	return v
}

func TestIndentJSON(t *testing.T) {
	body := `{"id":"ord_1","line_items":[{"sku":"A","qty":2},{"sku":"B"}],"meta":{},"tags":[],"ok":true,"n":-1.5e10,"nil":null}`
	v := mustView(t, body, "application/json", Pretty)

	want := []string{
		`{`,
		`  "id": "ord_1",`,
		`  "line_items": [`,
		`    {`,
		`      "sku": "A",`,
		`      "qty": 2`,
		`    },`,
		`    {`,
		`      "sku": "B"`,
		`    }`,
		`  ],`,
		`  "meta": {},`,
		`  "tags": [],`,
		`  "ok": true,`,
		`  "n": -1.5e10,`,
		`  "nil": null`,
		`}`,
	}
	if got := lines(v); !equal(got, want) {
		t.Errorf("pretty JSON =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Every fold spans lines and reports its direct children. The empty
	// containers are not folds: there is nothing to collapse.
	got := v.Folds(0, v.Lines())
	wantFolds := []Fold{
		{Line: 0, End: 16, Count: 7, Object: true},  // the document
		{Line: 2, End: 10, Count: 2, Object: false}, // line_items
		{Line: 3, End: 6, Count: 2, Object: true},   // its first object
		{Line: 7, End: 9, Count: 1, Object: true},   // its second
	}
	if len(got) != len(wantFolds) {
		t.Fatalf("folds = %+v, want %+v", got, wantFolds)
	}
	for i := range wantFolds {
		if got[i] != wantFolds[i] {
			t.Errorf("fold %d = %+v, want %+v", i, got[i], wantFolds[i])
		}
	}
}

// The formatter must not change any value. It copies literals through
// verbatim, so a big integer, a high-precision decimal and an escape sequence
// all survive — none of which is true of a decode-and-re-encode.
func TestIndentJSONPreservesLiteralsExactly(t *testing.T) {
	body := `{"big":12345678901234567890123,"exact":0.1000000000000000055511151231257827,` +
		`"esc":"a\"b\\cé\n","unicode":"héllo ✓"}`
	v := mustView(t, body, "application/json", Pretty)
	text := string(v.Text())
	for _, literal := range []string{
		"12345678901234567890123",
		"0.1000000000000000055511151231257827",
		`"a\"b\\cé\n"`,
		`"héllo ✓"`,
	} {
		if !strings.Contains(text, literal) {
			t.Errorf("the formatter did not keep %s verbatim:\n%s", literal, text)
		}
	}
	// And the result is still the same JSON document.
	var before, after any
	if err := json.Unmarshal([]byte(body), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(v.Text(), &after); err != nil {
		t.Fatalf("the formatted body no longer parses: %v", err)
	}
}

func TestIndentJSONRejectsBrokenInput(t *testing.T) {
	for _, body := range []string{
		`{"a":1`,
		`{"a":"unterminated`,
		`}`,
		`{"a":@}`,
	} {
		if _, err := New([]byte(body), "application/json").View(Pretty); err == nil {
			t.Errorf("%q: want an error, got a formatted view", body)
		}
	}
}

// A body the server called text/plain but which is plainly JSON still formats:
// refusing over a wrong header is pedantry the reader pays for.
func TestKindSniffsPastAMisleadingHeader(t *testing.T) {
	cases := []struct {
		contentType, body string
		want              Kind
	}{
		{"application/json", `{}`, KindJSON},
		{"application/vnd.acme+json", `{}`, KindJSON},
		{"application/json; charset=utf-8", `{}`, KindJSON},
		{"text/plain", `  {"a":1}`, KindJSON},
		{"", `[1]`, KindJSON},
		{"application/octet-stream", `<a/>`, KindXML},
		{"text/xml", `<a/>`, KindXML},
		{"text/plain", `hello`, KindText},
		{"text/csv", `a,b`, KindText},
		{"text/plain", ``, KindText},
	}
	for _, tc := range cases {
		if got := KindOf(tc.contentType, []byte(tc.body)); got != tc.want {
			t.Errorf("KindOf(%q, %q) = %q, want %q", tc.contentType, tc.body, got, tc.want)
		}
	}
}

func TestRawViewSplitsLinesWithoutChangingBytes(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"empty", "", []string{""}},
		{"one line", "hello", []string{"hello"}},
		{"trailing newline", "a\nb\nc\n", []string{"a", "b", "c"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
		{"blank lines kept", "a\n\nb", []string{"a", "", "b"}},
		{"crlf", "a\r\nb", []string{"a", "b"}},
	}
	for _, tc := range cases {
		v := mustView(t, tc.body, "text/plain", Raw)
		if got := lines(v); !equal(got, tc.want) {
			t.Errorf("%s: raw lines = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A minified body is one enormous physical line, and one DOM node of that size
// is not something a browser lays out. The raw view wraps it — and joining the
// pieces gives the bytes back.
func TestRawViewWrapsALongLineLosslessly(t *testing.T) {
	body := strings.Repeat("x", WrapWidth*2+7)
	v := mustView(t, body, "text/plain", Raw)
	if v.Lines() != 3 {
		t.Fatalf("lines = %d, want 3", v.Lines())
	}
	var joined strings.Builder
	for i := 0; i < v.Lines(); i++ {
		line := v.Line(i)
		if len(line) > WrapWidth {
			t.Errorf("line %d is %d bytes, over the %d wrap", i, len(line), WrapWidth)
		}
		joined.WriteString(line)
	}
	if joined.String() != body {
		t.Error("joining the wrapped lines does not reproduce the body")
	}
}

// The wrap must land on a rune boundary or the display shows two replacement
// glyphs where one character was.
func TestRawViewWrapsOnRuneBoundaries(t *testing.T) {
	// "é" is two bytes, so a run of them puts a boundary mid-rune at WrapWidth.
	body := strings.Repeat("é", WrapWidth)
	v := mustView(t, body, "text/plain", Raw)
	var joined strings.Builder
	for i := 0; i < v.Lines(); i++ {
		line := v.Line(i)
		if !utf8Valid(line) {
			t.Errorf("line %d is not valid UTF-8: %q", i, line)
		}
		joined.WriteString(line)
	}
	if joined.String() != body {
		t.Error("joining the wrapped lines does not reproduce the body")
	}
}

func TestNoPrettyViewForPlainText(t *testing.T) {
	d := New([]byte("just words"), "text/plain")
	if d.HasPretty() {
		t.Error("HasPretty is true for plain text")
	}
	if _, err := d.View(Pretty); err != ErrNoPrettyView {
		t.Errorf("View(Pretty) error = %v, want ErrNoPrettyView", err)
	}
	// The raw view is always there.
	if _, err := d.View(Raw); err != nil {
		t.Errorf("View(Raw): %v", err)
	}
}

func TestIndentXML(t *testing.T) {
	body := `<?xml version="1.0"?><order id="1"><item sku="A"/><note>hi</note></order>`
	v := mustView(t, body, "application/xml", Pretty)
	want := []string{
		`<?xml version="1.0"?>`,
		`<order id="1">`,
		`  <item sku="A"/>`,
		`  <note>`,
		`    hi`,
		`  </note>`,
		`</order>`,
	}
	if got := lines(v); !equal(got, want) {
		t.Errorf("pretty XML =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The view is built once however many callers ask at once.
func TestViewIsBuiltOnceAndCached(t *testing.T) {
	d := New([]byte(`{"a":[1,2,3]}`), "application/json")
	first, err := d.View(Pretty)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.View(Pretty)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("View(Pretty) built a second view instead of returning the cached one")
	}
}

func TestFoldsRangeQuery(t *testing.T) {
	// Ten sibling objects, so folds open on known lines.
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"i":%d}`, i)
	}
	b.WriteString("]")
	v := mustView(t, b.String(), "application/json", Pretty)

	all := v.Folds(0, v.Lines())
	if len(all) != 11 { // the array plus ten objects
		t.Fatalf("folds = %d, want 11", len(all))
	}
	// A window over the middle returns only the folds opening inside it.
	window := v.Folds(10, 20)
	for _, f := range window {
		if f.Line < 10 || f.Line >= 20 {
			t.Errorf("fold at line %d is outside the queried window", f.Line)
		}
	}
	if len(window) == 0 {
		t.Error("a window in the middle of the document returned no folds")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func utf8Valid(s string) bool { return utf8.ValidString(s) }
