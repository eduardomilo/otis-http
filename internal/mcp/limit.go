package mcp

import (
	"sync"
	"time"
)

// Rate limiting (docs/MCP.md §10).
//
// Per capability, token bucket. WRITE is limited more loosely than RUN because
// a write is recoverable and a send is not — but it is limited at all, because
// an agent in a loop creating files is a mess somebody cleans up by hand.
//
// The limits are per running app rather than per client: two agents pointed at
// the same Otis share a budget, because the thing being protected is the
// user's API and their collection, not fairness between clients.

// rate is one capability's budget.
type rate struct {
	// perSecond is the sustained rate.
	perSecond float64
	// burst is the bucket's capacity.
	burst float64
}

var rates = map[Capability]rate{
	CapRead:  {perSecond: 10, burst: 30},
	CapRun:   {perSecond: 1, burst: 5},
	CapWrite: {perSecond: 2, burst: 10},
}

// A Limiter is the token buckets for one running app.
//
// Safe for concurrent use: tool calls arrive on the HTTP server's goroutines.
type Limiter struct {
	mu      sync.Mutex
	tokens  map[Capability]float64
	last    map[Capability]time.Time
	started bool
	// now is the clock, injectable so a test can exercise refill without
	// sleeping. A limiter tested with real sleeps is a limiter tested at one
	// rate, slowly.
	now func() time.Time
}

// NewLimiter returns a Limiter with every bucket full.
//
// Full rather than empty: the burst is what the first few calls of a session
// are for, and an agent that has to wait a second before its first read would
// look broken.
func NewLimiter() *Limiter {
	return &Limiter{
		tokens: map[Capability]float64{},
		last:   map[Capability]time.Time{},
		now:    time.Now,
	}
}

// Allow takes one token from c's bucket, reporting whether there was one.
//
// **Call this when a call is approved, never when it is asked.** A call that
// needs a confirmation consumes its budget on approval (§10): if asking spent
// the token, an agent could drain the bucket with calls a person refuses, and
// refusing would then cost that person their own next send. That is a rule
// about *where* this is called from, which no signature can enforce — the
// audit log is what makes a violation visible, since a refused call that
// consumed budget shows up as `refused` next to a `rate-limited`.
func (l *Limiter) Allow(c Capability) bool {
	limit, known := rates[c]
	if !known {
		// An unknown capability is not a free pass. Every capability with a
		// budget is in the table above; anything else is a bug, and the safe
		// reading of a bug in a limiter is "no".
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	last, seen := l.last[c]
	if !seen {
		l.tokens[c] = limit.burst
		last = now
	}
	if elapsed := now.Sub(last).Seconds(); elapsed > 0 {
		l.tokens[c] = min(limit.burst, l.tokens[c]+elapsed*limit.perSecond)
	}
	l.last[c] = now

	if l.tokens[c] < 1 {
		return false
	}
	l.tokens[c]--
	return true
}

// Available reports c's current budget, for the indicator's popover.
func (l *Limiter) Available(c Capability) float64 {
	limit, known := rates[c]
	if !known {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, seen := l.last[c]; !seen {
		return limit.burst
	}
	elapsed := l.now().Sub(l.last[c]).Seconds()
	return min(limit.burst, l.tokens[c]+elapsed*limit.perSecond)
}
