package services

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/httpclient"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/response"
	"github.com/otis-http/otis/internal/script"
	"github.com/otis-http/otis/internal/scriptrun"
	"github.com/otis-http/otis/internal/secrets"
)

// How much of the past is kept.
//
// A response is held so the tab that asked for it can still show it, and a
// long session would otherwise grow without bound. Two limits, because either
// alone is wrong: a count cannot tell one 40 MB body from a hundred small
// ones, and a byte budget alone would keep thousands of empty 204s. The oldest
// responses are dropped until both hold.
const (
	MaxKeptResponses = 24
	MaxKeptBytes     = 384 << 20 // 384 MiB of bodies and their indexes
)

// SendState is where a send has got to.
type SendState string

const (
	SendInFlight  SendState = "in-flight"
	SendComplete  SendState = "complete"
	SendFailed    SendState = "failed"
	SendCancelled SendState = "cancelled"
)

// FailureKind classifies a send that produced no response.
//
// The classes exist because the response pane renders them differently: a
// refused connection, an unknown host and an expired certificate need
// different things done about them, and "Error: dial tcp: ..." tells the
// reader to go and parse a Go error themselves.
type FailureKind string

const (
	FailDNS        FailureKind = "dns"        // the host does not resolve
	FailRefused    FailureKind = "refused"    // nothing is listening
	FailTLS        FailureKind = "tls"        // the certificate was rejected
	FailTimeout    FailureKind = "timeout"    // @timeout expired
	FailCancelled  FailureKind = "cancelled"  // the user stopped it
	FailRedirect   FailureKind = "redirect"   // too many hops
	FailResolve    FailureKind = "resolve"    // a variable or secret is missing
	FailRequest    FailureKind = "request"    // the request could not be built
	FailNetwork    FailureKind = "network"    // any other transport failure
	FailCollection FailureKind = "collection" // the file is not a sendable request
	// FailScript is a script that threw or was killed by its timeout. It is
	// its own kind because the response pane shows it differently: the phase,
	// the file and line, and the console output that led up to it
	// (docs/FORMAT.md §9.10).
	FailScript FailureKind = "script"
)

// SentHeader is one header as it went on the wire, masked.
type SentHeader struct {
	Name string `json:"name"`
	// Value has every secret the request used replaced by the mask
	// (docs/FORMAT.md §5). Masking is presentation only; the real value is
	// what was sent.
	Value string `json:"value"`
	// Secret is true when masking changed the value, so the window can say
	// why it is showing dots.
	Secret bool `json:"secret"`
}

// SentRequest is what actually went out, masked.
//
// It exists so the claim the Headers tab makes is checkable: "7 sent · 4 local
// · 3 inherited" is a prediction, and this is the record. They must agree, and
// a test asserts they do.
type SentRequest struct {
	Method  string       `json:"method"`
	URL     string       `json:"url"`
	Headers []SentHeader `json:"headers"`
	// BodyBytes is the length of the body sent, after a "< ./file" body was
	// read and variables were substituted.
	BodyBytes int64 `json:"bodyBytes"`
}

// ResponseMeta is everything about a response except the body.
type ResponseMeta struct {
	SendID string `json:"sendId"`
	// Path is the request's node ID.
	Path   string    `json:"path"`
	State  SendState `json:"state"`
	Status string    `json:"status"`
	// StatusCode is 0 when there was no response.
	StatusCode int    `json:"statusCode"`
	Proto      string `json:"proto"`
	// Headers are the response headers in the order the server sent them
	// where Go preserves it, else sorted; duplicates are kept.
	Headers []SentHeader `json:"headers"`
	Cookies []Cookie     `json:"cookies"`
	// Size is the body's length in bytes.
	Size int64 `json:"size"`
	// DurationMs is the whole exchange, including reading the body.
	DurationMs float64 `json:"durationMs"`
	Timing     Timings `json:"timing"`
	// At is when the response completed, for the header row's clock.
	At time.Time `json:"at"`
	// Redirects lists followed hops.
	Redirects []httpclient.Redirect `json:"redirects,omitempty"`
	FinalURL  string                `json:"finalUrl"`
	// Body describes the renderings available (see BodyInfo).
	Body BodyInfo `json:"body"`
	// Request is what went on the wire, masked.
	Request SentRequest `json:"request"`
	// Warnings are non-fatal notes from preparing the request.
	Warnings []string `json:"warnings,omitempty"`
	// Tests are the assertions the post-response scripts declared
	// (docs/FORMAT.md §9.9). They also arrive one at a time as
	// events.ScriptTest; these are the complete set, so a tab reopened later
	// still has them.
	Tests []script.TestResult `json:"tests"`
	// Console is everything the scripts printed, in order, already masked.
	Console []script.ConsoleLine `json:"console"`
	// ScriptError is a post-response script that threw or was killed. The
	// response is still here: it arrived, and hiding it because a script
	// about it failed would lose what you need to fix the script (§9.10).
	ScriptError *ScriptFailure `json:"scriptError,omitempty"`
}

// Timings is the exchange broken down, in milliseconds. Zero means "not
// observed" — DNS and connect are zero on a reused connection.
type Timings struct {
	DNSMs     float64 `json:"dnsMs"`
	ConnectMs float64 `json:"connectMs"`
	TLSMs     float64 `json:"tlsMs"`
	TTFBMs    float64 `json:"ttfbMs"`
	TotalMs   float64 `json:"totalMs"`
}

// Cookie is one cookie the response set.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  string `json:"expires,omitempty"`
	MaxAge   int    `json:"maxAge,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	SameSite string `json:"sameSite,omitempty"`
}

// ScriptFailure is a script that threw or was killed, with where it happened
// (docs/FORMAT.md §9.10).
type ScriptFailure struct {
	// Phase is "pre-request" or "post-response".
	Phase string `json:"phase"`
	// Path and Line locate it: "orders/_pre.js", line 3.
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
	// Message is already masked and never names a secret's value.
	Message string `json:"message"`
	// Timeout marks a phase killed by its budget rather than a throw.
	Timeout bool `json:"timeout,omitempty"`
}

// BodyInfo says what the body is and how to ask for it.
type BodyInfo struct {
	// Kind is "json", "xml" or "text": what the window colours it as.
	Kind response.Kind `json:"kind"`
	// ContentType is the response's own header, verbatim.
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	// HasPretty is whether a formatted view is worth asking for. Whether it
	// succeeds is only known once it is built (a body can be labelled JSON
	// and not parse), which is what BodyView reports.
	HasPretty bool `json:"hasPretty"`
	// UTF8 is false for a binary body, which the window shows as a summary
	// rather than as text.
	UTF8 bool `json:"utf8"`
}

// BodyView is one rendering, described. Lines is what a virtualizer needs.
type BodyView struct {
	SendID string            `json:"sendId"`
	View   response.ViewKind `json:"view"`
	Lines  int               `json:"lines"`
	Bytes  int64             `json:"bytes"`
	// Unavailable is set when this rendering does not exist — a body that is
	// not JSON after all, or one past the size where formatting is worth it.
	// It is a fact about the body, not an error, so the raw view is still
	// there and the message says which.
	Unavailable string `json:"unavailable,omitempty"`
}

// BodyLine is one display line with its fold, if it opens one.
type BodyLine struct {
	Text string `json:"text"`
	// Fold is set on a line that opens a collapsible node.
	Fold *response.Fold `json:"fold,omitempty"`
}

// BodyChunk is a window of lines.
type BodyChunk struct {
	SendID string            `json:"sendId"`
	View   response.ViewKind `json:"view"`
	From   int               `json:"from"`
	Lines  []BodyLine        `json:"lines"`
	Total  int               `json:"total"`
}

// MaxChunkLines bounds one Lines call, so a caller asking for a million lines
// gets a refusal rather than the 40 MB marshal this package exists to avoid.
const MaxChunkLines = 2000

// SendStarted is the events.SendStarted payload.
type SendStarted struct {
	SendID string `json:"sendId"`
	Path   string `json:"path"`
	Method string `json:"method"`
	// URL is masked.
	URL string    `json:"url"`
	At  time.Time `json:"at"`
	// Env is the environment it resolved against, "" for none.
	Env string `json:"env"`
}

// SendFailure is the events.SendError payload. It never carries a secret: the
// message is masked, and a missing secret is named by key, never by value
// (docs/FORMAT.md §5).
type SendFailure struct {
	SendID string      `json:"sendId"`
	Path   string      `json:"path"`
	Kind   FailureKind `json:"kind"`
	// Message is one line, already readable.
	Message string `json:"message"`
	// Detail is the longer explanation, when there is one worth showing.
	Detail string `json:"detail,omitempty"`
	// DurationMs is how long it took to fail.
	DurationMs float64   `json:"durationMs"`
	At         time.Time `json:"at"`
}

// stored is one finished send, kept so its body can be paged.
type stored struct {
	meta ResponseMeta
	doc  *response.Document
	// mask hides the secret values this request used.
	//
	// The window is shown the body exactly as it came back, because the
	// person looking at it is the one who owns the credential. An agent is
	// not, and the response body is the one part of a send Otis cannot mask
	// in advance: an endpoint that echoes a request header sends the
	// credential straight back in its own body, and no amount of care taken
	// over the *request* prevents that. Keeping the masker with the response
	// is what lets a tool result be redacted where it is built, instead of
	// the MCP layer re-resolving the request to work out what to hide — and
	// re-resolving would be a second answer to that question.
	mask func(string) string
}

// SendService sends requests and holds what came back.
//
// The response body never crosses the binding whole: it stays in a
// response.Document here and the window asks for the lines it is about to
// draw. Sending itself is asynchronous — Send returns an id at once and the
// outcome arrives as an event — because a send can take thirty seconds and a
// binding call that blocks for thirty seconds blocks the window.
type SendService struct {
	app         *application.App
	collections *CollectionService
	// emitter overrides where events go. It exists so a test can wait for
	// what the service emitted instead of polling for what it did.
	emitter func(name string, data any)

	mu sync.Mutex
	// session is the cookie jar and AWS credential cache shared by the
	// requests of one collection, rebuilt when the collection changes.
	session    *httpclient.Session
	sessionDir string
	// vars holds the variables a run set (docs/FORMAT.md §4.5).
	vars *resolve.Session
	// inflight maps a send id to the func that stops it.
	inflight map[string]context.CancelFunc
	// responses maps a send id to what came back; order keeps the eviction
	// of the oldest cheap.
	responses map[string]*stored
	order     []string
	// secrets is the store real sends resolve against. The editor's read
	// paths use secrets.Placeholder instead; this is the only place a real
	// value is fetched, and it never leaves this process.
	secrets secrets.Store
}

// NewSendService constructs the service over the open collection.
func NewSendService(collections *CollectionService, store secrets.Store) *SendService {
	if store == nil {
		store = secrets.NewMemory()
	}
	return &SendService{
		collections: collections,
		inflight:    map[string]context.CancelFunc{},
		responses:   map[string]*stored{},
		vars:        resolve.NewSession(),
		secrets:     store,
	}
}

// ServiceStartup resolves the application and registers the cleanup that runs
// when a collection closes.
//
// The hook is registered here rather than in main.go so that dropping the
// per-collection state stays an unexported concern: every exported method on
// this struct becomes a binding the window can call, and "reset the session"
// is not something the window has any business doing.
func (s *SendService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	s.collections.OnClose(s.resetSession)
	return nil
}

// ServiceShutdown stops every send in flight, so no goroutine outlives the app.
func (s *SendService) ServiceShutdown() error {
	s.cancelAll()
	return nil
}

// vars is the session variable store, for the environment editor and for the
// script engine when it lands. Unexported: the store itself is of no use to
// the window, which reads it through SessionVars.
func (s *SendService) varsStore() *resolve.Session { return s.vars }

// Send starts sending the request at nodePath against envName ("" for none)
// and returns the send's id immediately.
//
// Everything after this point arrives as an event keyed by that id:
// events.SendStarted, then events.SendComplete or events.SendError. The id is
// the window's handle on the send — it is what Cancel takes and what the body
// calls take.
func (s *SendService) Send(nodePath, envName string) (string, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	node := loaded.Find(nodePath)
	switch {
	case node == nil:
		return "", fmt.Errorf("%s is not in the collection", nodePath)
	case node.Kind != collection.KindRequest:
		return "", fmt.Errorf("%s is a folder, not a request", nodePath)
	case node.Broken:
		return "", fmt.Errorf("%s: %s", nodePath, node.Error)
	case node.Request == nil:
		return "", fmt.Errorf("%s contains no request", nodePath)
	}

	id := newSendID()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.inflight[id] = cancel
	s.mu.Unlock()

	started := time.Now()
	// The goroutine is why Send can answer at once. It owns the send from
	// here: it emits started, does the work, emits the outcome, and releases
	// the cancel func whatever happens.
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.inflight, id)
			s.mu.Unlock()
		}()
		s.run(ctx, id, loaded, node, envName, started)
	}()
	return id, nil
}

// A beforeRequest runs before each request in a folder run, and a non-nil
// error means that request is not sent.
//
// Only the MCP server passes one; the window and the CLI pass nil. See
// runFolder for why it is a parameter.
type beforeRequest func(ctx context.Context, node *collection.Node) error

// RefusedRequest is one request a run did not send because beforeRequest said
// not to. It is kept apart from Failed because "a person said no" is not a
// fault of the request.
type RefusedRequest struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// requestNode finds a sendable request, with the same checks Send makes.
func (s *SendService) requestNode(nodePath string) (*collection.Collection, *collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, err
	}
	node := loaded.Find(nodePath)
	switch {
	case node == nil:
		return nil, nil, fmt.Errorf("%s is not in the collection", nodePath)
	case node.Kind != collection.KindRequest:
		return nil, nil, fmt.Errorf("%s is a folder, not a request", nodePath)
	case node.Broken:
		return nil, nil, fmt.Errorf("%s: %s", nodePath, node.Error)
	case node.Request == nil:
		return nil, nil, fmt.Errorf("%s contains no request", nodePath)
	}
	return loaded, node, nil
}

// sendNow sends and waits, for a caller that needs the outcome rather than an
// event.
//
// It is the same s.run the window's Send button reaches, on the caller's
// goroutine instead of a new one — so a tool call, a click and `otis run`
// resolve, prepare and send identically, which is the property CLAUDE.md asks
// for and the reason this is a wrapper rather than a second sender.
func (s *SendService) sendNow(ctx context.Context, nodePath, envName string) (outcome, error) {
	loaded, node, err := s.requestNode(nodePath)
	if err != nil {
		return outcome{}, err
	}
	id := newSendID()
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.inflight[id] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.inflight, id)
		s.mu.Unlock()
	}()
	return s.run(ctx, id, loaded, node, envName, time.Now()), nil
}

// folderRequests gathers a folder's runnable requests, in the folder's order.
func (s *SendService) folderRequests(nodePath string) (*collection.Collection, *collection.Node, []*collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, nil, err
	}
	folder := loaded.Find(nodePath)
	switch {
	case folder == nil:
		return nil, nil, nil, fmt.Errorf("%s is not in the collection", displayPath(nodePath))
	case folder.Kind != collection.KindFolder:
		return nil, nil, nil, fmt.Errorf("%s is a request, not a folder", nodePath)
	}
	var requests []*collection.Node
	walk(folder, func(n *collection.Node) bool {
		if n.Kind == collection.KindRequest && !n.Broken && n.Request != nil {
			requests = append(requests, n)
		}
		return true
	})
	if len(requests) == 0 {
		return nil, nil, nil, fmt.Errorf("%s has no requests to run", displayPath(nodePath))
	}
	return loaded, folder, requests, nil
}

// runFolderNow runs a folder and waits, calling before for each request.
func (s *SendService) runFolderNow(
	ctx context.Context, nodePath, envName string, stopOnFailure bool, before beforeRequest,
) (RunComplete, error) {
	loaded, folder, requests, err := s.folderRequests(nodePath)
	if err != nil {
		return RunComplete{}, err
	}
	id := newSendID()
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.inflight[id] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.inflight, id)
		s.mu.Unlock()
	}()

	started := time.Now()
	paths := make([]string, 0, len(requests))
	for _, n := range requests {
		paths = append(paths, n.ID)
	}
	// The window is told, so a run an agent started shows in the run pane
	// exactly like one the Run button started. An agent driving Otis is not
	// something that should happen invisibly.
	s.emit(events.RunStarted, RunStarted{
		RunID: id, Folder: folder.ID, Requests: paths,
		Env: envName, StopOnFailure: stopOnFailure, At: started,
	})
	return s.runFolder(ctx, id, loaded, folder, requests, envName, stopOnFailure, started, before), nil
}

// newestSendID is the most recent held response, or "".
//
// `order` is append-ordered by keep, so the last element is the newest. It is
// what a tool call means by "the last response" when it names no send id.
func (s *SendService) newestSendID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return ""
	}
	return s.order[len(s.order)-1]
}

// Cancel stops a send in flight. Cancelling one that has already finished is
// not an error: the click and the response can cross.
func (s *SendService) Cancel(sendID string) error {
	s.mu.Lock()
	cancel := s.inflight[sendID]
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

// cancelAll stops every send in flight.
func (s *SendService) cancelAll() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.inflight))
	for _, cancel := range s.inflight {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Meta returns a finished send's metadata, for a window that reconnected or
// re-rendered after the event went by.
func (s *SendService) Meta(sendID string) (ResponseMeta, error) {
	entry, err := s.stored(sendID)
	if err != nil {
		return ResponseMeta{}, err
	}
	return entry.meta, nil
}

// View describes one rendering of a body, building it if this is the first
// call. Formatting a large body happens here, on the binding's goroutine and
// not on the window's, which is the point.
func (s *SendService) View(sendID string, view response.ViewKind) (BodyView, error) {
	entry, err := s.stored(sendID)
	if err != nil {
		return BodyView{}, err
	}
	v, err := entry.doc.View(view)
	if err != nil {
		if errors.Is(err, response.ErrNoPrettyView) {
			return BodyView{
				SendID:      sendID,
				View:        view,
				Unavailable: "This body has no formatted view; showing it as it arrived.",
			}, nil
		}
		// A body labelled JSON that does not parse is worth saying out loud
		// rather than silently falling back: it is usually the interesting
		// thing about the response.
		return BodyView{
			SendID:      sendID,
			View:        view,
			Unavailable: "This body is not valid " + string(entry.doc.Kind()) + ": " + err.Error(),
		}, nil
	}
	return BodyView{SendID: sendID, View: view, Lines: v.Lines(), Bytes: v.Bytes}, nil
}

// Lines returns count display lines starting at from, with the folds that open
// inside them. It is how the body reaches the window: a viewport at a time.
func (s *SendService) Lines(sendID string, view response.ViewKind, from, count int) (BodyChunk, error) {
	entry, err := s.stored(sendID)
	if err != nil {
		return BodyChunk{}, err
	}
	v, err := entry.doc.View(view)
	if err != nil {
		return BodyChunk{}, err
	}
	if from < 0 {
		from = 0
	}
	if count < 0 {
		count = 0
	}
	if count > MaxChunkLines {
		return BodyChunk{}, fmt.Errorf("a chunk is at most %d lines; %d were asked for", MaxChunkLines, count)
	}
	to := from + count
	if to > v.Lines() {
		to = v.Lines()
	}
	chunk := BodyChunk{SendID: sendID, View: view, From: from, Total: v.Lines()}
	folds := v.Folds(from, to)
	at := 0
	for line := from; line < to; line++ {
		out := BodyLine{Text: v.Line(line)}
		if at < len(folds) && int(folds[at].Line) == line {
			fold := folds[at]
			out.Fold = &fold
			at++
		}
		chunk.Lines = append(chunk.Lines, out)
	}
	if chunk.Lines == nil {
		chunk.Lines = []BodyLine{}
	}
	return chunk, nil
}

// SessionVars returns the variables runs have set, read-only.
//
// Read-only on purpose: they are set by a run, and the only writes the window
// gets are Clear and ClearScope. There is no setter here for the same reason
// there is no secret getter — the value's whole contract is where it can and
// cannot go (docs/FORMAT.md §4.5).
func (s *SendService) SessionVars() []resolve.SessionValue {
	out := s.vars.List()
	if out == nil {
		return []resolve.SessionValue{}
	}
	return out
}

// SessionScope returns the variables a run set for one scope and owner: a
// folder's node ID, or an environment's name (docs/FORMAT.md §4.5).
//
// It is what the folder view's Session group lists, which has to be the
// folder's own values and not the collection's — a group headed "this
// machine only" that showed somebody else's folder's variables would be
// answering a different question from the one it asks.
func (s *SendService) SessionScope(scope resolve.SessionScope, owner string) []resolve.SessionValue {
	var out []resolve.SessionValue
	for _, v := range s.vars.List() {
		if v.Scope == scope && v.Owner == owner {
			out = append(out, v)
		}
	}
	if out == nil {
		return []resolve.SessionValue{}
	}
	return out
}

// ClearSessionVars forgets every session variable.
func (s *SendService) ClearSessionVars() error {
	s.vars.Clear()
	s.emit(events.SessionVarsChanged, nil)
	return nil
}

// ClearSessionScope forgets one folder's or one environment's session
// variables.
func (s *SendService) ClearSessionScope(scope resolve.SessionScope, owner string) error {
	s.vars.ClearScope(scope, owner)
	s.emit(events.SessionVarsChanged, nil)
	return nil
}

// Discard frees a response's body. The window calls it when the tab that was
// showing it closes.
func (s *SendService) Discard(sendID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop(sendID)
	return nil
}

// outcome is what one send came to. Exactly one of meta and failure is set.
//
// It exists so a folder run can wait for a send and know what happened,
// without listening to its own events. Send itself ignores the return: the
// events are the window's channel and the only one it needs.
type outcome struct {
	meta    *ResponseMeta
	failure *SendFailure
}

// ok reports a send that produced a response the run counts as a pass: a
// status under 400 (docs/FORMAT.md §6 — a 4xx or 5xx is a response, not an
// error, and the CLI maps it to exit code 1).
func (o outcome) ok() bool {
	return o.meta != nil && o.meta.StatusCode < 400
}

// run performs one send. It runs on its own goroutine; every exit emits.
func (s *SendService) run(ctx context.Context, id string, loaded *collection.Collection, node *collection.Node, envName string, started time.Time) outcome {
	var env *resolve.Environment
	if envName != "" {
		var err error
		if env, err = resolve.LoadEnvironment(loaded.Dir, envName); err != nil {
			return outcome{failure: s.fail(id, node.ID, FailResolve, "The environment could not be read.", err.Error(), started)}
		}
	}

	// docs/FORMAT.md §9.2: pre-request scripts run *before* resolution, so a
	// folder header can reference a value one of them sets. Inheritance is
	// structural and already computed by the resolver, which hands it to the
	// Rewrite hook below.
	plan := scriptrun.PlanFor(loaded, node)
	budget, err := script.TimeoutOf(node.Request)
	if err != nil {
		return outcome{failure: s.fail(id, node.ID, FailRequest,
			"The script timeout is not a number of seconds.", fmt.Sprintf("%s: %v", node.ID, err), started)}
	}
	sv := s.scriptVars(loaded, node, env)
	var preResult script.Result
	// The hook's own failure, kept separate: InCollection assigns to its own
	// error after the hook returns, so sharing one variable would lose this.
	var preErr error

	res, err := resolve.InCollection(loaded, node, resolve.Options{
		Env:     env,
		Secrets: s.secrets,
		Session: s.vars,
		// The same map the store writes, so a scratch value a hook sets is
		// in the scope this resolution is about to build. Extra is read
		// after Rewrite runs, which is what makes that work.
		Extra: sv.Request,
		Rewrite: func(entry *httpfile.Request, eff *resolve.Effective) {
			if len(plan.Pre) == 0 {
				return
			}
			view := scriptrun.RequestView(node, entry, eff)
			preResult, preErr = script.Run(script.Options{
				Phase:      script.Pre,
				Scripts:    plan.Pre,
				Request:    view,
				Vars:       sv,
				Secrets:    s.secrets,
				Env:        env,
				Collection: resolve.CollectionKey(loaded),
				Modules:    scriptrun.ModuleLoader{Root: loaded.Dir},
				Timeout:    budget,
				OnConsole:  func(line script.ConsoleLine) { s.emitConsole(id, node.ID, line) },
			})
			if preErr != nil {
				return
			}
			if view.Changed() {
				scriptrun.ApplyView(entry, eff, view)
			}
		},
	})
	if scriptErr := asScriptError(preErr); scriptErr != nil {
		return outcome{failure: s.failScript(id, node.ID, scriptErr, started)}
	}
	if preErr != nil {
		return outcome{failure: s.fail(id, node.ID, FailScript,
			"A pre-request script failed.", preErr.Error(), started)}
	}
	if err != nil {
		kind, message := classifyResolveError(err)
		return outcome{failure: s.fail(id, node.ID, kind, message, err.Error(), started)}
	}

	session := s.wireSession(loaded.Dir)
	req, warnings, err := httpclient.Prepare(ctx, res, node.Request, filepath.Dir(node.Path), session.PrepareOptions())
	if err != nil {
		// Masked: a Prepare failure can quote an argument, and an AWS secret
		// key is an argument.
		return outcome{failure: s.fail(id, node.ID, FailRequest, "The request could not be prepared.", res.Mask(err.Error()), started)}
	}

	// A secret a script placed through secrets.ref is substituted here, after
	// every script has run and just before the wire: this is the one moment
	// the real value exists outside the keychain, and it is registered with
	// the masker so everything Otis *shows* about the send still says
	// [secret:name] (docs/FORMAT.md §9.7).
	scriptrun.SubstituteHandles(res, req, preResult.Handles)

	// The window is told what is on the wire before the wire answers, so the
	// in-flight state can name what it is waiting for.
	s.emit(events.SendStarted, SendStarted{
		SendID: id, Path: node.ID, Method: req.Method,
		URL: res.Mask(req.URL), At: started, Env: envName,
	})

	client := &httpclient.Client{Session: session}
	resp, err := client.Do(ctx, req)
	if err != nil {
		kind, message := classifySendError(err)
		return outcome{failure: s.fail(id, node.ID, kind, message, res.Mask(err.Error()), started)}
	}

	doc := response.New(resp.Body, resp.Headers.Get("Content-Type"))
	meta := ResponseMeta{
		SendID:     id,
		Path:       node.ID,
		State:      SendComplete,
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
		Proto:      resp.Proto,
		Headers:    responseHeaders(resp.Headers),
		Cookies:    responseCookies(resp),
		Size:       resp.Size,
		DurationMs: ms(resp.Duration),
		Timing: Timings{
			DNSMs:     ms(resp.Timing.DNS),
			ConnectMs: ms(resp.Timing.Connect),
			TLSMs:     ms(resp.Timing.TLS),
			TTFBMs:    ms(resp.Timing.TTFB),
			TotalMs:   ms(resp.Timing.Total),
		},
		At:        time.Now(),
		Redirects: resp.Redirects,
		FinalURL:  resp.FinalURL,
		Body: BodyInfo{
			Kind:        doc.Kind(),
			ContentType: resp.Headers.Get("Content-Type"),
			Size:        doc.Size(),
			HasPretty:   doc.HasPretty(),
			UTF8:        utf8Body(resp.Body),
		},
		Request:  sentRequest(res, req),
		Warnings: warningStrings(warnings),
	}

	// docs/FORMAT.md §9.1: post-response scripts, then the tests they
	// declared. A script that throws here does *not* discard the response —
	// the response arrived, and hiding it because a script about it failed
	// would lose the thing you need to fix the script (§9.10).
	if len(plan.Post) > 0 {
		postResult, postErr := script.Run(script.Options{
			Phase:   script.Post,
			Scripts: plan.Post,
			Request: scriptrun.SentView(node, res, req),
			Response: &script.ResponseView{
				Status: resp.StatusCode, StatusText: scriptrun.StatusText(resp.Status),
				Headers: scriptrun.ResponseHeaders(resp.Headers), Body: string(resp.Body),
				Size: resp.Size,
				Timings: script.Timings{
					DNS: ms(resp.Timing.DNS), Connect: ms(resp.Timing.Connect),
					TLS: ms(resp.Timing.TLS), TTFB: ms(resp.Timing.TTFB),
					Total: ms(resp.Timing.Total),
				},
			},
			Vars:       sv,
			Secrets:    s.secrets,
			Env:        env,
			Collection: resolve.CollectionKey(loaded),
			Modules:    scriptrun.ModuleLoader{Root: loaded.Dir},
			Timeout:    budget,
			OnTest:     func(result script.TestResult) { s.emitTest(id, node.ID, result) },
			OnConsole:  func(line script.ConsoleLine) { s.emitConsole(id, node.ID, line) },
		})
		meta.Tests = postResult.Tests
		meta.Console = append(meta.Console, postResult.Console...)
		if scriptErr := asScriptError(postErr); scriptErr != nil {
			meta.ScriptError = describeScriptError(scriptErr)
		} else if postErr != nil {
			meta.ScriptError = &ScriptFailure{Phase: string(script.Post), Message: postErr.Error()}
		}
	}
	meta.Console = append(preResult.Console, meta.Console...)
	if meta.Tests == nil {
		meta.Tests = []script.TestResult{}
	}
	if meta.Console == nil {
		meta.Console = []script.ConsoleLine{}
	}

	s.keep(id, &stored{meta: meta, doc: doc, mask: res.Mask})
	s.emit(events.SendComplete, meta)
	return outcome{meta: &meta}
}

// fail records and emits a send that produced no response, and returns what
// it emitted so a folder run can report the same thing in its summary.
func (s *SendService) fail(id, path string, kind FailureKind, message, detail string, started time.Time) *SendFailure {
	failure := SendFailure{
		SendID:     id,
		Path:       path,
		Kind:       kind,
		Message:    message,
		Detail:     detail,
		DurationMs: ms(time.Since(started)),
		At:         time.Now(),
	}
	s.emit(events.SendError, failure)
	return &failure
}

// sessionFor returns the cookie jar and credential cache for a collection,
// making a new one when the collection changed. Cookies belong to the
// collection being exercised, not to the process (docs/FORMAT.md §6).
func (s *SendService) wireSession(dir string) *httpclient.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.sessionDir != dir {
		s.session, s.sessionDir = httpclient.NewSession(), dir
	}
	return s.session
}

// resetSession drops the cookie jar, the credential cache, the held responses
// and every session variable. Closing a collection does this: none of it
// belongs to the next one.
func (s *SendService) resetSession() {
	s.cancelAll()
	s.mu.Lock()
	s.session, s.sessionDir = nil, ""
	for id := range s.responses {
		delete(s.responses, id)
	}
	s.order = nil
	s.mu.Unlock()
	s.vars.Clear()
	s.emit(events.SessionVarsChanged, nil)
}

func (s *SendService) keep(id string, entry *stored) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[id] = entry
	s.order = append(s.order, id)
	// Never evict the one just stored, however large: the window is about to
	// ask for it, and answering "that response is gone" to the send that has
	// only this moment finished would be absurd.
	for len(s.order) > 1 && (len(s.order) > MaxKeptResponses || s.footprint() > MaxKeptBytes) {
		s.drop(s.order[0])
	}
}

// footprint is what the held responses cost. The caller holds the mutex.
//
// Recomputed rather than accumulated, because a document grows when a view is
// built from it — asking for the formatted body of a response stored an hour
// ago changes what it costs, and a running total would not know.
func (s *SendService) footprint() int64 {
	var total int64
	for _, entry := range s.responses {
		total += entry.doc.Footprint()
	}
	return total
}

// heldBytes is what the held responses cost, for the tests that care that the
// limit is real.
func (s *SendService) heldBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.footprint()
}

// drop forgets one response. The caller holds the mutex.
func (s *SendService) drop(id string) {
	delete(s.responses, id)
	kept := s.order[:0]
	for _, existing := range s.order {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	s.order = kept
}

func (s *SendService) stored(sendID string) (*stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.responses[sendID]
	if !ok {
		return nil, fmt.Errorf("no response is being held for send %s", sendID)
	}
	return entry, nil
}

// redactor returns the redaction gate for a held response.
//
// Every tool result derived from a send goes through the Redactor this
// returns. It is unexported on purpose: it hands back a value that closes
// over secret strings, which must never become part of the binding surface
// the window can call.
func (s *SendService) redactor(sendID string) (*mcp.Redactor, error) {
	entry, err := s.stored(sendID)
	if err != nil {
		return nil, err
	}
	if entry.mask == nil {
		// Unreachable in correct code: the masker is a method value on the
		// request's *resolve.Resolved, so it is set for every send that is
		// held. It is refused rather than defaulted to mcp.NoSecrets()
		// because that default is permissive — a send whose masker was not
		// threaded through would redact nothing and say so to nobody, which
		// is the one failure this whole path exists to prevent.
		return nil, fmt.Errorf("mcp: send %s is held without a masker, refusing to build a result", sendID)
	}
	return mcp.NewRedactor(entry.mask), nil
}

func (s *SendService) emit(name string, data any) {
	if s.emitter != nil {
		s.emitter(name, data)
		return
	}
	if s.app == nil {
		return // not running under Wails and not under test
	}
	s.app.Event.Emit(name, data)
}

// sentRequest records what went on the wire, masked.
func sentRequest(res *resolve.Resolved, req *httpclient.Request) SentRequest {
	out := SentRequest{
		Method:    req.Method,
		URL:       res.Mask(req.URL),
		BodyBytes: int64(len(req.Body)),
	}
	for _, h := range req.Headers {
		masked := res.Mask(h.Value)
		out.Headers = append(out.Headers, SentHeader{
			Name:   h.Name,
			Value:  masked,
			Secret: masked != h.Value,
		})
	}
	if out.Headers == nil {
		out.Headers = []SentHeader{}
	}
	return out
}

// responseHeaders flattens the response headers, keeping duplicates.
func responseHeaders(headers http.Header) []SentHeader {
	out := []SentHeader{}
	for name, values := range headers {
		for _, v := range values {
			out = append(out, SentHeader{Name: name, Value: v})
		}
	}
	// http.Header is a map, so its range order is random; sorting is what
	// keeps the Headers tab from reshuffling on every render.
	sortHeaders(out)
	return out
}

func responseCookies(resp *httpclient.Response) []Cookie {
	out := []Cookie{}
	for _, c := range (&http.Response{Header: resp.Headers}).Cookies() {
		cookie := Cookie{
			Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			MaxAge: c.MaxAge, Secure: c.Secure, HTTPOnly: c.HttpOnly,
		}
		if !c.Expires.IsZero() {
			cookie.Expires = c.Expires.UTC().Format(time.RFC3339)
		}
		switch c.SameSite {
		case http.SameSiteLaxMode:
			cookie.SameSite = "Lax"
		case http.SameSiteStrictMode:
			cookie.SameSite = "Strict"
		case http.SameSiteNoneMode:
			cookie.SameSite = "None"
		}
		out = append(out, cookie)
	}
	return out
}

// classifySendError turns a transport failure into a class and a line of
// readable text. The Go error goes in Detail; this is what the pane leads with.
func classifySendError(err error) (FailureKind, string) {
	switch {
	case errors.Is(err, httpclient.ErrCancelled), errors.Is(err, context.Canceled):
		return FailCancelled, "Cancelled."
	case errors.Is(err, httpclient.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return FailTimeout, "The request timed out. Raise it with # @timeout <seconds>."
	case errors.Is(err, httpclient.ErrTooManyRedirects):
		return FailRedirect, "Too many redirects. Add # @no-redirect to see the first one."
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return FailDNS, fmt.Sprintf("The host %s could not be resolved.", dns.Name)
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return FailTLS, "The server's TLS certificate was rejected."
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return FailTLS, "The server did not answer with TLS. Check whether the URL should be http://."
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return FailRefused, "The connection was refused: nothing is listening there."
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return FailNetwork, "The connection was reset by the server."
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return FailNetwork, "The host could not be reached."
	}
	// A TLS failure that is not one of the typed ones still reads as TLS to
	// anyone looking at it, and the class decides what the pane suggests.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "remote error" {
		return FailTLS, "The TLS handshake failed."
	}
	return FailNetwork, "The request could not be sent."
}

// classifyResolveError turns a resolution failure into a class and a line. It
// never quotes a value: a missing secret is named by key (docs/FORMAT.md §5),
// and an unresolved variable by name.
func classifyResolveError(err error) (FailureKind, string) {
	var missing *resolve.MissingError
	if errors.As(err, &missing) {
		if len(missing.Names) == 1 {
			return FailResolve, fmt.Sprintf("The variable %s is not defined. Set it in an environment or a folder.", missing.Names[0])
		}
		return FailResolve, "Some variables are not defined: " + joinNames(missing.Names) + "."
	}
	var secret *resolve.SecretError
	if errors.As(err, &secret) {
		if errors.Is(secret, secrets.ErrNotFound) {
			return FailResolve, fmt.Sprintf("The secret %s is not set on this machine. Its keychain entry is %s.", secret.Name, secret.Key)
		}
		return FailResolve, fmt.Sprintf("The secret %s could not be read from the keychain.", secret.Name)
	}
	var cycle *resolve.CycleError
	if errors.As(err, &cycle) {
		return FailResolve, "A variable refers back to itself: " + joinArrow(cycle.Chain) + "."
	}
	var inherit *resolve.Error
	if errors.As(err, &inherit) {
		return FailResolve, inherit.Msg + " (" + inherit.Source.String() + ")"
	}
	return FailResolve, "The request could not be resolved."
}

func warningStrings(warnings []httpclient.Warning) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]string, len(warnings))
	for i, w := range warnings {
		out[i] = string(w)
	}
	return out
}

// newSendID is a random opaque handle. Random rather than a counter so an id
// from a previous collection can never name a response in this one.
func newSendID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only reachable if the OS entropy source fails; the clock is a
		// worse id but a working one, and refusing to send would be worse
		// than either.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// sortHeaders orders headers by name, then by value, so a duplicated name
// keeps a stable order between renders.
func sortHeaders(headers []SentHeader) {
	sort.SliceStable(headers, func(i, j int) bool {
		if headers[i].Name != headers[j].Name {
			return headers[i].Name < headers[j].Name
		}
		return headers[i].Value < headers[j].Value
	})
}

// utf8Body reports whether a body can be shown as text at all. A body that is
// not valid UTF-8 is summarised rather than rendered, the way the CLI does it
// (docs/FORMAT.md §8).
func utf8Body(body []byte) bool { return utf8.Valid(body) }

func joinNames(names []string) string { return strings.Join(names, ", ") }

func joinArrow(chain []string) string { return strings.Join(chain, " → ") }

// RunState is where a folder run has got to.
type RunState string

const (
	RunRunning   RunState = "running"
	RunFinished  RunState = "finished"
	RunStopped   RunState = "stopped"
	RunCancelled RunState = "cancelled"
)

// RunStarted announces a folder run and what it is about to do.
type RunStarted struct {
	RunID string `json:"runId"`
	// Folder is the folder's node path, "" for the collection root.
	Folder string `json:"folder"`
	// Requests are the paths about to be sent, in the order they will be
	// sent — which is the folder's display order, and therefore `.order`
	// (docs/FORMAT.md §2.2).
	Requests      []string  `json:"requests"`
	Env           string    `json:"env,omitempty"`
	StopOnFailure bool      `json:"stopOnFailure"`
	At            time.Time `json:"at"`
}

// RunResult is one request's outcome inside a folder run.
type RunResult struct {
	RunID string `json:"runId"`
	// Index is the request's position in RunStarted.Requests, so the window
	// can fill in a row it has already drawn rather than guessing.
	Index int    `json:"index"`
	Path  string `json:"path"`
	Name  string `json:"name"`
	// SendID is the send this was, so the response pane can show its body.
	SendID string `json:"sendId"`
	// Passed is a response with a status under 400. A 4xx or 5xx is a
	// response, not an error (docs/FORMAT.md §6), and it fails the run.
	Passed     bool    `json:"passed"`
	StatusCode int     `json:"statusCode,omitempty"`
	Status     string  `json:"status,omitempty"`
	DurationMs float64 `json:"durationMs"`
	// Message is why it failed, already readable and already masked.
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// RunComplete is a folder run's summary.
type RunComplete struct {
	RunID  string   `json:"runId"`
	Folder string   `json:"folder"`
	State  RunState `json:"state"`
	Passed int      `json:"passed"`
	Failed int      `json:"failed"`
	// Skipped is what stop-on-failure did not get to.
	Skipped    int       `json:"skipped"`
	Total      int       `json:"total"`
	DurationMs float64   `json:"durationMs"`
	At         time.Time `json:"at"`
	// Refused lists requests a beforeRequest hook declined, which only an
	// agent run has. Kept apart from Failed: a person saying no to a
	// confirmation is not a fault of the request, and counting it as one
	// would make an agent's run look broken when it was governed.
	Refused []RefusedRequest `json:"refused,omitempty"`
}

// RunFolder sends every request below a folder, one at a time, in the folder's
// display order — which is `.order` where there is one and alphabetical where
// there is not (docs/FORMAT.md §2.2).
//
// One at a time, deliberately: the requests in a folder are usually a sequence
// (create an order, then read it back), a post-response script sets the
// variable the next request references, and they share one cookie jar. Running
// them in parallel would make the outcome depend on scheduling.
//
// It answers with the run's id immediately and reports itself over events:
// events.RunStarted with the whole plan, events.RunResult per request as it
// finishes, and events.RunComplete with the summary. Each request also emits
// its own send events, so the response pane fills in as the run goes.
func (s *SendService) RunFolder(nodePath, envName string, stopOnFailure bool) (string, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	folder := loaded.Find(nodePath)
	switch {
	case folder == nil:
		return "", fmt.Errorf("%s is not in the collection", displayPath(nodePath))
	case folder.Kind != collection.KindFolder:
		return "", fmt.Errorf("%s is a request, not a folder", nodePath)
	}

	var requests []*collection.Node
	walk(folder, func(n *collection.Node) bool {
		if n.Kind == collection.KindRequest && !n.Broken && n.Request != nil {
			requests = append(requests, n)
		}
		return true
	})
	if len(requests) == 0 {
		return "", fmt.Errorf("%s has no requests to run", displayPath(nodePath))
	}

	paths := make([]string, 0, len(requests))
	for _, n := range requests {
		paths = append(paths, n.ID)
	}

	id := newSendID()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.inflight[id] = cancel
	s.mu.Unlock()

	started := time.Now()
	s.emit(events.RunStarted, RunStarted{
		RunID: id, Folder: folder.ID, Requests: paths,
		Env: envName, StopOnFailure: stopOnFailure, At: started,
	})

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.inflight, id)
			s.mu.Unlock()
		}()
		s.runFolder(ctx, id, loaded, folder, requests, envName, stopOnFailure, started, nil)
	}()
	return id, nil
}

// runFolder is the sequence itself. It runs on its own goroutine.
//
// It returns the summary as well as emitting it, so a test can drive the
// sequence directly rather than waiting on an event the service does not emit
// outside Wails. The window ignores the return and reads the event.
func (s *SendService) runFolder(
	ctx context.Context,
	runID string,
	loaded *collection.Collection,
	folder *collection.Node,
	requests []*collection.Node,
	envName string,
	stopOnFailure bool,
	started time.Time,
	before beforeRequest,
) RunComplete {
	summary := RunComplete{RunID: runID, Folder: folder.ID, State: RunFinished, Total: len(requests)}

	for i, node := range requests {
		if ctx.Err() != nil {
			summary.State = RunCancelled
			summary.Skipped = len(requests) - i
			break
		}
		// The hook is where an agent's per-request confirmation happens
		// (docs/MCP.md §6.5). It is a parameter rather than a second loop
		// because what "running a folder" means — the order, the session
		// values flowing between requests, stopOnFailure — has to be one
		// thing whether the Run button or an agent started it.
		if before != nil {
			if err := before(ctx, node); err != nil {
				summary.Refused = append(summary.Refused, RefusedRequest{
					Path: node.ID, Reason: err.Error(),
				})
				if stopOnFailure {
					summary.State = RunStopped
					summary.Skipped = len(requests) - i - 1
					break
				}
				continue
			}
		}
		// Each request is a send in its own right: its own id, its own
		// events, its own held response. The run is the sequence, not a
		// different kind of send.
		sendID := newSendID()
		at := time.Now()
		out := s.run(ctx, sendID, loaded, node, envName, at)

		result := RunResult{
			RunID: runID, Index: i, Path: node.ID, Name: node.Name,
			SendID: sendID, Passed: out.ok(), At: time.Now(),
		}
		switch {
		case out.meta != nil:
			result.StatusCode = out.meta.StatusCode
			result.Status = out.meta.Status
			result.DurationMs = out.meta.DurationMs
			if !out.ok() {
				result.Message = out.meta.Status
			}
		case out.failure != nil:
			result.DurationMs = out.failure.DurationMs
			result.Message = out.failure.Message
		}
		if result.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		s.emit(events.RunResult, result)

		if !result.Passed && stopOnFailure {
			summary.State = RunStopped
			summary.Skipped = len(requests) - i - 1
			break
		}
	}

	if ctx.Err() != nil && summary.State == RunFinished {
		summary.State = RunCancelled
	}
	summary.DurationMs = ms(time.Since(started))
	summary.At = time.Now()
	s.emit(events.RunComplete, summary)
	return summary
}

// scriptVars builds the variable store a send's scripts write through.
func (s *SendService) scriptVars(loaded *collection.Collection, node *collection.Node, env *resolve.Environment) *scriptrun.Vars {
	sv := &scriptrun.Vars{
		Request: map[string]string{},
		Folder:  path.Dir(node.ID),
		Session: s.vars,
		Origin:  node.ID,
	}
	if sv.Folder == "." {
		sv.Folder = ""
	}
	// The scope a script reads through is built with secrets.Placeholder, so
	// vars.get can say a name resolves without ever fetching a value.
	sv.Scope = func() *resolve.Scope {
		levels := resolve.LevelsOf(node)
		scope := resolve.NewScope(levels, env, secrets.Placeholder{}, resolve.CollectionKey(loaded))
		if s.vars != nil {
			scope.WithSession(s.vars, resolve.FolderChain(node.ID))
		}
		return scope.WithExtra(sv.Request)
	}
	if env != nil {
		sv.EnvName = env.Name
		sv.ReadEnv = func(name string) (string, bool) {
			v, ok := env.Values[name]
			if !ok || v.Secret {
				return "", false
			}
			return v.Value, true
		}
		// vars.env.set writes a committed file, which is the loudest thing in
		// the API and deliberately so (docs/FORMAT.md §9.4). It goes through
		// the write guard like every write Otis makes, and announces itself.
		sv.WriteEnv = func(name, value string) error {
			return s.writeEnvVar(loaded, env, name, value)
		}
	}
	return sv
}

// writeEnvVar persists vars.env.set to the active environment file.
func (s *SendService) writeEnvVar(loaded *collection.Collection, env *resolve.Environment, name, value string) error {
	// Re-read rather than reuse the parsed copy: a script writing during a
	// folder run must not clobber a change made between requests.
	current, err := resolve.LoadEnvironment(loaded.Dir, env.Name)
	if err != nil {
		return err
	}
	if existing, ok := current.Values[name]; ok && existing.Secret {
		return fmt.Errorf("%s declares %q as a secret; a script cannot overwrite one with a plain value",
			current.Path, name)
	}
	if _, ok := current.Values[name]; !ok {
		current.Order = append(current.Order, name)
	}
	current.Values[name] = resolve.EnvValue{Value: value, Kind: resolve.EnvString}

	target := filepath.Join(loaded.Dir, filepath.FromSlash(current.Path))
	release := s.collections.Guard().Writing(target)
	writeErr := writeFileAtomic(target, current.Marshal())
	release()
	if writeErr != nil {
		return fmt.Errorf("writing %s: %w", current.Path, writeErr)
	}
	// The guard means the watcher will not announce this, so the write does —
	// through the hook the environment service registered, rather than by
	// reaching into it from here.
	s.collections.NotifyDiskChange()
	return nil
}

// asScriptError digs a *script.Error out of an error chain.
func asScriptError(err error) *script.Error {
	for err != nil {
		if e, ok := err.(*script.Error); ok {
			return e
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil
		}
		err = u.Unwrap()
	}
	return nil
}

// describeScriptError converts a script failure for the window.
func describeScriptError(err *script.Error) *ScriptFailure {
	return &ScriptFailure{
		Phase: string(err.Phase), Path: err.Path, Line: err.Line,
		Message: err.Msg, Timeout: err.Timeout,
	}
}

// failScript reports a script failure as a send failure.
func (s *SendService) failScript(id, nodePath string, err *script.Error, started time.Time) *SendFailure {
	message := "A pre-request script failed."
	if err.Timeout {
		message = "A script ran longer than its budget and was stopped."
	}
	failure := s.fail(id, nodePath, FailScript, message, err.Error(), started)
	return failure
}

// emitTest streams one test result as it completes (docs/FORMAT.md §9.9).
func (s *SendService) emitTest(sendID, nodePath string, result script.TestResult) {
	s.emit(events.ScriptTest, ScriptTest{SendID: sendID, Path: nodePath, Result: result})
}

// emitConsole streams one console line.
func (s *SendService) emitConsole(sendID, nodePath string, line script.ConsoleLine) {
	s.emit(events.ScriptConsole, ScriptConsole{SendID: sendID, Path: nodePath, Line: line})
}

// ScriptTest is one streamed test result.
type ScriptTest struct {
	SendID string            `json:"sendId"`
	Path   string            `json:"path"`
	Result script.TestResult `json:"result"`
}

// ScriptConsole is one streamed console line.
type ScriptConsole struct {
	SendID string             `json:"sendId"`
	Path   string             `json:"path"`
	Line   script.ConsoleLine `json:"line"`
}
