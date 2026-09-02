package httpfile

import (
	"io"
	"sort"
	"strings"
)

// String serializes the file in canonical form. Parsing the result yields a
// File equal to f (ignoring line numbers). Files already in canonical form
// serialize byte-for-byte; see docs/FORMAT.md "Canonical form".
func (f *File) String() string {
	var b strings.Builder
	_ = f.write(&b)
	return b.String()
}

// WriteTo writes the canonical serialization of f to w.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	var b strings.Builder
	_ = f.write(&b)
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// Serialize is String as a free function, for symmetry with ParseString.
func Serialize(f *File) string { return f.String() }

func (f *File) write(b *strings.Builder) error {
	for i, r := range f.Requests {
		if i > 0 {
			b.WriteString("\n")
		}
		if i > 0 || r.Title != "" {
			b.WriteString("###")
			if r.Title != "" {
				b.WriteString(" ")
				b.WriteString(r.Title)
			}
			b.WriteString("\n")
		}
		r.writePreamble(b)
		if r.HasRequestLine() {
			b.WriteString(r.Method)
			b.WriteString(" ")
			b.WriteString(r.URL)
			if r.Version != "" {
				b.WriteString(" ")
				b.WriteString(r.Version)
			}
			b.WriteString("\n")
		}
		for _, h := range r.Headers {
			b.WriteString(h.Name)
			b.WriteString(": ")
			b.WriteString(h.Value)
			b.WriteString("\n")
		}
		if !r.Body.IsEmpty() {
			b.WriteString("\n")
			r.Body.write(b)
		}
		if len(r.PostScripts) > 0 || r.Redirect != nil {
			b.WriteString("\n")
		}
		for _, s := range r.PostScripts {
			s.write(b, ">")
		}
		if r.Redirect != nil {
			b.WriteString(">>")
			if r.Redirect.Force {
				b.WriteString("!")
			}
			b.WriteString(" ")
			b.WriteString(r.Redirect.Path)
			b.WriteString("\n")
		}
	}
	return nil
}

// preambleItem lets comments, variables, directives and pre-request scripts
// be emitted in their original relative order.
type preambleItem struct {
	line  int
	order int // insertion order for items without a line number
	write func(*strings.Builder)
}

func (r *Request) writePreamble(b *strings.Builder) {
	var items []preambleItem
	add := func(line int, w func(*strings.Builder)) {
		items = append(items, preambleItem{line: line, order: len(items), write: w})
	}
	for _, c := range r.Comments {
		c := c
		add(c.Line, func(b *strings.Builder) {
			b.WriteString(c.Style)
			b.WriteString(c.Text)
			b.WriteString("\n")
		})
	}
	for _, v := range r.Variables {
		v := v
		add(v.Line, func(b *strings.Builder) {
			b.WriteString("@")
			b.WriteString(v.Name)
			b.WriteString(" = ")
			b.WriteString(v.Value)
			b.WriteString("\n")
		})
	}
	for _, d := range r.Directives {
		d := d
		add(d.Line, func(b *strings.Builder) {
			b.WriteString(d.Style)
			b.WriteString(" @")
			b.WriteString(d.Name)
			if d.Value != "" {
				b.WriteString(" ")
				b.WriteString(d.Value)
			}
			b.WriteString("\n")
		})
	}
	for _, s := range r.PreScripts {
		s := s
		add(s.Line, func(b *strings.Builder) { s.write(b, "<") })
	}
	// Items with a known line keep source order; new items (line 0) follow
	// in the canonical order comments, variables, directives, scripts.
	sort.SliceStable(items, func(i, j int) bool {
		li, lj := items[i].line, items[j].line
		switch {
		case li == 0 && lj == 0:
			return items[i].order < items[j].order
		case li == 0:
			return false
		case lj == 0:
			return true
		default:
			return li < lj
		}
	})
	for _, it := range items {
		it.write(b)
	}
}

func (body Body) write(b *strings.Builder) {
	if body.FilePath != "" {
		b.WriteString("<")
		if body.SubstituteVariables {
			b.WriteString("@")
			b.WriteString(body.Encoding)
		}
		b.WriteString(" ")
		b.WriteString(body.FilePath)
		b.WriteString("\n")
		return
	}
	b.WriteString(body.Raw)
	b.WriteString("\n")
}

func (s Script) write(b *strings.Builder, marker string) {
	b.WriteString(marker)
	b.WriteString(" ")
	if s.FilePath != "" {
		b.WriteString(s.FilePath)
		b.WriteString("\n")
		return
	}
	b.WriteString("{%")
	b.WriteString(s.Text)
	b.WriteString("%}\n")
}
