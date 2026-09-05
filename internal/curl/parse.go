package curl

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/otis-http/otis/internal/httpfile"
)

// Result is a parsed command as an Otis request.
type Result struct {
	// Request is the entry to write, already carrying its `@name` and any
	// notes as comments.
	Request *httpfile.Request
	// Notes are the parts of the command Otis did not translate, in the order
	// they were found. They are also written into the request as comments —
	// this copy is for a dialog that wants to show them before writing.
	Notes []string
	// Name is a suggested display name, derived from the URL.
	Name string
}

// Parse turns a `curl` command into a request.
//
// # What is not translated, and why it is a comment rather than an error
//
// A pasted command is somebody's working example. Refusing the whole thing
// because one flag has no `.http` equivalent would throw away the ninety per
// cent that does translate, so anything unhandled becomes a `#` comment at
// the top of the entry — the same thing the Postman importer does with an
// auth type it cannot express. The comment is in the file, in the diff, and
// on screen before the file is written.
//
// # Two flags whose absence means nothing
//
// `-L` is dropped because Otis follows redirects anyway. Its *absence* is
// dropped too, and that is the one deliberate infidelity here: curl does not
// follow redirects by default, so a strict reading would put `@no-redirect`
// on almost every imported request. That would be recording curl's default
// as though it were the author's decision. What was pasted is a request; how
// it treats a 302 is a choice the person can now make in Otis.
func Parse(command string) (*Result, error) {
	words, err := splitWords(command)
	if err != nil {
		return nil, err
	}
	words = dropLeadingCurl(words)
	if len(words) == 0 {
		return nil, fmt.Errorf("that does not look like a curl command")
	}

	p := &parser{}
	if err := p.run(words); err != nil {
		return nil, err
	}
	return p.result()
}

// parser accumulates what the flags said.
type parser struct {
	method   string
	rawURL   string
	headers  []httpfile.Header
	data     []string
	form     bool
	getStyle bool
	user     string
	timeout  string
	notes    []string
}

func (p *parser) run(words []string) error {
	for i := 0; i < len(words); i++ {
		word := words[i]
		// A value that comes with the flag: `--header=x` as well as
		// `--header x`, because both forms are in the wild.
		flag, inline, hasInline := word, "", false
		if strings.HasPrefix(word, "--") {
			if name, value, found := strings.Cut(word, "="); found {
				flag, inline, hasInline = name, value, true
			}
		}
		next := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return words[i], nil
		}

		var err error
		switch flag {
		case "-X", "--request":
			p.method, err = next()
		case "-H", "--header":
			var h string
			if h, err = next(); err == nil {
				p.addHeader(h)
			}
		case "-d", "--data", "--data-raw", "--data-ascii", "--data-binary":
			var d string
			if d, err = next(); err == nil {
				p.data = append(p.data, d)
			}
		case "--data-urlencode":
			var d string
			if d, err = next(); err == nil {
				p.data = append(p.data, encodeURLEncoded(d))
			}
		case "-F", "--form", "--form-string":
			if _, err = next(); err == nil {
				p.form = true
			}
		case "-G", "--get":
			p.getStyle = true
		case "-u", "--user":
			p.user, err = next()
		case "-b", "--cookie":
			var c string
			if c, err = next(); err == nil {
				p.headers = append(p.headers, httpfile.Header{Name: "Cookie", Value: c})
			}
		case "-A", "--user-agent":
			var a string
			if a, err = next(); err == nil {
				p.headers = append(p.headers, httpfile.Header{Name: "User-Agent", Value: a})
			}
		case "-e", "--referer":
			var r string
			if r, err = next(); err == nil {
				p.headers = append(p.headers, httpfile.Header{Name: "Referer", Value: strings.TrimSuffix(r, ";auto")})
			}
		case "--url":
			p.rawURL, err = next()
		case "-m", "--max-time":
			p.timeout, err = next()
		case "-L", "--location", "--compressed", "-s", "--silent", "-S",
			"--show-error", "-f", "--fail", "-i", "--include", "-v", "--verbose",
			"--no-progress-meter", "-#", "--progress-bar":
			// Nothing to record. These are curl's own output and transport
			// behaviour, and Otis either does the same thing already
			// (following redirects, decompressing) or the flag is about a
			// terminal there is not one of.
		case "-k", "--insecure":
			p.note("--insecure was not translated: `.http` has no directive for skipping TLS verification. `otis run --insecure` is the equivalent on the command line.")
		case "-o", "--output", "-O", "--remote-name":
			if flag == "-o" || flag == "--output" {
				_, err = next()
			}
			p.note("the output flag was not translated: a response is written to a file with `>> ./path` (docs/FORMAT.md §1.11).")
		default:
			switch {
			case strings.HasPrefix(word, "-"):
				p.note(fmt.Sprintf("%s was not translated.", word))
				// A flag Otis does not know may or may not take a value, and
				// guessing wrong would swallow the URL. Anything that looks
				// like a value is left for the loop, where a bare word
				// becomes the URL only if there is not one yet.
			case p.rawURL == "":
				p.rawURL = word
			default:
				p.note(fmt.Sprintf("%q was not translated.", word))
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) addHeader(raw string) {
	name, value, found := strings.Cut(raw, ":")
	if !found {
		// `-H 'X-Thing;'` is curl's way of sending an empty header.
		name = strings.TrimSuffix(raw, ";")
		value = ""
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	p.headers = append(p.headers, httpfile.Header{Name: name, Value: strings.TrimSpace(value)})
}

func (p *parser) note(text string) { p.notes = append(p.notes, text) }

func (p *parser) result() (*Result, error) {
	if p.rawURL == "" {
		return nil, fmt.Errorf("the command names no URL")
	}
	target := p.rawURL
	if !strings.Contains(target, "://") {
		// curl assumes http:// for a bare host. Otis does not guess at send
		// time, so the guess is made here where it is visible in the file.
		target = "https://" + target
		p.note("the URL had no scheme; https:// was assumed.")
	}

	body := strings.Join(p.data, "&")

	// -G moves the data into the query string, which is what makes a curl
	// command with -d a GET rather than a POST.
	if p.getStyle && body != "" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + body
		body = ""
	}

	method := strings.ToUpper(p.method)
	if method == "" {
		method = "GET"
		if body != "" || p.form {
			method = "POST"
		}
	}

	entry := &httpfile.Request{Method: method, URL: target, Headers: p.headers}

	if p.form {
		p.note("a multipart form (-F) was not translated: write the body out with its boundary, or send it as raw text.")
	}
	if p.user != "" {
		user, password, _ := strings.Cut(p.user, ":")
		value := user
		if password != "" {
			value += " " + password
		}
		entry.Directives = append(entry.Directives,
			httpfile.Directive{Style: "#", Name: "auth", Value: "basic " + value})
	}
	if p.timeout != "" {
		if _, err := strconv.ParseFloat(p.timeout, 64); err == nil {
			entry.Directives = append(entry.Directives,
				httpfile.Directive{Style: "#", Name: "timeout", Value: p.timeout})
		} else {
			p.note(fmt.Sprintf("--max-time %q is not a number and was not translated.", p.timeout))
		}
	}
	if body != "" {
		entry.Body = httpfile.Body{Raw: body}
	}

	name := suggestName(target, method)
	entry.Directives = append([]httpfile.Directive{
		{Style: "#", Name: "name", Value: name},
	}, entry.Directives...)

	for _, note := range p.notes {
		entry.Comments = append(entry.Comments, httpfile.Comment{Style: "#", Text: " " + note})
	}

	return &Result{Request: entry, Notes: p.notes, Name: name}, nil
}

// dropLeadingCurl removes the program name, however it was written.
func dropLeadingCurl(words []string) []string {
	if len(words) == 0 {
		return words
	}
	first := strings.ToLower(words[0])
	first = strings.TrimSuffix(first, ".exe")
	if first == "curl" || strings.HasSuffix(first, "/curl") {
		return words[1:]
	}
	return words
}

// encodeURLEncoded applies --data-urlencode's rules: `name=value` encodes the
// value, a bare `value` encodes the whole thing.
func encodeURLEncoded(arg string) string {
	if name, value, found := strings.Cut(arg, "="); found && name != "" {
		return name + "=" + url.QueryEscape(value)
	}
	return url.QueryEscape(strings.TrimPrefix(arg, "="))
}

// suggestName is a display name derived from the URL: the method and the last
// meaningful path segment, which is what a person would have called it.
func suggestName(target, method string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return method
	}
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	for i := len(segments) - 1; i >= 0; i-- {
		// A trailing id says less than the collection it is in, so `/orders/42`
		// is "Orders" rather than "42".
		if _, err := strconv.Atoi(segments[i]); err == nil {
			continue
		}
		return title(method) + " " + segments[i]
	}
	if parsed.Host != "" {
		return title(method) + " " + parsed.Host
	}
	return title(method)
}

func title(method string) string {
	m := strings.ToUpper(method)
	if m == "" {
		return "Request"
	}
	return m[:1] + strings.ToLower(m[1:])
}
