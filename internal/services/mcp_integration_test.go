package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/mcpserver"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// The whole feature, wired as the app wires it (docs/MCP.md §15).
//
// Everything below the MCP server is real: a real collection on disk, the real
// CollectionService, the real SendService, the real environment and git
// services, a real loopback listener, and mcp-go's own client. The only things
// stubbed are Wails' event bus and "is a window open", because there is no
// window in a test — and those are exactly the two the service already has
// seams for.
//
// What this catches that internal/mcpserver's own tests cannot: the wiring.
// Those tests use fakes for the four boundaries, so they prove the *policy* is
// right; this proves the boundaries are plugged into the services that own the
// work.

type agentFixture struct {
	t        *testing.T
	root     string
	service  *MCPService
	sends    *SendService
	endpoint mcp.Endpoint

	mu       sync.Mutex
	confirms []MCPConfirmation
	answer   bool
}

// agentApp builds the service over a real collection.
func agentApp(t *testing.T, files map[string]string, store secrets.Store) *agentFixture {
	t.Helper()
	// The endpoint file and the audit log live in the OS config directory, so
	// without this the tests would write to — and Disconnect would delete —
	// the developer's real ~/…/otis/mcp.json, and any Otis actually running
	// would have its endpoint pulled out from under it. Pointing HOME at a
	// temp directory contains both, and exercises the real path resolution
	// rather than an injected seam.
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(configHome, ".config"))
	t.Setenv("AppData", filepath.Join(configHome, "AppData", "Roaming"))
	if path, err := mcp.DefaultEndpointPath(); err != nil || !strings.HasPrefix(path, configHome) {
		t.Skipf("os.UserConfigDir does not follow the environment here: %v %v", path, err)
	}

	root := t.TempDir()
	for rel, content := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}

	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	settingsStore := settings.NewStore(settingsPath)
	collections := NewCollectionService(settingsStore)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}

	sends := NewSendService(collections, store)
	t.Cleanup(sends.cancelAll)
	requests := NewRequestService(collections)
	folders := NewFolderService(collections, sends)
	environments := NewEnvironmentService(collections, settingsStore, store)
	gitState := NewGitService(collections)

	fixture := &agentFixture{t: t, root: root, answer: true}
	service := NewMCPService(settingsStore, collections, sends, requests, folders, environments, gitState)
	service.windowPresent = func() bool { return true }
	service.emitter = func(name string, data any) {
		if name != events.MCPConfirm {
			return
		}
		if confirmation, ok := data.(MCPConfirmation); ok {
			fixture.mu.Lock()
			fixture.confirms = append(fixture.confirms, confirmation)
			answer := fixture.answer
			id := confirmation.ID
			fixture.mu.Unlock()
			// The window's reply, on its own goroutine because AskInWindow is
			// blocking on it.
			go func() { _ = service.Answer(id, answer) }()
		}
	}
	fixture.service, fixture.sends = service, sends
	t.Cleanup(func() { _ = service.stop() })
	return fixture
}

// enable turns the server and the named capabilities on, and connects.
func (f *agentFixture) connect(caps ...mcp.Capability) *mcpclient.Client {
	f.t.Helper()
	if _, err := f.service.SetEnabled(true); err != nil {
		f.t.Fatal(err)
	}
	for _, capability := range caps {
		if _, err := f.service.SetCapability(string(capability), true); err != nil {
			f.t.Fatalf("granting %s: %v", capability, err)
		}
	}
	path, err := mcp.DefaultEndpointPath()
	if err != nil {
		f.t.Skipf("no config directory: %v", err)
	}
	endpoint, err := mcp.ReadEndpoint(path)
	if err != nil {
		f.t.Fatalf("the endpoint file was not written: %v", err)
	}
	f.endpoint = endpoint

	client, err := mcpclient.NewStreamableHttpClient(endpoint.URL,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + endpoint.Token}))
	if err != nil {
		f.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	f.t.Cleanup(cancel)
	if err := client.Start(ctx); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Initialize(ctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "integration-test", Version: "1.0"},
		},
	}); err != nil {
		f.t.Fatal(err)
	}
	return client
}

func (f *agentFixture) call(client *mcpclient.Client, name string, args map[string]any) (string, bool) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := client.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		f.t.Fatalf("calling %s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(mcpgo.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

func (f *agentFixture) asked() []MCPConfirmation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MCPConfirmation{}, f.confirms...)
}

// A fresh install exposes nothing: the listener is off and so are all three
// capabilities, so there is nothing to connect to and nothing to allow.
func TestTheShippedStateExposesNothing(t *testing.T) {
	f := agentApp(t, map[string]string{"get.http": "GET http://127.0.0.1:1/x\n"}, secrets.NewMemory())

	status, err := f.service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Running || status.Read || status.Run || status.Write {
		t.Errorf("a fresh install exposes something: %+v", status)
	}
	if !status.AlwaysConfirmSends {
		t.Error("alwaysConfirmSends is off out of the box")
	}
	if !status.PersistAuditLog {
		t.Error("the audit log is off out of the box")
	}
	// And no endpoint file, so no client can find it.
	if path, err := mcp.DefaultEndpointPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			t.Error("an endpoint file exists with the server off")
		}
	}
}

// An agent reads the real collection, through the real services.
func TestAnAgentReadsTheRealCollection(t *testing.T) {
	f := agentApp(t, map[string]string{
		// The auth is on the folder, not the root, so both the tree's
		// hasSettings and the header's provenance are about the same file.
		"orders/_folder.http": "# @auth bearer {{apiKey}}\n",
		"orders/create.http":  "# @name Create order\nPOST http://127.0.0.1:1/orders\nContent-Type: application/json\n\n{\"sku\":\"A-1\"}\n",
		"env/dev.json":        `{"host":"127.0.0.1","apiKey":{"$secret":"keychain"}}`,
	}, secrets.NewMemory())
	client := f.connect(mcp.CapRead)

	out, isErr := f.call(client, "list_requests", map[string]any{"includeFolders": true})
	if isErr {
		t.Fatalf("list_requests failed: %s", out)
	}
	var tree mcpserver.Tree
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Requests) != 1 || tree.Requests[0].Path != "orders/create.http" {
		t.Fatalf("unexpected tree: %+v", tree.Requests)
	}
	if tree.Requests[0].Name != "Create order" || tree.Requests[0].Method != "POST" {
		t.Errorf("the tree lost the name or method: %+v", tree.Requests[0])
	}
	if len(tree.Folders) != 1 {
		t.Fatalf("got %d folders, want the one: %+v", len(tree.Folders), tree.Folders)
	}
	// `_folder.http` is settings and not a tree row (docs/FORMAT.md §2.1), so
	// it hangs off its folder — and an agent is told it is there, because it
	// is the one thing about a folder that changes what its requests send.
	if !tree.Folders[0].HasSettings {
		t.Errorf("the folder's _folder.http was not reported: %+v", tree.Folders[0])
	}

	out, isErr = f.call(client, "get_request", map[string]any{
		"path": "orders/create.http", "environment": "dev"})
	if isErr {
		t.Fatalf("get_request failed: %s", out)
	}
	var view mcpserver.RequestView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	// The source is the real file's bytes, which is what makes
	// update_request a read-modify-write rather than a reconstruction.
	if !strings.Contains(view.Source, "# @name Create order") {
		t.Errorf("source is not the file's text: %q", view.Source)
	}
	if view.Method != "POST" {
		t.Errorf("method = %q", view.Method)
	}
	// The inherited auth header is described, and its provenance names the
	// folder file it came from.
	var sawAuth bool
	for _, header := range view.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			sawAuth = true
			if !header.Inherited || !strings.Contains(header.Source, "_folder.http") {
				t.Errorf("the inherited auth header has no provenance: %+v", header)
			}
		}
	}
	if !sawAuth && view.Auth.Kind == "" {
		t.Error("neither an Authorization header nor an auth directive was reported")
	}
	// git is not initialised here, so the honest answer is "no repository" —
	// a normal state, not an error.
	if view.GitStatus != "no-repository" {
		t.Errorf("gitStatus = %q, want no-repository", view.GitStatus)
	}

	out, isErr = f.call(client, "list_environments", nil)
	if isErr {
		t.Fatalf("list_environments failed: %s", out)
	}
	if !strings.Contains(out, "dev") {
		t.Errorf("the environment is missing: %s", out)
	}
	// Never a value, secret or not.
	if strings.Contains(out, "127.0.0.1") {
		t.Errorf("list_environments carried a value: %s", out)
	}
}

// §15 test 5, through the whole app: a secret in a response body an endpoint
// echoed back must not reach an agent.
func TestASecretCannotReachAnAgentThroughTheRealServer(t *testing.T) {
	const value = "sk-live-integration-must-not-leak"
	server := echoServer(t)

	store := secrets.NewMemory()
	f := agentApp(t, map[string]string{
		"_folder.http": "# @auth bearer {{apiKey}}\n",
		"get.http":     "GET " + server + "/me\n",
		"env/dev.json": `{"apiKey":{"$secret":"keychain"}}`,
	}, store)
	if err := store.Set(secrets.Key(filepath.Base(f.root), "dev", "apiKey"), value); err != nil {
		t.Fatal(err)
	}
	// The environment has to be active for the send to resolve against it,
	// the same way the title strip makes it active.
	f.activate(t, "dev")

	client := f.connect(mcp.CapRead, mcp.CapRun)

	out, isErr := f.call(client, "send_request", map[string]any{"path": "get.http"})
	if isErr {
		t.Fatalf("the preview failed: %s", out)
	}
	var preview mcpserver.SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.UsesSecret {
		t.Error("the preview does not say a secret is involved")
	}
	if len(preview.Secrets) == 0 {
		t.Error("the preview does not name the secret")
	}
	if strings.Contains(out, value) {
		t.Fatalf("THE SECRET REACHED THE AGENT in a preview: %s", out)
	}

	out, isErr = f.call(client, "send_request", map[string]any{
		"path": "get.http", "intent": preview.Intent})
	if isErr {
		t.Fatalf("the send failed: %s", out)
	}
	if strings.Contains(out, value) {
		t.Fatalf("THE SECRET REACHED THE AGENT in a send result: %s", out)
	}

	// And through the response body, which is where an echoing endpoint puts
	// it and the one place Otis cannot mask in advance.
	out, isErr = f.call(client, "get_last_response", nil)
	if isErr {
		t.Fatalf("get_last_response failed: %s", out)
	}
	if strings.Contains(out, value) {
		t.Fatalf("THE SECRET REACHED THE AGENT in a response body: %s", out)
	}
	if !strings.Contains(out, "•••••") {
		t.Fatalf("nothing was masked, so this proves nothing: %s", out)
	}

	// A person was asked, in the window, and the confirmation named the
	// secret without carrying it.
	asked := f.asked()
	if len(asked) != 1 {
		t.Fatalf("a person was asked %d times, want once", len(asked))
	}
	blob, _ := json.Marshal(asked[0])
	if strings.Contains(string(blob), value) {
		t.Fatalf("the confirmation carried the secret: %s", blob)
	}
	if len(asked[0].Secrets) == 0 {
		t.Error("the confirmation does not name the secret")
	}
}

// A refusal in the window stops the send, and the agent is told why.
func TestRefusingInTheWindowStopsTheSend(t *testing.T) {
	server := echoServer(t)
	f := agentApp(t, map[string]string{"get.http": "GET " + server + "/me\n"}, secrets.NewMemory())
	f.mu.Lock()
	f.answer = false
	f.mu.Unlock()

	client := f.connect(mcp.CapRun)
	out, _ := f.call(client, "send_request", map[string]any{"path": "get.http"})
	var preview mcpserver.SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	out, isErr := f.call(client, "send_request", map[string]any{
		"path": "get.http", "intent": preview.Intent})
	if !isErr {
		t.Fatalf("a refused send succeeded: %s", out)
	}
	if !strings.Contains(out, "refused") {
		t.Errorf("the agent is not told it was refused: %s", out)
	}
	// Nothing was sent, so nothing is held.
	if id := f.sends.newestSendID(); id != "" {
		t.Errorf("a response is held for a refused send: %s", id)
	}
}

// The WRITE tools go through the real services, so the invariants hold: the
// file is named for the slug, `.order` is untouched, and a new folder gets a
// _folder.http.
func TestAnAgentsWritesGoThroughTheRealServices(t *testing.T) {
	f := agentApp(t, map[string]string{
		"orders/.order":      "# hand written\ncreate\n",
		"orders/create.http": "GET http://127.0.0.1:1/a\n",
	}, secrets.NewMemory())
	gitInit(t, f.root)

	orderBefore, err := os.ReadFile(filepath.Join(f.root, "orders", ".order"))
	if err != nil {
		t.Fatal(err)
	}

	client := f.connect(mcp.CapRead, mcp.CapWrite)

	out, isErr := f.call(client, "create_request", map[string]any{
		"folder": "orders", "name": "Cancel order"})
	if isErr {
		t.Fatalf("create_request failed: %s", out)
	}
	var created mcpserver.Created
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	if created.Path != "orders/cancel-order.http" {
		t.Errorf("path = %q, want the slug Go chose", created.Path)
	}
	if _, err := os.Stat(filepath.Join(f.root, "orders", "cancel-order.http")); err != nil {
		t.Errorf("the file was not written: %v", err)
	}
	// The invariant that has its own test in internal/services, holding for
	// an agent's write too: adding a request does not touch `.order`.
	orderAfter, err := os.ReadFile(filepath.Join(f.root, "orders", ".order"))
	if err != nil {
		t.Fatal(err)
	}
	if string(orderAfter) != string(orderBefore) {
		t.Errorf(".order was rewritten by an agent's create:\n%q\n%q", orderBefore, orderAfter)
	}

	out, isErr = f.call(client, "create_folder", map[string]any{
		"parent": "", "name": "Payment methods"})
	if isErr {
		t.Fatalf("create_folder failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(f.root, "payment-methods", "_folder.http")); err != nil {
		t.Errorf("a new folder has no _folder.http, so git will not track it: %v", err)
	}

	// update_request refuses text that does not parse, and leaves the file.
	before, err := os.ReadFile(filepath.Join(f.root, "orders", "create.http"))
	if err != nil {
		t.Fatal(err)
	}
	if out, isErr := f.call(client, "update_request", map[string]any{
		"path": "orders/create.http", "text": "###\n###\nnot a request line at all\n"}); !isErr {
		t.Errorf("unparseable text was accepted: %s", out)
	}
	after, err := os.ReadFile(filepath.Join(f.root, "orders", "create.http"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("a refused update changed the file")
	}

	// And a real edit lands.
	if out, isErr := f.call(client, "update_request", map[string]any{
		"path": "orders/create.http", "text": "# @name Create\nPOST http://127.0.0.1:1/a\n"}); isErr {
		t.Fatalf("update_request failed: %s", out)
	}
	edited, err := os.ReadFile(filepath.Join(f.root, "orders", "create.http"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(edited), "@name Create") {
		t.Errorf("the edit did not land: %q", edited)
	}
}

// WRITE cannot be granted without git, because the review gate is git status
// and WRITE is only safe because of that gate (§3).
func TestWriteCannotBeGrantedWithoutGit(t *testing.T) {
	f := agentApp(t, map[string]string{"get.http": "GET http://127.0.0.1:1/x\n"}, secrets.NewMemory())
	if _, err := f.service.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.WriteBlocked == "" {
		t.Fatal("WRITE is offered in a collection that is not a git repository")
	}
	if _, err := f.service.SetCapability("write", true); err == nil {
		t.Fatal("WRITE was granted with no git")
	}
	status, err = f.service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Write {
		t.Error("WRITE ended up on anyway")
	}
}

// The kill switch, through the service: the token stops working, the endpoint
// file goes, and every capability is off so reconnecting is not enough.
func TestDisconnectThroughTheService(t *testing.T) {
	f := agentApp(t, map[string]string{"get.http": "GET http://127.0.0.1:1/x\n"}, secrets.NewMemory())
	f.connect(mcp.CapRead, mcp.CapRun)
	endpointPath, err := mcp.DefaultEndpointPath()
	if err != nil {
		t.Skip(err)
	}

	if err := f.service.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(endpointPath); !os.IsNotExist(err) {
		t.Error("the endpoint file survived Disconnect")
	}
	status, err := f.service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Running || status.Read || status.Run || status.Write {
		t.Errorf("something survived Disconnect: %+v", status)
	}

	// A client with the old token cannot get back in.
	client, err := mcpclient.NewStreamableHttpClient("http://127.0.0.1:1/mcp",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + f.endpoint.Token}))
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Start(ctx); err == nil {
			if _, err := client.Initialize(ctx, mcpgo.InitializeRequest{}); err == nil {
				t.Error("the old token still works")
			}
		}
		_ = client.Close()
	}
}

// Closing the collection disconnects: every tool is scoped to the open
// collection, so a server left running across a close would have no scope, and
// the next collection is not the one anybody granted anything for.
func TestClosingTheCollectionDisconnects(t *testing.T) {
	f := agentApp(t, map[string]string{"get.http": "GET http://127.0.0.1:1/x\n"}, secrets.NewMemory())
	f.connect(mcp.CapRead)

	if err := f.service.collections.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.Read {
		t.Errorf("the server survived the collection closing: %+v", status)
	}
}

// --- fixtures -----------------------------------------------------------

// echoServer is an endpoint that hands the request's Authorization header back
// in its own body.
//
// That is the shape §15 test 5 needs, and the reason a response body has to be
// masked at all: no amount of care taken over the *request* stops a server
// returning the credential it was given.
func echoServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"youSent": r.Header.Get("Authorization"),
		})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// activate makes an environment the active one, which is a per-machine
// setting and so lives in the settings file (docs/FORMAT.md §4.3).
func (f *agentFixture) activate(t *testing.T, name string) {
	t.Helper()
	current, err := f.service.settings.Get()
	if err != nil {
		t.Fatal(err)
	}
	current.ActiveEnv = name
	if err := f.service.settings.Set(current); err != nil {
		t.Fatal(err)
	}
}

// gitInit makes the collection a repository, so WRITE can be granted.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"add", "."}, {"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}
}

// Opening the first collection of a session must not disconnect.
//
// This is the launch order, and it is a regression test for a real bug: the
// service restores an enabled server in ServiceStartup, and the *window* then
// opens the collection. CollectionService fires its OnClose hooks from Open as
// well as from Close — opening the first collection is a change from "none" —
// so the unguarded handler tore down the server it had just started, every
// time. Only running the app found it: every test until now opened the
// collection before the service existed.
func TestOpeningTheFirstCollectionDoesNotDisconnect(t *testing.T) {
	// Isolated from the real config directory for the same reason agentApp
	// is: this one starts a listener and writes an endpoint file.
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(configHome, ".config"))
	t.Setenv("AppData", filepath.Join(configHome, "AppData", "Roaming"))
	if path, err := mcp.DefaultEndpointPath(); err != nil || !strings.HasPrefix(path, configHome) {
		t.Skipf("os.UserConfigDir does not follow the environment here: %v %v", path, err)
	}

	root := t.TempDir()
	write(t, filepath.Join(root, "get.http"), "GET http://127.0.0.1:1/x\n")

	settingsStore := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(settingsStore)
	t.Cleanup(func() { collections.stopWatching() })
	sends := NewSendService(collections, secrets.NewMemory())
	t.Cleanup(sends.cancelAll)

	service := NewMCPService(settingsStore, collections, sends,
		NewRequestService(collections), NewFolderService(collections, sends),
		NewEnvironmentService(collections, settingsStore, secrets.NewMemory()),
		NewGitService(collections))
	service.windowPresent = func() bool { return true }
	t.Cleanup(func() { _ = service.stop() })

	// The server comes up first, as it does at launch from persisted
	// settings, with no collection open yet.
	if _, err := service.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCapability("read", true); err != nil {
		t.Fatal(err)
	}

	// Then the window opens the collection.
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || !status.Running {
		t.Fatalf("opening the first collection tore the server down: %+v", status)
	}
	if !status.Read {
		t.Error("the READ capability was revoked by opening a collection")
	}
	if path, err := mcp.DefaultEndpointPath(); err == nil {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the endpoint file is gone: %v", err)
		}
	}

	// And switching to a *different* collection still disconnects, which is
	// the behaviour the guard must not have broken.
	other := t.TempDir()
	write(t, filepath.Join(other, "get.http"), "GET http://127.0.0.1:1/y\n")
	if _, err := collections.Open(other); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.Read {
		t.Errorf("switching collections did not disconnect: %+v", status)
	}
}

// --- docs/MCP.md §8.1: the session write ---------------------------------

// sessionFixture is a collection whose environment defines the host and the
// credential, and whose folder file defines one variable — the three shapes
// rule 1 has to refuse — plus two requests that chain on a name nothing
// defines, which is the shape it has to allow.
func sessionFixture(t *testing.T) *agentFixture {
	t.Helper()
	return agentApp(t, map[string]string{
		"env/qa.json":           `{"apiHost": "https://api.qa.test", "apiKey": {"$secret": "keychain"}}`,
		"flow/_folder.http":     "@stage = qa\n",
		"flow/create.http":      "POST {{apiHost}}/things\n",
		"flow/get.http":         "GET {{apiHost}}/things/{{thingId}}\n",
		"flow/sub/_folder.http": "# nested\n",
		"flow/sub/nested.http":  "GET {{apiHost}}/nested\n",
	}, secrets.NewMemory())
}

// §15.33 — rule 1 is a rule, not a dialog. Every name a session value could
// outrank is refused, each naming where it is defined, and **nobody is asked**:
// this is the case with no floor under a misread, so it never reaches a person.
func TestSessionWriteRefusesEveryNameItCouldOutrank(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapSession)

	for name, where := range map[string]string{
		"apiHost": "env/qa.json",       // the host — the redirect this rule exists for
		"apiKey":  "env/qa.json",       // the credential
		"stage":   "flow/_folder.http", // the folder's own committed variable
	} {
		out, isErr := f.call(client, "set_session_variable", map[string]any{
			"folder": "flow", "name": name, "value": "https://evil.test",
		})
		if !isErr {
			t.Errorf("%s: the set was allowed: %s", name, out)
			continue
		}
		if !strings.Contains(out, where) {
			t.Errorf("%s: refusal does not name %s: %s", name, where, out)
		}
	}
	if asked := f.asked(); len(asked) != 0 {
		t.Errorf("a refusal asked a person %d times: %+v", len(asked), asked)
	}
	// And nothing was set.
	if got := f.sends.SessionVars(); len(got) != 0 {
		t.Errorf("session = %+v, want empty", got)
	}
}

// A name above the folder is refused too — a session value at `flow/sub`
// outranks `flow/_folder.http` as well as its own.
func TestSessionWriteRefusesANameDefinedAbove(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapSession)

	out, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow/sub", "name": "stage", "value": "prod",
	})
	if !isErr || !strings.Contains(out, "flow/_folder.http") {
		t.Errorf("a name defined above was not refused: %s", out)
	}
}

// §15.34 — the name a flow actually chains on goes through, in two phases,
// and a person is asked in the window every time.
func TestSessionWriteAsksAPersonAndThenSets(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapSession)

	// Phase 1 sets nothing and asks nobody.
	preview, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_1",
	})
	if isErr {
		t.Fatalf("preview refused: %s", preview)
	}
	if len(f.sends.SessionVars()) != 0 || len(f.asked()) != 0 {
		t.Fatal("phase 1 set a value or asked a person")
	}
	// It reports the blast radius: three requests are under flow/.
	if !strings.Contains(preview, `"reaches":3`) {
		t.Errorf("preview does not report the reach: %s", preview)
	}
	intent := intentFrom(t, preview)

	// Phase 2 asks, and the person says yes.
	f.answer = true
	out, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_1", "intent": intent,
	})
	if isErr {
		t.Fatalf("the set failed: %s", out)
	}

	asked := f.asked()
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want once", len(asked))
	}
	if asked[0].Kind != mcpserver.ConfirmSession {
		t.Errorf("confirmation kind = %q, want session", asked[0].Kind)
	}
	if asked[0].Variable != "thingId" || asked[0].Value != "thing_1" || asked[0].Reaches != 3 {
		t.Errorf("the dialog does not describe the write: %+v", asked[0])
	}

	values := f.sends.SessionScope(resolve.SessionFolder, "flow")
	if len(values) != 1 || values[0].Name != "thingId" || values[0].Value != "thing_1" {
		t.Fatalf("session = %+v, want thingId=thing_1 for flow/", values)
	}
	// Provenance is the whole account of a value in no file.
	if !strings.Contains(values[0].Origin, "agent") {
		t.Errorf("origin = %q, want it to say an agent set it", values[0].Origin)
	}
}

// A refusal in the window sets nothing.
func TestSessionWriteRefusedInTheWindowSetsNothing(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapSession)

	preview, _ := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_1",
	})
	f.answer = false
	out, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_1",
		"intent": intentFrom(t, preview),
	})
	if !isErr {
		t.Errorf("a refused set reported success: %s", out)
	}
	if got := f.sends.SessionVars(); len(got) != 0 {
		t.Errorf("session = %+v, want empty after a refusal", got)
	}
}

// The tool is not offered at all without its own grant, and RUN does not
// imply it.
func TestSessionWriteNeedsItsOwnGrant(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapRun)

	out, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "x",
	})
	if !isErr {
		t.Fatalf("RUN alone allowed a session write: %s", out)
	}
	if len(f.sends.SessionVars()) != 0 {
		t.Error("a value was set without the grant")
	}
}

// §15.35 — the fingerprint binds the intent to the value, so an agent cannot
// preview a harmless set and redeem a different one.
func TestSessionIntentIsBoundToTheValue(t *testing.T) {
	f := sessionFixture(t)
	f.activate(t, "qa")
	client := f.connect(mcp.CapSession)

	preview, _ := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_1",
	})
	f.answer = true
	out, isErr := f.call(client, "set_session_variable", map[string]any{
		"folder": "flow", "name": "thingId", "value": "thing_2",
		"intent": intentFrom(t, preview),
	})
	if !isErr {
		t.Fatalf("an intent taken for one value was spent on another: %s", out)
	}
	if got := f.sends.SessionVars(); len(got) != 0 {
		t.Errorf("session = %+v, want empty", got)
	}
}

// intentFrom digs the intent out of a phase-1 result.
func intentFrom(t *testing.T, preview string) string {
	t.Helper()
	var body struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(preview), &body); err != nil || body.Intent == "" {
		t.Fatalf("no intent in the preview (%v): %s", err, preview)
	}
	return body.Intent
}
