package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The audit log (docs/MCP.md §9).
//
// Every tool call is recorded, whatever the outcome — including the ones that
// were refused, which are the interesting ones.
//
// The design decision that matters here is not in the writing, it is in the
// Entry type: there is no field for a request body, a response body, a
// header, a URL or a file's contents. That is what makes "no payload ever
// reaches the log" a property of the code rather than of the care taken by
// whoever adds the next tool. A log that quoted payloads would become the
// thing you have to protect, and it lives outside the collection precisely so
// it is not something anyone has to think about protecting.
//
// The division of labour with git is deliberate and is not a compromise: this
// file says *that* an agent touched orders/create-order.http at 11:02, and the
// diff view says what it did to it, with a diff, which git holds anyway.

// AuditDecision is what happened to a tool call.
//
// It is not mcp.Outcome. Outcome is what the policy *decided* before anyone
// was asked — proceed, confirm, deny — and this is how the call actually
// ended, which is the only thing worth recording. A call the policy sent to a
// confirmation can still end refused, timed-out or confirmed, and a log that
// stored the policy's answer could not tell those apart.
type AuditDecision string

const (
	// Allowed: policy let it through with nobody asked.
	Allowed AuditDecision = "allowed"
	// Confirmed: a person was asked and said yes.
	Confirmed AuditDecision = "confirmed"
	// Refused: a person was asked and said no.
	Refused AuditDecision = "refused"
	// DeniedByPolicy: never reached a person.
	DeniedByPolicy AuditDecision = "denied-by-policy"
	// TimedOut: a person was asked and did not answer.
	TimedOut AuditDecision = "timed-out"
	// RateLimited: refused by §10's limiter.
	RateLimited AuditDecision = "rate-limited"
)

// AuditSurface is where the person was actually asked.
//
// Also not mcp.Surface. That one is a *constraint* the policy places on a
// call — "this may only be confirmed in the window" — and this is where the
// asking happened. Recording the second is what makes §6.4 checkable after
// the fact: a call the policy marked window-only that was logged as confirmed
// on the client is a bug you can find in the log.
type AuditSurface string

const (
	// OnClient: the MCP client asked, by elicitation.
	OnClient AuditSurface = "client"
	// InWindow: Otis' own window asked.
	InWindow AuditSurface = "window"
	// NotAsked is for a call nobody was asked about — allowed, denied by
	// policy, or rate-limited.
	NotAsked AuditSurface = ""
)

// An Entry is one line of the log.
//
// Every field here is a name, a code or a duration. Nothing in this struct can
// hold a payload, and nothing may be added to it that can: the test in
// audit_test.go drives real sends with real secrets past it and fails if any
// value, body or header reaches a line.
type Entry struct {
	// At is when, in UTC.
	At time.Time `json:"at"`
	// Collection is the open collection's directory name. Without it a log
	// spanning two collections cannot say which orders/create-order.http was
	// touched. The name, not the absolute path — the path is on this machine
	// and adds nothing the name does not.
	Collection string `json:"collection"`
	// Tool is the tool's name.
	Tool string `json:"tool"`
	// Target is the node path of the request or folder, never a URL. A URL
	// can carry a credential in a query parameter; a node path cannot carry
	// anything that was not already committed to the repository.
	Target string `json:"target,omitempty"`
	// Environment is the environment's name, or "" for none.
	Environment string `json:"environment"`
	// Surface is where the person was asked.
	Surface AuditSurface `json:"surface"`
	// Decision is how the call ended.
	Decision AuditDecision `json:"decision"`
	// Status is an HTTP status code, a failure kind, or "created"/"modified"
	// for a write. A short code, never a message: a message is free text and
	// free text is where a URL, and then a query parameter, and then a
	// credential ends up.
	Status string `json:"status,omitempty"`
	// DurationMs is how long the call took.
	DurationMs float64 `json:"durationMs"`
	// Client is the MCP client's declared name and version.
	Client string `json:"client,omitempty"`
}

// MaxAuditBytes is the size at which the log rotates. Unbounded growth in a
// file nobody looks at is how a gigabyte of JSON turns up in a config
// directory two years later.
const MaxAuditBytes = 5 << 20

// maxRemembered bounds the in-memory copy the UI panel lists. The file is the
// record; this is just what §9's panel shows without reading it back.
const maxRemembered = 500

// DefaultAuditPath is the log's place: beside settings.json and the secret key
// index, in the OS config directory.
//
// Never inside the collection. A log in the repository would be committed, and
// then everyone on the branch would hold a record of which endpoints were
// called from your machine — the same reasoning that keeps the active
// environment out of the collection (docs/FORMAT.md §4.3).
func DefaultAuditPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("mcp: locating the config directory: %w", err)
	}
	return filepath.Join(dir, "otis", "mcp-audit.jsonl"), nil
}

// A Log records tool calls.
//
// Safe for concurrent use: tool calls arrive on the HTTP server's goroutines.
type Log struct {
	mu sync.Mutex
	// path is the file, or "" when only the in-memory copy is kept.
	path string
	// size tracks the file's length so an append does not stat it. It is
	// seeded once, on the first write.
	size   int64
	sized  bool
	recent []Entry
}

// NewLog returns a Log appending to path.
//
// An empty path keeps the session's calls in memory only, which is what
// `mcp.persistAuditLog: false` selects. The privacy trade is stated in §9.1
// rather than buried: turning the MCP server on also starts a durable record
// of which endpoints an agent was asked to call, and that switch exists for
// people who do not want the record.
func NewLog(path string) *Log { return &Log{path: path} }

// Record appends one entry.
//
// The returned error is about the *file*, and the intended handling is to
// surface it on the in-app indicator rather than to fail the tool call: a
// config directory that has become unwritable must not stop Otis working, and
// the entry is still in the in-memory list either way. A caller that wants
// "no audit, no action" has to implement that itself, deliberately.
func (l *Log) Record(e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.At = e.At.UTC().Truncate(time.Second)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.recent = append(l.recent, e)
	if len(l.recent) > maxRemembered {
		l.recent = l.recent[len(l.recent)-maxRemembered:]
	}
	if l.path == "" {
		return nil
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("mcp: encoding an audit entry: %w", err)
	}
	line = append(line, '\n')

	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("mcp: creating the config directory: %w", err)
	}
	if !l.sized {
		if info, err := os.Stat(l.path); err == nil {
			l.size = info.Size()
		}
		l.sized = true
	}
	if l.size+int64(len(line)) > MaxAuditBytes {
		if err := l.rotate(); err != nil {
			return err
		}
	}

	// 0600 because the log records the shape of your infrastructure — which
	// environments exist, which endpoints get called. Not secret, not public.
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("mcp: opening the audit log: %w", err)
	}
	defer f.Close()
	n, err := f.Write(line)
	l.size += int64(n)
	if err != nil {
		return fmt.Errorf("mcp: appending to the audit log: %w", err)
	}
	return nil
}

// rotate moves the log aside once, dropping any previous rotation.
//
// One generation, not a numbered series: the cap exists to bound the disk a
// config directory uses, and a series that keeps ten of them does not bound
// it.
func (l *Log) rotate() error {
	prev := l.path + ".1"
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: dropping the rotated audit log: %w", err)
	}
	if err := os.Rename(l.path, prev); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: rotating the audit log: %w", err)
	}
	l.size = 0
	return nil
}

// Recent returns up to n entries, newest first, for §9's panel.
func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.recent) {
		n = len(l.recent)
	}
	out := make([]Entry, 0, n)
	for i := len(l.recent) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.recent[i])
	}
	return out
}

// Path is the file this Log appends to, or "" when it is memory-only.
func (l *Log) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path
}
