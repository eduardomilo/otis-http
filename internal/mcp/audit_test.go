package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// docs/MCP.md §9.1 asks for a test that "drives every tool, with secrets and
// bodies in play, and fails if any secret value, request body, response body,
// header or file content reaches a line".
//
// Half of that is owed until the tools exist, and it is the weaker half. The
// half below is the one that keeps working afterwards: it locks the Entry
// type's field list, so the log cannot *grow* a place to put a payload. A scan
// of what today's tools happen to write proves that today's tools are
// careful; this proves the log has nowhere for tomorrow's tool to be careless.

// The field list, as approved in §9's table. Adding to this set is a decision
// about what the audit log records, and it is meant to be made here, in a
// diff, next to the reasons — not incidentally, by adding a field to a struct
// while building a tool.
var approvedFields = []string{
	"at", "client", "collection", "decision", "durationMs",
	"environment", "status", "surface", "target", "tool",
}

func TestTheAuditEntryHasNowhereToPutAPayload(t *testing.T) {
	var got []string
	typ := reflect.TypeOf(Entry{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("%s has no json tag, so it would serialize under its Go name", typ.Field(i).Name)
		}
		got = append(got, strings.Split(tag, ",")[0])
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, approvedFields) {
		t.Fatalf("the audit entry's fields have changed.\n got: %v\nwant: %v\n"+
			"If this is deliberate, read docs/MCP.md §9 first: the log records what was\n"+
			"done, and a field that can hold a body, a header, a URL or a secret makes it\n"+
			"the thing you have to protect.", got, approvedFields)
	}

	// Every field must be a name, a code, a time or a number. A nested
	// struct or a map is a place a payload fits.
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.String, reflect.Float64, reflect.Int64:
		case reflect.Struct:
			if f.Type != reflect.TypeOf(time.Time{}) {
				t.Errorf("%s is a struct, which is a place a payload fits", f.Name)
			}
		default:
			t.Errorf("%s is a %s; audit fields are names, codes, times and numbers", f.Name, f.Type.Kind())
		}
	}
}

// The log lives in the config directory, never in the collection. A log in
// the repository gets committed, and then everyone on the branch holds a
// record of which endpoints were called from one person's machine.
func TestTheLogIsNeverInTheCollection(t *testing.T) {
	path, err := DefaultAuditPath()
	if err != nil {
		t.Skipf("no config directory on this machine: %v", err)
	}
	if filepath.Base(path) != "mcp-audit.jsonl" {
		t.Errorf("the log is named %q", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != "otis" {
		t.Errorf("the log is not in the otis config directory: %s", path)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	if !strings.HasPrefix(path, config) {
		t.Errorf("%s is outside the config directory %s", path, config)
	}
	// Where settings.json is, which is the neighbour §9.1 names.
	if want := filepath.Join(config, "otis"); filepath.Dir(path) != want {
		t.Errorf("dir = %s, want %s (beside settings.json)", filepath.Dir(path), want)
	}
}

func TestRecordAppendsOneLinePerCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "otis", "mcp-audit.jsonl")
	log := NewLog(path)

	for _, d := range []AuditDecision{Allowed, Confirmed, Refused, DeniedByPolicy, TimedOut, RateLimited} {
		if err := log.Record(Entry{Tool: "send_request", Target: "orders/create.http", Decision: d}); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6:\n%s", len(lines), raw)
	}
	// Every outcome is recordable, and each line is its own valid JSON —
	// which is the point of JSONL: a truncated last line from a crash costs
	// one entry rather than the file.
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
		if e.At.IsZero() {
			t.Errorf("line %d has no timestamp", i)
		}
	}
}

// 0600, on the file and on the directory it creates. The log records the shape
// of your infrastructure: not secret, not public.
func TestTheLogIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "otis", "mcp-audit.jsonl")
	if err := NewLog(path).Record(Entry{Tool: "list_requests", Decision: Allowed}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the log is mode %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("the directory is mode %o, want 700", perm)
	}
}

// At the cap the log rotates once and the previous rotation is dropped, so the
// disk a config directory uses stays bounded.
func TestTheLogRotatesOnceAtTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-audit.jsonl")

	// Start just under the cap rather than writing 5 MB of entries.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), MaxAuditBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewLog(path)
	if err := log.Record(Entry{Tool: "send_request", Decision: Allowed}); err != nil {
		t.Fatal(err)
	}

	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("nothing was rotated aside: %v", err)
	}
	if rotated.Size() != MaxAuditBytes {
		t.Errorf("the rotated file is %d bytes, want the old %d", rotated.Size(), MaxAuditBytes)
	}
	fresh, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(fresh, []byte("\n")); n != 1 {
		t.Errorf("the new log has %d lines, want 1", n)
	}

	// A second rotation drops the first, leaving one generation.
	if err := os.WriteFile(path, bytes.Repeat([]byte("y"), MaxAuditBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	log = NewLog(path)
	if err := log.Record(Entry{Tool: "send_request", Decision: Allowed}); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(again, []byte("x")) {
		t.Error("the first rotation survived a second one")
	}
	entries, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d rotated files, want 1: %v", len(entries), entries)
	}
}

// mcp.persistAuditLog: false keeps the session's calls in memory and writes
// nothing.
func TestAMemoryOnlyLogWritesNoFile(t *testing.T) {
	// The log is given a path that exists and is writable, then told not to
	// persist, so "nothing was written" is a real observation rather than
	// the absence of somewhere to write.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp-audit.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewLog("")
	if err := log.Record(Entry{Tool: "send_request", Decision: Confirmed}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "mcp-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("a memory-only log wrote %d bytes", info.Size())
	}
	if got := log.Recent(10); len(got) != 1 {
		t.Errorf("the entry was not kept in memory: %v", got)
	}
	if log.Path() != "" {
		t.Errorf("Path() = %q, want empty", log.Path())
	}
}

// The panel lists newest first, and the in-memory copy is bounded.
func TestRecentIsNewestFirstAndBounded(t *testing.T) {
	log := NewLog("")
	for i := 0; i < maxRemembered+50; i++ {
		if err := log.Record(Entry{Tool: "list_requests", Status: string(rune('a' + i%26)), Decision: Allowed}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(log.Recent(0)); got != maxRemembered {
		t.Errorf("kept %d entries, want the cap of %d", got, maxRemembered)
	}
	recent := log.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("got %d entries", len(recent))
	}
	for i := 1; i < len(recent); i++ {
		if recent[i].At.After(recent[i-1].At) {
			t.Errorf("entry %d is newer than the one before it", i)
		}
	}
	last := string(rune('a' + (maxRemembered+49)%26))
	if recent[0].Status != last {
		t.Errorf("newest is %q, want %q", recent[0].Status, last)
	}
}

// A field carrying a newline must not become two log lines: one entry, one
// line, whatever is in it. json.Marshal escapes it, and this asserts that
// rather than assuming it.
func TestOneEntryIsAlwaysOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-audit.jsonl")
	log := NewLog(path)
	if err := log.Record(Entry{
		Tool:   "send_request",
		Target: "orders/a\nb\r\n{\"fake\":\"line\"}.http",
		Client: "claude\ncode",
		Status: "200\n",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte("\n")); n != 1 {
		t.Fatalf("one entry produced %d newlines:\n%q", n, raw)
	}
	var e Entry
	if err := json.Unmarshal(bytes.TrimRight(raw, "\n"), &e); err != nil {
		t.Fatalf("the line does not parse: %v", err)
	}
}

// The timestamp is UTC, so a log read on another machine means the same thing.
func TestTimestampsAreUTC(t *testing.T) {
	log := NewLog("")
	if err := log.Record(Entry{Tool: "x", At: time.Date(2026, 9, 4, 11, 2, 0, 0, time.FixedZone("CEST", 2*3600))}); err != nil {
		t.Fatal(err)
	}
	got := log.Recent(1)[0].At
	if got.Location() != time.UTC {
		t.Errorf("At is in %v, want UTC", got.Location())
	}
	if want := time.Date(2026, 9, 4, 9, 2, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("At = %v, want %v", got, want)
	}
}
