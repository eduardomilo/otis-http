package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The endpoint file (docs/MCP.md §2).
//
// The port is OS-assigned and the token is minted at startup, so both change
// every launch. That is the decision in §14.1, taken as recommended: a stable
// port is a worse security posture for a marginal convenience, and the
// convenience is bought back by `otis mcp config`, which prints the current
// block for pasting.

// MCPPath is the path the server answers on.
const MCPPath = "/mcp"

// An Endpoint is what a client needs to reach the server.
type Endpoint struct {
	// Port is the OS-assigned port on 127.0.0.1.
	Port int `json:"port"`
	// Token is the bearer token. It lives here and nowhere else on disk.
	Token string `json:"token"`
	// URL is the full endpoint, so a client's configuration can be copied
	// without anybody assembling it by hand.
	URL string `json:"url"`
	// PID is the process holding the listener, so a stale file left by a
	// crash can be told from a live one.
	PID int `json:"pid"`
}

// DefaultEndpointPath is `<config>/otis/mcp.json`, beside settings.json.
func DefaultEndpointPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("mcp: locating the config directory: %w", err)
	}
	return filepath.Join(dir, "otis", "mcp.json"), nil
}

// NewEndpoint describes a listener on port with token.
func NewEndpoint(port int, token string) Endpoint {
	return Endpoint{
		Port:  port,
		Token: token,
		URL:   fmt.Sprintf("http://127.0.0.1:%d%s", port, MCPPath),
		PID:   os.Getpid(),
	}
}

// Write saves the endpoint to path, mode 0600.
//
// The file is removed and recreated with O_EXCL rather than written over,
// because os.WriteFile does not narrow the permissions of a file that already
// exists: a mcp.json left world-readable by anything else would stay that way,
// and this file holds the token.
func (e Endpoint) Write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mcp: creating the config directory: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: replacing the endpoint file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("mcp: writing the endpoint file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return fmt.Errorf("mcp: encoding the endpoint file: %w", err)
	}
	return nil
}

// ReadEndpoint loads the endpoint file, for `otis mcp config`.
func ReadEndpoint(path string) (Endpoint, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, err
	}
	var e Endpoint
	if err := json.Unmarshal(raw, &e); err != nil {
		return Endpoint{}, fmt.Errorf("mcp: %s is not readable as an endpoint: %w", path, err)
	}
	return e, nil
}

// RemoveEndpoint deletes the file. A missing file is not an error: stopping a
// server that never wrote one is a normal path, and so is stopping twice.
func RemoveEndpoint(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: removing the endpoint file: %w", err)
	}
	return nil
}

// ClientBlock renders the configuration block a client needs.
//
// This is the whole answer to "the port changed again": one command prints
// what to paste. It is JSON with a `mcpServers` wrapper because that is the
// shape every client that reads a file expects; §2's example shows the inner
// object, and the wrapper is what makes the output usable without editing.
func (e Endpoint) ClientBlock() string {
	block := map[string]any{
		"mcpServers": map[string]any{
			"otis": map[string]any{
				"type":    "http",
				"url":     e.URL,
				"headers": map[string]string{"Authorization": "Bearer " + e.Token},
			},
		},
	}
	// Indented for pasting into a file a person maintains by hand.
	out, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		// Unreachable: the value is built here out of strings.
		return ""
	}
	return string(out)
}
