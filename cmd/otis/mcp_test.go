package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/mcp"
)

// configHome points os.UserConfigDir at a temporary directory on all three
// platforms, so this exercises the real path resolution rather than an
// injected seam — and never reads the developer's own mcp.json.
func configHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)                       // darwin, linux
	t.Setenv("XDG_CONFIG_HOME", dir+"/.config") // linux
	t.Setenv("AppData", dir+"/AppData/Roaming") // windows
	path, err := mcp.DefaultEndpointPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Skipf("os.UserConfigDir does not follow the environment here: %s", path)
	}
	return path
}

func TestMCPConfigPrintsTheClientBlock(t *testing.T) {
	path := configHome(t)
	if err := mcp.NewEndpoint(51234, "tok-abc").Write(path); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run("mcp", "config")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}

	var block struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &block); err != nil {
		t.Fatalf("the output is not valid JSON: %v\n%s", err, out)
	}
	otis := block.Servers["otis"]
	if otis.URL != "http://127.0.0.1:51234/mcp" {
		t.Errorf("url = %q", otis.URL)
	}
	if otis.Headers["Authorization"] != "Bearer tok-abc" {
		t.Errorf("Authorization = %q", otis.Headers["Authorization"])
	}
}

// The overwhelmingly likely cause of a missing file is that the server is
// simply not on, and the fix is a switch in the app — so that is what it says,
// rather than a file-not-found.
func TestMCPConfigSaysWhatToDoWhenNothingIsRunning(t *testing.T) {
	configHome(t)

	out, errOut, code := run("mcp", "config")
	if code == 0 {
		t.Fatalf("exit 0 with no server running: %s", out)
	}
	said := errOut + out
	for _, want := range []string{"not running", "enable it"} {
		if !strings.Contains(said, want) {
			t.Errorf("the message does not mention %q: %s", want, said)
		}
	}
	// And it explains why there is no CLI server to start instead.
	if !strings.Contains(said, "headless") {
		t.Errorf("the message does not explain why there is no headless server: %s", said)
	}
}

func TestMCPConfigCanPrintJustThePath(t *testing.T) {
	path := configHome(t)
	out, errOut, code := run("mcp", "config", "--path")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := strings.TrimSpace(out); got != path {
		t.Errorf("got %q, want %q", got, path)
	}
	// --path works with no server running, which is the point of it.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the test wrote an endpoint file it should not have")
	}
}

// There is deliberately no `otis mcp serve`: a headless server would be a
// listener with no window to ask anyone for a confirmation in, which is the
// shape docs/MCP.md §6.4 refuses.
func TestThereIsNoHeadlessServer(t *testing.T) {
	for _, args := range [][]string{{"mcp", "serve"}, {"mcp", "start"}, {"mcp", "listen"}} {
		if _, _, code := run(args...); code == 0 {
			t.Errorf("otis %s succeeded", strings.Join(args, " "))
		}
	}
}

// dispatch.go asks the command tree rather than a list, so a new command is
// reserved automatically — this asserts that held for `mcp`, since a file or
// directory named `mcp` must not open the window.
func TestMCPIsACommandNameNotAWindowPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp"), []byte("GET http://x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := WindowPathIn(dir, []string{"mcp"})
	if ok || got != "" {
		t.Errorf("WindowPathIn returned (%q, %v) for a command name", got, ok)
	}

	// The control: the same file under a name that is not a command does
	// open the window, so the assertion above is about `mcp` and not about
	// something else refusing the path.
	if err := os.WriteFile(filepath.Join(dir, "ping.http"), []byte("GET http://x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := WindowPathIn(dir, []string{"ping.http"}); !ok {
		t.Error("a plain .http file did not resolve to a window path")
	}
}
