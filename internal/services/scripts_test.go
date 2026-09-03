package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/script"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// sent is what the server saw, so a test can assert on the wire rather than
// on Otis' own account of it.
type sent struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

// scriptCollection lays out a collection whose scripts exercise the whole of
// docs/FORMAT.md §9, and returns the send service over it.
func scriptCollection(t *testing.T, base string, files map[string]string) (*SendService, string, *secrets.Memory) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	write(t, filepath.Join(root, "env", "local.json"),
		`{"baseUrl": `+jsonString(base)+`, "apiKey": {"$secret": "keychain"}}`)

	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	vault := secrets.NewMemory()
	if err := vault.Set(secrets.Key(root[strings.LastIndex(root, "/")+1:], "local", "apiKey"), "sk_live_SECRET"); err != nil {
		t.Fatal(err)
	}
	sends := NewSendService(collections, vault)
	t.Cleanup(func() { sends.cancelAll() })
	return sends, root, vault
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// echo records what it was sent and answers with JSON.
func echo(t *testing.T, seen *[]sent) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(body)
		}
		*seen = append(*seen, sent{
			Method: r.Method, Path: r.URL.RequestURI(),
			Headers: r.Header.Clone(), Body: string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ord_1","line_items":[{"sku":"A"},{"sku":"B"}]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// sendAndWait runs one send synchronously and returns what it produced.
func sendAndWait(t *testing.T, sends *SendService, nodePath, env string) (ResponseMeta, *SendFailure) {
	t.Helper()
	loaded, err := sends.collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find(nodePath)
	if node == nil {
		t.Fatalf("%s is not in the collection", nodePath)
	}
	out := sends.run(t.Context(), "send-1", loaded, node, env, time.Now())
	if out.meta != nil {
		return *out.meta, nil
	}
	if out.failure != nil {
		return ResponseMeta{}, out.failure
	}
	t.Fatal("the send produced neither a response nor a failure")
	return ResponseMeta{}, nil
}

// The design's own arrangement: a folder hook mints an idempotency key, a
// folder header references it, and the header resolves — which is only
// possible because pre-request scripts run before resolution
// (docs/FORMAT.md §9.2, DESIGN-NOTES §9.8).
func TestPreRequestScriptSetsAValueAFolderHeaderReferences(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"lib/idempotency.js": "export function idempotencyKey() {\n  return \"idem_\" + crypto.randomUUID();\n}\n",
		"orders/_folder.http": "Accept: application/json\n" +
			"Idempotency-Key: {{idemKey}}\n",
		"orders/_pre.js": "import { idempotencyKey } from \"../lib/idempotency.js\";\n" +
			"vars.request.set(\"idemKey\", idempotencyKey());\n",
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	meta, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}
	if meta.StatusCode != 201 {
		t.Fatalf("status = %d", meta.StatusCode)
	}
	if len(seen) != 1 {
		t.Fatalf("the server saw %d requests", len(seen))
	}
	key := seen[0].Headers.Get("Idempotency-Key")
	if !strings.HasPrefix(key, "idem_") {
		t.Errorf("Idempotency-Key = %q, want the value the script minted", key)
	}
	if strings.Contains(key, "{{") {
		t.Errorf("Idempotency-Key = %q, want it resolved", key)
	}
}

// The order of §9.1: outermost folder first, then the request's own hook,
// then its inline block. Post-response the exact reverse.
func TestHookOrder(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"_pre.js":                     `vars.request.set("order", (vars.request.get("order") || "") + "root>");`,
		"_post.js":                    `console.log("post", "root");`,
		"orders/_pre.js":              `vars.request.set("order", (vars.request.get("order") || "") + "folder>");`,
		"orders/_post.js":             `console.log("post", "folder");`,
		"orders/create-order.pre.js":  `vars.request.set("order", (vars.request.get("order") || "") + "file>");`,
		"orders/create-order.post.js": `console.log("post", "file");`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n" +
			"X-Order: {{order}}\n" +
			"\n" +
			"{}\n" +
			"\n" +
			"> {%\n  console.log(\"post\", \"inline\");\n%}\n",
	})
	// The pre-request inline block appends last of the pre hooks.
	write(t, filepath.Join(sends.collections.Current().Path, "orders", "create-order.http"),
		"< {%\n  vars.request.set(\"order\", vars.request.get(\"order\") + \"inline\");\n%}\n"+
			"POST {{baseUrl}}/orders\n"+
			"X-Order: {{order}}\n"+
			"\n"+
			"{}\n"+
			"\n"+
			"> {%\n  console.log(\"post\", \"inline\");\n%}\n")
	if _, err := sends.collections.Tree(); err != nil {
		t.Fatal(err)
	}

	meta, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}

	// Pre-request: root, then the folder, then the request's file hook, then
	// its inline block.
	if got := seen[0].Headers.Get("X-Order"); got != "root>folder>file>inline" {
		t.Errorf("pre-request order = %q, want root>folder>file>inline", got)
	}

	// Post-response: the exact reverse.
	var order []string
	for _, line := range meta.Console {
		if strings.HasPrefix(line.Text, "post ") {
			order = append(order, strings.TrimPrefix(line.Text, "post "))
		}
	}
	if strings.Join(order, ">") != "inline>file>folder>root" {
		t.Errorf("post-response order = %v, want inline>file>folder>root", order)
	}
}

// A script may shape the request, and what it produced is what goes on the
// wire (docs/FORMAT.md §9.5).
func TestPreRequestScriptShapesTheWire(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_folder.http": "Accept: application/json\nX-Drop-Me: yes\n",
		"orders/_pre.js": `
			request.method = "put";
			request.url = "{{baseUrl}}/orders?expand=customer";
			request.headers.set("Content-Type", "application/json");
			request.headers.remove("X-Drop-Me");
			request.body = JSON.stringify({currency: "usd"});
		`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n\n{}\n",
	})

	if _, failure := sendAndWait(t, sends, "orders/create-order.http", "local"); failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}
	got := seen[0]
	if got.Method != "PUT" {
		t.Errorf("method = %q", got.Method)
	}
	if got.Path != "/orders?expand=customer" {
		t.Errorf("path = %q", got.Path)
	}
	if got.Headers.Get("X-Drop-Me") != "" {
		t.Error("the removed header was still sent")
	}
	if got.Body != `{"currency":"usd"}` {
		t.Errorf("body = %q", got.Body)
	}
}

// A secret handle on a header reaches the wire as the real value and appears
// nowhere else — the whole point of the handle.
func TestSecretHandleReachesTheWireAndNothingElse(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_pre.js": `
			request.headers.set("Authorization", secrets.ref("apiKey").prefix("Bearer "));
			console.log("header is", request.headers.get("Authorization"));
		`,
		"orders/_post.js":          `console.log("sent", request.headers.get("Authorization"));`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	meta, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}

	// The wire got the real thing.
	if got := seen[0].Headers.Get("Authorization"); got != "Bearer sk_live_SECRET" {
		t.Errorf("the wire saw %q, want the real value", got)
	}

	// Nothing Otis reports does. This is the assertion that matters.
	var everything strings.Builder
	for _, line := range meta.Console {
		everything.WriteString(line.Text + "\n")
	}
	for _, h := range meta.Request.Headers {
		everything.WriteString(h.Name + ": " + h.Value + "\n")
	}
	if strings.Contains(everything.String(), "sk_live_SECRET") {
		t.Fatalf("the secret leaked into what the window is shown:\n%s", everything.String())
	}
	if !strings.Contains(everything.String(), "[secret:apiKey]") {
		t.Errorf("no mask anywhere:\n%s", everything.String())
	}
}

// Tests run, stream, and land on the response (docs/FORMAT.md §9.9).
func TestPostResponseTests(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/create-order.http": "POST {{baseUrl}}/orders\n" +
			"\n" +
			"{}\n" +
			"\n" +
			"> {%\n" +
			"  test(\"201 created\", () => expect(response.status).toBe(201));\n" +
			"  test(\"two line items\", () => expect(response.json().line_items).toHaveLength(2));\n" +
			"  test(\"this fails\", () => expect(response.status).toBe(500));\n" +
			"%}\n",
	})

	meta, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}
	if len(meta.Tests) != 3 {
		t.Fatalf("tests = %+v", meta.Tests)
	}
	if !meta.Tests[0].Passed || !meta.Tests[1].Passed || meta.Tests[2].Passed {
		t.Errorf("verdicts = %+v", meta.Tests)
	}
	// A failing test does not fail the send: the response arrived.
	if meta.StatusCode != 201 {
		t.Errorf("status = %d, want the response kept", meta.StatusCode)
	}
	if meta.ScriptError != nil {
		t.Errorf("ScriptError = %+v, want none: a failing test is not a script error", meta.ScriptError)
	}
}

// A post-response script that throws does not discard the response
// (docs/FORMAT.md §9.10).
func TestPostResponseScriptErrorKeepsTheResponse(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_post.js":          "const a = 1;\nnope();\n",
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	meta, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("a post-response throw must not fail the send: %s", failure.Message)
	}
	if meta.StatusCode != 201 {
		t.Errorf("status = %d, want the response kept", meta.StatusCode)
	}
	if meta.ScriptError == nil {
		t.Fatal("no ScriptError")
	}
	if meta.ScriptError.Path != "orders/_post.js" || meta.ScriptError.Line != 2 {
		t.Errorf("ScriptError = %+v, want the file and line", meta.ScriptError)
	}
	if meta.ScriptError.Phase != string(script.Post) {
		t.Errorf("Phase = %q", meta.ScriptError.Phase)
	}
}

// A pre-request script that throws fails the send: there is nothing to send.
func TestPreRequestScriptErrorFailsTheSend(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_pre.js":           "nope();\n",
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	_, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure == nil {
		t.Fatal("a pre-request throw should fail the send")
	}
	if failure.Kind != FailScript {
		t.Errorf("kind = %q, want %q", failure.Kind, FailScript)
	}
	if !strings.Contains(failure.Detail, "orders/_pre.js:1") {
		t.Errorf("detail = %q, want the file and line", failure.Detail)
	}
	if len(seen) != 0 {
		t.Error("the request was sent despite the pre-request failure")
	}
}

// vars.session.set writes the session store, keyed by the request's folder,
// with the provenance §4.5 requires.
func TestScriptSetsASessionVariable(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_post.js": `
			if (response.status === 201) vars.session.set("orderId", response.json().id);
		`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	if _, failure := sendAndWait(t, sends, "orders/create-order.http", "local"); failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}

	values := sends.SessionScope(resolve.SessionFolder, "orders")
	if len(values) != 1 {
		t.Fatalf("session = %+v, want one value for orders/", values)
	}
	got := values[0]
	if got.Name != "orderId" || got.Value != "ord_1" {
		t.Errorf("session value = %+v", got)
	}
	// Provenance is the whole account of a value that is in no file.
	if got.Origin != "orders/create-order.http" || got.At.IsZero() {
		t.Errorf("provenance = %+v", got)
	}
}

// A session variable one request sets resolves in the next, which is the
// sequence the design's README describes.
func TestASessionVariableReachesTheNextRequest(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_post.js": `
			if (response.status === 201 && request.path.indexOf("create") >= 0)
				vars.session.set("orderId", response.json().id);
		`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
		"orders/get-order.http":    "POST {{baseUrl}}/orders/{{orderId}}\n",
	})

	if _, failure := sendAndWait(t, sends, "orders/create-order.http", "local"); failure != nil {
		t.Fatalf("create failed: %s — %s", failure.Message, failure.Detail)
	}
	if _, failure := sendAndWait(t, sends, "orders/get-order.http", "local"); failure != nil {
		t.Fatalf("get failed: %s — %s", failure.Message, failure.Detail)
	}
	if len(seen) != 2 {
		t.Fatalf("the server saw %d requests", len(seen))
	}
	if seen[1].Path != "/orders/ord_1" {
		t.Errorf("the second request went to %q, want the id the first one set", seen[1].Path)
	}
}

// vars.env.set writes the committed environment file — the one call in the
// API that changes a file somebody will review (docs/FORMAT.md §9.4).
func TestScriptWritesTheEnvironmentFile(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, root, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_post.js":          `vars.env.set("cursor", response.json().id);`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})

	if _, failure := sendAndWait(t, sends, "orders/create-order.http", "local"); failure != nil {
		t.Fatalf("the send failed: %s — %s", failure.Message, failure.Detail)
	}

	env, err := resolve.LoadEnvironment(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Values["cursor"].Value; got != "ord_1" {
		t.Errorf("env cursor = %q, want it written to the file", got)
	}
	// The write goes through the guard, or the watcher reports Otis' own
	// write as somebody else's change.
	target := filepath.Join(root, "env", "local.json")
	if !sends.collections.Guard().Suppressed(target) {
		t.Error("the environment write did not hold the write guard")
	}
	// And a secret is not overwritable with a plain value.
	sends2, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_post.js":          `vars.env.set("apiKey", "plain");`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})
	meta, failure := sendAndWait(t, sends2, "orders/create-order.http", "local")
	if failure != nil {
		t.Fatalf("the send failed: %s", failure.Message)
	}
	if meta.ScriptError == nil || !strings.Contains(meta.ScriptError.Message, "secret") {
		t.Errorf("ScriptError = %+v, want it to refuse overwriting a secret", meta.ScriptError)
	}
}

// A module resolves inside the collection and nowhere else, and one outside
// it cannot be read whatever the specifier says.
func TestModuleImportsAreConfinedInASend(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, root, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_pre.js":           `import { x } from "../../outside.js";` + "\n" + `vars.request.set("x", x);`,
		"orders/create-order.http": "POST {{baseUrl}}/orders\n",
	})
	// A real file just outside the collection, so the refusal is about the
	// rule and not about the file being absent.
	write(t, filepath.Join(filepath.Dir(root), "outside.js"), `export const x = "escaped";`)

	_, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	if failure == nil {
		t.Fatal("importing from outside the collection should fail the send")
	}
	if !strings.Contains(failure.Detail, "outside the collection") {
		t.Errorf("detail = %q, want it to say why", failure.Detail)
	}
}

// A script that will not stop is stopped, and the send fails with a message
// rather than hanging.
func TestScriptTimeoutFailsTheSend(t *testing.T) {
	var seen []sent
	server := echo(t, &seen)
	sends, _, _ := scriptCollection(t, server.URL, map[string]string{
		"orders/_pre.js":           `while (true) {}`,
		"orders/create-order.http": "# @script-timeout 0.2\nPOST {{baseUrl}}/orders\n",
	})

	started := time.Now()
	_, failure := sendAndWait(t, sends, "orders/create-order.http", "local")
	elapsed := time.Since(started)

	if failure == nil {
		t.Fatal("an infinite loop should fail the send")
	}
	if failure.Kind != FailScript {
		t.Errorf("kind = %q, want %q", failure.Kind, FailScript)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %s; the @script-timeout was not honoured", elapsed)
	}
	if len(seen) != 0 {
		t.Error("the request was sent anyway")
	}
}
