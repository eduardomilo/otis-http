package script

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/httpfile"
)

// post builds a post-response run with sensible defaults.
func post(t *testing.T, code string) Options {
	t.Helper()
	return Options{
		Phase:   Post,
		Scripts: []Source{{Path: "orders/_post.js", Code: code}},
		Response: &ResponseView{
			Status: 201, StatusText: "Created",
			Headers: []HeaderPair{{Name: "Content-Type", Value: "application/json"}},
			Body:    `{"id":"ord_1","line_items":[{"sku":"A"},{"sku":"B"}],"total":1299}`,
			Size:    64,
			Timings: Timings{Total: 12.5, TTFB: 9},
		},
		Request: &RequestView{Method: "POST", URL: "https://x.test/orders", Path: "orders/create.http", Name: "Create order"},
		Vars:    newMapStore(),
	}
}

// pre builds a pre-request run.
func pre(t *testing.T, code string) Options {
	t.Helper()
	return Options{
		Phase:   Pre,
		Scripts: []Source{{Path: "orders/_pre.js", Code: code}},
		Request: &RequestView{
			Method: "POST", URL: "{{baseUrl}}/orders", Path: "orders/create.http", Name: "Create order",
			Headers: []HeaderPair{{Name: "Accept", Value: "application/json"}},
			Body:    `{"currency":"{{currency}}"}`,
		},
		Vars: newMapStore(),
	}
}

// A script gets a JavaScript realm and nothing else: no filesystem, no
// process, no network, no timers (docs/FORMAT.md §9.3).
func TestSandboxHasNoCapabilities(t *testing.T) {
	// Absent entirely: goja never had them and nothing here adds them.
	for _, name := range []string{
		"Deno", "Bun", "global", "module", "exports", "__dirname", "__filename",
		"Buffer", "navigator", "window", "document", "localStorage", "self",
		"performance", "queueMicrotask", "structuredClone",
	} {
		t.Run("absent "+name, func(t *testing.T) {
			result, err := Run(post(t, fmt.Sprintf(`console.log(typeof %s);`, name)))
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Console[0].Text; got != "undefined" {
				t.Errorf("typeof %s = %q, want undefined", name, got)
			}
		})
	}

	// Stubbed so the message says why, rather than "not defined".
	for name := range forbidden {
		t.Run("refused "+name, func(t *testing.T) {
			_, err := Run(post(t, fmt.Sprintf(`%s("x");`, name)))
			if err == nil {
				t.Fatalf("%s should be refused", name)
			}
			if !strings.Contains(err.Error(), "not available in a script") {
				t.Errorf("error = %v, want it to explain", err)
			}
		})
	}
}

// A script cannot reach the filesystem, spawn a process, or open a socket —
// the increment's own verification, stated as the attempts somebody would
// actually make.
func TestSandboxCannotReachTheMachine(t *testing.T) {
	attempts := map[string]string{
		"read a file":     `require("fs").readFileSync("/etc/passwd");`,
		"spawn":           `require("child_process").execSync("id");`,
		"fetch a URL":     `fetch("https://example.test/");`,
		"open a socket":   `new WebSocket("ws://example.test/");`,
		"read the env":    `process.env.HOME;`,
		"XHR":             `new XMLHttpRequest();`,
		"dynamic import":  `import("fs");`,
		"schedule a wait": `setTimeout(() => {}, 1);`,
	}
	for name, code := range attempts {
		t.Run(name, func(t *testing.T) {
			if _, err := Run(post(t, code)); err == nil {
				t.Errorf("%s should not be possible", name)
			}
		})
	}
}

// The one capability a script cannot build for itself, and the one the
// design's own lib/idempotency.js needs.
func TestCryptoRandomUUID(t *testing.T) {
	result, err := Run(post(t, `
		const a = crypto.randomUUID(), b = crypto.randomUUID();
		console.log(a.length, a === b, /^[0-9a-f-]{36}$/.test(a));
	`))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "36 false true" {
		t.Errorf("console = %q", got)
	}
}

// An infinite loop is killed by the timeout, with a message rather than a
// hung window.
func TestTimeoutKillsAnInfiniteLoop(t *testing.T) {
	opts := post(t, `while (true) {}`)
	opts.Timeout = 150 * time.Millisecond

	started := time.Now()
	_, err := Run(opts)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("an infinite loop should be killed")
	}
	var scriptErr *Error
	if !asError(err, &scriptErr) {
		t.Fatalf("error = %v, want a script Error", err)
	}
	if !scriptErr.Timeout {
		t.Errorf("Timeout = false; error = %v", err)
	}
	if !strings.Contains(err.Error(), TimeoutDirective) {
		t.Errorf("error = %v, want it to name the directive that changes it", err)
	}
	// It must actually stop, not merely report.
	if elapsed > 3*time.Second {
		t.Errorf("took %s to kill; the interrupt is not working", elapsed)
	}
}

// A tight recursive loop is the other shape of the same problem.
func TestTimeoutKillsRunawayRecursion(t *testing.T) {
	opts := post(t, `
		let n = 0;
		function spin() { n++; while (n % 1000000 !== 0) { n++; } spin(); }
		spin();
	`)
	opts.Timeout = 150 * time.Millisecond
	if _, err := Run(opts); err == nil {
		t.Fatal("runaway recursion should be killed or throw")
	}
}

func TestScriptTimeoutDirective(t *testing.T) {
	cases := map[string]struct {
		directives []httpfile.Directive
		want       time.Duration
		fails      bool
	}{
		"absent":       {nil, DefaultTimeout, false},
		"seconds":      {[]httpfile.Directive{{Name: "script-timeout", Value: "2"}}, 2 * time.Second, false},
		"decimals":     {[]httpfile.Directive{{Name: "script-timeout", Value: "0.5"}}, 500 * time.Millisecond, false},
		"last wins":    {[]httpfile.Directive{{Name: "script-timeout", Value: "2"}, {Name: "script-timeout", Value: "3"}}, 3 * time.Second, false},
		"zero":         {[]httpfile.Directive{{Name: "script-timeout", Value: "0"}}, 0, true},
		"negative":     {[]httpfile.Directive{{Name: "script-timeout", Value: "-1"}}, 0, true},
		"not a number": {[]httpfile.Directive{{Name: "script-timeout", Value: "soon"}}, 0, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := TimeoutOf(&httpfile.Request{Directives: c.directives})
			if c.fails {
				if err == nil {
					t.Errorf("TimeoutOf = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("TimeoutOf = %v, want %v", got, c.want)
			}
		})
	}
}

// The response a post-response script sees (docs/FORMAT.md §9.6).
func TestResponseView(t *testing.T) {
	result, err := Run(post(t, `
		console.log(response.status, response.statusText, response.ok, response.size);
		console.log(response.headers.get("content-type"));
		console.log(response.json().id, response.json().line_items.length);
		console.log(response.timings.total);
	`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"201 Created true 64",
		"application/json",
		"ord_1 2",
		"12.5",
	}
	for i, line := range want {
		if result.Console[i].Text != line {
			t.Errorf("console[%d] = %q, want %q", i, result.Console[i].Text, line)
		}
	}
}

// A body that is not JSON throws with the parse error, which is more use
// than a null: a script asserting on it wants to know that is what happened.
func TestResponseJSONThrowsOnNonJSON(t *testing.T) {
	opts := post(t, `response.json();`)
	opts.Response.Body = "<html>not json</html>"
	if _, err := Run(opts); err == nil {
		t.Error("response.json() on HTML should throw")
	} else if !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %v", err)
	}
}

// A pre-request script sees the template, not resolved values
// (docs/FORMAT.md §9.2). That is what lets a folder header reference a value
// the script is about to set.
func TestPreRequestSeesTheTemplate(t *testing.T) {
	result, err := Run(pre(t, `
		console.log(request.url);
		console.log(request.body);
		console.log(request.method, request.name, request.path);
	`))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "{{baseUrl}}/orders" {
		t.Errorf("request.url = %q, want the unresolved template", got)
	}
	if got := result.Console[1].Text; got != `{"currency":"{{currency}}"}` {
		t.Errorf("request.body = %q, want the unresolved template", got)
	}
	if got := result.Console[2].Text; got != "POST Create order orders/create.http" {
		t.Errorf("console = %q", got)
	}
}

// Shaping the request (docs/FORMAT.md §9.5).
func TestPreRequestCanShapeTheRequest(t *testing.T) {
	opts := pre(t, `
		request.method = "put";
		request.url = "{{baseUrl}}/orders?expand=customer";
		request.body = '{"currency":"usd"}';
		request.headers.set("Accept", "application/vnd.api+json");
		request.headers.add("X-Trace", "abc");
		request.headers.set("Idempotency-Key", "{{idemKey}}");
		request.headers.remove("X-Trace");
	`)
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Error("Changed = false after a script altered the request")
	}
	view := opts.Request
	if view.Method != "PUT" {
		t.Errorf("method = %q, want it upper-cased", view.Method)
	}
	if view.URL != "{{baseUrl}}/orders?expand=customer" {
		t.Errorf("url = %q", view.URL)
	}
	if view.Body != `{"currency":"usd"}` {
		t.Errorf("body = %q", view.Body)
	}
	headers := map[string]string{}
	for _, h := range view.Headers {
		headers[h.Name] = h.Value
	}
	if headers["Accept"] != "application/vnd.api+json" {
		t.Errorf("Accept = %q, want it replaced", headers["Accept"])
	}
	// A script may write a template: it is resolved in step 3 like any other.
	if headers["Idempotency-Key"] != "{{idemKey}}" {
		t.Errorf("Idempotency-Key = %q", headers["Idempotency-Key"])
	}
	if _, still := headers["X-Trace"]; still {
		t.Error("X-Trace was not removed")
	}
}

// The request cannot be changed after it has been sent.
func TestPostResponseCannotChangeTheRequest(t *testing.T) {
	for _, code := range []string{
		`request.url = "https://elsewhere.test/";`,
		`request.headers.set("X", "1");`,
		`request.headers.remove("Accept");`,
	} {
		if _, err := Run(post(t, code)); err == nil {
			t.Errorf("%s should be refused in a post-response script", code)
		} else if !strings.Contains(err.Error(), "already been sent") {
			t.Errorf("error = %v, want it to say why", err)
		}
	}
}

// test and expect are post-response only: a test asserts on a response.
func TestTestIsPostResponseOnly(t *testing.T) {
	_, err := Run(pre(t, `test("nope", () => {});`))
	if err == nil {
		t.Fatal("test() in a pre-request script should be refused")
	}
	if !strings.Contains(err.Error(), "post-response") {
		t.Errorf("error = %v", err)
	}
}

// Every hook in a phase shares one realm, so a value one puts on vars is
// visible to the next (docs/FORMAT.md §9.1).
func TestScriptsInAPhaseShareOneRealm(t *testing.T) {
	opts := post(t, "")
	opts.Scripts = []Source{
		{Path: "_post.js", Code: `globalThis.fromRoot = "root"; vars.session.set("a", "1");`},
		{Path: "orders/_post.js", Code: `console.log(fromRoot, vars.session.get("a"));`},
	}
	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Console[0].Text; got != "root 1" {
		t.Errorf("console = %q, want the first hook's values visible to the second", got)
	}
}

// A failure names the file and the line (docs/FORMAT.md §9.10).
func TestScriptErrorNamesFileAndLine(t *testing.T) {
	opts := post(t, "const a = 1;\nconst b = 2;\nnope();\n")
	_, err := Run(opts)
	if err == nil {
		t.Fatal("want an error")
	}
	var scriptErr *Error
	if !asError(err, &scriptErr) {
		t.Fatalf("error = %v, want a script Error", err)
	}
	if scriptErr.Path != "orders/_post.js" {
		t.Errorf("Path = %q", scriptErr.Path)
	}
	if scriptErr.Line != 3 {
		t.Errorf("Line = %d, want 3", scriptErr.Line)
	}
	if !strings.Contains(scriptErr.Msg, "nope") {
		t.Errorf("Msg = %q", scriptErr.Msg)
	}
	if got := scriptErr.Error(); !strings.Contains(got, "orders/_post.js:3") {
		t.Errorf("Error() = %q, want the file and line", got)
	}
}

// An inline block's line numbers are relative to the block, so the block's
// offset in the .http file is added back: an error in a `> {% %}` says which
// line of the request file it was.
func TestScriptErrorInAnInlineBlockNamesTheRequestFileLine(t *testing.T) {
	opts := post(t, "nope();\n")
	// The block's `> {%` marker is on line 20 of the request file.
	opts.Scripts = []Source{{Path: "orders/create.http", Line: 20, Code: "nope();\n"}}
	_, err := Run(opts)
	if err == nil {
		t.Fatal("want an error")
	}
	var scriptErr *Error
	asError(err, &scriptErr)
	if scriptErr.Line != 21 {
		t.Errorf("Line = %d, want 21 (line 1 of a block starting at 20)", scriptErr.Line)
	}
}

// console output is captured, not printed: it belongs in the window beside
// the response it explains.
func TestConsoleIsCaptured(t *testing.T) {
	var streamed []ConsoleLine
	opts := post(t, `
		console.log("one", 2, true);
		console.warn("careful");
		console.error({a: 1});
	`)
	opts.OnConsole = func(line ConsoleLine) { streamed = append(streamed, line) }

	result, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Console) != 3 {
		t.Fatalf("console = %+v", result.Console)
	}
	if result.Console[0].Text != "one 2 true" || result.Console[0].Level != "log" {
		t.Errorf("console[0] = %+v", result.Console[0])
	}
	if result.Console[1].Level != "warn" || result.Console[2].Level != "error" {
		t.Errorf("levels = %q %q", result.Console[1].Level, result.Console[2].Level)
	}
	if len(streamed) != 3 {
		t.Errorf("streamed %d lines, want 3", len(streamed))
	}
}
