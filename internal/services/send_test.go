package services

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/response"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// recorder stands in for the Wails event bus: the service emits, and the test
// waits for what it emitted. Without it every test here would poll.
type recorder struct {
	mu   sync.Mutex
	seen []recorded
	// arrived carries every event as well, so await can block rather than
	// spin. Buffered generously: a test that stops reading must not wedge the
	// service's goroutine.
	arrived chan recorded
}

type recorded struct {
	name string
	data any
}

func newRecorder() *recorder {
	return &recorder{arrived: make(chan recorded, 256)}
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	r.seen = append(r.seen, recorded{name, data})
	r.mu.Unlock()
	select {
	case r.arrived <- recorded{name, data}:
	default: // the test is not listening; `seen` still has it
	}
}

// await blocks until an event with the given name arrives, or the test fails.
// An event that arrived before the call counts: a send can finish before the
// test gets round to waiting for it.
func (r *recorder) await(t *testing.T, name string) any {
	t.Helper()
	r.mu.Lock()
	for _, e := range r.seen {
		if e.name == name {
			r.mu.Unlock()
			return e.data
		}
	}
	r.mu.Unlock()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-r.arrived:
			if e.name == name {
				return e.data
			}
		case <-deadline:
			r.mu.Lock()
			var names []string
			for _, e := range r.seen {
				names = append(names, e.name)
			}
			r.mu.Unlock()
			t.Fatalf("timed out waiting for %s; saw %v", name, names)
		}
	}
}

// reset forgets what has been seen, for a test that sends more than once.
func (r *recorder) reset() {
	r.mu.Lock()
	r.seen = nil
	r.mu.Unlock()
	for {
		select {
		case <-r.arrived:
		default:
			return
		}
	}
}

// sendFixture opens a collection with one folder level and returns a send
// service wired to a recorder.
func sendFixture(t *testing.T, files map[string]string, store secrets.Store) (*SendService, *recorder) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	collections := NewCollectionService(settings.NewStore(filepath.Join(t.TempDir(), "settings.json")))
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	svc := NewSendService(collections, store)
	rec := newRecorder()
	svc.emitter = rec.emit
	t.Cleanup(svc.cancelAll)
	return svc, rec
}

func TestSendAgainstALocalServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=abc; Path=/; HttpOnly")
		w.Header().Add("X-Trace", "one")
		w.Header().Add("X-Trace", "two")
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"echoed":{"method":%q,"accept":%q,"auth":%q},"items":[1,2]}`,
			r.Method, r.Header.Get("Accept"), r.Header.Get("Authorization"))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{
		"_folder.http":       "# @auth bearer {{token}}\nAccept: application/json\n",
		"orders/create.http": "# @name Create order\nPOST " + server.URL + "/orders\nX-Client: otis\n\n{\"a\":1}\n",
		"env/dev.json":       `{"token":"tok_123"}`,
	}, nil)

	id, err := svc.Send("orders/create.http", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Send returned no id")
	}

	started := rec.await(t, events.SendStarted).(SendStarted)
	if started.SendID != id || started.Method != "POST" {
		t.Errorf("started = %+v", started)
	}

	meta := rec.await(t, events.SendComplete).(ResponseMeta)
	if meta.SendID != id {
		t.Errorf("complete is for %s, want %s", meta.SendID, id)
	}
	if meta.StatusCode != 201 {
		t.Errorf("status = %d, want 201", meta.StatusCode)
	}
	if meta.State != SendComplete {
		t.Errorf("state = %q", meta.State)
	}
	if meta.DurationMs <= 0 {
		t.Error("duration is not positive")
	}
	if meta.Size == 0 {
		t.Error("size is zero")
	}
	if meta.Body.Kind != response.KindJSON {
		t.Errorf("body kind = %q, want json", meta.Body.Kind)
	}
	if !meta.Body.HasPretty {
		t.Error("HasPretty is false for a JSON body")
	}

	// Duplicated response headers are both kept.
	var traces []string
	for _, h := range meta.Headers {
		if h.Name == "X-Trace" {
			traces = append(traces, h.Value)
		}
	}
	if len(traces) != 2 {
		t.Errorf("X-Trace = %v, want both values", traces)
	}
	// Cookies are parsed out for their own tab.
	if len(meta.Cookies) != 1 || meta.Cookies[0].Name != "session" || !meta.Cookies[0].HTTPOnly {
		t.Errorf("cookies = %+v", meta.Cookies)
	}

	// The body is paged, not marshalled: the view says how many lines, and
	// Lines returns a window of them.
	view, err := svc.View(id, response.Pretty)
	if err != nil {
		t.Fatal(err)
	}
	if view.Unavailable != "" {
		t.Fatalf("pretty view unavailable: %s", view.Unavailable)
	}
	if view.Lines < 5 {
		t.Errorf("pretty view has %d lines", view.Lines)
	}
	chunk, err := svc.Lines(id, response.Pretty, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunk.Lines) != 4 || chunk.Total != view.Lines {
		t.Errorf("chunk = %+v", chunk)
	}
	if chunk.Lines[0].Text != "{" {
		t.Errorf("first line = %q, want {", chunk.Lines[0].Text)
	}
}

// The Headers tab predicts what will be sent; the response records it. They
// have to agree, including the Authorization the inherited @auth becomes.
func TestWireRequestMatchesTheHeadersTabPrediction(t *testing.T) {
	var got http.Header
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotMethod = r.Header.Clone(), r.Method
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	files := map[string]string{
		"_folder.http":        "X-Tenant: acme\nAccept-Language: en-GB\n",
		"orders/_folder.http": "# @auth bearer {{token}}\nAccept: application/json\n",
		"orders/create.http": "# @name Create order\nPOST " + server.URL + "/orders\n" +
			"Content-Type: application/json\nAccept: application/vnd.acme+json\nX-Tenant: !inherit\n",
		"env/dev.json": `{"token":"tok_123"}`,
	}
	svc, rec := sendFixture(t, files, nil)
	requests := NewRequestService(svc.collections)

	// What the editor claims.
	doc, err := requests.Load("orders/create.http", "dev")
	if err != nil {
		t.Fatal(err)
	}
	predicted := map[string]string{}
	for _, h := range doc.Effective.Headers {
		predicted[strings.ToLower(h.Name)] = h.Value
	}
	if doc.AuthHeader != nil {
		predicted[strings.ToLower(doc.AuthHeader.Name)] = doc.AuthHeader.Value
	}
	t.Logf("Headers tab predicts %d sent (%d local, %d inherited)",
		doc.Counts.Sent, doc.Counts.Local, doc.Counts.Inherited)

	// What went out.
	if _, err := svc.Send("orders/create.http", "dev"); err != nil {
		t.Fatal(err)
	}
	meta := rec.await(t, events.SendComplete).(ResponseMeta)

	if gotMethod != "POST" {
		t.Errorf("method on the wire = %q", gotMethod)
	}
	// Every header the tab predicted is on the wire, with the variables now
	// resolved.
	for name, claimed := range predicted {
		onWire := got.Get(name)
		if onWire == "" {
			t.Errorf("%s was predicted but is not on the wire", name)
			continue
		}
		// The prediction carries {{variables}}; the wire carries values. The
		// prediction is checked by substituting the one variable in play.
		want := strings.ReplaceAll(claimed, "{{token}}", "tok_123")
		if onWire != want {
			t.Errorf("%s: wire %q, tab predicted %q", name, onWire, want)
		}
	}
	// And nothing the tab did not predict, bar what net/http adds itself.
	added := map[string]bool{"user-agent": true, "content-length": true, "accept-encoding": true}
	for name := range got {
		lower := strings.ToLower(name)
		if _, claimed := predicted[lower]; !claimed && !added[lower] {
			t.Errorf("%s is on the wire but the Headers tab did not predict it", name)
		}
	}
	// X-Tenant was switched off with !inherit, so it must not be there.
	if got.Get("X-Tenant") != "" {
		t.Errorf("X-Tenant = %q; !inherit should have removed it", got.Get("X-Tenant"))
	}
	// The recorded request agrees with the count, so the pane and the tab
	// cannot drift.
	if len(meta.Request.Headers) != doc.Counts.Sent {
		t.Errorf("recorded %d headers on the wire, the tab predicted %d sent",
			len(meta.Request.Headers), doc.Counts.Sent)
	}
}

// A secret must not reach the window, and the record of what was sent is one
// of the places it would be easiest to leak.
func TestSentRequestMasksSecrets(t *testing.T) {
	const value = "sk-live-do-not-print"
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	store := secrets.NewMemory()
	svc, rec := sendFixture(t, map[string]string{
		"_folder.http": "# @auth bearer {{apiKey}}\n",
		"get.http":     "GET " + server.URL + "/me\n",
		"env/dev.json": `{"apiKey":{"$secret":"keychain"}}`,
	}, store)
	// The collection key is the root directory's base name.
	root := svc.collections.Current().Path
	if err := store.Set(secrets.Key(filepath.Base(root), "dev", "apiKey"), value); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Send("get.http", "dev"); err != nil {
		t.Fatal(err)
	}
	started := rec.await(t, events.SendStarted).(SendStarted)
	meta := rec.await(t, events.SendComplete).(ResponseMeta)

	// The real value went on the wire...
	if seen != "Bearer "+value {
		t.Fatalf("the server saw %q, want the real credential", seen)
	}
	// ...and nothing the window receives carries it.
	for _, h := range meta.Request.Headers {
		if strings.Contains(h.Value, value) {
			t.Errorf("the recorded request header %s carries the secret", h.Name)
		}
		if h.Name == "Authorization" {
			if !h.Secret {
				t.Error("the Authorization header is not marked as masked")
			}
			if !strings.Contains(h.Value, resolve.MaskPlaceholder) {
				t.Errorf("Authorization = %q, want the mask", h.Value)
			}
		}
	}
	if strings.Contains(started.URL, value) || strings.Contains(meta.Request.URL, value) {
		t.Error("a URL carries the secret")
	}
}

// A missing secret names its key, never a value, and says what to do.
func TestMissingSecretIsAReadableFailure(t *testing.T) {
	svc, rec := sendFixture(t, map[string]string{
		"get.http":     "GET https://example.invalid/me\nAuthorization: Bearer {{apiKey}}\n",
		"env/dev.json": `{"apiKey":{"$secret":"keychain"}}`,
	}, secrets.NewMemory())

	if _, err := svc.Send("get.http", "dev"); err != nil {
		t.Fatal(err)
	}
	failure := rec.await(t, events.SendError).(SendFailure)
	if failure.Kind != FailResolve {
		t.Errorf("kind = %q, want resolve", failure.Kind)
	}
	if !strings.Contains(failure.Message, "apiKey") {
		t.Errorf("message does not name the secret: %q", failure.Message)
	}
	if !strings.Contains(failure.Message, "/dev/apiKey") {
		t.Errorf("message does not name the keychain entry: %q", failure.Message)
	}
	// It never got as far as the wire.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, e := range rec.seen {
		if e.name == events.SendStarted {
			t.Error("a send that could not resolve still reported itself started")
		}
	}
}

func TestUnresolvedVariableIsAReadableFailure(t *testing.T) {
	svc, rec := sendFixture(t, map[string]string{
		"get.http": "GET {{baseUrl}}/me\n",
	}, nil)
	if _, err := svc.Send("get.http", ""); err != nil {
		t.Fatal(err)
	}
	failure := rec.await(t, events.SendError).(SendFailure)
	if failure.Kind != FailResolve || !strings.Contains(failure.Message, "baseUrl") {
		t.Errorf("failure = %+v, want a resolve failure naming baseUrl", failure)
	}
}

// Each transport failure class gets its own reading, because the pane renders
// them differently and "dial tcp: ..." is not something to hand a user.
func TestFailureClasses(t *testing.T) {
	// A port nothing is listening on: bind one, note it, release it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + listener.Addr().String() + "/"
	listener.Close()

	// A TLS server, spoken to as if it were plaintext, and the reverse.
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tlsServer.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // outlives the @timeout below
	}))
	defer plain.Close()

	cases := []struct {
		name     string
		file     string
		want     FailureKind
		contains string
	}{
		{
			name: "unknown host",
			file: "GET http://otis-does-not-exist.invalid/\n",
			want: FailDNS, contains: "resolved",
		},
		{
			name: "connection refused",
			file: "GET " + refused + "\n",
			want: FailRefused, contains: "refused",
		},
		{
			name: "untrusted certificate",
			file: "GET " + tlsServer.URL + "/\n",
			want: FailTLS, contains: "certificate",
		},
		{
			name: "timeout",
			file: "# @timeout 0.2\nGET " + plain.URL + "/\n",
			want: FailTimeout, contains: "timed out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, rec := sendFixture(t, map[string]string{"get.http": tc.file}, nil)
			if _, err := svc.Send("get.http", ""); err != nil {
				t.Fatal(err)
			}
			failure := rec.await(t, events.SendError).(SendFailure)
			if failure.Kind != tc.want {
				t.Errorf("kind = %q, want %q (message %q, detail %q)",
					failure.Kind, tc.want, failure.Message, failure.Detail)
			}
			if !strings.Contains(strings.ToLower(failure.Message), tc.contains) {
				t.Errorf("message = %q, want it to mention %q", failure.Message, tc.contains)
			}
			// The message is a sentence, not a Go error.
			if strings.Contains(failure.Message, "dial tcp") || strings.Contains(failure.Message, "x509:") {
				t.Errorf("message is a Go error rather than a reading: %q", failure.Message)
			}
			t.Logf("%s -> %s: %s", tc.name, failure.Kind, failure.Message)
		})
	}
}

// Cancelling mid-flight stops the send, reports it as cancelled rather than
// failed, and leaves no goroutine behind.
func TestCancelMidFlight(t *testing.T) {
	release := make(chan struct{})
	arrived := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(arrived) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	svc, rec := sendFixture(t, map[string]string{
		"slow.http": "# @timeout 30\nGET " + server.URL + "/slow\n",
	}, nil)

	id, err := svc.Send("slow.http", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the server")
	}
	if err := svc.Cancel(id); err != nil {
		t.Fatal(err)
	}

	failure := rec.await(t, events.SendError).(SendFailure)
	if failure.Kind != FailCancelled {
		t.Errorf("kind = %q, want cancelled", failure.Kind)
	}
	if failure.SendID != id {
		t.Errorf("the failure is for %s, want %s", failure.SendID, id)
	}
	// The goroutine released its cancel func, so nothing is in flight.
	deadline := time.Now().Add(5 * time.Second)
	for {
		svc.mu.Lock()
		remaining := len(svc.inflight)
		svc.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d sends still in flight after the cancel", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Cancelling again is not an error: the click and the response can cross.
	if err := svc.Cancel(id); err != nil {
		t.Errorf("cancelling a finished send: %v", err)
	}
}

// A body that says it is JSON and is not says so, and the raw view still works.
func TestBodyThatIsNotWhatItClaims(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"truncated": `))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{"get.http": "GET " + server.URL + "/\n"}, nil)
	id, err := svc.Send("get.http", "")
	if err != nil {
		t.Fatal(err)
	}
	rec.await(t, events.SendComplete)

	view, err := svc.View(id, response.Pretty)
	if err != nil {
		t.Fatal(err)
	}
	if view.Unavailable == "" {
		t.Error("a body that does not parse reported a formatted view")
	}
	t.Logf("pretty unavailable: %s", view.Unavailable)
	raw, err := svc.View(id, response.Raw)
	if err != nil || raw.Lines == 0 {
		t.Errorf("raw view = %+v, %v; it must always work", raw, err)
	}
}

func TestLinesRefusesAnOversizedChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	svc, rec := sendFixture(t, map[string]string{"get.http": "GET " + server.URL + "/\n"}, nil)
	id, _ := svc.Send("get.http", "")
	rec.await(t, events.SendComplete)

	if _, err := svc.Lines(id, response.Raw, 0, MaxChunkLines+1); err == nil {
		t.Error("Lines accepted a chunk past the limit; the point of paging is that it cannot")
	}
	// Past the end is empty rather than an error: a virtualizer overshoots.
	chunk, err := svc.Lines(id, response.Raw, 10_000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunk.Lines) != 0 {
		t.Errorf("lines past the end = %+v, want none", chunk.Lines)
	}
}

// Session variables resolve into a request and are read-only to the window.
func TestSessionVariablesResolveAndClear(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{
		"orders/get.http": "GET " + server.URL + "/orders/{{orderId}}\n",
	}, nil)
	svc.varsStore().Set(resolve.SessionValue{
		Scope: resolve.SessionFolder, Owner: "orders",
		Name: "orderId", Value: "ord_9", Origin: "orders/create.http",
	})

	if _, err := svc.Send("orders/get.http", ""); err != nil {
		t.Fatal(err)
	}
	rec.await(t, events.SendComplete)
	if seen != "/orders/ord_9" {
		t.Errorf("path on the wire = %q, want the session value resolved", seen)
	}

	// The window sees it with its provenance and no way to set one.
	listed := svc.SessionVars()
	if len(listed) != 1 || listed[0].Origin != "orders/create.http" || listed[0].At.IsZero() {
		t.Errorf("SessionVars = %+v, want one value with its origin and time", listed)
	}
	if err := svc.ClearSessionVars(); err != nil {
		t.Fatal(err)
	}
	if got := svc.SessionVars(); len(got) != 0 {
		t.Errorf("Clear left %+v", got)
	}
}

// Closing a collection drops the cookies, the credentials and the session
// variables: none of it belongs to the next collection.
func TestResetSessionOnCollectionClose(t *testing.T) {
	svc, _ := sendFixture(t, map[string]string{"get.http": "GET https://example.invalid/\n"}, nil)
	svc.varsStore().Set(resolve.SessionValue{Scope: resolve.SessionFolder, Name: "a", Value: "1"})
	svc.collections.OnClose(svc.resetSession)

	if err := svc.collections.Close(); err != nil {
		t.Fatal(err)
	}
	if got := svc.SessionVars(); len(got) != 0 {
		t.Errorf("closing the collection left session variables: %+v", got)
	}
}

func TestSendRefusesWhatIsNotARequest(t *testing.T) {
	svc, _ := sendFixture(t, map[string]string{
		"orders/create.http": "POST https://example.invalid/\n",
		"broken.http":        "GET https://example.invalid/ junk trailing\n",
	}, nil)
	for _, path := range []string{"orders", "broken.http", "nope.http"} {
		if _, err := svc.Send(path, ""); err == nil {
			t.Errorf("Send(%q) was accepted", path)
		}
	}
}

// Cookies are kept per collection and sent back on the next request
// (docs/FORMAT.md §6), and @no-cookie-jar bypasses that.
func TestCookieJarIsSharedAcrossSends(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Cookie"))
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{
		"one.http":  "GET " + server.URL + "/one\n",
		"two.http":  "GET " + server.URL + "/two\n",
		"bare.http": "# @no-cookie-jar\nGET " + server.URL + "/bare\n",
	}, nil)

	for _, path := range []string{"one.http", "two.http", "bare.http"} {
		rec.reset()
		if _, err := svc.Send(path, ""); err != nil {
			t.Fatal(err)
		}
		rec.await(t, events.SendComplete)
	}
	if len(seen) != 3 {
		t.Fatalf("the server saw %d requests", len(seen))
	}
	if seen[0] != "" {
		t.Errorf("the first request already carried a cookie: %q", seen[0])
	}
	if !strings.Contains(seen[1], "session=abc") {
		t.Errorf("the second request did not carry the cookie: %q", seen[1])
	}
	if seen[2] != "" {
		t.Errorf("@no-cookie-jar still sent a cookie: %q", seen[2])
	}
}

// The limit on kept responses is real: a long session cannot grow without
// bound, and the response just stored is never the one evicted.
func TestOldResponsesAreEvicted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{"get.http": "GET " + server.URL + "/\n"}, nil)
	var ids []string
	for i := 0; i < MaxKeptResponses+3; i++ {
		rec.reset()
		id, err := svc.Send("get.http", "")
		if err != nil {
			t.Fatal(err)
		}
		rec.await(t, events.SendComplete)
		ids = append(ids, id)
	}
	svc.mu.Lock()
	kept := len(svc.responses)
	svc.mu.Unlock()
	if kept > MaxKeptResponses {
		t.Errorf("%d responses kept, over the limit of %d", kept, MaxKeptResponses)
	}
	// The newest is still there; the oldest is not.
	if _, err := svc.Meta(ids[len(ids)-1]); err != nil {
		t.Errorf("the newest response was evicted: %v", err)
	}
	if _, err := svc.Meta(ids[0]); err == nil {
		t.Error("the oldest response is still held after the limit was passed")
	}
	// Discard frees one on request.
	last := ids[len(ids)-1]
	if err := svc.Discard(last); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Meta(last); err == nil {
		t.Error("Discard did not free the response")
	}
}

func TestRedirectsAreFollowedAndRecorded(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/from" {
			http.Redirect(w, r, target.URL+"/to", http.StatusFound)
			return
		}
		w.Write([]byte(`{"arrived":true}`))
	}))
	defer target.Close()

	svc, rec := sendFixture(t, map[string]string{
		"go.http":   "GET " + target.URL + "/from\n",
		"stay.http": "# @no-redirect\nGET " + target.URL + "/from\n",
	}, nil)

	if _, err := svc.Send("go.http", ""); err != nil {
		t.Fatal(err)
	}
	meta := rec.await(t, events.SendComplete).(ResponseMeta)
	if meta.StatusCode != 200 || len(meta.Redirects) != 1 {
		t.Errorf("followed = %d %+v, want 200 after one hop", meta.StatusCode, meta.Redirects)
	}
	if !strings.HasSuffix(meta.FinalURL, "/to") {
		t.Errorf("final URL = %q", meta.FinalURL)
	}

	rec.reset()
	if _, err := svc.Send("stay.http", ""); err != nil {
		t.Fatal(err)
	}
	meta = rec.await(t, events.SendComplete).(ResponseMeta)
	if meta.StatusCode != http.StatusFound {
		t.Errorf("@no-redirect gave %d, want 302", meta.StatusCode)
	}
}

func TestMetaForAnUnknownSendIsAnError(t *testing.T) {
	svc, _ := sendFixture(t, map[string]string{"get.http": "GET https://example.invalid/\n"}, nil)
	if _, err := svc.Meta("nope"); err == nil {
		t.Error("Meta accepted an unknown send id")
	}
	if _, err := svc.Lines("nope", response.Raw, 0, 1); err == nil {
		t.Error("Lines accepted an unknown send id")
	}
}

func TestClassifySendErrorFallsBackReadably(t *testing.T) {
	kind, message := classifySendError(errors.New("something the transport made up"))
	if kind != FailNetwork {
		t.Errorf("kind = %q, want network", kind)
	}
	if message == "" || strings.Contains(message, "made up") {
		t.Errorf("message = %q; the Go error belongs in Detail", message)
	}
}
