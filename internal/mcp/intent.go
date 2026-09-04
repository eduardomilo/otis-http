package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Two-phase send (docs/MCP.md §6.2).
//
// `send_request` and `run_folder` called without an intent describe what would
// happen and send nothing; called with the intent handed back, they proceed to
// the confirmation. **There is no code path in which a single tool call sends
// anything**, on every client, always — being universal is what makes that a
// property rather than a configuration.
//
// Two phases are not consent. An agent will echo an intent back without a
// thought, and anything it can be talked into once it can be talked into
// twice. Phase 2 is where a *person* is asked; an implementation in which a
// returned intent skipped that would be a bug of the worst kind, which is why
// nothing in this file grants anything — Redeem returns an intent, and the
// caller still has to go through Decide.

// IntentTTL is how long a preview stays spendable (§14.5, decided: 60s).
// Longer leaves dialogs and stale previews lying around; shorter makes a
// person race a timer they did not start.
const IntentTTL = 60 * time.Second

var (
	// ErrNoSuchIntent covers an id that was never issued, has been spent, or
	// has been voided. They are deliberately one error: telling an agent
	// which of the three it hit would let it probe the store.
	ErrNoSuchIntent = errors.New("mcp: no such intent, preview again")
	// ErrIntentExpired is a preview older than IntentTTL.
	ErrIntentExpired = errors.New("mcp: this preview has expired, preview again")
	// ErrIntentStale means the request changed after it was previewed.
	ErrIntentStale = errors.New("mcp: the request changed since it was previewed, preview again")
)

// An Intent is one previewed operation.
//
// It holds a *fingerprint* of the request rather than the request, so the
// store never keeps a header value, a body or a session value — and therefore
// never keeps a credential. That is not incidental: this is a map that lives
// for a minute at a time in a process an agent is talking to.
type Intent struct {
	// ID is what the agent hands back.
	ID string `json:"intent"`
	// Tool is which tool issued it, so an intent for a send cannot be spent
	// on a folder run.
	Tool string `json:"-"`
	// Target is the node path previewed.
	Target string `json:"-"`
	// Environment is the environment previewed against.
	Environment string `json:"-"`
	// Fingerprint is the hash of the resolved request.
	Fingerprint string `json:"-"`
	// ExpiresAt is when it stops being spendable.
	ExpiresAt time.Time `json:"expiresAt"`
}

// A RequestPrint is everything the fingerprint covers.
//
// Body and headers are in it because without them two phases would be a hole
// rather than a gate: preview something harmless, edit it with
// `update_request`, then spend the intent on what the preview never described.
// With WRITE enabled that is not hypothetical.
type RequestPrint struct {
	Method  string
	URL     string
	Headers []string
	Body    []byte
	// Environment matters because the same request against production is a
	// different operation from the same request against local.
	Environment string
	// Session is the session values the resolution consumed (FORMAT.md §4.5).
	// A request whose token came from a previous response is a different
	// request when that token changes.
	Session map[string]string
}

// Fingerprint hashes the print.
//
// Each field is length-prefixed before hashing, so no arrangement of one
// field's contents can imitate the next. Without that, a header of
// "X: a" plus a body of "b" and a header of "X: ab" plus an empty body would
// hash the same, and the fingerprint would be defeated by moving a byte.
func (p RequestPrint) Fingerprint() string {
	h := sha256.New()
	write := func(label, s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write([]byte(label))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	write("method", p.Method)
	write("url", p.URL)
	write("env", p.Environment)

	// Sorted, because header order is not part of what a request means and a
	// map iteration would make the fingerprint unstable between the phases.
	headers := append([]string{}, p.Headers...)
	sort.Strings(headers)
	write("headers.count", fmt.Sprint(len(headers)))
	for _, header := range headers {
		write("header", header)
	}
	write("body", string(p.Body))

	keys := make([]string, 0, len(p.Session))
	for k := range p.Session {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	write("session.count", fmt.Sprint(len(keys)))
	for _, k := range keys {
		write("session.key", k)
		write("session.value", p.Session[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// An Intents store holds the previews that have not been spent.
type Intents struct {
	mu sync.Mutex
	by map[string]*Intent
	// now is injectable so expiry can be tested without sleeping.
	now func() time.Time
}

// NewIntents returns an empty store.
func NewIntents() *Intents {
	return &Intents{by: map[string]*Intent{}, now: time.Now}
}

// Issue records a preview and returns it.
func (s *Intents) Issue(tool, target, environment, fingerprint string) (*Intent, error) {
	id, err := intentID()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	intent := &Intent{
		ID:          id,
		Tool:        tool,
		Target:      target,
		Environment: environment,
		Fingerprint: fingerprint,
		ExpiresAt:   s.now().Add(IntentTTL).UTC().Truncate(time.Second),
	}
	s.by[id] = intent
	return intent, nil
}

// Redeem spends an intent, checking that the request has not changed since it
// was previewed.
//
// **Single-use, and spent on every outcome including a failed one.** A
// mismatch voids the intent rather than leaving it for another attempt: an
// agent that can retry a fingerprint check can search for a request that
// matches, and there is nothing a second attempt at the same intent could
// legitimately be.
//
// Redeeming grants nothing. The caller still has to ask Decide, and then a
// person.
func (s *Intents) Redeem(tool, id, fingerprint string) (*Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	intent, ok := s.by[id]
	if !ok {
		return nil, ErrNoSuchIntent
	}
	delete(s.by, id)

	if !s.now().Before(intent.ExpiresAt) {
		return nil, ErrIntentExpired
	}
	// An intent issued for a send is not spendable on a folder run. Both are
	// in one store, so without this the tool name would be decoration.
	if intent.Tool != tool {
		return nil, ErrNoSuchIntent
	}
	if intent.Fingerprint != fingerprint {
		return nil, ErrIntentStale
	}
	return intent, nil
}

// Void drops every outstanding intent.
//
// The kill switch calls it (§10): a preview that survived "Disconnect agents"
// would be a send waiting to happen on the other side of a reconnect.
func (s *Intents) Void() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.by = map[string]*Intent{}
}

// Outstanding is how many previews are live, for the indicator.
func (s *Intents) Outstanding() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	return len(s.by)
}

// sweepLocked drops expired intents so the map does not grow across a long
// session of previews nobody spent.
func (s *Intents) sweepLocked() {
	now := s.now()
	for id, intent := range s.by {
		if !now.Before(intent.ExpiresAt) {
			delete(s.by, id)
		}
	}
}

// intentID returns an unguessable id.
//
// Unguessable rather than sequential: the id is the only thing standing
// between one agent's preview and another's, and a counter would let a second
// client spend the first's intent by asking for the next number.
func intentID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcp: minting an intent id: %w", err)
	}
	return "i_" + hex.EncodeToString(buf), nil
}
