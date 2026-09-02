// Package response holds a response body in Go and serves it to the window a
// screenful at a time.
//
// The body never crosses the binding boundary whole. A 40 MB response
// marshalled through one call would be a 40 MB JSON string built in Go, parsed
// in the webview and handed to the DOM, and every one of those three steps
// blocks. Instead the bytes stay here, indexed by line, and the window asks
// for the lines it is about to draw.
//
// Formatting and indexing are this package's work, not the window's. That is
// the point: pretty-printing a large JSON body in JavaScript is what makes
// other clients stutter, and Go does it once, off the UI thread, in a single
// pass that also records where every collapsible node begins and ends.
package response

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// ViewKind is one way of rendering a body.
type ViewKind string

const (
	// Raw is the bytes as they arrived, split for display only.
	Raw ViewKind = "raw"
	// Pretty is re-indented. Only structured bodies have one.
	Pretty ViewKind = "pretty"
)

// WrapWidth is the width at which the raw view breaks a long physical line.
//
// A minified JSON body is one line, and a 40 MB line is not something a
// browser can lay out: one DOM node of that size janks whatever else is on
// screen. The raw view therefore breaks at this many bytes, on a rune
// boundary. It is a display wrap — no byte is added, removed or reordered, and
// joining the lines back yields the body exactly.
const WrapWidth = 512

// MaxPrettyBytes bounds what will be re-indented. Beyond it the raw view is
// the only one offered: indenting doubles the memory for a body already too
// large to be worth reading as a document rather than searching as text.
const MaxPrettyBytes = 64 << 20 // 64 MiB

// MaxIndexBytes bounds what will be indexed by line at all. A body past it is
// still held and can still be written to a file; it is simply not something to
// read in a pane, and the line index for it would be larger than most of the
// responses anyone will ever look at. It is also what keeps the int32 offsets
// in View honest.
const MaxIndexBytes = 512 << 20 // 512 MiB

// ErrTooLargeToIndex means the body is past MaxIndexBytes. Like
// ErrNoPrettyView it is a fact about the body rather than a failure: the size
// is still known, and so is the option of saving it.
var ErrTooLargeToIndex = errors.New("this body is too large to display")

// ErrNoPrettyView means the body has no formatted rendering: it is not a
// structured type Otis knows, it does not parse, or it is beyond
// MaxPrettyBytes. It is not a failure; the raw view is still there.
var ErrNoPrettyView = errors.New("this body has no formatted view")

// Kind is the body's structure, which decides whether a pretty view exists and
// how the window colours it.
type Kind string

const (
	KindJSON Kind = "json"
	KindXML  Kind = "xml"
	KindText Kind = "text"
)

// Fold is a collapsible node: the line that opens a container, the line that
// closes it, and how many direct children it has (screen 1a's "2 items" chip).
// Fields are int32 for the same reason View's offsets are: a large body has
// hundreds of thousands of folds, and 32 bytes each is more memory than the
// text they describe.
type Fold struct {
	// Line is the 0-based line the container opens on.
	Line int32 `json:"line"`
	// End is the 0-based line it closes on.
	End int32 `json:"end"`
	// Count is the number of direct children.
	Count int32 `json:"count"`
	// Object is true for "{", false for "[".
	Object bool `json:"object"`
}

// View is a body rendered one way and indexed by line.
//
// The index is deliberately narrow. A 40 MB body formats to nearly four
// million lines, and an index of two int64 slices over that is 60 MB — more
// than the body — held for as long as the response is. int32 offsets halve it,
// and a view whose lines are separated by exactly one byte does not store the
// ends at all. The offsets are int32 because MaxIndexBytes keeps every indexed
// body inside that range.
type View struct {
	Kind ViewKind
	// Bytes is the length of the rendered text.
	Bytes int64

	text []byte
	// starts[i] is the offset of line i, with a sentinel entry at the end
	// holding one past the last line's terminator.
	starts []int32
	// ends[i] is one past line i's last byte, excluding its terminator. It is
	// nil when every line is followed by exactly one separator byte, which is
	// the formatted views' case; the raw view needs it because a CRLF ending
	// and a display wrap both break that rule.
	ends []int32
	// folds is ordered by Line, so a range query is two binary searches.
	folds []Fold
}

// Lines is the number of display lines in the view.
func (v *View) Lines() int { return len(v.starts) - 1 }

// Line returns one line's text, or "" when i is out of range.
func (v *View) Line(i int) string {
	if i < 0 || i >= v.Lines() {
		return ""
	}
	start := v.starts[i]
	end := v.starts[i+1] - 1 // the separator byte
	if v.ends != nil {
		end = v.ends[i]
	}
	if end < start {
		end = start
	}
	return string(v.text[start:end])
}

// Footprint is roughly how much memory the view holds, for the eviction
// policy that decides how many responses are worth keeping.
func (v *View) Footprint() int64 {
	if v == nil {
		return 0
	}
	return int64(len(v.text)) +
		int64(cap(v.starts))*4 +
		int64(cap(v.ends))*4 +
		int64(cap(v.folds))*int64(foldSize)
}

// foldSize is sizeof(Fold): three int32s and a bool, padded to 16.
const foldSize = 16

// Text is the whole rendered body. Callers that hand this to the window are
// the reason this package exists; use Line and Folds instead.
func (v *View) Text() []byte { return v.text }

// Folds returns the folds opening in [from, to), in line order.
//
// Two binary searches, not a scan. A 40 MB body has of the order of a million
// folds, and walking to the middle of that list on every scroll step costs
// more than drawing the frame it is for.
func (v *View) Folds(from, to int) []Fold {
	lo := sort.Search(len(v.folds), func(i int) bool { return int(v.folds[i].Line) >= from })
	hi := lo + sort.Search(len(v.folds)-lo, func(i int) bool { return int(v.folds[lo+i].Line) >= to })
	return v.folds[lo:hi]
}

// Document is one response body with the views computed from it.
//
// Views are built on first use and cached: the window asks for metadata as
// soon as the response lands, and for a formatted view only if the Body tab is
// showing one, so a send never pays for a rendering nobody looks at.
type Document struct {
	body        []byte
	contentType string
	kind        Kind

	mu     sync.Mutex
	views  map[ViewKind]*View
	errs   map[ViewKind]error
	onceMu sync.Map // ViewKind -> *sync.Once
}

// New indexes a response body. contentType is the response's Content-Type
// header, which decides the body's Kind.
func New(body []byte, contentType string) *Document {
	return &Document{
		body:        body,
		contentType: contentType,
		kind:        KindOf(contentType, body),
		views:       map[ViewKind]*View{},
		errs:        map[ViewKind]error{},
	}
}

// Kind is the body's structure.
func (d *Document) Kind() Kind { return d.kind }

// Size is the body's length in bytes.
func (d *Document) Size() int64 { return int64(len(d.body)) }

// Body is the raw bytes. It is for the code that writes a response to a file
// (docs/FORMAT.md §1.11), not for anything that crosses a binding.
func (d *Document) Body() []byte { return d.body }

// HasPretty reports whether a formatted view is worth asking for. It is cheap:
// it looks at the type and the size, not at the content.
func (d *Document) HasPretty() bool {
	return d.kind != KindText && len(d.body) > 0 && len(d.body) <= MaxPrettyBytes
}

// Indexable reports whether the body can be shown in a pane at all.
func (d *Document) Indexable() bool { return len(d.body) <= MaxIndexBytes }

// Footprint is roughly how much memory this document holds: the body plus
// whatever views have been built from it. The service evicts on this rather
// than on a count of responses, because one 40 MB body costs as much as a
// hundred small ones and a count cannot tell them apart.
func (d *Document) Footprint() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	total := int64(len(d.body))
	for _, v := range d.views {
		total += v.Footprint()
	}
	return total
}

// View returns the requested rendering, building it on first use. A Pretty
// request for a body that has none returns ErrNoPrettyView.
func (d *Document) View(kind ViewKind) (*View, error) {
	// One Once per kind, so two viewports asking at the same moment do the
	// work once and the second waits rather than duplicating it.
	value, _ := d.onceMu.LoadOrStore(kind, &sync.Once{})
	once := value.(*sync.Once)
	once.Do(func() {
		view, err := d.build(kind)
		d.mu.Lock()
		d.views[kind], d.errs[kind] = view, err
		d.mu.Unlock()
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.views[kind], d.errs[kind]
}

func (d *Document) build(kind ViewKind) (*View, error) {
	if len(d.body) > MaxIndexBytes {
		return nil, ErrTooLargeToIndex
	}
	switch kind {
	case Raw:
		return indexRaw(d.body), nil
	case Pretty:
		if !d.HasPretty() {
			return nil, ErrNoPrettyView
		}
		switch d.kind {
		case KindJSON:
			return indentJSON(d.body)
		case KindXML:
			return indentXML(d.body)
		}
		return nil, ErrNoPrettyView
	}
	return nil, errors.New("response: unknown view " + string(kind))
}

// KindOf classifies a body from its Content-Type, falling back to sniffing the
// first non-space byte when the header is missing or useless — plenty of
// servers answer JSON as text/plain, and refusing to format it because of that
// would be pedantry the reader pays for.
func KindOf(contentType string, body []byte) Kind {
	base := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch {
	case base == "application/json" || strings.HasSuffix(base, "+json"):
		return KindJSON
	case base == "application/xml" || base == "text/xml" || strings.HasSuffix(base, "+xml"):
		return KindXML
	case base == "text/html":
		// HTML is close enough to XML to indent usefully and far enough from
		// it to be worth saying so if that ever stops being true.
		return KindXML
	}
	if base == "" || base == "text/plain" || base == "application/octet-stream" {
		return sniff(body)
	}
	return KindText
}

func sniff(body []byte) Kind {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return KindJSON
		case '<':
			return KindXML
		}
		return KindText
	}
	return KindText
}

// indexRaw indexes the body as it arrived, breaking physical lines longer than
// WrapWidth so no single display line can be too large to lay out.
func indexRaw(body []byte) *View {
	v := &View{Kind: Raw, text: body, Bytes: int64(len(body))}
	// The raw view keeps ends: a CRLF line ending and a display wrap both
	// break the "one separator byte" rule that lets a view drop them.
	at := 0
	for at <= len(body) {
		// The physical line: up to the next newline, or the end.
		end := len(body)
		next := at
		if i := indexByte(body, at, '\n'); i >= 0 {
			end, next = i, i+1
		} else {
			next = len(body) + 1
		}
		// A trailing "\r" belongs to the line ending, not to the text.
		text := end
		if text > at && body[text-1] == '\r' {
			text--
		}
		if text-at <= WrapWidth {
			v.starts = append(v.starts, int32(at))
			v.ends = append(v.ends, int32(text))
		} else {
			for piece := at; piece < text; {
				stop := piece + WrapWidth
				if stop >= text {
					// The last piece ends at the line's end; there is no byte
					// past it to look at for a rune boundary.
					stop = text
				} else {
					// Back up to a rune boundary so a wrap never splits a
					// character into two replacement glyphs.
					for stop > piece && !utf8.RuneStart(body[stop]) {
						stop--
					}
					if stop == piece {
						stop = piece + WrapWidth // a run of continuation bytes
					}
				}
				v.starts = append(v.starts, int32(piece))
				v.ends = append(v.ends, int32(stop))
				piece = stop
			}
		}
		at = next
	}
	// A body ending in a newline indexes one trailing empty line above; drop
	// it, the way an editor shows "3 lines" for "a\nb\nc\n".
	if n := len(v.starts); n > 1 && v.starts[n-1] == v.ends[n-1] && int(v.starts[n-1]) == len(body) {
		v.starts, v.ends = v.starts[:n-1], v.ends[:n-1]
	}
	if len(body) == 0 {
		v.starts, v.ends = []int32{0}, []int32{0}
	}
	// The sentinel closes the last line. ends is authoritative here, so its
	// value only has to be past the last start.
	v.starts = append(v.starts, int32(len(body)+1))
	return v
}

func indexByte(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}
