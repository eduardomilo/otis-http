package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/otis-http/otis/internal/buildinfo"
	"github.com/otis-http/otis/internal/mcp"
)

// The server's lifecycle (docs/MCP.md §2, §10).
//
// There is no headless mode and no way to start this from the command line.
// The listener belongs to the running app because everything that makes it
// safe is there: the window a confirmation appears in (§6.4), the open
// collection, and the keychain.

// Options builds a Server.
type Options struct {
	// Source is what the tools read and write. Required.
	Source Source
	// Grants answers what is currently allowed, at call time rather than at
	// registration, because the user can flip a switch while this runs.
	Grants Grantor
	// Audit is where every call is recorded. Required: a server that cannot
	// record is not one this package will start.
	Audit *mcp.Log
	// EndpointPath is where the port and token are written for a client to
	// read. Empty means the default beside settings.json.
	EndpointPath string
}

// A Server is the loopback MCP listener and the tools on it.
type Server struct {
	source Source
	grants Grantor
	audit  *mcp.Log

	auth    *mcp.Auth
	limits  *mcp.Limiter
	intents *mcp.Intents

	mcp *mcpsrv.MCPServer

	mu           sync.Mutex
	http         *http.Server
	listener     net.Listener
	endpointPath string
	port         int
	// kill is cancelled by Disconnect, which is how in-flight tool calls —
	// including a send already on the wire — are stopped rather than left to
	// finish after the switch was thrown.
	kill     context.CancelFunc
	killCtx  context.Context
	auditErr error
}

// New builds a Server. It does not listen; call Start.
func New(opts Options) (*Server, error) {
	if opts.Source == nil {
		return nil, errors.New("mcpserver: no source")
	}
	if opts.Grants == nil {
		return nil, errors.New("mcpserver: no grants")
	}
	if opts.Audit == nil {
		// Not a convenience default: an unrecorded agent is the thing §9
		// exists to prevent, and a nil log here would be a silent one.
		return nil, errors.New("mcpserver: no audit log")
	}
	s := &Server{
		source:       opts.Source,
		grants:       opts.Grants,
		audit:        opts.Audit,
		auth:         &mcp.Auth{},
		limits:       mcp.NewLimiter(),
		intents:      mcp.NewIntents(),
		endpointPath: opts.EndpointPath,
	}
	if s.endpointPath == "" {
		path, err := mcp.DefaultEndpointPath()
		if err != nil {
			return nil, err
		}
		s.endpointPath = path
	}
	return s, nil
}

// instructions is what a client shows about this server.
//
// It states the two things an agent's own behaviour should account for, so a
// competent client does not have to discover them by being refused: nothing
// sends on one call, and a person is asked before anything is sent.
const instructions = `Otis exposes the HTTP collection currently open in the Otis desktop app.

Two things about sending, which are properties of this server and not
suggestions: a send always takes two calls — the first previews and sends
nothing, the second spends the returned intent — and a person is asked to
confirm before anything leaves the machine. Preview first, show the person
the resolved URL you got back, then send.

Paths are collection-relative, as they appear in the tree. Values that came
from a secret are masked and cannot be read.`

// Start mints a token, opens the listener and begins serving.
//
// It returns the endpoint a client needs. The token is new every time: there
// is no way to resume a previous one, which is what makes Disconnect final.
func (s *Server) Start() (mcp.Endpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return mcp.Endpoint{}, errors.New("mcpserver: already running")
	}

	// 127.0.0.1 by literal address, never "localhost", which can resolve to
	// something that is not loopback. Port 0, so the OS assigns one.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return mcp.Endpoint{}, fmt.Errorf("mcpserver: opening the loopback listener: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		return mcp.Endpoint{}, errors.New("mcpserver: the listener has no TCP address")
	}
	if !addr.IP.IsLoopback() {
		// Unreachable given the address above, and checked anyway: the whole
		// security model assumes this listener is not on the network.
		listener.Close()
		return mcp.Endpoint{}, fmt.Errorf("mcpserver: refusing to serve on %s, which is not loopback", addr.IP)
	}

	token, err := s.auth.Mint()
	if err != nil {
		listener.Close()
		return mcp.Endpoint{}, err
	}

	s.mcp = mcpsrv.NewMCPServer(
		"otis", buildinfo.Version,
		mcpsrv.WithToolCapabilities(true),
		mcpsrv.WithInstructions(instructions),
		// Elicitation is how a person is asked on the everyday surface
		// (§6.3). Declaring it here is what lets RequestElicitation work;
		// the cases §6.4 keeps in Otis' window do not use it.
		mcpsrv.WithElicitation(),
	)
	s.registerAll()

	s.killCtx, s.kill = context.WithCancel(context.Background())
	streamable := mcpsrv.NewStreamableHTTPServer(s.mcp, mcpsrv.WithEndpointPath(mcp.MCPPath))
	s.http = &http.Server{
		Handler: s.auth.Guard(streamable),
		// A client that opens a connection and says nothing must not hold a
		// slot forever.
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.listener = listener
	s.port = addr.Port

	endpoint := mcp.NewEndpoint(addr.Port, token)
	if err := endpoint.Write(s.endpointPath); err != nil {
		s.stopLocked()
		return mcp.Endpoint{}, err
	}

	// Both captured as locals rather than read off s inside the goroutine.
	// stopLocked sets s.http to nil, so a Stop that lands before this
	// goroutine is scheduled would otherwise be a nil dereference — and
	// reading the field here at all would be a race with the mutex held by
	// whoever is stopping.
	srv, ln := s.http, listener
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Nothing to do but stop: the listener is gone, so the endpoint
			// file is a claim that is no longer true and must not be left
			// behind.
			_ = s.Stop()
		}
	}()
	return endpoint, nil
}

// Disconnect is the kill switch (§10).
//
// In order: the token is revoked so the next call fails authentication;
// outstanding previews are voided so none can be spent after a reconnect;
// in-flight calls are cancelled, including sends already on the wire; the
// listener closes and the endpoint file is deleted; and every capability is
// turned off, so reconnecting is not enough and re-enabling is a deliberate
// act.
//
// A new token is minted next time. There is no way to get the old one back.
func (s *Server) Disconnect() error {
	s.mu.Lock()
	s.auth.Revoke()
	s.intents.Void()
	s.stopLocked()
	s.mu.Unlock()

	// Last, and outside the lock: this reaches into the app's settings, and
	// it is the step that makes a reconnect insufficient.
	return s.grants.RevokeAll()
}

// Stop closes the listener without turning the capabilities off. It is for
// shutting the app down, not for the kill switch.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth.Revoke()
	s.intents.Void()
	s.stopLocked()
	return nil
}

func (s *Server) stopLocked() {
	if s.kill != nil {
		s.kill()
		s.kill = nil
	}
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		s.http = nil
	}
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	// The endpoint file is a claim that a server is reachable, so it goes
	// when the server does — a stale one sends a client to a closed port with
	// a token that no longer exists.
	_ = mcp.RemoveEndpoint(s.endpointPath)
	s.port = 0
}

// Running reports whether the listener is open.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listener != nil
}

// Port is the assigned port, or 0.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Recent is the audit log's newest entries, for the indicator's popover.
func (s *Server) Recent(n int) []mcp.Entry { return s.audit.Recent(n) }

// AuditError is the last failure to write the audit log, or nil.
//
// It is surfaced on the indicator rather than failing tool calls: a config
// directory that has become unwritable must not stop Otis working (§9.1).
func (s *Server) AuditError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auditErr
}

// record writes an audit entry, keeping any file error for the indicator.
func (s *Server) record(entry mcp.Entry) {
	err := s.audit.Record(entry)
	s.mu.Lock()
	s.auditErr = err
	s.mu.Unlock()
}

// collectionName is the open collection's directory name, for the audit log.
// The name and not the path: the path is on this machine and adds nothing.
func (s *Server) collectionName() string {
	dir, ok := s.source.Collection()
	if !ok {
		return ""
	}
	return baseName(dir)
}

// baseName is filepath.Base without importing filepath for one call on a
// value that is always a real directory path.
func baseName(dir string) string {
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			if i == len(dir)-1 {
				return baseName(dir[:i])
			}
			return dir[i+1:]
		}
	}
	return dir
}
