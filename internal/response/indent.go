package response

import (
	"bytes"
	"errors"
	"fmt"
)

// Indent is the indentation one nesting level adds. Two spaces, matching what
// the design draws (screen 1a) and what Otis writes when it formats a request
// body.
const Indent = "  "

// indentJSON re-indents a JSON body and, in the same pass, records the line
// index and every collapsible node.
//
// One pass, and a byte-level one: the scanner copies literals through
// untouched rather than decoding and re-encoding them, so a number keeps its
// exact digits, a string keeps its exact escapes, and a value too large for a
// float64 survives. Going through encoding/json would rewrite all three.
//
// It is also why the fold index is free. A separate pass to find collapsible
// nodes would have to re-derive the nesting the formatter already knows, and
// the item counts — the "2 items" chip of screen 1a — are only knowable while
// the container is open.
func indentJSON(body []byte) (*View, error) {
	// Sized from the input rather than grown. Indentation takes a minified
	// body to a little under twice its length, and letting append double a
	// 60 MB buffer means allocating 120 MB and copying 60 — measurably the
	// largest cost in the pass. The line index is sized the same way: a
	// formatted JSON line averages well over eight bytes, so this is an
	// over-estimate that never reallocates.
	out := make([]byte, 0, 2*len(body)+16)
	v := &View{Kind: Pretty}
	// ends stays nil: every line here is followed by exactly one "\n", so a
	// line's end is derivable from the next line's start (see View).
	v.starts = make([]int32, 0, len(body)/8+16)

	// The line currently being written starts here in out.
	lineStart := 0
	// open holds one entry per container being written, innermost last.
	type frame struct {
		line     int  // the line the container opened on
		count    int  // direct children seen so far
		object   bool // "{" rather than "["
		multi    bool // written across more than one line
		foldSlot int  // index into v.folds, or -1 until the fold is reserved
	}
	var open []frame
	depth := 0

	newline := func() {
		v.starts = append(v.starts, int32(lineStart))
		out = append(out, '\n')
		lineStart = len(out)
	}
	indent := func() {
		for i := 0; i < depth; i++ {
			out = append(out, Indent...)
		}
	}

	at := 0
	// needValue tracks whether the next literal is a value (so the container
	// above it gains a child) rather than a key.
	expectKey := false

	for at < len(body) {
		c := body[at]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			// Whitespace between tokens carries no meaning in JSON and is
			// exactly what this pass is replacing.
			at++
			continue

		case c == '{' || c == '[':
			if len(open) > 0 && !expectKey {
				open[len(open)-1].count++
			}
			out = append(out, c)
			// The fold is reserved now and filled in on close, so folds stay
			// in opening-line order and a range query is a binary search.
			v.folds = append(v.folds, Fold{Line: int32(len(v.starts)), Object: c == '{'})
			open = append(open, frame{
				line:     len(v.starts),
				object:   c == '{',
				foldSlot: len(v.folds) - 1,
			})
			depth++
			expectKey = c == '{'
			at++
			// An empty container stays on one line: "{}" reads better than a
			// brace with nothing between it and its partner.
			if next := skipSpace(body, at); next < len(body) && (body[next] == '}' || body[next] == ']') {
				continue
			}
			newline()
			indent()
			continue

		case c == '}' || c == ']':
			if len(open) == 0 {
				return nil, fmt.Errorf("unbalanced %q at byte %d", c, at)
			}
			top := open[len(open)-1]
			open = open[:len(open)-1]
			depth--
			if top.multi {
				newline()
				indent()
			}
			out = append(out, c)
			f := &v.folds[top.foldSlot]
			f.End = int32(len(v.starts))
			f.Count = int32(top.count)
			if !top.multi {
				// Nothing was written across a line boundary, so there is
				// nothing to collapse. Dropping the entry keeps the index to
				// the folds the window can actually draw a chevron for.
				v.folds[top.foldSlot].Count = -1
			}
			expectKey = false
			at++
			continue

		case c == ',':
			out = append(out, c)
			if len(open) > 0 {
				open[len(open)-1].multi = true
				expectKey = open[len(open)-1].object
			}
			newline()
			indent()
			at++
			continue

		case c == ':':
			out = append(out, ':', ' ')
			expectKey = false
			at++
			continue

		case c == '"':
			end, err := scanString(body, at)
			if err != nil {
				return nil, err
			}
			if len(open) > 0 {
				open[len(open)-1].multi = true
				if !expectKey {
					open[len(open)-1].count++
				}
			}
			out = append(out, body[at:end]...)
			at = end
			continue

		default:
			end := scanLiteral(body, at)
			if end == at {
				return nil, fmt.Errorf("unexpected byte %q at byte %d", c, at)
			}
			if len(open) > 0 {
				open[len(open)-1].multi = true
				if !expectKey {
					open[len(open)-1].count++
				}
			}
			out = append(out, body[at:end]...)
			at = end
			continue
		}
	}
	if len(open) != 0 {
		return nil, errors.New("unexpected end of JSON: a container is still open")
	}
	// The last line has no trailing newline, so the sentinel is one past the
	// end: the last line's end is starts[n]-1 == len(out).
	v.starts = append(v.starts, int32(lineStart), int32(len(out)+1))

	// Drop the reserved entries that turned out not to span lines, into a
	// slice of exactly the right size. Filtering in place would keep the
	// capacity the reservations grew to, which for a large body is tens of
	// megabytes held for nothing.
	keep := 0
	for _, f := range v.folds {
		if f.Count >= 0 && f.End > f.Line {
			keep++
		}
	}
	kept := make([]Fold, 0, keep)
	for _, f := range v.folds {
		if f.Count >= 0 && f.End > f.Line {
			kept = append(kept, f)
		}
	}
	v.folds = kept

	v.text = out
	v.Bytes = int64(len(out))
	return v, nil
}

func skipSpace(b []byte, at int) int {
	for at < len(b) {
		switch b[at] {
		case ' ', '\t', '\r', '\n':
			at++
		default:
			return at
		}
	}
	return at
}

// scanString returns the offset one past the closing quote of the string
// starting at b[at], which must be '"'.
func scanString(b []byte, at int) (int, error) {
	for i := at + 1; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++ // the escaped byte, whatever it is, is not a terminator
		case '"':
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string at byte %d", at)
}

// scanLiteral returns the offset one past a number, true, false or null.
func scanLiteral(b []byte, at int) int {
	i := at
	for i < len(b) {
		c := b[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || c == '-' || c == '+' || c == '.' || c == 'E' {
			i++
			continue
		}
		break
	}
	return i
}

// indentXML re-indents XML and HTML, one element per line.
//
// Deliberately not a parser: it splits on tag boundaries and tracks depth. XML
// in a response is something to read, and a reader is better served by a
// document that indents even when it is not well-formed than by an error
// message about byte 4,000,192. Text content is kept verbatim, so nothing is
// lost when the guess about a tag is wrong.
func indentXML(body []byte) (*View, error) {
	out := make([]byte, 0, len(body)+len(body)/4+16)
	v := &View{Kind: Pretty}
	lineStart := 0
	depth := 0

	flush := func() {
		v.starts = append(v.starts, int32(lineStart))
		out = append(out, '\n')
		lineStart = len(out)
	}
	writeIndent := func() {
		for i := 0; i < depth; i++ {
			out = append(out, Indent...)
		}
	}

	at := 0
	for at < len(body) {
		open := bytes.IndexByte(body[at:], '<')
		if open < 0 {
			if text := bytes.TrimSpace(body[at:]); len(text) > 0 {
				writeIndent()
				out = append(out, text...)
				flush()
			}
			break
		}
		if text := bytes.TrimSpace(body[at : at+open]); len(text) > 0 {
			writeIndent()
			out = append(out, text...)
			flush()
		}
		at += open
		close := bytes.IndexByte(body[at:], '>')
		if close < 0 {
			// A "<" with no ">" is text, not a tag.
			writeIndent()
			out = append(out, bytes.TrimSpace(body[at:])...)
			flush()
			break
		}
		tag := body[at : at+close+1]
		closing := len(tag) > 1 && tag[1] == '/'
		selfClosing := len(tag) > 2 && tag[len(tag)-2] == '/'
		declaration := len(tag) > 1 && (tag[1] == '?' || tag[1] == '!')

		if closing && depth > 0 {
			depth--
		}
		writeIndent()
		out = append(out, tag...)
		flush()
		if !closing && !selfClosing && !declaration {
			depth++
		}
		at += close + 1
	}
	switch {
	case len(v.starts) == 0:
		v.starts = []int32{0, 1}
	case lineStart < len(out):
		v.starts = append(v.starts, int32(lineStart), int32(len(out)+1))
	default:
		// flush() left a trailing newline; the last line is already recorded
		// and the sentinel closes it.
		out = out[:len(out)-1]
		v.starts = append(v.starts, int32(len(out)+1))
	}
	v.text = out
	v.Bytes = int64(len(out))
	return v, nil
}
