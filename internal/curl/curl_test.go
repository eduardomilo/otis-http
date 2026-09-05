package curl

import (
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/httpclient"
	"github.com/otis-http/otis/internal/httpfile"
)

func TestSplitWords(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want []string
	}{
		{"plain", `curl https://x.test`, []string{"curl", "https://x.test"}},
		{"single quotes", `-H 'A: b c'`, []string{"-H", "A: b c"}},
		{"double quotes with an escape", `-H "A: \"b\""`, []string{"-H", `A: "b"`}},
		{
			"a backslash in double quotes is literal unless it is special",
			`-H "Path: C:\Users\me"`,
			[]string{"-H", `Path: C:\Users\me`},
		},
		{
			"line continuations, which every multi-line curl has",
			"curl 'https://x.test' \\\n  -H 'A: b'",
			[]string{"curl", "https://x.test", "-H", "A: b"},
		},
		{
			"an apostrophe inside a single-quoted word, shell-style",
			`--data-raw 'it'\''s'`,
			[]string{"--data-raw", "it's"},
		},
		{
			"ANSI-C quoting, which is what a browser emits for a newline",
			`--data-raw $'a\nb\tc\x41'`,
			[]string{"--data-raw", "a\nb\tcA"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitWords(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("word %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSplitWordsRefusesAnUnbalancedQuote(t *testing.T) {
	for _, in := range []string{`curl 'https://x.test`, `curl "https://x.test`, `curl $'oops`} {
		if _, err := splitWords(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestParse(t *testing.T) {
	t.Run("a browser's copy-as-cURL", func(t *testing.T) {
		got := parse(t, `curl 'https://api.test/v2/orders' \
  -H 'accept: application/json' \
  -H 'authorization: Bearer abc123' \
  --data-raw '{"sku":"A-1"}' \
  --compressed`)
		if got.Request.Method != "POST" {
			t.Errorf("method = %q, want POST — data with no -X is a POST", got.Request.Method)
		}
		if got.Request.URL != "https://api.test/v2/orders" {
			t.Errorf("url = %q", got.Request.URL)
		}
		if len(got.Request.Headers) != 2 {
			t.Errorf("headers = %+v", got.Request.Headers)
		}
		if got.Request.Body.Raw != `{"sku":"A-1"}` {
			t.Errorf("body = %q", got.Request.Body.Raw)
		}
		if len(got.Notes) != 0 {
			t.Errorf("notes = %q — --compressed needs none", got.Notes)
		}
		if got.Name != "Post orders" {
			t.Errorf("name = %q", got.Name)
		}
	})

	t.Run("-u becomes @auth basic, because it is not a header in the source", func(t *testing.T) {
		got := parse(t, `curl -u alice:s3cret https://api.test/ping`)
		if v, ok := got.Request.Directive("auth"); !ok || v != "basic alice s3cret" {
			t.Errorf("auth = %q %v", v, ok)
		}
	})

	t.Run("-G moves the data into the query string", func(t *testing.T) {
		got := parse(t, `curl -G https://api.test/search -d q=shoes -d page=2`)
		if got.Request.Method != "GET" {
			t.Errorf("method = %q", got.Request.Method)
		}
		if got.Request.URL != "https://api.test/search?q=shoes&page=2" {
			t.Errorf("url = %q", got.Request.URL)
		}
		if got.Request.Body.Raw != "" {
			t.Errorf("body = %q, want none", got.Request.Body.Raw)
		}
	})

	t.Run("several -d are joined with &, as curl does", func(t *testing.T) {
		got := parse(t, `curl https://api.test/f -d a=1 -d b=2`)
		if got.Request.Body.Raw != "a=1&b=2" {
			t.Errorf("body = %q", got.Request.Body.Raw)
		}
	})

	t.Run("--data-urlencode encodes the value and not the name", func(t *testing.T) {
		got := parse(t, `curl https://api.test/f --data-urlencode 'q=a b&c'`)
		if got.Request.Body.Raw != "q=a+b%26c" {
			t.Errorf("body = %q", got.Request.Body.Raw)
		}
	})

	t.Run("--header=value, the equals form", func(t *testing.T) {
		got := parse(t, `curl --url=https://api.test/x --header=A:b`)
		if got.Request.URL != "https://api.test/x" || len(got.Request.Headers) != 1 {
			t.Errorf("url = %q headers = %+v", got.Request.URL, got.Request.Headers)
		}
	})

	t.Run("--max-time becomes @timeout", func(t *testing.T) {
		got := parse(t, `curl --max-time 5 https://api.test/x`)
		if v, ok := got.Request.Directive("timeout"); !ok || v != "5" {
			t.Errorf("timeout = %q %v", v, ok)
		}
	})

	t.Run("cookie, agent and referer become the headers they are", func(t *testing.T) {
		got := parse(t, `curl -b 'a=1' -A 'Otis/1' -e 'https://ref.test;auto' https://api.test/x`)
		want := map[string]string{"Cookie": "a=1", "User-Agent": "Otis/1", "Referer": "https://ref.test"}
		for name, value := range want {
			if v, ok := got.Request.Header(name); !ok || v != value {
				t.Errorf("%s = %q %v, want %q", name, v, ok, value)
			}
		}
	})
}

// Everything Otis cannot express becomes a comment in the file rather than an
// error, so ninety per cent of a working example is not thrown away for the
// sake of the other ten.
func TestParseRecordsWhatItCouldNotTranslate(t *testing.T) {
	for _, tt := range []struct{ name, command, wants string }{
		{"insecure", `curl -k https://api.test/x`, "--insecure"},
		{"multipart", `curl -F file=@a.txt https://api.test/x`, "multipart"},
		{"an unknown flag", `curl --wat https://api.test/x`, "--wat"},
		{"output to a file", `curl -o out.json https://api.test/x`, ">> ./path"},
		{"a missing scheme", `curl api.test/x`, "https:// was assumed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(t, tt.command)
			if !strings.Contains(strings.Join(got.Notes, "\n"), tt.wants) {
				t.Errorf("notes = %q, want one mentioning %q", got.Notes, tt.wants)
			}
			// And the note is in the file, not only in the dialog.
			var comments []string
			for _, c := range got.Request.Comments {
				comments = append(comments, c.Text)
			}
			if !strings.Contains(strings.Join(comments, "\n"), tt.wants) {
				t.Errorf("comments = %q", comments)
			}
		})
	}
}

func TestParseRefusesWhatIsNotACommand(t *testing.T) {
	for _, in := range []string{"", "   ", "curl", "curl -X POST"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestFormat(t *testing.T) {
	t.Run("a GET with no body has no -X, because curl would not need one", func(t *testing.T) {
		got := Format(&httpclient.Request{Method: "GET", URL: "https://api.test/x"})
		if strings.Contains(got, "-X") {
			t.Errorf("got %q", got)
		}
		if !strings.Contains(got, "-L") {
			t.Error("no -L: Otis follows redirects and curl does not, so the command must ask")
		}
	})

	t.Run("@no-redirect is where curl's own default is already right", func(t *testing.T) {
		got := Format(&httpclient.Request{
			Method: "GET", URL: "https://api.test/x",
			Options: httpclient.Options{NoRedirect: true},
		})
		if strings.Contains(got, "-L") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a quote in a value survives", func(t *testing.T) {
		got := Format(&httpclient.Request{
			Method: "POST", URL: "https://api.test/x",
			Headers: []httpclient.Header{{Name: "X-Note", Value: "it's fine"}},
			Body:    []byte(`{"q":"it's"}`),
		})
		if !strings.Contains(got, `'X-Note: it'\''s fine'`) {
			t.Errorf("header not quoted: %q", got)
		}
		if !strings.Contains(got, `--data-raw '{"q":"it'\''s"}'`) {
			t.Errorf("body not quoted: %q", got)
		}
	})

	t.Run("a timeout becomes --max-time", func(t *testing.T) {
		got := Format(&httpclient.Request{
			Method: "GET", URL: "https://api.test/x",
			Options: httpclient.Options{Timeout: 5 * time.Second},
		})
		if !strings.Contains(got, "--max-time 5") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a body that is not text is named rather than mangled", func(t *testing.T) {
		got := Format(&httpclient.Request{
			Method: "POST", URL: "https://api.test/x",
			Body: []byte{0xff, 0xfe, 0x00},
		})
		if strings.Contains(got, "--data-raw") {
			t.Errorf("binary body was emitted: %q", got)
		}
		if !strings.Contains(got, "3 bytes that are not text") {
			t.Errorf("got %q", got)
		}
	})
}

// The pair is the test that either half is right: a command that survives
// Format then Parse is a command whose meaning was understood in both
// directions.
func TestFormatThenParseIsTheSameRequest(t *testing.T) {
	original := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.test/v2/orders?expand=lines",
		Headers: []httpclient.Header{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "Authorization", Value: "Bearer abc'123"},
		},
		Body:    []byte("{\n  \"sku\": \"A-1\"\n}"),
		Options: httpclient.Options{Timeout: 12 * time.Second},
	}

	got := parse(t, Format(original))
	if got.Request.Method != original.Method {
		t.Errorf("method = %q, want %q", got.Request.Method, original.Method)
	}
	if got.Request.URL != original.URL {
		t.Errorf("url = %q, want %q", got.Request.URL, original.URL)
	}
	if got.Request.Body.Raw != string(original.Body) {
		t.Errorf("body = %q, want %q", got.Request.Body.Raw, original.Body)
	}
	for _, want := range original.Headers {
		if v, ok := got.Request.Header(want.Name); !ok || v != want.Value {
			t.Errorf("%s = %q %v, want %q", want.Name, v, ok, want.Value)
		}
	}
	if v, ok := got.Request.Directive("timeout"); !ok || v != "12" {
		t.Errorf("timeout = %q %v", v, ok)
	}
	if len(got.Notes) != 0 {
		t.Errorf("notes = %q — nothing Format writes should be untranslatable", got.Notes)
	}
}

// The whole point of the import is that it writes a file, so the file has to
// parse back into what was parsed out.
func TestAParsedCommandRoundTripsThroughTheSerializer(t *testing.T) {
	got := parse(t, `curl -X PATCH 'https://api.test/orders/7' -H 'Content-Type: application/json' --data-raw '{"status":"paid"}' -u alice:pw --max-time 9`)
	file := &httpfile.File{Requests: []*httpfile.Request{got.Request}}
	text := file.String()

	reparsed, err := httpfile.ParseString(text)
	if err != nil {
		t.Fatalf("the written file does not parse: %v\n%s", err, text)
	}
	if again := reparsed.String(); again != text {
		t.Errorf("not canonical:\n--- wrote ---\n%s\n--- again ---\n%s", text, again)
	}
	entry := reparsed.Requests[0]
	if entry.Method != "PATCH" || entry.Body.Raw != `{"status":"paid"}` {
		t.Errorf("entry = %+v", entry)
	}
	if v, _ := entry.Directive("auth"); v != "basic alice pw" {
		t.Errorf("auth = %q", v)
	}
}

func parse(t *testing.T, command string) *Result {
	t.Helper()
	got, err := Parse(command)
	if err != nil {
		t.Fatalf("Parse(%q): %v", command, err)
	}
	return got
}
