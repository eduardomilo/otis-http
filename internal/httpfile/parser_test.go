package httpfile

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestFixturesGolden parses every testdata/*.http fixture and compares the
// JSON form of the result with testdata/<name>.golden.json.
func TestFixturesGolden(t *testing.T) {
	for _, path := range fixtures(t) {
		name := strings.TrimSuffix(filepath.Base(path), ".http")
		t.Run(name, func(t *testing.T) {
			f, err := ParseFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, err := json.MarshalIndent(f, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
			}
		})
	}
}

// TestRoundTrip checks parse → serialize → parse is lossless (ignoring line
// numbers) and that serialization is idempotent.
func TestRoundTrip(t *testing.T) {
	for _, path := range fixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			f1, err := ParseFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			out1 := f1.String()
			f2, err := ParseString(out1)
			if err != nil {
				t.Fatalf("re-parse of serialized output failed: %v\n--- output ---\n%s", err, out1)
			}
			if a, b := withoutLines(f1), withoutLines(f2); !reflect.DeepEqual(a, b) {
				t.Errorf("round trip changed structure\n--- first ---\n%s\n--- second ---\n%s\n--- serialized ---\n%s", asJSON(a), asJSON(b), out1)
			}
			if out2 := f2.String(); out2 != out1 {
				t.Errorf("serialization not idempotent\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
			}
		})
	}
}

// TestCanonicalFixturesByteExact: files written in canonical form (the Otis
// fixtures and the JetBrains ones, which use the same layout) must survive
// parse → serialize byte for byte, so that editing a file and saving it
// produces no spurious diff.
func TestCanonicalFixturesByteExact(t *testing.T) {
	for _, path := range fixtures(t) {
		base := filepath.Base(path)
		canonical := strings.HasPrefix(base, "otis_") || strings.HasPrefix(base, "jetbrains_")
		// The implied-method fixture is deliberately non-canonical: "URL"
		// serializes as "GET URL".
		if !canonical || base == "jetbrains_implied_method.http" {
			continue
		}
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseString(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := f.String(); got != string(src) {
				t.Errorf("not byte-exact\n--- got ---\n%s\n--- want ---\n%s", got, src)
			}
		})
	}
}

func TestRequestLine(t *testing.T) {
	tests := []struct {
		name                 string
		src                  string
		method, url, version string
	}{
		{"method and url", "GET https://a.test/x", "GET", "https://a.test/x", ""},
		{"with version", "POST https://a.test/x HTTP/1.1", "POST", "https://a.test/x", "HTTP/1.1"},
		{"custom method", "PURGE https://a.test/x", "PURGE", "https://a.test/x", ""},
		{"implied GET", "https://a.test/x", "GET", "https://a.test/x", ""},
		{"variable host", "DELETE {{host}}/items/1", "DELETE", "{{host}}/items/1", ""},
		{"absolute path", "GET /relative/path", "GET", "/relative/path", ""},
		{"multi-line query", "GET https://a.test/c\n    ?page=2\n    &size=10", "GET", "https://a.test/c?page=2&size=10", ""},
		{"leading whitespace", "  GET https://a.test/x  ", "GET", "https://a.test/x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := ParseString(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.Requests) != 1 {
				t.Fatalf("got %d requests, want 1", len(f.Requests))
			}
			r := f.Requests[0]
			if r.Method != tt.method || r.URL != tt.url || r.Version != tt.version {
				t.Errorf("got %q %q %q, want %q %q %q", r.Method, r.URL, r.Version, tt.method, tt.url, tt.version)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	f, err := ParseString("GET https://a.test\ncontent-type: application/json\nX-Trace-ID:  abc  \nAccept:*/*\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Header{
		{Name: "content-type", Value: "application/json", Line: 2},
		{Name: "X-Trace-ID", Value: "abc", Line: 3},
		{Name: "Accept", Value: "*/*", Line: 4},
	}
	if got := f.Requests[0].Headers; !reflect.DeepEqual(got, want) {
		t.Errorf("headers = %+v, want %+v", got, want)
	}
	if v, ok := f.Requests[0].Header("CONTENT-TYPE"); !ok || v != "application/json" {
		t.Errorf("case-insensitive lookup failed: %q %v", v, ok)
	}
}

func TestDirectivesAndComments(t *testing.T) {
	src := "# plain comment\n# @name  Create thing \n//@no-redirect\n// @timeout 30\n#@flag\nGET https://a.test\n"
	f, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Requests[0]
	wantD := []Directive{
		{Style: "#", Name: "name", Value: "Create thing", Line: 2},
		{Style: "//", Name: "no-redirect", Line: 3},
		{Style: "//", Name: "timeout", Value: "30", Line: 4},
		{Style: "#", Name: "flag", Line: 5},
	}
	if !reflect.DeepEqual(r.Directives, wantD) {
		t.Errorf("directives = %+v\nwant %+v", r.Directives, wantD)
	}
	wantC := []Comment{{Style: "#", Text: " plain comment", Line: 1}}
	if !reflect.DeepEqual(r.Comments, wantC) {
		t.Errorf("comments = %+v, want %+v", r.Comments, wantC)
	}
	if r.Name() != "Create thing" {
		t.Errorf("Name() = %q", r.Name())
	}
}

func TestName(t *testing.T) {
	tests := []struct{ src, want string }{
		{"### Title only\nGET https://a.test\n", "Title only"},
		{"### Title\n# @name directive wins\nGET https://a.test\n", "directive wins"},
		{"GET https://a.test\n", ""},
	}
	for _, tt := range tests {
		f, err := ParseString(tt.src)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Requests[0].Name(); got != tt.want {
			t.Errorf("Name() = %q, want %q for %q", got, tt.want, tt.src)
		}
	}
}

func TestVariables(t *testing.T) {
	f, err := ParseString("@a = 1\n@b=two words \n@c.d-e =  \nGET https://a.test\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Variable{{"a", "1", 1}, {"b", "two words", 2}, {"c.d-e", "", 3}}
	if got := f.Requests[0].Variables; !reflect.DeepEqual(got, want) {
		t.Errorf("variables = %+v, want %+v", got, want)
	}
}

func TestBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Body
	}{
		{"none", "GET https://a.test\n", Body{}},
		{"blank lines only", "GET https://a.test\n\n\n\n", Body{}},
		{"raw", "POST https://a.test\n\n{\n  \"a\": 1\n}\n", Body{Raw: "{\n  \"a\": 1\n}", Line: 3}},
		{"trailing blank lines stripped", "POST https://a.test\n\nx\n\n\n", Body{Raw: "x", Line: 3}},
		{"leading blank lines stripped", "POST https://a.test\n\n\n\nx\n", Body{Raw: "x", Line: 5}},
		{"internal blank line and trailing spaces kept", "POST https://a.test\n\na  \n\nb\n", Body{Raw: "a  \n\nb", Line: 3}},
		{"no trailing newline at EOF", "POST https://a.test\n\n{}", Body{Raw: "{}", Line: 3}},
		{"hash lines are body", "POST https://a.test\n\n# not a comment\n", Body{Raw: "# not a comment", Line: 3}},
		{"file", "POST https://a.test\n\n< ./a.json\n", Body{FilePath: "./a.json", Line: 3}},
		{"file with substitution", "POST https://a.test\n\n<@ ./a.json\n", Body{FilePath: "./a.json", SubstituteVariables: true, Line: 3}},
		{"file with encoding", "POST https://a.test\n\n<@latin1 ./a.json\n", Body{FilePath: "./a.json", SubstituteVariables: true, Encoding: "latin1", Line: 3}},
		{"file ref inside multipart stays raw", "POST https://a.test\n\n--b\n\n< ./a.json\n--b--\n", Body{Raw: "--b\n\n< ./a.json\n--b--", Line: 3}},
		{"body ends at separator", "POST https://a.test\n\n{}\n\n###\nGET https://b.test\n", Body{Raw: "{}", Line: 3}},
		{"body ends at post script", "POST https://a.test\n\n{}\n> {% x %}\n", Body{Raw: "{}", Line: 3}},
		{"body ends at redirect", "POST https://a.test\n\n{}\n>> ./out.json\n", Body{Raw: "{}", Line: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := ParseString(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Requests[0].Body; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("body = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestScripts(t *testing.T) {
	src := "< {% pre(); %}\n< {%\n  a();\n  b();\n%}\nGET https://a.test\n\n> {%\n  post();\n%}\n> ./h.js\n>> ./out.json\n"
	f, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Requests[0]
	wantPre := []Script{{Text: " pre(); ", Line: 1}, {Text: "\n  a();\n  b();\n", Line: 2}}
	if !reflect.DeepEqual(r.PreScripts, wantPre) {
		t.Errorf("pre = %+v, want %+v", r.PreScripts, wantPre)
	}
	wantPost := []Script{{Text: "\n  post();\n", Line: 8}, {FilePath: "./h.js", Line: 11}}
	if !reflect.DeepEqual(r.PostScripts, wantPost) {
		t.Errorf("post = %+v, want %+v", r.PostScripts, wantPost)
	}
	if r.Redirect == nil || *r.Redirect != (Redirect{Path: "./out.json", Line: 12}) {
		t.Errorf("redirect = %+v", r.Redirect)
	}
}

func TestSettingsOnlyEntry(t *testing.T) {
	f, err := ParseString("@base = x\n# @auth bearer t\nAccept: */*\nX-A: b\n")
	if err != nil {
		t.Fatal(err)
	}
	r := f.Requests[0]
	if r.HasRequestLine() {
		t.Fatal("expected no request line")
	}
	if len(r.Headers) != 2 || len(r.Variables) != 1 || len(r.Directives) != 1 {
		t.Errorf("got %+v", r)
	}
}

func TestMultipleRequestsAndEmptyBlocks(t *testing.T) {
	f, err := ParseString("###\nGET https://a.test\n###\n\n###\nGET https://b.test\n###\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Requests) != 2 {
		t.Fatalf("got %d requests, want 2 (empty blocks dropped)", len(f.Requests))
	}
	if f.Requests[1].SeparatorLine != 5 {
		t.Errorf("separator line = %d, want 5", f.Requests[1].SeparatorLine)
	}
}

func TestEmptyInputs(t *testing.T) {
	for _, src := range []string{"", "\n\n", "\ufeff", "\r\n"} {
		f, err := ParseString(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
		}
		if len(f.Requests) != 0 {
			t.Errorf("%q: got %d requests", src, len(f.Requests))
		}
	}
}

func TestMalformed(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		line    int
		wantMsg string
	}{
		{"header without colon", "GET https://a.test\nContent-Type application/json\n", 2, "expected header"},
		{"lowercase method", "get https://a.test\n", 1, "expected request line"},
		{"garbage before request", "hello world\nGET https://a.test\n", 1, "expected request line"},
		{"method without url", "POST\n", 1, "missing a URL"},
		{"junk after url", "GET https://a.test extra\n", 1, `unexpected "extra"`},
		{"junk after version", "GET https://a.test HTTP/1.1 extra\n", 1, `unexpected "extra"`},
		{"bad variable", "@ = 1\nGET https://a.test\n", 1, "invalid variable"},
		{"body file before request", "< ./a.json\nGET https://a.test\n", 1, "body file reference before request line"},
		{"post script before request", "> {% x %}\nGET https://a.test\n", 1, "response handler before request line"},
		{"unterminated pre script", "< {%\n  a();\nGET https://a.test\n", 3, "unterminated script block opened at line 1"},
		{"unterminated post script", "GET https://a.test\n\n> {%\n  a();\n", 4, "unterminated script block opened at line 3"},
		{"trailing junk after script close", "GET https://a.test\n\n> {% a(); %} junk\n", 3, `unexpected "junk"`},
		{"junk after handler", "GET https://a.test\n\n{}\n> {% a %}\nGET https://b.test\n", 5, "unexpected content after response handler"},
		{"duplicate redirect", "GET https://a.test\n\n>> ./a\n>> ./b\n", 4, "duplicate response redirect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(tt.src)
			if err == nil {
				t.Fatal("expected error")
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error is %T, want *ParseError", err)
			}
			if pe.Line != tt.line {
				t.Errorf("line = %d, want %d (%v)", pe.Line, tt.line, err)
			}
			if !strings.Contains(pe.Msg, tt.wantMsg) {
				t.Errorf("message %q does not contain %q", pe.Msg, tt.wantMsg)
			}
		})
	}
}

func TestParseFileErrorHasPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.http")
	if err := os.WriteFile(path, []byte("GET https://a.test\nnope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseFile(path)
	if err == nil || !strings.HasPrefix(err.Error(), path+": line 2:") {
		t.Errorf("error = %v", err)
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Error("ParseError not unwrappable")
	}
}

func TestSerializeNewItemsCanonicalOrder(t *testing.T) {
	// Items without line numbers (built programmatically) are emitted in the
	// canonical order: comments, variables, directives, pre-scripts.
	f := &File{Requests: []*Request{{
		PreScripts: []Script{{Text: " s() "}},
		Directives: []Directive{{Style: "#", Name: "name", Value: "New"}},
		Variables:  []Variable{{Name: "v", Value: "1"}},
		Comments:   []Comment{{Style: "#", Text: " c"}},
		Method:     "GET", URL: "https://a.test",
		Headers: []Header{{Name: "Accept", Value: "*/*"}},
		Body:    Body{Raw: "{}"},
	}}}
	want := "# c\n@v = 1\n# @name New\n< {% s() %}\nGET https://a.test\nAccept: */*\n\n{}\n"
	if got := f.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func fixtures(t *testing.T) []string {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.http"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return paths
}

// withoutLines returns a deep copy of f with every line number zeroed.
func withoutLines(f *File) *File {
	data, _ := json.Marshal(f)
	var c File
	_ = json.Unmarshal(data, &c)
	for _, r := range c.Requests {
		r.SeparatorLine, r.Line, r.Body.Line = 0, 0, 0
		for i := range r.Comments {
			r.Comments[i].Line = 0
		}
		for i := range r.Variables {
			r.Variables[i].Line = 0
		}
		for i := range r.Directives {
			r.Directives[i].Line = 0
		}
		for i := range r.PreScripts {
			r.PreScripts[i].Line = 0
		}
		for i := range r.Headers {
			r.Headers[i].Line = 0
		}
		for i := range r.PostScripts {
			r.PostScripts[i].Line = 0
		}
		if r.Redirect != nil {
			r.Redirect.Line = 0
		}
	}
	return &c
}

func asJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
