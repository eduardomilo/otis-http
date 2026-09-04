package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheEndpointFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "otis", "mcp.json")
	if err := NewEndpoint(51234, "tok").Write(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mcp.json is mode %o, want 600 — it holds the token", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("the config directory is mode %o, want 700", perm)
	}
}

// The reason Write removes and recreates instead of writing over: os.WriteFile
// does not narrow an existing file's permissions, so a mcp.json left
// world-readable by anything else would stay that way while holding a token.
func TestWriteNarrowsThePermissionsOfAFileItFinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewEndpoint(51234, "tok").Write(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("a pre-existing 0644 file stayed mode %o, want 600", perm)
	}
}

func TestTheEndpointRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	token, err := MintToken()
	if err != nil {
		t.Fatal(err)
	}
	want := NewEndpoint(51234, token)
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEndpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.URL != "http://127.0.0.1:51234/mcp" {
		t.Errorf("URL = %q", got.URL)
	}
	// Loopback by literal address, never "localhost", which can resolve to
	// something that is not loopback.
	if strings.Contains(got.URL, "localhost") {
		t.Error("the URL uses localhost rather than 127.0.0.1")
	}
	if got.PID != os.Getpid() {
		t.Errorf("PID = %d, want this process so a stale file can be spotted", got.PID)
	}
}

// The file is deleted when the server stops, and stopping twice — or stopping
// a server that never started — is a normal path.
func TestRemovingTheEndpointIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := RemoveEndpoint(path); err != nil {
		t.Errorf("removing a file that was never written: %v", err)
	}
	if err := NewEndpoint(1, "t").Write(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEndpoint(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the endpoint file survived removal")
	}
	if err := RemoveEndpoint(path); err != nil {
		t.Errorf("removing twice: %v", err)
	}
}

// The whole answer to "the port changed again": one command prints what to
// paste, so an unstable port costs a paste rather than a stable-port security
// posture (§14.1).
func TestTheClientBlockIsUsableAsWritten(t *testing.T) {
	e := NewEndpoint(51234, "tok-123")
	var block struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(e.ClientBlock()), &block); err != nil {
		t.Fatalf("the block is not valid JSON: %v\n%s", err, e.ClientBlock())
	}
	otis, ok := block.Servers["otis"]
	if !ok {
		t.Fatalf("no otis entry: %s", e.ClientBlock())
	}
	if otis.Type != "http" {
		t.Errorf("type = %q, want http", otis.Type)
	}
	if otis.URL != e.URL {
		t.Errorf("url = %q, want %q", otis.URL, e.URL)
	}
	if got := otis.Headers["Authorization"]; got != "Bearer tok-123" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestTheEndpointPathIsBesideSettings(t *testing.T) {
	path, err := DefaultEndpointPath()
	if err != nil {
		t.Skipf("no config directory: %v", err)
	}
	if filepath.Base(path) != "mcp.json" {
		t.Errorf("the file is named %q", filepath.Base(path))
	}
	audit, err := DefaultAuditPath()
	if err != nil {
		t.Skip(err)
	}
	if filepath.Dir(path) != filepath.Dir(audit) {
		t.Errorf("mcp.json is in %s and the audit log in %s; both belong beside settings.json",
			filepath.Dir(path), filepath.Dir(audit))
	}
}

// A malformed file is a readable error, not a panic: a client that half-wrote
// it, or a hand edit, must not stop `otis mcp config` from saying what is
// wrong.
func TestAMalformedEndpointFileIsAReadableError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEndpoint(path)
	if err == nil {
		t.Fatal("a malformed file parsed")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not name the file: %v", err)
	}
}
