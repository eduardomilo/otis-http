package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/response"
	"github.com/otis-http/otis/internal/secrets"
)

// docs/MCP.md §15, verification step 5 — the redaction guarantee.
//
// This is written before any MCP tool exists, because it is the only step in
// that list whose failure is unrecoverable. A bad send can be re-run and a bad
// write is in git; a credential in an agent's transcript is disclosed, and the
// transcript is retained, replayed and sent to a model provider.
//
// The shape of the test is the worst case rather than a typical one: an
// endpoint that echoes the request's Authorization header into its own
// response body. Nothing about how carefully Otis masks the *request* helps
// there — the credential comes back as the server's data, in the one part of a
// send Otis cannot mask in advance. Tests may not touch the network
// (CLAUDE.md), so the endpoint is an httptest server, which is also the only
// way to have it echo on purpose.
func TestASecretCannotReachAnAgentThroughAResponseBody(t *testing.T) {
	const value = "sk-live-must-not-reach-an-agent"

	var sawOnTheWire string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawOnTheWire = r.Header.Get("Authorization")
		// The hostile-by-accident endpoint: it hands the credential back.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"youSent":   map[string]string{"authorization": r.Header.Get("Authorization")},
			"tokenEcho": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		})
	}))
	defer server.Close()

	store := secrets.NewMemory()
	svc, rec := sendFixture(t, map[string]string{
		"_folder.http": "# @auth bearer {{apiKey}}\n",
		"get.http":     "GET " + server.URL + "/me\n",
		"env/dev.json": `{"apiKey":{"$secret":"keychain"}}`,
	}, store)
	root := svc.collections.Current().Path
	if err := store.Set(secrets.Key(filepath.Base(root), "dev", "apiKey"), value); err != nil {
		t.Fatal(err)
	}

	id, err := svc.Send("get.http", "dev")
	if err != nil {
		t.Fatal(err)
	}
	rec.await(t, events.SendStarted)
	meta := rec.await(t, events.SendComplete).(ResponseMeta)
	if meta.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", meta.StatusCode)
	}

	// The send really happened, with the real credential. Without this the
	// rest of the test would pass just as well against a request that never
	// resolved the secret at all.
	if sawOnTheWire != "Bearer "+value {
		t.Fatalf("the server saw %q, want the real credential", sawOnTheWire)
	}

	chunk, err := svc.Lines(id, response.Pretty, 0, 200)
	if err != nil {
		t.Fatal(err)
	}

	// The body Otis holds *does* carry the credential. This assertion is
	// what makes the next one mean something: if the body were clean here,
	// the redaction below would be proving nothing.
	held, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(held), value) {
		t.Fatalf("the held body does not carry the secret, so this test cannot show redaction works: %s", held)
	}

	// What an agent would receive.
	redactor, err := svc.redactor(id)
	if err != nil {
		t.Fatal(err)
	}
	out, err := redactor.Marshal(chunk)
	if err != nil {
		t.Fatalf("redaction refused the result: %v", err)
	}
	if strings.Contains(string(out), value) {
		t.Fatalf("THE SECRET REACHED THE AGENT: %s", out)
	}
	if !strings.Contains(string(out), resolve.MaskPlaceholder) {
		t.Fatalf("nothing was masked, so the body was not what it should have been: %s", out)
	}
	// Both echoes are covered, not just the one inside the Bearer prefix.
	if n := strings.Count(string(out), resolve.MaskPlaceholder); n < 2 {
		t.Errorf("got %d masked spots, want both echoes of the credential: %s", n, out)
	}

	// And the same for the response metadata a tool would return beside it.
	metaOut, err := redactor.Marshal(meta)
	if err != nil {
		t.Fatalf("redaction refused the metadata: %v", err)
	}
	if strings.Contains(string(metaOut), value) {
		t.Fatalf("THE SECRET REACHED THE AGENT through the metadata: %s", metaOut)
	}
}

// A send with no secret in it must still come back through a working
// redactor, so that no tool ends up written against a nil one.
func TestARequestWithNoSecretsStillHasARedactor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	svc, rec := sendFixture(t, map[string]string{
		"get.http": "GET " + server.URL + "/ok\n",
	}, secrets.NewMemory())

	id, err := svc.Send("get.http", "")
	if err != nil {
		t.Fatal(err)
	}
	rec.await(t, events.SendStarted)
	rec.await(t, events.SendComplete)

	redactor, err := svc.redactor(id)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := svc.Lines(id, response.Pretty, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	out, err := redactor.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ok") {
		t.Errorf("the body did not survive redaction: %s", out)
	}
	if strings.Contains(string(out), resolve.MaskPlaceholder) {
		t.Errorf("something was masked in a request with no secrets: %s", out)
	}
}

// The redactor for a send that is not held is an error, not a permissive
// default. A tool that has lost track of its send id must not get a Redactor
// that masks nothing.
func TestNoRedactorForAnUnknownSend(t *testing.T) {
	svc, _ := sendFixture(t, map[string]string{"get.http": "GET http://127.0.0.1:1/x\n"}, secrets.NewMemory())
	if _, err := svc.redactor("nope"); err == nil {
		t.Fatal("an unknown send id produced a redactor")
	}
}
