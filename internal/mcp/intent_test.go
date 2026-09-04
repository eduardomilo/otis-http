package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func printFor() RequestPrint {
	return RequestPrint{
		Method:      "POST",
		URL:         "https://api.acme.com/v2/orders",
		Headers:     []string{"Content-Type: application/json", "Authorization: Bearer sk-live-abc"},
		Body:        []byte(`{"sku":"A-1"}`),
		Environment: "production",
		Session:     map[string]string{"orderId": "7"},
	}
}

func issued(t *testing.T, s *Intents, p RequestPrint) *Intent {
	t.Helper()
	intent, err := s.Issue("send_request", "orders/create.http", p.Environment, p.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

// Single-use. An intent spent once is gone, so a replayed tool call is a
// preview again rather than a second send.
func TestAnIntentIsSingleUse(t *testing.T) {
	s := NewIntents()
	p := printFor()
	intent := issued(t, s, p)

	if _, err := s.Redeem("send_request", intent.ID, p.Fingerprint()); err != nil {
		t.Fatalf("the first redemption failed: %v", err)
	}
	_, err := s.Redeem("send_request", intent.ID, p.Fingerprint())
	if !errors.Is(err, ErrNoSuchIntent) {
		t.Errorf("the second redemption gave %v, want ErrNoSuchIntent", err)
	}
	if n := s.Outstanding(); n != 0 {
		t.Errorf("%d intents outstanding after spending the only one", n)
	}
}

// The fingerprint binding, which is what makes two phases a gate rather than
// a hole: preview something harmless, edit it with update_request, then spend
// the intent on what the preview never described.
func TestTheFingerprintCoversEverythingThatChangesWhatIsSent(t *testing.T) {
	base := printFor()

	for _, tc := range []struct {
		name  string
		alter func(*RequestPrint)
	}{
		{"the method", func(p *RequestPrint) { p.Method = "DELETE" }},
		{"the url", func(p *RequestPrint) { p.URL = "https://api.acme.com/v2/orders/9" }},
		{"the host only", func(p *RequestPrint) { p.URL = "https://evil.example/v2/orders" }},
		{"a header value", func(p *RequestPrint) { p.Headers[0] = "Content-Type: text/plain" }},
		{"an added header", func(p *RequestPrint) { p.Headers = append(p.Headers, "X-Admin: 1") }},
		{"a removed header", func(p *RequestPrint) { p.Headers = p.Headers[:1] }},
		{"the body", func(p *RequestPrint) { p.Body = []byte(`{"sku":"A-2"}`) }},
		{"one byte of the body", func(p *RequestPrint) { p.Body = []byte(`{"sku":"A-1"} `) }},
		{"an emptied body", func(p *RequestPrint) { p.Body = nil }},
		{"the environment", func(p *RequestPrint) { p.Environment = "local" }},
		{"a session value", func(p *RequestPrint) { p.Session["orderId"] = "8" }},
		{"an added session value", func(p *RequestPrint) { p.Session["token"] = "t" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewIntents()
			intent := issued(t, s, base)

			altered := printFor()
			tc.alter(&altered)
			if altered.Fingerprint() == base.Fingerprint() {
				t.Fatalf("changing %s did not change the fingerprint", tc.name)
			}

			_, err := s.Redeem("send_request", intent.ID, altered.Fingerprint())
			if !errors.Is(err, ErrIntentStale) {
				t.Fatalf("redeeming after changing %s gave %v, want ErrIntentStale", tc.name, err)
			}
			// And it is spent, so an agent cannot search for a request that
			// matches by retrying the check.
			if _, err := s.Redeem("send_request", intent.ID, base.Fingerprint()); !errors.Is(err, ErrNoSuchIntent) {
				t.Errorf("a stale redemption left the intent spendable: %v", err)
			}
		})
	}
}

// Header order is not part of what a request means, so it must not make an
// intent unspendable between the phases — a map iteration on the way in would.
func TestTheFingerprintIgnoresHeaderOrder(t *testing.T) {
	a := printFor()
	b := printFor()
	b.Headers = []string{b.Headers[1], b.Headers[0]}
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("reordering headers changed the fingerprint")
	}
}

// Length prefixing, checked at the boundary it protects: without it, moving a
// byte from a header into the body would leave the hash unchanged.
func TestFieldBoundariesCannotBeShifted(t *testing.T) {
	one := RequestPrint{Method: "GET", URL: "u", Headers: []string{"X: a"}, Body: []byte("b")}
	two := RequestPrint{Method: "GET", URL: "u", Headers: []string{"X: ab"}, Body: nil}
	if one.Fingerprint() == two.Fingerprint() {
		t.Error("a byte moved from a header to the body left the fingerprint unchanged")
	}

	three := RequestPrint{Method: "GE", URL: "Tu"}
	four := RequestPrint{Method: "GET", URL: "u"}
	if three.Fingerprint() == four.Fingerprint() {
		t.Error("a byte moved from the method to the url left the fingerprint unchanged")
	}
}

// An identical request fingerprints identically, or the gate would never open.
func TestTheFingerprintIsStable(t *testing.T) {
	first := printFor().Fingerprint()
	for i := 0; i < 20; i++ {
		if got := printFor().Fingerprint(); got != first {
			t.Fatalf("the same request fingerprinted differently: %s then %s", first, got)
		}
	}
	if len(first) != 64 {
		t.Errorf("the fingerprint is %d characters, want a sha256 hex digest", len(first))
	}
}

// The store keeps a hash, never the request: this is a map that lives in a
// process an agent is talking to.
func TestTheStoreHoldsNoHeaderBodyOrSecret(t *testing.T) {
	const secret = "sk-live-abc"
	s := NewIntents()
	p := printFor()
	intent := issued(t, s, p)

	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	// The wire form, which is what reaches the agent.
	for _, forbidden := range []string{secret, "sk-live", `{"sku":"A-1"}`, "Bearer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the intent carries %q: %s", forbidden, raw)
		}
	}
	// And the struct itself, which is what lives in the process.
	all := intent.ID + intent.Tool + intent.Target + intent.Environment + intent.Fingerprint
	for _, forbidden := range []string{secret, `{"sku":"A-1"}`, "Content-Type"} {
		if strings.Contains(all, forbidden) {
			t.Errorf("the stored intent carries %q", forbidden)
		}
	}
}

func TestAnIntentExpires(t *testing.T) {
	clock := time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC)
	s := NewIntents()
	s.now = func() time.Time { return clock }
	p := printFor()
	intent := issued(t, s, p)

	if want := clock.Add(IntentTTL); !intent.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", intent.ExpiresAt, want)
	}

	clock = clock.Add(IntentTTL - time.Second)
	if _, err := s.Redeem("send_request", intent.ID, p.Fingerprint()); err != nil {
		t.Errorf("an intent one second short of expiry was refused: %v", err)
	}

	// And one that has run out.
	intent = issued(t, s, p)
	clock = clock.Add(IntentTTL)
	_, err := s.Redeem("send_request", intent.ID, p.Fingerprint())
	if !errors.Is(err, ErrIntentExpired) {
		t.Errorf("an expired intent gave %v, want ErrIntentExpired", err)
	}
	if IntentTTL != 60*time.Second {
		t.Errorf("IntentTTL = %v, want the 60s of §14.5", IntentTTL)
	}
}

// One store holds both tools' previews, so the tool name has to be checked or
// it is decoration.
func TestAnIntentIsBoundToItsTool(t *testing.T) {
	s := NewIntents()
	p := printFor()
	intent := issued(t, s, p)

	_, err := s.Redeem("run_folder", intent.ID, p.Fingerprint())
	if !errors.Is(err, ErrNoSuchIntent) {
		t.Errorf("a send intent was spendable on a folder run: %v", err)
	}
}

// Unguessable, because the id is the only thing between one client's preview
// and another's.
func TestIntentIDsAreUnguessable(t *testing.T) {
	s := NewIntents()
	p := printFor()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		intent := issued(t, s, p)
		if seen[intent.ID] {
			t.Fatal("an intent id repeated")
		}
		if !strings.HasPrefix(intent.ID, "i_") || len(intent.ID) != 34 {
			t.Fatalf("id = %q, want i_ and 32 hex characters", intent.ID)
		}
		seen[intent.ID] = true
	}
}

// A preview that survived "Disconnect agents" would be a send waiting to
// happen on the other side of a reconnect.
func TestTheKillSwitchVoidsEveryPreview(t *testing.T) {
	s := NewIntents()
	p := printFor()
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, issued(t, s, p).ID)
	}
	if n := s.Outstanding(); n != 5 {
		t.Fatalf("%d outstanding, want 5", n)
	}

	s.Void()

	if n := s.Outstanding(); n != 0 {
		t.Errorf("%d intents survived Void", n)
	}
	for _, id := range ids {
		if _, err := s.Redeem("send_request", id, p.Fingerprint()); !errors.Is(err, ErrNoSuchIntent) {
			t.Errorf("intent %s survived Void: %v", id, err)
		}
	}
}

// Expired previews nobody spent must not accumulate across a long session.
func TestExpiredIntentsAreSweptUp(t *testing.T) {
	clock := time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC)
	s := NewIntents()
	s.now = func() time.Time { return clock }
	p := printFor()
	for i := 0; i < 10; i++ {
		issued(t, s, p)
	}
	clock = clock.Add(IntentTTL + time.Second)
	if n := s.Outstanding(); n != 0 {
		t.Errorf("%d expired intents are still held", n)
	}
	issued(t, s, p)
	if n := s.Outstanding(); n != 1 {
		t.Errorf("%d outstanding, want just the fresh one", n)
	}
}

// Two clients previewing at once must not corrupt the store or hand out a
// shared id.
func TestTheStoreHoldsUnderConcurrency(t *testing.T) {
	s := NewIntents()
	p := printFor()
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			intent, err := s.Issue("send_request", "orders/create.http", "production", p.Fingerprint())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ids[intent.ID] {
				t.Error("two goroutines were handed the same intent id")
			}
			ids[intent.ID] = true
		}()
	}
	wg.Wait()
	if n := s.Outstanding(); n != 100 {
		t.Errorf("%d outstanding, want 100", n)
	}
}
