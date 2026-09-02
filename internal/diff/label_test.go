package diff

import "testing"

// The file the design's screen 1b is a diff of: a request whose body changed
// and whose post-response script gained a test line.
const requestFile = `# @name Create order
# @timeout 30
@currency = USD
POST {{baseUrl}}/orders
Content-Type: application/json
Idempotency-Key: {{$uuid}}

{
  "currency": "{{currency}}",
  "expand": ["line_items"]
}

> {%
  test("created", () => expect(response.status).toBe(201));
%}
`

func TestLabelNamesThePartOfARequestFile(t *testing.T) {
	cases := []struct {
		name  string
		line  int
		label string
	}{
		{"the @name directive", 1, "@name"},
		{"the @timeout directive", 2, "@timeout"},
		{"a variable", 3, "variables"},
		{"the request line", 4, "POST {{baseUrl}}/orders"},
		{"a header", 5, LabelHeaders},
		{"the second header", 6, LabelHeaders},
		// The design's rule: a hunk in the body is headed by the request
		// line, not by "body" and not by an offset.
		{"the body's first line", 8, "POST {{baseUrl}}/orders"},
		{"inside the body", 10, "POST {{baseUrl}}/orders"},
		{"the post-response script", 13, LabelTests},
		{"inside the script", 14, LabelTests},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Hunk{Lines: []Line{{Kind: Added, Text: "x", New: c.line}}}
			if got := Label("orders/create-order.http", requestFile, h); got != c.label {
				t.Errorf("Label at line %d = %q, want %q", c.line, got, c.label)
			}
		})
	}
}

// _folder.http has no request line (docs/FORMAT.md §1.9), so its parts are
// named by what they are.
func TestLabelOfFolderSettings(t *testing.T) {
	const folder = `# @auth bearer {{apiKey}}
@currency = USD
Accept: application/json
`
	cases := map[int]string{1: "@auth", 2: LabelVars, 3: LabelHeaders}
	for line, want := range cases {
		h := Hunk{Lines: []Line{{Kind: Added, Text: "x", New: line}}}
		if got := Label("orders/_folder.http", folder, h); got != want {
			t.Errorf("Label at line %d = %q, want %q", line, got, want)
		}
	}
}

// A file with several entries says which request a hunk is in. The separator
// carries the name, and the parts below it carry their own.
func TestLabelNamesTheEntryInAMultiRequestFile(t *testing.T) {
	const two = `GET {{baseUrl}}/one

### Second
POST {{baseUrl}}/two

{"a": 1}
`
	h := func(line int) Hunk { return Hunk{Lines: []Line{{Kind: Added, Text: "x", New: line}}} }
	if got := Label("x.http", two, h(3)); got != "Second" {
		t.Errorf("separator label = %q, want the entry title", got)
	}
	if got := Label("x.http", two, h(6)); got != "POST {{baseUrl}}/two" {
		t.Errorf("body label = %q, want the second request line", got)
	}
}

// Anything that is not a request file keeps the raw offsets, and so does a
// request file that does not parse: a confidently wrong header is worse than
// an honest "@@ -12,7 +12,9 @@".
func TestLabelIsEmptyWhereNothingCanBeDerived(t *testing.T) {
	h := Hunk{Lines: []Line{{Kind: Added, Text: "x", New: 1}}}
	for _, c := range []struct{ name, path, content string }{
		{"an order file", "orders/.order", "a.http\nb.http\n"},
		{"an environment", "env/staging.json", `{"a": 1}`},
		{"a readme", "orders/README.md", "# Orders\n"},
		{"a broken request file", "x.http", "GET https://x.test nonsense trailing\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Label(c.path, c.content, h); got != "" {
				t.Errorf("Label = %q, want none", got)
			}
		})
	}
}

// A pure deletion has no new-side line to point at, so the label names the
// region the lines came out of.
func TestLabelOfAPureDeletionUsesTheOldSide(t *testing.T) {
	h := Hunk{Lines: []Line{{Kind: Removed, Text: "  \"expand\": []", Old: 10}}}
	if got := Label("orders/create-order.http", requestFile, h); got != "POST {{baseUrl}}/orders" {
		t.Errorf("Label = %q", got)
	}
}
