package httpfile

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var (
	// # @name value   //@name   # @no-redirect
	directiveRe = regexp.MustCompile(`^(#|//)\s*@([A-Za-z][\w.-]*)(?:[ \t]+(.*?))?\s*$`)
	// @name = value   @name=value
	variableRe = regexp.MustCompile(`^@([^\s=]+)\s*=\s*(.*?)\s*$`)
	// token ":" value  (RFC 7230 token characters)
	headerRe = regexp.MustCompile("^([!#$%&'*+.^_`|~0-9A-Za-z-]+):(.*)$")
	methodRe = regexp.MustCompile(`^[A-Z]+$`)
	// < ./file   <@ ./file   <@latin1 ./file
	bodyFileRe = regexp.MustCompile(`^<(@[\w-]*)?[ \t]+(\S.*?)\s*$`)
	// >> ./file   >>! ./file
	redirectRe = regexp.MustCompile(`^>>(!?)[ \t]+(\S.*?)\s*$`)
	// > ./handler.js
	scriptFileRe = regexp.MustCompile(`^>[ \t]+(\S.*?)\s*$`)
)

// ParseFile reads and parses the file at path. Errors are prefixed with the
// path.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Parse parses .http content from r.
func Parse(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return ParseString(string(data))
}

// ParseString parses .http content. Line endings may be LF or CRLF; a UTF-8
// BOM is ignored. On error the result is nil and the error is a *ParseError
// carrying the 1-based line number.
func ParseString(src string) (*File, error) {
	p := &parser{}
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	p.lines = strings.Split(src, "\n")
	// A trailing newline yields a final empty element that is not a line.
	if n := len(p.lines); n > 0 && p.lines[n-1] == "" {
		p.lines = p.lines[:n-1]
	}
	if err := p.run(); err != nil {
		return nil, err
	}
	return &File{Requests: p.done}, nil
}

type state int

const (
	statePreamble state = iota // before the request line
	stateHeaders               // request line seen, reading headers
	stateBody                  // blank line seen, reading body
	stateAfter                 // body closed by a handler/redirect
)

type parser struct {
	lines []string
	pos   int // index of the current line (0-based)
	state state
	cur   *Request
	done  []*Request
	body  []string // raw body lines being accumulated
}

func (p *parser) errorf(line int, format string, args ...any) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) run() error {
	p.cur = &Request{}
	for p.pos = 0; p.pos < len(p.lines); p.pos++ {
		line := p.lines[p.pos]
		lineNo := p.pos + 1
		var err error
		switch p.state {
		case statePreamble:
			err = p.preambleLine(line, lineNo)
		case stateHeaders:
			err = p.headerLine(line, lineNo)
		case stateBody:
			err = p.bodyLine(line, lineNo)
		case stateAfter:
			err = p.afterLine(line, lineNo)
		}
		if err != nil {
			return err
		}
	}
	p.finishRequest()
	return nil
}

// finishRequest closes the current entry and starts a new one. Entries with
// no content at all (e.g. a trailing "###") are dropped.
func (p *parser) finishRequest() {
	if p.state == stateBody {
		p.closeBody()
	}
	r := p.cur
	if !isEmptyRequest(r) {
		p.done = append(p.done, r)
	}
	p.cur = &Request{}
	p.state = statePreamble
	p.body = nil
}

func isEmptyRequest(r *Request) bool {
	return r.Title == "" && len(r.Comments) == 0 && len(r.Variables) == 0 &&
		len(r.Directives) == 0 && len(r.PreScripts) == 0 && r.Method == "" &&
		len(r.Headers) == 0 && r.Body.IsEmpty() && len(r.PostScripts) == 0 &&
		r.Redirect == nil
}

func isSeparator(line string) bool { return strings.HasPrefix(line, "###") }

func isComment(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//")
}

func (p *parser) startSeparator(line string, lineNo int) {
	p.finishRequest()
	p.cur.Title = strings.TrimSpace(line[3:])
	p.cur.SeparatorLine = lineNo
}

// addComment records a comment or directive line.
func (p *parser) addComment(line string, lineNo int) {
	if m := directiveRe.FindStringSubmatch(line); m != nil {
		p.cur.Directives = append(p.cur.Directives, Directive{Style: m[1], Name: m[2], Value: m[3], Line: lineNo})
		return
	}
	style := "#"
	if strings.HasPrefix(line, "//") {
		style = "//"
	}
	p.cur.Comments = append(p.cur.Comments, Comment{Style: style, Text: line[len(style):], Line: lineNo})
}

func (p *parser) preambleLine(line string, lineNo int) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return nil
	case isSeparator(line):
		p.startSeparator(line, lineNo)
		return nil
	case isComment(line):
		p.addComment(line, lineNo)
		return nil
	case strings.HasPrefix(line, "@"):
		m := variableRe.FindStringSubmatch(line)
		if m == nil {
			return p.errorf(lineNo, "invalid variable declaration %q: expected \"@name = value\"", line)
		}
		p.cur.Variables = append(p.cur.Variables, Variable{Name: m[1], Value: m[2], Line: lineNo})
		return nil
	case strings.HasPrefix(trimmed, "< {%"):
		s, err := p.readScript(strings.TrimSpace(line)[1:], lineNo)
		if err != nil {
			return err
		}
		p.cur.PreScripts = append(p.cur.PreScripts, s)
		return nil
	case strings.HasPrefix(trimmed, "> {%"), strings.HasPrefix(trimmed, ">>"):
		return p.errorf(lineNo, "response handler before request line")
	case strings.HasPrefix(trimmed, "<"):
		return p.errorf(lineNo, "body file reference before request line")
	}
	if p.looksLikeRequestLine(trimmed) {
		if err := p.parseRequestLine(trimmed, lineNo); err != nil {
			return err
		}
		p.state = stateHeaders
		return nil
	}
	if m := headerRe.FindStringSubmatch(line); m != nil {
		// Settings-only entry (no request line), e.g. _folder.http.
		p.cur.Headers = append(p.cur.Headers, Header{Name: m[1], Value: strings.TrimSpace(m[2]), Line: lineNo})
		p.state = stateHeaders
		return nil
	}
	return p.errorf(lineNo, "expected request line (METHOD URL) or header, got %q", line)
}

// looksLikeRequestLine distinguishes "GET http://..." and bare URLs from
// headers such as "Host: example.com" (which also contains a colon).
func (p *parser) looksLikeRequestLine(trimmed string) bool {
	first := trimmed
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		first = trimmed[:i]
	}
	if methodRe.MatchString(first) {
		return true
	}
	return strings.Contains(first, "://") || strings.HasPrefix(first, "/") || strings.HasPrefix(first, "{{")
}

func (p *parser) parseRequestLine(trimmed string, lineNo int) error {
	fields := strings.Fields(trimmed)
	var method, url, version string
	if methodRe.MatchString(fields[0]) {
		method = fields[0]
		fields = fields[1:]
		if len(fields) == 0 {
			return p.errorf(lineNo, "request line %q is missing a URL", method)
		}
	} else {
		// JetBrains allows omitting the method; GET is implied.
		method = "GET"
	}
	url = fields[0]
	fields = fields[1:]
	if len(fields) > 0 {
		if !strings.HasPrefix(fields[0], "HTTP/") {
			return p.errorf(lineNo, "unexpected %q after URL: expected HTTP version or end of line", fields[0])
		}
		version = fields[0]
		fields = fields[1:]
	}
	if len(fields) > 0 {
		return p.errorf(lineNo, "unexpected %q after HTTP version", fields[0])
	}
	p.cur.Method, p.cur.URL, p.cur.Version, p.cur.Line = method, url, version, lineNo
	return nil
}

func (p *parser) headerLine(line string, lineNo int) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		p.state = stateBody
		p.body = nil
		return nil
	case isSeparator(line):
		p.startSeparator(line, lineNo)
		return nil
	case isComment(line):
		p.addComment(line, lineNo)
		return nil
	case strings.HasPrefix(trimmed, "> {%"), scriptFileRe.MatchString(trimmed):
		p.state = stateAfter
		return p.afterLine(line, lineNo)
	case redirectRe.MatchString(trimmed):
		p.state = stateAfter
		return p.afterLine(line, lineNo)
	}
	// Multi-line URL: indented continuation lines starting with ? or &
	// before any header has been read.
	if p.cur.Method != "" && len(p.cur.Headers) == 0 && line != trimmed &&
		(strings.HasPrefix(trimmed, "?") || strings.HasPrefix(trimmed, "&")) {
		p.cur.URL += trimmed
		return nil
	}
	m := headerRe.FindStringSubmatch(line)
	if m == nil {
		return p.errorf(lineNo, "expected header (\"Name: value\") or blank line before body, got %q", line)
	}
	p.cur.Headers = append(p.cur.Headers, Header{Name: m[1], Value: strings.TrimSpace(m[2]), Line: lineNo})
	return nil
}

func (p *parser) bodyLine(line string, lineNo int) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case isSeparator(line):
		p.closeBody()
		p.startSeparator(line, lineNo)
		return nil
	case strings.HasPrefix(trimmed, "> {%"), scriptFileRe.MatchString(trimmed), redirectRe.MatchString(trimmed):
		p.closeBody()
		p.state = stateAfter
		return p.afterLine(line, lineNo)
	}
	p.body = append(p.body, line)
	return nil
}

// closeBody turns the accumulated body lines into cur.Body.
func (p *parser) closeBody() {
	lines := p.body
	p.body = nil
	firstLine := p.pos - len(lines) + 1 // 1-based line of the first accumulated line
	// Strip leading and trailing blank lines; they separate, they are not body.
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	lines = lines[start:end]
	if len(lines) == 0 {
		return
	}
	p.cur.Body.Line = firstLine + start
	if len(lines) == 1 {
		if m := bodyFileRe.FindStringSubmatch(strings.TrimSpace(lines[0])); m != nil {
			p.cur.Body.FilePath = m[2]
			if m[1] != "" {
				p.cur.Body.SubstituteVariables = true
				p.cur.Body.Encoding = m[1][1:]
			}
			return
		}
	}
	p.cur.Body.Raw = strings.Join(lines, "\n")
}

func (p *parser) afterLine(line string, lineNo int) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return nil
	case isSeparator(line):
		p.startSeparator(line, lineNo)
		return nil
	case isComment(line):
		p.addComment(line, lineNo)
		return nil
	case strings.HasPrefix(trimmed, "> {%"):
		s, err := p.readScript(trimmed[1:], lineNo)
		if err != nil {
			return err
		}
		p.cur.PostScripts = append(p.cur.PostScripts, s)
		return nil
	}
	if m := redirectRe.FindStringSubmatch(trimmed); m != nil {
		if p.cur.Redirect != nil {
			return p.errorf(lineNo, "duplicate response redirect (\">>\")")
		}
		p.cur.Redirect = &Redirect{Path: m[2], Force: m[1] == "!", Line: lineNo}
		return nil
	}
	if m := scriptFileRe.FindStringSubmatch(trimmed); m != nil {
		p.cur.PostScripts = append(p.cur.PostScripts, Script{FilePath: m[1], Line: lineNo})
		return nil
	}
	return p.errorf(lineNo, "unexpected content after response handler: %q", line)
}

// readScript reads a "{% ... %}" block. rest is the current line starting at
// the character after the "<" or ">" marker. Multi-line blocks advance p.pos.
// The returned Text is the content between the delimiters, verbatim.
func (p *parser) readScript(rest string, lineNo int) (Script, error) {
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "{%") {
		return Script{}, p.errorf(lineNo, "expected \"{%%\" to open a script block")
	}
	rest = rest[2:]
	if i := strings.Index(rest, "%}"); i >= 0 {
		if tail := strings.TrimSpace(rest[i+2:]); tail != "" {
			return Script{}, p.errorf(lineNo, "unexpected %q after \"%%}\"", tail)
		}
		return Script{Text: rest[:i], Line: lineNo}, nil
	}
	var b strings.Builder
	b.WriteString(rest)
	for p.pos+1 < len(p.lines) {
		p.pos++
		line := p.lines[p.pos]
		if i := strings.Index(line, "%}"); i >= 0 {
			if tail := strings.TrimSpace(line[i+2:]); tail != "" {
				return Script{}, p.errorf(p.pos+1, "unexpected %q after \"%%}\"", tail)
			}
			b.WriteString("\n")
			b.WriteString(line[:i])
			return Script{Text: b.String(), Line: lineNo}, nil
		}
		b.WriteString("\n")
		b.WriteString(line)
	}
	return Script{}, p.errorf(len(p.lines), "unterminated script block opened at line %d: missing \"%%}\"", lineNo)
}
