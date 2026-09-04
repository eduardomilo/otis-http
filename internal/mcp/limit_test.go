package mcp

import (
	"sync"
	"testing"
	"time"
)

// The rates as docs/MCP.md §10 states them. Locked here so a change to the
// table is a change to this test, made deliberately: the numbers encode that
// a send is not recoverable and a write is.
func TestTheRatesAreTheOnesInTheDocument(t *testing.T) {
	want := map[Capability]rate{
		CapRead:  {perSecond: 10, burst: 30},
		CapRun:   {perSecond: 1, burst: 5},
		CapWrite: {perSecond: 2, burst: 10},
	}
	if len(rates) != len(want) {
		t.Fatalf("got %d capabilities with rates, want %d", len(rates), len(want))
	}
	for c, w := range want {
		got, ok := rates[c]
		if !ok {
			t.Errorf("%s has no rate", c)
			continue
		}
		if got != w {
			t.Errorf("%s = %v/s burst %v, want %v/s burst %v", c, got.perSecond, got.burst, w.perSecond, w.burst)
		}
	}
	// The ordering that carries the reasoning: RUN is the tightest, because
	// a send cannot be taken back.
	if rates[CapRun].perSecond >= rates[CapWrite].perSecond {
		t.Error("RUN is not tighter than WRITE, which inverts §10's reasoning")
	}
	if rates[CapWrite].perSecond >= rates[CapRead].perSecond {
		t.Error("WRITE is not tighter than READ")
	}
}

func TestTheBucketStartsFullAndRefills(t *testing.T) {
	clock := time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC)
	l := NewLimiter()
	l.now = func() time.Time { return clock }

	// The burst is what the first calls of a session are for.
	for i := 0; i < 5; i++ {
		if !l.Allow(CapRun) {
			t.Fatalf("call %d was refused inside the burst of 5", i+1)
		}
	}
	if l.Allow(CapRun) {
		t.Fatal("the sixth call was allowed past a burst of 5")
	}

	// One second, one token, at 1/s.
	clock = clock.Add(time.Second)
	if !l.Allow(CapRun) {
		t.Error("a second later, one call should be allowed")
	}
	if l.Allow(CapRun) {
		t.Error("a second later, two calls were allowed at 1/s")
	}

	// The bucket does not grow past its burst however long it is idle.
	clock = clock.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if !l.Allow(CapRun) {
			t.Fatalf("call %d after an idle hour was refused", i+1)
		}
	}
	if l.Allow(CapRun) {
		t.Error("an idle hour banked more than the burst")
	}
}

func TestEachCapabilityHasItsOwnBucket(t *testing.T) {
	clock := time.Now()
	l := NewLimiter()
	l.now = func() time.Time { return clock }

	// Drain RUN.
	for l.Allow(CapRun) {
	}
	// READ and WRITE are untouched: an agent that has been sending too fast
	// can still be told what is in the collection.
	if !l.Allow(CapRead) {
		t.Error("draining RUN took READ's budget")
	}
	if !l.Allow(CapWrite) {
		t.Error("draining RUN took WRITE's budget")
	}
}

// A bug in a limiter reads as "no". An unknown capability is not a free pass.
func TestAnUnknownCapabilityIsRefused(t *testing.T) {
	l := NewLimiter()
	if l.Allow(Capability("admin")) {
		t.Error("an unknown capability was allowed")
	}
	if l.Allow(Capability("")) {
		t.Error("the empty capability was allowed")
	}
	if got := l.Available(Capability("admin")); got != 0 {
		t.Errorf("Available for an unknown capability = %v, want 0", got)
	}
}

// Tool calls arrive on the HTTP server's goroutines, so the buckets must hold
// under concurrency: exactly the burst gets through, never more.
func TestTheBucketHoldsUnderConcurrency(t *testing.T) {
	clock := time.Now()
	l := NewLimiter()
	l.now = func() time.Time { return clock }

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow(CapRead) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if want := int(rates[CapRead].burst); allowed != want {
		t.Errorf("%d of 200 concurrent calls were allowed, want exactly the burst of %d", allowed, want)
	}
}

func TestAvailableReportsTheBudget(t *testing.T) {
	clock := time.Date(2026, 9, 4, 11, 0, 0, 0, time.UTC)
	l := NewLimiter()
	l.now = func() time.Time { return clock }

	if got := l.Available(CapRun); got != 5 {
		t.Errorf("a fresh bucket has %v, want the burst of 5", got)
	}
	for i := 0; i < 5; i++ {
		l.Allow(CapRun)
	}
	if got := l.Available(CapRun); got != 0 {
		t.Errorf("a drained bucket has %v, want 0", got)
	}
	clock = clock.Add(2 * time.Second)
	if got := l.Available(CapRun); got != 2 {
		t.Errorf("two seconds later the bucket has %v, want 2", got)
	}
}
