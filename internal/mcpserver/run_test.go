package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

// fakeSender stands in for the send service. It records what was actually
// sent, which is what most of these tests assert on: the interesting question
// is not what the tool returned but whether anything left the machine.
type fakeSender struct {
	mu sync.Mutex

	method, url, environment string
	usesSecret               bool
	secretNames              []string
	review                   mcp.Review
	env                      resolve.EnvMeta
	hasEnv                   bool

	// sent is every path Send actually ran, in order.
	sent []string
	// plan is what a folder run would do.
	plan []string
	// prepareErr fails resolution.
	prepareErr error
}

func (f *fakeSender) prepared(path string) Prepared {
	return Prepared{
		Path:           path,
		Name:           "Create order",
		Method:         f.method,
		URL:            f.url,
		Environment:    f.environment,
		HasEnvironment: f.hasEnv,
		UsesSecret:     f.usesSecret,
		SecretNames:    f.secretNames,
		Review:         f.review,
		Env:            f.env,
		Print: mcp.RequestPrint{
			Method: f.method, URL: f.url, Environment: f.environment,
			Headers: []string{"Authorization: Bearer " + theSecret},
			Body:    []byte(`{"sku":"A-1"}`),
		},
	}
}

func (f *fakeSender) Prepare(path, environment string) (Prepared, *mcp.Redactor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prepareErr != nil {
		return Prepared{}, f.redactor(), f.prepareErr
	}
	return f.prepared(path), f.redactor(), nil
}

func (f *fakeSender) redactor() *mcp.Redactor {
	x := &resolve.Expander{Secrets: []string{theSecret}}
	return mcp.NewRedactor(x.Mask)
}

func (f *fakeSender) Send(ctx context.Context, path, environment string) (SendResult, *mcp.Redactor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, path)
	result := SendResult{
		SendID: "s_1", Status: 201, StatusText: "Created",
		// The response carries the credential back, as an echoing endpoint
		// would, so masking has something to do here too.
		ResolvedURL: f.url + "?token=" + theSecret,
		Size:        42,
	}
	return result, f.redactor(), nil
}

func (f *fakeSender) FolderPlan(path string) (FolderPlan, *mcp.Redactor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := FolderPlan{Path: path}
	for _, p := range f.plan {
		plan.Requests = append(plan.Requests, f.prepared(p))
	}
	return plan, f.redactor(), nil
}

func (f *fakeSender) RunFolder(ctx context.Context, path string, stopOnFailure bool, before Confirm) (RunResult, *mcp.Redactor, error) {
	f.mu.Lock()
	paths := append([]string{}, f.plan...)
	f.mu.Unlock()

	var out RunResult
	out.RunID = "r_1"
	for _, p := range paths {
		f.mu.Lock()
		prepared := f.prepared(p)
		f.mu.Unlock()
		if err := before(ctx, prepared); err != nil {
			out.Results = append(out.Results, RunItem{Path: p, Refused: true, Error: err.Error()})
			out.Summary.Refused++
			if stopOnFailure {
				break
			}
			continue
		}
		f.mu.Lock()
		f.sent = append(f.sent, p)
		f.mu.Unlock()
		out.Results = append(out.Results, RunItem{Path: p, SendID: "s_" + p, Status: 200})
		out.Summary.Sent++
	}
	return out, f.redactor(), nil
}

func (f *fakeSender) sentPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

// fakeWriter stands in for the request and folder services.
type fakeWriter struct {
	mu      sync.Mutex
	created []string
	updated []string
}

func (w *fakeWriter) CreateRequest(folder, name, text string) (Created, *mcp.Redactor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The `-2` §12 warns about: Go resolves collisions and the agent must use
	// what comes back.
	path := strings.TrimPrefix(folder+"/", "/") + "create-order-2.http"
	w.created = append(w.created, path)
	return Created{Path: path, Slug: "create-order-2"}, mcp.NoSecrets(), nil
}

func (w *fakeWriter) CreateFolder(parent, name string) (Created, *mcp.Redactor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	path := strings.TrimPrefix(parent+"/", "/") + "payments"
	w.created = append(w.created, path)
	return Created{Path: path, Slug: "payments"}, mcp.NoSecrets(), nil
}

func (w *fakeWriter) UpdateRequest(path, text string) (Updated, *mcp.Redactor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !strings.Contains(text, "http") {
		return Updated{}, mcp.NoSecrets(), errors.New("that text is not a valid .http file")
	}
	w.updated = append(w.updated, path)
	return Updated{Path: path, Status: "modified"}, mcp.NoSecrets(), nil
}

func (w *fakeWriter) UpdateDocumentation(folder, text string) (Updated, *mcp.Redactor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	path := "README.md"
	if folder != "" {
		path = folder + "/README.md"
	}
	// Nothing refuses a README: it is text, and there is no parse to fail.
	w.updated = append(w.updated, path)
	return Updated{Path: path, Status: "modified"}, mcp.NoSecrets(), nil
}

// fakeAsker is Otis' window.
type fakeAsker struct {
	mu     sync.Mutex
	open   bool
	answer bool
	delay  time.Duration
	seen   []Confirmation
}

func (a *fakeAsker) WindowOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.open
}

func (a *fakeAsker) AskInWindow(ctx context.Context, c Confirmation) (bool, error) {
	a.mu.Lock()
	a.seen = append(a.seen, c)
	delay, answer := a.delay, a.answer
	a.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return answer, nil
}

func (a *fakeAsker) asked() []Confirmation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Confirmation{}, a.seen...)
}

// elicitAnswer answers every elicitation the same way, for the client side.
type elicitAnswer struct {
	action  mcpgo.ElicitationResponseAction
	proceed bool
	delay   time.Duration

	mu   sync.Mutex
	seen []string
}

func (e *elicitAnswer) Elicit(ctx context.Context, request mcpgo.ElicitationRequest) (*mcpgo.ElicitationResult, error) {
	e.mu.Lock()
	e.seen = append(e.seen, request.Params.Message)
	e.mu.Unlock()
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &mcpgo.ElicitationResult{
		ElicitationResponse: mcpgo.ElicitationResponse{
			Action:  e.action,
			Content: map[string]any{"proceed": e.proceed},
		},
	}, nil
}

func (e *elicitAnswer) messages() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.seen...)
}

// runFixture wires a server with a sender, a writer and a window.
func runFixture(t *testing.T, grants mcp.Grants) (*Server, *fakeSender, *fakeWriter, *fakeAsker) {
	t.Helper()
	dir := t.TempDir()
	source := &fakeSource{dir: dir + "/acme-api"}
	sender := &fakeSender{
		method: "POST", url: "https://api.acme.com/v2/orders",
		environment: "staging", review: mcp.Reviewed, hasEnv: true,
	}
	writer := &fakeWriter{}
	asker := &fakeAsker{open: true, answer: true}

	s, err := New(Options{
		Source: source, Sender: sender, Writer: writer, Asker: asker,
		Grants:       &fakeGrants{grants: grants},
		Audit:        mcp.NewLog(dir + "/mcp-audit.jsonl"),
		EndpointPath: dir + "/mcp.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, sender, writer, asker
}

// connectWith is connect() plus an elicitation handler, so the client surface
// can answer.
func connectWith(t *testing.T, endpoint mcp.Endpoint, answer *elicitAnswer) *mcpclient.Client {
	t.Helper()
	options := []mcpclient.ClientOption{}
	if answer != nil {
		options = append(options, mcpclient.WithElicitationHandler(answer))
	}
	c, err := mcpclient.NewStreamableHttpClient(endpoint.URL,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + endpoint.Token}))
	if err != nil {
		t.Fatal(err)
	}
	if answer != nil {
		// The handler has to be set at construction, so the client is built
		// once with it rather than adjusted afterwards.
		c = mcpclient.NewClient(c.GetTransport(), options...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	params := mcpgo.InitializeParams{
		ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpgo.Implementation{Name: "test-client", Version: "1.0"},
	}
	if answer != nil {
		params.Capabilities.Elicitation = &mcpgo.ElicitationCapability{}
	}
	if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{Params: params}); err != nil {
		t.Fatal(err)
	}
	return c
}

// The property: there is no shape of call that sends on one tool call.
func TestNothingSendsOnOneCall(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	if isErr {
		t.Fatalf("the preview failed: %s", out)
	}
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}

	if got := sender.sentPaths(); len(got) != 0 {
		t.Fatalf("phase 1 sent %v", got)
	}
	if len(asker.asked()) != 0 {
		t.Error("phase 1 asked somebody; nobody is asked at phase 1")
	}
	if preview.Intent == "" {
		t.Error("no intent was issued")
	}
	if preview.ResolvedURL != "https://api.acme.com/v2/orders" {
		t.Errorf("resolvedUrl = %q", preview.ResolvedURL)
	}
	if preview.WillAsk == "" {
		t.Error("willAsk is empty; an agent cannot tell a person what is coming")
	}

	// Phase 2 with the intent does send.
	out, isErr = callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if isErr {
		t.Fatalf("phase 2 failed: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 1 {
		t.Fatalf("phase 2 sent %v, want one send", got)
	}
}

// The fingerprint binding: preview something harmless, edit it, and the intent
// is void rather than spendable on what the preview never described.
func TestAnEditBetweenThePhasesVoidsTheIntent(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Run: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}

	// What update_request would do.
	sender.mu.Lock()
	sender.url = "https://evil.example/collect"
	sender.mu.Unlock()

	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if !isErr {
		t.Fatalf("a repointed request was sent under the old preview: %s", out)
	}
	if !strings.Contains(out, "changed since it was previewed") {
		t.Errorf("the refusal does not say why: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Fatalf("it sent anyway: %v", got)
	}
}

func TestAnIntentCannotBeSpentTwice(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Run: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"path": "orders/create-order.http", "intent": preview.Intent}
	if _, isErr := callTool(t, c, "send_request", args); isErr {
		t.Fatal("the first spend failed")
	}
	out, isErr := callTool(t, c, "send_request", args)
	if !isErr {
		t.Fatalf("the intent was spent twice: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 1 {
		t.Errorf("it sent %d times", len(got))
	}
}

// Production is §6.4 case 1: answerable only in Otis' own window, whatever the
// client would like.
func TestAProductionSendIsAnsweredOnlyInTheWindow(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	sender.environment = "production"
	sender.env = resolve.EnvMeta{ConfirmBeforeSend: true}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	// A client that says yes to everything. Its answer must not count.
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: true}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.WillAsk, "window") {
		t.Errorf("willAsk does not say the window: %q", preview.WillAsk)
	}

	// The window refuses.
	asker.mu.Lock()
	asker.answer = false
	asker.mu.Unlock()

	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if !isErr {
		t.Fatalf("the window refused and it sent anyway: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Fatalf("it sent: %v", got)
	}
	if len(asker.asked()) != 1 {
		t.Errorf("the window was asked %d times, want 1", len(asker.asked()))
	}
}

// §5.1: an unreviewed send that would consume a secret is the case where a
// mistaken click cannot be taken back, so it is marked, window-only, and the
// dialog is given the host to put in its button.
func TestTheDangerConfirmationIsMarkedAndWindowOnly(t *testing.T) {
	s, _, _, asker := runFixture(t, mcp.Grants{Run: true})
	s.sender.(*fakeSender).review = mcp.Unreviewed
	s.sender.(*fakeSender).usesSecret = true
	s.sender.(*fakeSender).secretNames = []string{"apiKey"}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: true}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatalf("the window approved and it did not send")
	}

	asked := asker.asked()
	if len(asked) != 1 {
		t.Fatalf("the window was asked %d times", len(asked))
	}
	c1 := asked[0]
	if !c1.Danger {
		t.Error("an unreviewed secret send was not marked as the danger confirmation")
	}
	if c1.Reviewed {
		t.Error("it was marked reviewed")
	}
	if !c1.UsesSecret || len(c1.Secrets) != 1 || c1.Secrets[0] != "apiKey" {
		t.Errorf("the secret is not named: %+v", c1.Secrets)
	}
	// §5.1: the button names the destination.
	if c1.Host != "api.acme.com" {
		t.Errorf("host = %q, want the URL's host for the button text", c1.Host)
	}
	if c1.Reason == "" {
		t.Error("no reason was given; a confirmation that says 'allow?' is worth nothing")
	}
	// §6.4 rule 3: the prompt names the client, the tool, the method, the URL
	// and the environment.
	if c1.Client == "" || c1.Tool != "send_request" || c1.Method != "POST" ||
		c1.URL == "" || c1.Environment == "" {
		t.Errorf("the confirmation does not name everything: %+v", c1)
	}
	// A secret value must not reach the dialog either.
	blob, _ := json.Marshal(c1)
	if strings.Contains(string(blob), theSecret) {
		t.Errorf("the confirmation carries the secret: %s", blob)
	}
}

// §6.5: with no window there is nobody to ask, so a window-only call fails —
// it does not fall back to the client, which would turn the one surface a
// client cannot auto-approve into one it can.
func TestAWindowOnlySendFailsWithNoWindow(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true})
	sender.env = resolve.EnvMeta{ConfirmBeforeSend: true}
	asker.mu.Lock()
	asker.open = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: true}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if !isErr {
		t.Fatalf("it sent with no window to ask in: %s", out)
	}
	if !strings.Contains(out, "no Otis window is open") {
		t.Errorf("the refusal is unclear: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Errorf("it sent: %v", got)
	}
}

// The everyday surface: an ordinary environment may be answered through the
// client, and a client that declines refuses the send.
func TestAnOrdinarySendCanBeAnsweredThroughTheClient(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	// No window, so only the client can answer — which is what isolates the
	// client surface in this test.
	asker.mu.Lock()
	asker.open = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionDecline}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if !isErr {
		t.Fatalf("a declined send went through: %s", out)
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Errorf("it sent: %v", got)
	}
	if len(answer.messages()) == 0 {
		t.Fatal("the client was never asked")
	}
	// The prompt says what it is asking about, not "allow?".
	msg := answer.messages()[0]
	for _, want := range []string{"POST", "api.acme.com", "staging"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the elicitation message does not mention %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, theSecret) {
		t.Errorf("the elicitation message carries the secret: %s", msg)
	}
}

// An "accept" whose content says no is a no. The action says the person
// engaged with the form; the field says what they chose.
func TestAnAcceptWithProceedFalseIsARefusal(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	asker.mu.Lock()
	asker.open = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: false}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); !isErr {
		t.Fatal("proceed:false was treated as consent")
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Errorf("it sent: %v", got)
	}
}

// An environment marked "allow", with alwaysConfirmSends off, is the only way
// a send proceeds unasked — and it still cannot get past the review gate.
func TestOnlyAnAllowEnvironmentSendsUnasked(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true})
	sender.env = resolve.EnvMeta{Agents: resolve.AgentAllow}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.WillAsk, "nobody") {
		t.Errorf("willAsk = %q, want it to say nobody", preview.WillAsk)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatal("an allow environment did not send")
	}
	if len(asker.asked()) != 0 {
		t.Error("somebody was asked on an allow environment with alwaysConfirmSends off")
	}
	if got := sender.sentPaths(); len(got) != 1 {
		t.Errorf("sent %v", got)
	}

	// And the same environment cannot get an unreviewed request past §5.
	sender.mu.Lock()
	sender.review = mcp.Unreviewed
	sender.mu.Unlock()
	out, _ = callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.WillAsk, "window") {
		t.Errorf("an unreviewed send on an allow environment says %q; the review gate is not "+
			"overridable", preview.WillAsk)
	}
}

// §14.12: alwaysConfirmSends is on by default and can only tighten, so a
// committed "allow" is a statement about the environment rather than a grant
// that takes effect unasked.
func TestAlwaysConfirmSendsOverridesAllow(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	sender.env = resolve.EnvMeta{Agents: resolve.AgentAllow}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatalf("the window approved and it did not send")
	}
	if len(asker.asked()) != 1 {
		t.Errorf("the window was asked %d times; alwaysConfirmSends must still ask", len(asker.asked()))
	}
}

// §10: a call needing confirmation spends its budget when it is approved, not
// when it is asked. Otherwise an agent could drain the bucket with calls a
// person refuses, and refusing would cost the person their own next send.
func TestRefusedSendsDoNotSpendTheBudget(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	asker.mu.Lock()
	asker.answer = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	// RUN bursts at 5. Previews spend, so use one preview and replay against
	// fresh previews only as budget allows; the point is the *refusals*.
	before := s.limits.Available(mcp.CapRun)
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	afterPreview := s.limits.Available(mcp.CapRun)
	if afterPreview >= before {
		t.Errorf("a preview did not spend a token: %v then %v", before, afterPreview)
	}

	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); !isErr {
		t.Fatal("a refused send succeeded")
	}
	afterRefusal := s.limits.Available(mcp.CapRun)
	if afterRefusal < afterPreview {
		t.Errorf("a refused send spent budget: %v then %v — a person refusing would cost "+
			"them their own next send", afterPreview, afterRefusal)
	}
	if got := sender.sentPaths(); len(got) != 0 {
		t.Errorf("it sent: %v", got)
	}
}

// §6.5: a folder run asks once PER REQUEST, not once for the folder. The agent
// previewing the folder once does not confirm anything on anybody's behalf.
func TestAFolderRunAsksPerRequest(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	sender.plan = []string{"orders/a.http", "orders/b.http", "orders/c.http"}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "run_folder", map[string]any{"path": "orders"})
	if isErr {
		t.Fatalf("the plan failed: %s", out)
	}
	var plan FolderPlanView
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(plan.Requests) != 3 {
		t.Fatalf("the plan has %d requests", len(plan.Requests))
	}
	// The number that tells an agent how many prompts it is about to cause.
	if plan.Confirmations != 3 {
		t.Errorf("confirmations = %d, want 3", plan.Confirmations)
	}
	if len(sender.sentPaths()) != 0 {
		t.Error("the plan sent something")
	}

	out, isErr = callTool(t, c, "run_folder", map[string]any{"path": "orders", "intent": plan.Intent})
	if isErr {
		t.Fatalf("the run failed: %s", out)
	}
	if n := len(asker.asked()); n != 3 {
		t.Errorf("a person was asked %d times for 3 requests; §6.5 says once per request", n)
	}
	if got := sender.sentPaths(); len(got) != 3 {
		t.Errorf("sent %v", got)
	}
}

// A refusal partway through a run stops that request and not the person's
// understanding of the rest: it is reported as refused, not as a failure.
func TestARefusedRequestInARunIsNotAFailure(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	sender.plan = []string{"orders/a.http", "orders/b.http"}
	asker.mu.Lock()
	asker.answer = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, _ := callTool(t, c, "run_folder", map[string]any{"path": "orders"})
	var plan FolderPlanView
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	out, isErr := callTool(t, c, "run_folder", map[string]any{"path": "orders", "intent": plan.Intent})
	if isErr {
		t.Fatalf("the run itself errored: %s", out)
	}
	var result RunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary.Refused != 2 {
		t.Errorf("refused = %d, want 2", result.Summary.Refused)
	}
	if result.Summary.Sent != 0 || result.Summary.Failed != 0 {
		t.Errorf("a refusal was counted as a send or a failure: %+v", result.Summary)
	}
	if len(sender.sentPaths()) != 0 {
		t.Error("it sent despite refusals")
	}
}

// Nothing proceeds by inattention: no answer within the timeout is a refusal.
func TestNoAnswerIsARefusal(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	asker.mu.Lock()
	// Longer than the test's patience; the context deadline is what ends it.
	asker.delay = time.Hour
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}

	// The tool call's own context is what expires first here; either way the
	// send must not happen.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "send_request", Arguments: map[string]any{
			"path": "orders/create-order.http", "intent": preview.Intent}},
	})
	if got := sender.sentPaths(); len(got) != 0 {
		t.Errorf("an unanswered confirmation sent anyway: %v", got)
	}
}

// The send result is masked like everything else: the fake's resolved URL
// carries a token in a query parameter, which is exactly how a URL leaks one.
func TestASendResultIsMasked(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Run: true})
	sender.env = resolve.EnvMeta{Agents: resolve.AgentAllow}

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if isErr {
		t.Fatalf("failed: %s", out)
	}
	if strings.Contains(out, theSecret) {
		t.Fatalf("THE SECRET REACHED THE AGENT in a send result: %s", out)
	}
	if !strings.Contains(out, resolve.MaskPlaceholder) {
		t.Errorf("nothing was masked, so this proves nothing: %s", out)
	}
}

// RUN off means no send, whatever else is true.
func TestRunOffMeansNoSend(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, isErr := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	if !isErr {
		t.Fatalf("a preview ran with RUN off: %s", out)
	}
	if len(sender.sentPaths()) != 0 {
		t.Error("it sent")
	}
}

// A build with no Sender does not offer the send tools at all, so "this
// cannot send" is a fact about what was constructed.
func TestAServerWithNoSenderOffersNoSendTools(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Options{
		Source: &fakeSource{dir: dir}, Grants: &fakeGrants{grants: allGranted()},
		Audit: mcp.NewLog(""), EndpointPath: dir + "/mcp.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })
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
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "send_request", "run_folder", "create_request", "create_folder", "update_request":
			t.Errorf("%s is offered with no Sender or Writer", tool.Name)
		}
	}
}

// The WRITE tools go through the services, and the agent must use the path
// that comes back.
func TestTheWriteTools(t *testing.T) {
	s, _, writer, _ := runFixture(t, mcp.Grants{Write: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "create_request", map[string]any{
		"folder": "orders", "name": "Create order"})
	if isErr {
		t.Fatalf("create_request failed: %s", out)
	}
	var created Created
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	// Go resolved a collision; the agent gets the real path.
	if created.Path != "orders/create-order-2.http" {
		t.Errorf("path = %q, want the path Go actually used", created.Path)
	}

	if out, isErr := callTool(t, c, "create_folder", map[string]any{
		"parent": "", "name": "Payment methods"}); isErr {
		t.Errorf("create_folder failed: %s", out)
	}

	if out, isErr := callTool(t, c, "update_request", map[string]any{
		"path": "orders/create-order.http", "text": "POST http://x\n"}); isErr {
		t.Errorf("update_request failed: %s", out)
	}
	// Text that does not parse is refused and the file is left alone.
	if out, isErr := callTool(t, c, "update_request", map[string]any{
		"path": "orders/create-order.http", "text": "nonsense"}); !isErr {
		t.Errorf("unparseable text was accepted: %s", out)
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.created) != 2 {
		t.Errorf("created %v", writer.created)
	}
	if len(writer.updated) != 1 {
		t.Errorf("updated %v, want only the one that parsed", writer.updated)
	}
}

// WRITE off means no write.
func TestWriteOffMeansNoWrite(t *testing.T) {
	s, _, writer, _ := runFixture(t, mcp.Grants{Read: true, Run: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"create_request", map[string]any{"folder": "", "name": "x"}},
		{"create_folder", map[string]any{"parent": "", "name": "x"}},
		{"update_request", map[string]any{"path": "a.http", "text": "GET http://x\n"}},
		{"update_documentation", map[string]any{"folder": "", "text": "# hello"}},
	} {
		if out, isErr := callTool(t, c, tc.tool, tc.args); !isErr {
			t.Errorf("%s ran with WRITE off: %s", tc.tool, out)
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.created)+len(writer.updated) != 0 {
		t.Error("something was written")
	}
}

// No tool renames or deletes anything, and none touches an environment,
// `.order`, a secret or git. §12's "not exposed, on purpose" list, asserted
// against the surface an agent actually sees.
func TestTheSurfaceOffersNothingItShouldNot(t *testing.T) {
	s, _, _, _ := runFixture(t, allGranted())
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

	allowed := map[string]bool{
		"list_requests": true, "get_request": true, "list_environments": true,
		"get_documentation":     true,
		"get_session_variables": true, "get_last_response": true, "get_test_results": true,
		"send_request": true, "run_folder": true,
		"create_request": true, "create_folder": true, "update_request": true,
		"update_documentation": true,
	}
	for _, tool := range tools.Tools {
		if !allowed[tool.Name] {
			t.Errorf("%s is offered and is not in docs/MCP.md §12's surface", tool.Name)
		}
		for _, forbidden := range []string{"delete", "rename", "remove", "commit", "stage",
			"discard", "secret", "order", "environment_set", "clear_session", "collection"} {
			if strings.Contains(strings.ToLower(tool.Name), forbidden) {
				t.Errorf("%s looks like a tool §12 does not expose", tool.Name)
			}
		}
	}
	if len(tools.Tools) != len(allowed) {
		t.Errorf("%d tools are offered, want %d", len(tools.Tools), len(allowed))
	}
}

// The client surface's approving path, and the round trip that carries it.
//
// §6.3 was written for a blocking ask; the protocol now makes it a return-and-
// retry (SEP-2322). This asserts the whole loop works: the tool returns
// "input required", mcp-go's client puts the question to the handler, and the
// retry spends the intent and sends. Without this test the round trip would be
// exercised only by its refusing half.
func TestAClientApprovalCompletesTheRoundTrip(t *testing.T) {
	s, sender, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	// No window, so the client is the only surface and the answer must have
	// come from there.
	asker.mu.Lock()
	asker.open = false
	asker.mu.Unlock()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: true}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	out, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent})
	if isErr {
		t.Fatalf("an approved send failed: %s", out)
	}
	var result SendResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("phase 2 did not return a send result: %v\n%s", err, out)
	}
	if result.Status != 201 {
		t.Errorf("status = %d", result.Status)
	}
	if got := sender.sentPaths(); len(got) != 1 {
		t.Fatalf("sent %v, want exactly one send", got)
	}
	if len(answer.messages()) != 1 {
		t.Errorf("the client was asked %d times, want 1", len(answer.messages()))
	}
	if len(asker.asked()) != 0 {
		t.Error("the window was asked as well; the two surfaces are not both used")
	}
}

// The audit log records that a person was asked, and then what they said.
// Without the first line, an agent putting prompts in front of somebody who
// never answers would leave no trace at all.
func TestAskingAndAnsweringAreBothAudited(t *testing.T) {
	s, _, _, asker := runFixture(t, mcp.Grants{Run: true, AlwaysConfirmSends: true})
	asker.mu.Lock()
	asker.open = false
	asker.mu.Unlock()
	auditPath := s.audit.Path()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	answer := &elicitAnswer{action: mcpgo.ElicitationResponseActionAccept, proceed: true}
	c := connectWith(t, endpoint, answer)

	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatal("the send failed")
	}

	entries := auditLines(t, auditPath)
	var sawAsked, sawConfirmed bool
	for _, e := range entries {
		switch e.Decision {
		case mcp.Asked:
			sawAsked = true
			if e.Status != "awaiting-confirmation" {
				t.Errorf("an asked entry has status %q", e.Status)
			}
		case mcp.Confirmed:
			sawConfirmed = true
			if e.Surface != mcp.OnClient {
				t.Errorf("surface = %q, want client — this is the field that shows §6.4 "+
					"was honoured", e.Surface)
			}
			if e.Status != "201" {
				t.Errorf("status = %q, want the HTTP status", e.Status)
			}
		}
	}
	if !sawAsked {
		t.Error("no audit line records that a person was asked")
	}
	if !sawConfirmed {
		t.Error("no audit line records that they said yes")
	}
}

// A window answer is audited as having come from the window, which is what
// makes a §6.4 violation findable after the fact.
func TestAWindowAnswerIsAuditedAsSuch(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Run: true})
	sender.env = resolve.EnvMeta{ConfirmBeforeSend: true}
	auditPath := s.audit.Path()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatal("the send failed")
	}
	var found bool
	for _, e := range auditLines(t, auditPath) {
		if e.Decision == mcp.Confirmed {
			found = true
			if e.Surface != mcp.InWindow {
				t.Errorf("a production send was answered on %q, want the window", e.Surface)
			}
		}
	}
	if !found {
		t.Error("no confirmed entry")
	}
}

// auditLines reads the audit file back.
func auditLines(t *testing.T, path string) []mcp.Entry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []mcp.Entry
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e mcp.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// The audit log records the environment a send actually resolved against, not
// the argument it was given.
//
// Most sends name no environment — they use whatever the person has active —
// so a log that recorded the argument would say "" for exactly the sends worth
// auditing. "Which environment did that credential go to" is the most
// security-relevant question the log answers after "which request", and it was
// answering it wrong until a live run showed an empty field on a send that had
// gone to `dev`.
func TestTheAuditLogRecordsTheResolvedEnvironment(t *testing.T) {
	s, sender, _, _ := runFixture(t, mcp.Grants{Run: true})
	sender.env = resolve.EnvMeta{Agents: resolve.AgentAllow}
	sender.environment = "staging"
	auditPath := s.audit.Path()

	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	// No `environment` argument: the agent takes the active one.
	out, _ := callTool(t, c, "send_request", map[string]any{"path": "orders/create-order.http"})
	var preview SendPreview
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if _, isErr := callTool(t, c, "send_request", map[string]any{
		"path": "orders/create-order.http", "intent": preview.Intent}); isErr {
		t.Fatal("the send failed")
	}

	entries := auditLines(t, auditPath)
	if len(entries) == 0 {
		t.Fatal("nothing was audited")
	}
	for _, entry := range entries {
		if entry.Environment != "staging" {
			t.Errorf("an audit line says environment %q, want the resolved \"staging\"", entry.Environment)
		}
	}
}

// A README is the documentation a folder carries for everyone on the branch,
// and writing one is the task an agent is most obviously useful for
// (docs/MCP.md §12). Both halves, because one without the other is either a
// blind overwrite or a read nothing acts on.
func TestAnAgentCanReadAndWriteAFoldersDocumentation(t *testing.T) {
	s, _, writer, _ := runFixture(t, allGranted())
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "get_documentation", map[string]any{"folder": "orders"})
	if isErr {
		t.Fatalf("get_documentation: %s", out)
	}
	for _, want := range []string{`"path":"orders/README.md"`, `"exists":true`, "What this folder is for."} {
		if !strings.Contains(out, want) {
			t.Errorf("get_documentation did not carry %q:\n%s", want, out)
		}
	}

	out, isErr = callTool(t, c, "update_documentation",
		map[string]any{"folder": "orders", "text": "# Orders\n\nRewritten.\n"})
	if isErr {
		t.Fatalf("update_documentation: %s", out)
	}
	if !strings.Contains(out, `"status":"modified"`) {
		t.Errorf("update_documentation = %s", out)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.updated) != 1 || writer.updated[0] != "orders/README.md" {
		t.Errorf("updated = %q", writer.updated)
	}
}

// The collection's own README is the folder with an empty path — the same
// rule every other tool follows, and the one the user asks for by name.
func TestTheCollectionsOwnReadmeIsTheEmptyFolder(t *testing.T) {
	s, _, writer, _ := runFixture(t, allGranted())
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)

	out, isErr := callTool(t, c, "get_documentation", map[string]any{"folder": ""})
	if isErr {
		t.Fatalf("get_documentation: %s", out)
	}
	if !strings.Contains(out, `"path":"README.md"`) {
		t.Errorf("the root README is not at README.md:\n%s", out)
	}
	if _, isErr := callTool(t, c, "update_documentation",
		map[string]any{"folder": "", "text": "# The collection\n"}); isErr {
		t.Fatal("writing the collection's own README failed")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.updated) != 1 || writer.updated[0] != "README.md" {
		t.Errorf("updated = %q", writer.updated)
	}
}

// Reading documentation is READ, not WRITE: an agent that can look at a
// collection can read its prose, and that is the grant working.
func TestReadingDocumentationNeedsOnlyRead(t *testing.T) {
	s, _, _, _ := runFixture(t, mcp.Grants{Read: true})
	endpoint, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, endpoint, endpoint.Token)
	if out, isErr := callTool(t, c, "get_documentation", map[string]any{"folder": ""}); isErr {
		t.Fatalf("get_documentation needed more than READ: %s", out)
	}
}
