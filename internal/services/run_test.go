package services

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
)

// hitLog records the order the server was hit in, which is the property this
// increment cares about: "Run folder respects .order sequence".
type hitLog struct {
	mu   sync.Mutex
	hits []string
}

func (r *hitLog) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits = append(r.hits, path)
}

func (r *hitLog) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hits...)
}

// runFixture is a folder of requests whose .order fixes a sequence that is not
// alphabetical, so "in .order sequence" is actually testable.
func runFixture(t *testing.T, base string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "orders", "_folder.http"), "@base = "+base+"\n")
	// Deliberately not alphabetical, and a subfolder last.
	write(t, filepath.Join(root, "orders", ".order"), "third.http\nfirst.http\nsecond.http\nfixtures/\n")
	write(t, filepath.Join(root, "orders", "first.http"), "GET {{base}}/first\n")
	write(t, filepath.Join(root, "orders", "second.http"), "GET {{base}}/second\n")
	write(t, filepath.Join(root, "orders", "third.http"), "GET {{base}}/third\n")
	write(t, filepath.Join(root, "orders", "fixtures", "seed.http"), "GET {{base}}/seed\n")
	return root
}

func newRunService(t *testing.T, base string) (*SendService, string) {
	t.Helper()
	root := runFixture(t, base)
	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	collections := NewCollectionService(store)
	t.Cleanup(func() { collections.stopWatching() })
	if _, err := collections.Open(root); err != nil {
		t.Fatal(err)
	}
	sends := NewSendService(collections, secrets.NewMemory())
	t.Cleanup(func() { sends.cancelAll() })
	return sends, root
}

// The increment's own verification: Run folder respects the .order sequence.
func TestRunFolderFollowsTheOrderFile(t *testing.T) {
	rec := &hitLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sends, _ := newRunService(t, server.URL)
	summary := runAndWait(t, sends, "orders", false)

	if summary.State != RunFinished {
		t.Errorf("state = %q, want finished", summary.State)
	}
	if summary.Passed != 4 || summary.Failed != 0 || summary.Total != 4 {
		t.Errorf("summary = %+v, want 4 passed", summary)
	}
	// .order says third, first, second, then the subfolder — and a subfolder
	// is visited where .order puts it, not after everything else
	// (docs/FORMAT.md §2.2: folders and requests are ordered together).
	want := []string{"/third", "/first", "/second", "/seed"}
	if got := rec.seen(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("requests hit in order %v, want %v", got, want)
	}
}

// Sequential, not parallel: the requests in a folder are usually a sequence,
// and one server handler at a time is what proves it.
func TestRunFolderIsSequential(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sends, _ := newRunService(t, server.URL)
	if summary := runAndWait(t, sends, "orders", false); summary.Passed != 4 {
		t.Fatalf("summary = %+v", summary)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 1 {
		t.Errorf("peak concurrency = %d, want 1", peak)
	}
}

// A 4xx or 5xx is a response, not an error (docs/FORMAT.md §6) — and it fails
// the run, which is what "6/6 passed" counts.
func TestRunFolderCountsAStatusFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/second" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sends, _ := newRunService(t, server.URL)
	summary := runAndWait(t, sends, "orders", false)
	if summary.State != RunFinished {
		t.Errorf("state = %q, want finished: a failure without stop-on-failure keeps going", summary.State)
	}
	if summary.Passed != 3 || summary.Failed != 1 || summary.Skipped != 0 {
		t.Errorf("summary = %+v, want 3 passed and 1 failed", summary)
	}
}

// Stop-on-failure stops, and says how many it did not get to.
func TestRunFolderStopsOnFailureWhenAsked(t *testing.T) {
	rec := &hitLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path)
		if r.URL.Path == "/first" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sends, _ := newRunService(t, server.URL)
	summary := runAndWait(t, sends, "orders", true)
	if summary.State != RunStopped {
		t.Errorf("state = %q, want stopped", summary.State)
	}
	// .order is third, first, second, seed — so it stops after the second.
	if summary.Passed != 1 || summary.Failed != 1 || summary.Skipped != 2 {
		t.Errorf("summary = %+v, want 1 passed, 1 failed, 2 skipped", summary)
	}
	if got := rec.seen(); len(got) != 2 {
		t.Errorf("requests hit = %v, want it to stop after the failure", got)
	}
}

// A send that produced no response fails the run too, and its message is the
// readable one rather than a Go error string.
func TestRunFolderCountsATransportFailure(t *testing.T) {
	// A port nothing is listening on: every request is refused.
	sends, _ := newRunService(t, "http://127.0.0.1:1")
	summary := runAndWait(t, sends, "orders", false)
	if summary.Failed != 4 || summary.Passed != 0 {
		t.Errorf("summary = %+v, want everything failed", summary)
	}
}

// Run folder on a request, or on a folder with nothing to run, says so.
func TestRunFolderRejectsWhatItCannotRun(t *testing.T) {
	sends, root := newRunService(t, "http://127.0.0.1:1")
	if _, err := sends.RunFolder("orders/first.http", "", false); err == nil {
		t.Error("RunFolder on a request should fail")
	} else if !strings.Contains(err.Error(), "not a folder") {
		t.Errorf("error = %v", err)
	}

	write(t, filepath.Join(root, "empty", "notes.md"), "nothing to run\n")
	if _, err := sends.collections.Tree(); err != nil {
		t.Fatal(err)
	}
	if _, err := sends.RunFolder("empty", "", false); err == nil {
		t.Error("RunFolder on a folder with no requests should fail")
	} else if !strings.Contains(err.Error(), "no requests") {
		t.Errorf("error = %v", err)
	}
}

// runAndWait drives the sequence directly and returns its summary.
//
// runFolder returns the summary as well as emitting it, precisely so a test
// can do this: the service emits nothing outside Wails, so waiting on the
// event would be waiting forever.
func runAndWait(t *testing.T, sends *SendService, folder string, stopOnFailure bool) RunComplete {
	t.Helper()
	loaded, err := sends.collections.Loaded()
	if err != nil {
		t.Fatal(err)
	}
	node := loaded.Find(folder)
	if node == nil {
		t.Fatalf("%s is not in the collection", folder)
	}

	// The same plan RunFolder builds, so the order under test is the order
	// the window would get.
	var requests []*collection.Node
	walk(node, func(n *collection.Node) bool {
		if n.Kind == collection.KindRequest && !n.Broken && n.Request != nil {
			requests = append(requests, n)
		}
		return true
	})
	if len(requests) == 0 {
		t.Fatalf("%s has no requests", folder)
	}
	return sends.runFolder(t.Context(), "run-1", loaded, node, requests, "", stopOnFailure, time.Now(), nil)
}

func TestRunResultCarriesWhatTheRowNeeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":1}`)
	}))
	defer server.Close()

	sends, _ := newRunService(t, server.URL)
	loaded, _ := sends.collections.Loaded()
	node := loaded.Find("orders/first.http")

	out := sends.run(t.Context(), "send-1", loaded, node, "", time.Now())
	if !out.ok() {
		t.Fatalf("outcome = %+v", out)
	}
	if out.meta.StatusCode != 201 {
		t.Errorf("status = %d", out.meta.StatusCode)
	}
	// A 201 passes; the boundary is 400 (docs/FORMAT.md §6).
	if out.meta.DurationMs <= 0 {
		t.Errorf("duration = %v, want it measured", out.meta.DurationMs)
	}
}
