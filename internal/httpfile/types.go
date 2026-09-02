// Package httpfile parses and serializes .http request files.
//
// The grammar is documented in docs/FORMAT.md, which is authoritative. The
// parser accepts files written for the JetBrains HTTP Client and the VS Code
// REST Client extension; Otis-specific syntax is limited to what those
// tools ignore (comment directives and {% %} script blocks).
//
// The parser is purely syntactic: {{variables}} are left untouched, body
// file references are recorded but not read, and script blocks are captured
// as raw text. Resolution and execution live in other packages.
package httpfile

import "fmt"

// File is one parsed .http file. A file holds zero or more entries separated
// by "###" lines. By Otis convention a request file holds exactly one entry
// with a request line; _folder.http files hold one entry without one.
type File struct {
	Requests []*Request `json:"requests"`
}

// Request is one "###"-delimited entry of a file.
//
// An entry without a request line (Method == "") is legal: it carries only
// comments, directives, variables and headers. That is how _folder.http
// expresses settings that cascade to the requests below it.
type Request struct {
	// Title is the text following the "###" separator, if any.
	Title string `json:"title,omitempty"`
	// SeparatorLine is the 1-based line of the "###" separator, 0 if the
	// entry is the first in the file and has no separator.
	SeparatorLine int `json:"separatorLine,omitempty"`

	// Preamble items. Their relative order in the source is recoverable
	// from Line; the serializer preserves it.
	Comments   []Comment   `json:"comments,omitempty"`
	Variables  []Variable  `json:"variables,omitempty"`
	Directives []Directive `json:"directives,omitempty"`
	PreScripts []Script    `json:"preScripts,omitempty"`

	// Request line. Method is empty when the entry has no request line.
	Method  string `json:"method,omitempty"`
	URL     string `json:"url,omitempty"`
	Version string `json:"version,omitempty"`
	Line    int    `json:"line,omitempty"`

	Headers []Header `json:"headers,omitempty"`
	Body    Body     `json:"body"`

	PostScripts []Script  `json:"postScripts,omitempty"`
	Redirect    *Redirect `json:"redirect,omitempty"`
}

// HasRequestLine reports whether the entry describes an HTTP request, as
// opposed to a settings-only entry such as the contents of _folder.http.
func (r *Request) HasRequestLine() bool { return r.Method != "" }

// Name returns the request's name: the value of the @name directive if
// present, otherwise the separator title, otherwise "".
func (r *Request) Name() string {
	if v, ok := r.Directive("name"); ok {
		return v
	}
	return r.Title
}

// Directive returns the value of the last directive with the given name.
func (r *Request) Directive(name string) (string, bool) {
	for i := len(r.Directives) - 1; i >= 0; i-- {
		if r.Directives[i].Name == name {
			return r.Directives[i].Value, true
		}
	}
	return "", false
}

// Header returns the value of the first header whose name matches
// case-insensitively.
func (r *Request) Header(name string) (string, bool) {
	for _, h := range r.Headers {
		if equalFold(h.Name, name) {
			return h.Value, true
		}
	}
	return "", false
}

// Comment is a "#" or "//" comment line outside a body that is not a
// directive.
type Comment struct {
	// Style is "#" or "//".
	Style string `json:"style"`
	// Text is everything after the comment marker, verbatim.
	Text string `json:"text"`
	Line int    `json:"line,omitempty"`
}

// Directive is a comment of the form "# @name value" or "// @name value".
// Value is empty for flag directives such as "# @no-redirect".
type Directive struct {
	Style string `json:"style"`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Line  int    `json:"line,omitempty"`
}

// Variable is a file-level "@name = value" declaration.
type Variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  int    `json:"line,omitempty"`
}

// Header is one request header. Name keeps its original casing and headers
// keep their original order.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  int    `json:"line,omitempty"`
}

// Body is the request body. Exactly one of Raw or FilePath is set, or
// neither for a body-less request.
type Body struct {
	// Raw is the body text verbatim, minus leading and trailing blank lines
	// and minus the final line terminator. Internal blank lines, trailing
	// spaces and indentation are preserved.
	Raw string `json:"raw,omitempty"`
	// FilePath is set for a "< ./path" body. The path is recorded relative
	// to the .http file and is not read by the parser.
	FilePath string `json:"filePath,omitempty"`
	// SubstituteVariables is true for the "<@ ./path" form, which asks for
	// {{variables}} inside the referenced file to be resolved.
	SubstituteVariables bool `json:"substituteVariables,omitempty"`
	// Encoding is the optional charset of the "<@charset ./path" form.
	Encoding string `json:"encoding,omitempty"`
	// Line is the first line of the body, 0 if there is no body.
	Line int `json:"line,omitempty"`
}

// IsEmpty reports whether the request has no body of either kind.
func (b Body) IsEmpty() bool { return b.Raw == "" && b.FilePath == "" }

// Script is a "< {% ... %}" pre-request or "> {% ... %}" post-response
// block, or the "> ./handler.js" external form. Text is the content between
// "{%" and "%}" verbatim, including surrounding newlines.
type Script struct {
	Text string `json:"text,omitempty"`
	// FilePath is set for the external "> ./handler.js" form.
	FilePath string `json:"filePath,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Redirect is a ">> ./path" or ">>! ./path" response-to-file instruction.
type Redirect struct {
	Path string `json:"path"`
	// Force is true for the ">>!" form, which overwrites an existing file.
	Force bool `json:"force,omitempty"`
	Line  int  `json:"line,omitempty"`
}

// ParseError describes a syntax error at a 1-based line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
