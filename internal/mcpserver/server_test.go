package mcpserver

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/resolve"
)

// theSecret is what the fake source resolves a credential to. Every read test
// asserts it does not come back.
const theSecret = "sk-live-must-not-reach-an-agent"

// fakeSource stands in for internal/services. It returns values that carry the
// secret, so the masking under test is masking something real rather than an
// already-clean payload.
type fakeSource struct {
	mu       sync.Mutex
	dir      string
	noDir    bool
	calls    []string
	failWith error
}

func (f *fakeSource) redactor() *mcp.Redactor {
	x := &resolve.Expander{Secrets: []string{theSecret}}
	return mcp.NewRedactor(x.Mask)
}

func (f *fakeSource) called(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeSource) Collection() (string, bool) {
	if f.noDir {
		return "", false
	}
	return f.dir, true
}

func (f *fakeSource) ListRequests(folder string, includeFolders bool) (Tree, *mcp.Redactor, error) {
	f.called("list_requests")
	if f.failWith != nil {
		return Tree{}, f.redactor(), f.failWith
	}
	tree := Tree{Requests: []TreeRequest{
		{Path: "orders/create-order.http", Name: "Create order", Method: "POST", Folder: "orders"},
	}}
	if includeFolders {
		tree.Folders = []TreeFolder{{Path: "orders", Name: "orders", HasSettings: true}}
	}
	return tree, f.redactor(), nil
}

func (f *fakeSource) GetRequest(path, environment string) (RequestView, *mcp.Redactor, error) {
	f.called("get_request")
	return RequestView{
		Path:   path,
		Name:   "Create order",
		Method: "POST",
		URL:    "https://api.acme.com/v2/orders",
		Headers: []HeaderView{
			// The case that matters: the effective value is the credential.
			{Name: "Authorization", Value: "Bearer " + theSecret, Source: "_folder.http", Inherited: true, Secret: true},
			{Name: "Content-Type", Value: "application/json", Source: path},
		},
		Auth: AuthView{Kind: "bearer", Source: "_folder.http", Secret: true},
		Body: BodyView{Kind: "json", ContentType: "application/json", Preview: `{"sku":"A-1"}`},
		Variables: []VariableView{
			{Name: "apiKey", Origin: "keychain", Secret: true},
			{Name: "host", Resolved: "api.acme.com", Origin: "env/production.json"},
		},
		Warnings: []string{},
		// A .http file that carries a literal credential somebody committed.
		Source:    "# @auth bearer " + theSecret + "\nPOST https://api.acme.com/v2/orders\n",
		GitStatus: "clean",
	}, f.redactor(), nil
}

func (f *fakeSource) ListEnvironments() ([]EnvironmentView, *mcp.Redactor, error) {
	f.called("list_environments")
	env := EnvironmentView{Name: "production", Active: true, ConfirmBeforeSend: true, Agents: "confirm"}
	env.Variables = []struct {
		Name   string `json:"name"`
		Secret bool   `json:"secret"`
	}{{Name: "apiKey", Secret: true}, {Name: "host", Secret: false}}
	return []EnvironmentView{env}, f.redactor(), nil
}

func (f *fakeSource) SessionVariables(folder string) ([]SessionVariable, *mcp.Redactor, error) {
	f.called("get_session_variables")
	return []SessionVariable{
		{Name: "orderId", Value: "7", Scope: "folder", Owner: "orders"},
		{Name: "token", Scope: "folder", Owner: "orders", Secret: true},
	}, f.redactor(), nil
}

func (f *fakeSource) LastResponse(sendID string, offset, limit int) (ResponseView, *mcp.Redactor, error) {
	f.called("get_last_response")
	view := ResponseView{SendID: "s_1", Status: 201, StatusText: "Created", Size: 42}
	view.Body = BodyPage{
		// The echo case: the server handed the credential back.
		Lines:  []string{"{", `  "echoed": "Bearer ` + theSecret + `"`, "}"},
		Offset: offset,
		Total:  3,
	}
	return view, f.redactor(), nil
}

func (f *fakeSource) TestResults(sendID string) (TestsView, *mcp.Redactor, error) {
	f.called("get_test_results")
	return TestsView{SendID: "s_1", Passed: 1, Failed: 0,
		Tests: []TestView{{Name: "status is 201", OK: true}}}, f.redactor(), nil
}

// fakeGrants is the app's switches.
type fakeGrants struct {
	mu      sync.Mutex
	grants  mcp.Grants
	revoked int
}

func (g *fakeGrants) Grants() mcp.Grants {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.grants
}

func (g *fakeGrants) RevokeAll() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants = mcp.Grants{}
	g.revoked++
	return nil
}

// serverFixture starts a real listener with a real audit log in a temp dir.
func serverFixture(t *testing.T, grants mcp.Grants) (*Server, *fakeSource, *fakeGrants, string) {
	t.Helper()
	dir := t.TempDir()
	source := &fakeSource{dir: filepath.Join(dir, "acme-api")}
	grantor := &fakeGrants{grants: grants}
	auditPath := filepath.Join(dir, "mcp-audit.jsonl")
	endpointPath := filepath.Join(dir, "mcp.json")

	s, err := New(Options{
		Source:       source,
		Grants:       grantor,
		Audit:        mcp.NewLog(auditPath),
		EndpointPath: endpointPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, source, grantor, auditPath
}

// connect returns an initialized MCP client over the real loopback listener.
func connect(t *testing.T, endpoint mcp.Endpoint, token string) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewStreamableHttpClient(endpoint.URL,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("starting the client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "test-client", Version: "1.0"},
		},
	}); err != nil {
		t.Fatalf("initializing: %v", err)
	}
	return c
}

// callTool runs a tool and returns its text payload plus whether it errored.
func callTool(t *testing.T, c *mcpclient.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(mcpgo.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

func allGranted() mcp.Grants {
	return mcp.Grants{Read: true, Run: true, Write: true, AlwaysConfirmSends: true}
}

// The spine, end to end: a real client over the real loopback listener with
// the real token, calling every READ tool.
func TestAnAgentCanReadTheCollection(t *testing.T) {
	s, source, _, _ := serverFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]mcpgo.Tool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = tool
	}
	for _, want := range []string{"list_requests", "get_request", "list_environments",
		"get_session_variables", "get_last_response", "get_test_results"} {
		if _, ok := names[want]; !ok {
			t.Errorf("%s is not offered", want)
		}
	}

	out, isErr := callTool(t, c, "list_requests", map[string]any{"includeFolders": true})
	if isErr {
		t.Fatalf("list_requests failed: %s", out)
	}
	var tree Tree
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatalf("list_requests did not return JSON: %v\n%s", err, out)
	}
	if len(tree.Requests) != 1 || tree.Requests[0].Path != "orders/create-order.http" {
		t.Errorf("unexpected tree: %+v", tree)
	}
	if len(tree.Folders) != 1 {
		t.Errorf("includeFolders was not passed through: %+v", tree)
	}

	// Every tool answers.
	for _, name := range []string{"list_environments", "get_session_variables",
		"get_last_response", "get_test_results"} {
		if out, isErr := callTool(t, c, name, nil); isErr {
			t.Errorf("%s failed: %s", name, out)
		}
	}
	if out, isErr := callTool(t, c, "get_request", map[string]any{"path": "orders/create-order.http"}); isErr {
		t.Errorf("get_request failed: %s", out)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.calls) != 6 {
		t.Errorf("the source saw %v", source.calls)
	}
}

// The guarantee, over the wire: no read hands an agent a secret value.
//
// Split by *why* each tool is safe, because the two reasons need different
// assertions and one of them would be vacuous under the other's. get_request
// and get_last_response carry resolved content, so the fake embeds the secret
// in them and the mask has to be visible in the output — otherwise "the
// secret is absent" would pass on a payload that never contained it. The
// other two carry no values at all by design (§12), so what has to be checked
// there is that the value is missing rather than masked.
func TestNoReadToolLeaksASecret(t *testing.T) {
	s, _, _, _ := serverFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	// The tools that resolve, where masking is load-bearing.
	for _, tc := range []struct {
		tool, where string
		args        map[string]any
	}{
		{"get_request", "an effective header and the file's own source",
			map[string]any{"path": "orders/create-order.http"}},
		{"get_last_response", "a response body the server echoed it into", nil},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			out, isErr := callTool(t, c, tc.tool, tc.args)
			if isErr {
				t.Fatalf("%s failed: %s", tc.tool, out)
			}
			if strings.Contains(out, theSecret) {
				t.Fatalf("THE SECRET REACHED THE AGENT through %s (%s):\n%s", tc.tool, tc.where, out)
			}
			// Without this the assertion above could pass on a payload that
			// never held the secret in the first place.
			if !strings.Contains(out, resolve.MaskPlaceholder) {
				t.Errorf("%s masked nothing, so the test proves nothing:\n%s", tc.tool, out)
			}
		})
	}

	// list_environments returns names and shapes and never a value, secret or
	// not: an environment's non-secret values are still somebody's
	// infrastructure. So the check is that no value is there to mask.
	t.Run("list_environments carries no values at all", func(t *testing.T) {
		out, isErr := callTool(t, c, "list_environments", nil)
		if isErr {
			t.Fatalf("failed: %s", out)
		}
		var envs []map[string]any
		if err := json.Unmarshal([]byte(out), &envs); err != nil {
			t.Fatal(err)
		}
		for _, env := range envs {
			vars, _ := env["variables"].([]any)
			for _, v := range vars {
				fields, _ := v.(map[string]any)
				for key := range fields {
					if key != "name" && key != "secret" {
						t.Errorf("an environment variable carries %q; only name and secret may be here: %v", key, fields)
					}
				}
			}
		}
		if strings.Contains(out, theSecret) || strings.Contains(out, "api.acme.com") {
			t.Errorf("list_environments carries a value: %s", out)
		}
	})

	// A session variable's value is withheld when it is a secret, and present
	// when it is not — the second half being what makes the first meaningful.
	t.Run("get_session_variables withholds a secret value", func(t *testing.T) {
		out, isErr := callTool(t, c, "get_session_variables", nil)
		if isErr {
			t.Fatalf("failed: %s", out)
		}
		var vars []SessionVariable
		if err := json.Unmarshal([]byte(out), &vars); err != nil {
			t.Fatal(err)
		}
		var checkedSecret, checkedPlain bool
		for _, v := range vars {
			if v.Secret {
				if v.Value != "" {
					t.Errorf("the secret session variable %s carries a value %q", v.Name, v.Value)
				}
				checkedSecret = true
				continue
			}
			if v.Value == "" {
				t.Errorf("the non-secret variable %s lost its value, so withholding is not selective", v.Name)
			}
			checkedPlain = true
		}
		if !checkedSecret || !checkedPlain {
			t.Fatalf("the fixture did not exercise both cases: %+v", vars)
		}
	})
}

// All three capabilities are off by default and the app ships with the server
// off (§3), so this is the common refusal — and it has to say what to turn on.
func TestReadIsOffByDefault(t *testing.T) {
	s, source, _, _ := serverFixture(t, mcp.Grants{})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "list_requests", nil)
	if !isErr {
		t.Fatalf("a read succeeded with no capability granted: %s", out)
	}
	if !strings.Contains(out, "off") || !strings.Contains(out, "popover") {
		t.Errorf("the refusal does not say what to turn on: %s", out)
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.calls) != 0 {
		t.Errorf("the source was reached despite the refusal: %v", source.calls)
	}
}

// Without the token nothing connects at all — the refusal is at the transport,
// before any tool exists as far as the client is concerned.
func TestWithoutTheTokenNothingConnects(t *testing.T) {
	s, _, _, _ := serverFixture(t, allGranted())
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{"", "wrong", endpoint.Token + "x"} {
		c, err := mcpclient.NewStreamableHttpClient(endpoint.URL,
			transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		startErr := c.Start(ctx)
		if startErr == nil {
			_, startErr = c.Initialize(ctx, mcpgo.InitializeRequest{
				Params: mcpgo.InitializeParams{
					ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
					ClientInfo:      mcpgo.Implementation{Name: "attacker", Version: "1"},
				},
			})
		}
		if startErr == nil {
			t.Errorf("a client with token %q connected", token)
		}
		_ = c.Close()
		cancel()
	}
}

// Every call is recorded, whatever the outcome — the refused ones are the
// interesting ones.
func TestEveryCallIsAudited(t *testing.T) {
	s, _, _, auditPath := serverFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	callTool(t, c, "list_requests", nil)
	callTool(t, c, "get_request", map[string]any{"path": "orders/create-order.http"})

	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d audit lines, want 2:\n%s", len(lines), raw)
	}

	var second mcp.Entry
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second.Tool != "get_request" {
		t.Errorf("tool = %q", second.Tool)
	}
	if second.Target != "orders/create-order.http" {
		t.Errorf("target = %q, want the node path", second.Target)
	}
	if second.Decision != mcp.Allowed {
		t.Errorf("decision = %q", second.Decision)
	}
	if second.Collection != "acme-api" {
		t.Errorf("collection = %q, want the directory's name", second.Collection)
	}
	if second.Surface != mcp.NotAsked {
		t.Errorf("surface = %q; a read asks nobody", second.Surface)
	}

	// And nothing sensitive reached a line, which is §9.1's assertion made
	// against real tool calls rather than against the type alone.
	for _, forbidden := range []string{theSecret, "Bearer", "api.acme.com", `{"sku"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the audit log carries %q:\n%s", forbidden, raw)
		}
	}
}

func TestARefusalIsAuditedAsDeniedByPolicy(t *testing.T) {
	s, _, _, auditPath := serverFixture(t, mcp.Grants{})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	callTool(t, c, "list_requests", nil)

	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var entry mcp.Entry
	if err := json.Unmarshal([]byte(strings.TrimRight(string(raw), "\n")), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Decision != mcp.DeniedByPolicy {
		t.Errorf("decision = %q, want denied-by-policy", entry.Decision)
	}
	if entry.Status != "capability-off" {
		t.Errorf("status = %q", entry.Status)
	}
}

// The kill switch, checked on every one of the things §10 says it does.
func TestDisconnectIsFinal(t *testing.T) {
	s, _, grantor, _ := serverFixture(t, allGranted())
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.endpointPath); err != nil {
		t.Fatalf("the endpoint file was not written: %v", err)
	}
	// An outstanding preview, which must not survive.
	if _, err := s.intents.Issue("send_request", "orders/create-order.http", "production", "fp"); err != nil {
		t.Fatal(err)
	}

	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}

	if s.auth.Valid(endpoint.Token) {
		t.Error("the token survived Disconnect")
	}
	if n := s.intents.Outstanding(); n != 0 {
		t.Errorf("%d previews survived Disconnect", n)
	}
	if s.Running() {
		t.Error("the listener survived Disconnect")
	}
	if _, err := os.Stat(s.endpointPath); !os.IsNotExist(err) {
		t.Error("the endpoint file survived Disconnect")
	}
	if got := grantor.Grants(); got.Read || got.Run || got.Write {
		t.Errorf("capabilities survived Disconnect: %+v — a reconnect would be enough", got)
	}
	if grantor.revoked != 1 {
		t.Errorf("RevokeAll was called %d times, want 1", grantor.revoked)
	}
}

// The rate limiter, over the wire.
func TestAFloodIsRateLimited(t *testing.T) {
	s, _, _, auditPath := serverFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	refused := 0
	for i := 0; i < 40; i++ {
		if out, isErr := callTool(t, c, "get_test_results", nil); isErr {
			if !strings.Contains(out, "rate-limit") && !strings.Contains(out, "Too many") {
				t.Fatalf("an unexpected failure: %s", out)
			}
			refused++
		}
	}
	if refused == 0 {
		t.Error("40 rapid reads were all allowed; READ bursts at 30")
	}

	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"rate-limited"`) {
		t.Error("a rate-limited call was not audited as such")
	}
}

// Every tool is scoped to the open collection, and that scope is what bounds
// them all — so with none open, nothing runs.
func TestNothingWorksWithNoCollectionOpen(t *testing.T) {
	s, source, _, _ := serverFixture(t, allGranted())
	source.noDir = true
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "list_requests", nil)
	if !isErr {
		t.Fatalf("a tool ran with no collection open: %s", out)
	}
	if !strings.Contains(out, "No collection") {
		t.Errorf("the refusal is unclear: %s", out)
	}
}

// The annotations are a hint to the client, never a gate — but a client that
// shows a person what a tool does should show something true.
func TestEveryReadToolIsAnnotatedReadOnly(t *testing.T) {
	s, _, _, _ := serverFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools were offered")
	}
	for _, tool := range tools.Tools {
		if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("%s is not annotated non-destructive", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("%s is not annotated closed-world", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
}

// The listener is on the loopback interface and the endpoint says so.
func TestTheListenerIsLoopback(t *testing.T) {
	s, _, _, _ := serverFixture(t, allGranted())
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(endpoint.URL, "http://127.0.0.1:") {
		t.Errorf("URL = %q, want a 127.0.0.1 address", endpoint.URL)
	}
	if s.Port() == 0 {
		t.Error("no port was assigned")
	}
	// Port 0 means OS-assigned, so two servers never collide and the port is
	// not something an attacker can assume.
	other, _, _, _ := serverFixture(t, allGranted())
	otherEndpoint, err := other.Start()
	if err != nil {
		t.Fatal(err)
	}
	if otherEndpoint.Port == endpoint.Port {
		t.Error("two servers were assigned the same port")
	}
}

// A server that cannot record is not one this package will start: an
// unrecorded agent is what §9 exists to prevent.
func TestAServerWithoutAnAuditLogDoesNotStart(t *testing.T) {
	_, err := New(Options{Source: &fakeSource{}, Grants: &fakeGrants{}})
	if err == nil {
		t.Fatal("a server was built with no audit log")
	}
	if !strings.Contains(err.Error(), "audit") {
		t.Errorf("the error does not name the reason: %v", err)
	}
}

// mcp-go offers result constructors that marshal the value themselves, which
// would go straight around Redactor.Marshal. Nothing in this package may call
// one.
//
// This walks the AST rather than searching the text, because the text of this
// package includes the doc comment in tools.go warning against exactly these
// names — a grep finds its own warning and reports a violation that is not
// there. Matching real call expressions is both precise and immune to that.
func TestNoToolBypassesRedaction(t *testing.T) {
	forbidden := map[string]bool{
		"NewToolResultStructured":     true,
		"NewToolResultStructuredOnly": true,
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no package was parsed, so this test checks nothing")
	}

	checked := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			checked++
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !forbidden[sel.Sel.Name] {
					return true
				}
				t.Errorf("%s:%d calls %s, which marshals the value itself and bypasses "+
					"Redactor.Marshal. Return the value and its redactor from the handler; "+
					"tools.go is the only place a result is serialized.",
					filepath.Base(name), fset.Position(call.Pos()).Line, sel.Sel.Name)
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no files were inspected")
	}

	// The guard is only worth having if it would fire, so prove the matcher
	// works on a call it should catch.
	probe, err := parser.ParseFile(fset, "probe.go",
		"package p\nfunc f() { mcpgo.NewToolResultStructured(nil, \"\") }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(probe, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && forbidden[sel.Sel.Name] {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Error("the matcher does not detect the call it exists to detect")
	}
}
