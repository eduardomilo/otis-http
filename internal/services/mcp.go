package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/git"
	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/mcpserver"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/response"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// MCPService is the agent server as the window sees it (docs/MCP.md).
//
// It is the one place `internal/mcpserver`'s four boundaries are implemented,
// and every one of them is implemented by *delegating* to the service that
// already owns that job: a send goes through SendService, a write through
// RequestService or FolderService, a read through the same loaders the editor
// uses. Nothing here reimplements an operation, which is what makes an agent's
// write subject to the same invariants a person's is — the write guard, the
// announcement, and `.order` left alone.
//
// It is also the only thing that turns the server on, and it will not do that
// without being asked twice: the listener and each capability are separate
// switches, all off in the zero settings.
type MCPService struct {
	settings     *settings.Store
	collections  *CollectionService
	sends        *SendService
	requests     *RequestService
	folders      *FolderService
	environments *EnvironmentService
	gitState     *GitService
	app          *application.App

	// emitter and windowPresent override where events go and whether there
	// is a window, so a test can drive the whole server without Wails. The
	// same seam SendService.emitter uses, for the same reason: waiting on
	// what the service emitted beats polling for what it did.
	emitter       func(name string, data any)
	windowPresent func() bool

	mu      sync.Mutex
	server  *mcpserver.Server
	log     *mcp.Log
	pending map[string]*waiting
}

// waiting is one confirmation the window has been asked about.
type waiting struct {
	confirmation mcpserver.Confirmation
	answer       chan bool
	// once guards the channel: an answer, a timeout and the kill switch can
	// all arrive, and only the first may be delivered.
	once sync.Once
}

// NewMCPService constructs the service. The server is not started.
func NewMCPService(
	store *settings.Store,
	collections *CollectionService,
	sends *SendService,
	requests *RequestService,
	folders *FolderService,
	environments *EnvironmentService,
	gitState *GitService,
) *MCPService {
	s := &MCPService{
		settings: store, collections: collections, sends: sends,
		requests: requests, folders: folders, environments: environments,
		gitState: gitState,
		pending:  map[string]*waiting{},
	}
	// Closing a collection disconnects. Every tool is scoped to the open
	// collection, so a server left running across a close would be a server
	// with no scope — and the next collection is not the one the person
	// granted anything for.
	//
	// Guarded on a collection *having been* open, because CollectionService
	// fires OnClose from Open as well as Close: opening the first collection
	// of a session counts as a change from "none", so an unguarded handler
	// disconnected the server that ServiceStartup had just restored, at every
	// launch. `current` is still the outgoing collection when the hooks run,
	// which is what makes the check possible here rather than needing a
	// second hook.
	collections.OnClose(func() {
		if collections.Current().Path == "" {
			return
		}
		_ = s.Disconnect()
	})
	return s
}

// ServiceStartup restores the server if it was left enabled.
func (s *MCPService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	current, err := s.settings.Get()
	if err != nil || !current.MCP.Enabled {
		return nil
	}
	// Enabled persists across launches, but the token does not: a new one is
	// minted and the client has to be reconfigured, which is §14.1's decision
	// showing up as a cost rather than being quietly avoided.
	if _, err := s.start(); err != nil {
		s.logf("the agent server could not start: %v", err)
	}
	return nil
}

// ServiceShutdown closes the listener and removes the endpoint file.
func (s *MCPService) ServiceShutdown() error { return s.stop() }

// --- The window's API ---------------------------------------------------

// MCPStatus is what the indicator and its popover draw (DESIGN-NOTES §9.22).
type MCPStatus struct {
	// Enabled is the listener; Running is whether it actually came up.
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Read, Run and Write are the three capabilities.
	Read  bool `json:"read"`
	Run   bool `json:"run"`
	Write bool `json:"write"`
	// AlwaysConfirmSends is §4 rule 4, on by default.
	AlwaysConfirmSends bool `json:"alwaysConfirmSends"`
	PersistAuditLog    bool `json:"persistAuditLog"`

	// Client is the connected MCP client's declared name, or "" when nothing
	// has called. The chip says `agent · idle` until this fills in.
	Client string `json:"client"`
	// Waiting is how many confirmations are outstanding. Exact, like every
	// other count in Otis (DESIGN-NOTES §8.5).
	Waiting int `json:"waiting"`
	// Port is the loopback port, or 0.
	Port int `json:"port"`

	// WriteBlocked explains why WRITE cannot be granted, or is empty when it
	// can. WRITE depends on the review gate and the review gate is git
	// status, so a collection outside git cannot have it (§3).
	WriteBlocked string `json:"writeBlocked,omitempty"`
	// AuditError is the last failure to write the audit log. Surfaced rather
	// than failing calls (§9.1).
	AuditError string `json:"auditError,omitempty"`
	// Recent is the newest audit entries for the popover.
	Recent []mcp.Entry `json:"recent"`
}

// Status is the current state.
func (s *MCPService) Status() (MCPStatus, error) {
	current, err := s.settings.Get()
	if err != nil {
		return MCPStatus{}, err
	}
	return s.statusFrom(current.MCP), nil
}

func (s *MCPService) statusFrom(m settings.MCP) MCPStatus {
	status := MCPStatus{
		Enabled: m.Enabled, Read: m.Read, Run: m.Run, Write: m.Write,
		AlwaysConfirmSends: m.AlwaysConfirmSends(),
		PersistAuditLog:    m.PersistAuditLog(),
		Recent:             []mcp.Entry{},
	}
	if err := mcp.CanGrantWrite(s.repoState()); err != nil {
		status.WriteBlocked = err.Error()
	}

	s.mu.Lock()
	server, log, waitingCount := s.server, s.log, len(s.pending)
	s.mu.Unlock()

	status.Waiting = waitingCount
	if server != nil {
		status.Running = server.Running()
		status.Port = server.Port()
		if err := server.AuditError(); err != nil {
			status.AuditError = err.Error()
		}
	}
	if log != nil {
		status.Recent = log.Recent(50)
		// Who is connected comes from the log rather than a second channel
		// for the same fact: every call records the client that made it, so
		// the newest entry is the answer and there is nothing to keep in
		// step. Until something calls, the chip says `agent · idle`.
		for _, entry := range status.Recent {
			if entry.Client != "" {
				status.Client = entry.Client
				break
			}
		}
	}
	return status
}

// SetEnabled turns the listener on or off.
func (s *MCPService) SetEnabled(on bool) (MCPStatus, error) {
	current, err := s.settings.Get()
	if err != nil {
		return MCPStatus{}, err
	}
	current.MCP.Enabled = on
	if !on {
		// Turning the listener off turns the capabilities off with it, for
		// the same reason the kill switch does: coming back should not be
		// enough to be driving the collection again.
		current.MCP.Read, current.MCP.Run, current.MCP.Write = false, false, false
	}
	if err := s.settings.Set(current); err != nil {
		return MCPStatus{}, err
	}

	if on {
		if _, err := s.start(); err != nil {
			return MCPStatus{}, err
		}
	} else if err := s.stop(); err != nil {
		return MCPStatus{}, err
	}
	status := s.statusFrom(current.MCP)
	s.emitStatus(status)
	return status, nil
}

// SetCapability turns one of the three capabilities on or off.
func (s *MCPService) SetCapability(name string, on bool) (MCPStatus, error) {
	current, err := s.settings.Get()
	if err != nil {
		return MCPStatus{}, err
	}
	switch mcp.Capability(name) {
	case mcp.CapRead:
		current.MCP.Read = on
	case mcp.CapRun:
		current.MCP.Run = on
	case mcp.CapWrite:
		// WRITE is safe because of the review gate, and that gate is git
		// status. With no git there is no notion of reviewed, so the
		// capability that depends on it is refused rather than silently
		// weakened (§3).
		if on {
			if err := mcp.CanGrantWrite(s.repoState()); err != nil {
				return MCPStatus{}, err
			}
		}
		current.MCP.Write = on
	default:
		return MCPStatus{}, fmt.Errorf("unknown capability %q", name)
	}
	if err := s.settings.Set(current); err != nil {
		return MCPStatus{}, err
	}
	status := s.statusFrom(current.MCP)
	s.emitStatus(status)
	return status, nil
}

// SetAlwaysConfirmSends turns §4 rule 4 on or off.
func (s *MCPService) SetAlwaysConfirmSends(on bool) (MCPStatus, error) {
	current, err := s.settings.Get()
	if err != nil {
		return MCPStatus{}, err
	}
	current.MCP.NeverConfirmSends = !on
	if err := s.settings.Set(current); err != nil {
		return MCPStatus{}, err
	}
	status := s.statusFrom(current.MCP)
	s.emitStatus(status)
	return status, nil
}

// SetPersistAuditLog turns the audit file on or off (§9.1).
//
// Turning it off keeps the session's calls in memory only. The switch exists
// because the log is a durable record of which endpoints you asked an agent to
// call, which is a new artifact and a real privacy trade — not as an
// afterthought.
func (s *MCPService) SetPersistAuditLog(on bool) (MCPStatus, error) {
	current, err := s.settings.Get()
	if err != nil {
		return MCPStatus{}, err
	}
	current.MCP.DoNotPersistAuditLog = !on
	if err := s.settings.Set(current); err != nil {
		return MCPStatus{}, err
	}
	status := s.statusFrom(current.MCP)
	s.emitStatus(status)
	return status, nil
}

// Disconnect is the kill switch (§10).
func (s *MCPService) Disconnect() error {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()

	// Every outstanding confirmation is refused. A dialog whose answer no
	// longer goes anywhere is worse than no dialog, because the next thing
	// the person does is click it.
	s.resolveAll("Otis disconnected agents.")

	var err error
	if server != nil {
		err = server.Disconnect()
	} else {
		err = s.bridge().RevokeAll()
	}
	_ = s.stop()
	status, statusErr := s.Status()
	if statusErr == nil {
		s.emitStatus(status)
	}
	return err
}

// Answer is the window's reply to a confirmation.
func (s *MCPService) Answer(id string, approve bool) error {
	s.mu.Lock()
	pending := s.pending[id]
	s.mu.Unlock()
	if pending == nil {
		// The call it belonged to has gone — it timed out, or the kill switch
		// was thrown. Not an error worth showing: the dialog is already
		// closing itself on MCPConfirmResolved.
		return nil
	}
	pending.once.Do(func() { pending.answer <- approve })
	return nil
}

// ClientBlock is the configuration to paste into an MCP client, the same block
// `otis mcp config` prints.
func (s *MCPService) ClientBlock() (string, error) {
	path, err := mcp.DefaultEndpointPath()
	if err != nil {
		return "", err
	}
	endpoint, err := mcp.ReadEndpoint(path)
	if err != nil {
		return "", fmt.Errorf("the agent server is not running")
	}
	return endpoint.ClientBlock(), nil
}

// --- Lifecycle ----------------------------------------------------------

func (s *MCPService) start() (mcp.Endpoint, error) {
	current, err := s.settings.Get()
	if err != nil {
		return mcp.Endpoint{}, err
	}

	auditPath := ""
	if current.MCP.PersistAuditLog() {
		if auditPath, err = mcp.DefaultAuditPath(); err != nil {
			return mcp.Endpoint{}, err
		}
	}
	log := mcp.NewLog(auditPath)

	// The bridge, not the service. Everything mcpserver may call lives on an
	// unexported type for one reason: Wails binds a service's exported
	// methods, and these would then be callable from the window — including
	// AskInWindow, which would let the window ask itself, and every method
	// that returns a *mcp.Redactor, which CLAUDE.md forbids from joining the
	// binding surface. The window's own API is the eight methods on
	// MCPService above and nothing else.
	bridge := s.bridge()
	server, err := mcpserver.New(mcpserver.Options{
		Source: bridge, Sender: bridge, Writer: bridge, Asker: bridge,
		Grants: bridge, Audit: log,
	})
	if err != nil {
		return mcp.Endpoint{}, err
	}

	s.mu.Lock()
	if s.server != nil {
		s.mu.Unlock()
		return mcp.Endpoint{}, errors.New("the agent server is already running")
	}
	s.server, s.log = server, log
	s.mu.Unlock()

	endpoint, err := server.Start()
	if err != nil {
		s.mu.Lock()
		s.server, s.log = nil, nil
		s.mu.Unlock()
		return mcp.Endpoint{}, err
	}
	return endpoint, nil
}

func (s *MCPService) stop() error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Stop()
}

// --- mcpserver.Grantor --------------------------------------------------

// Grants reads the switches at call time, because the person can flip one
// while a server is running.
func (s *agentBridge) Grants() mcp.Grants {
	current, err := s.settings.Get()
	if err != nil {
		// A settings file that cannot be read grants nothing. It is a cache
		// and is allowed to be missing; what must not happen is a read error
		// turning into a permission.
		return mcp.Grants{}
	}
	return mcp.Grants{
		Read: current.MCP.Read, Run: current.MCP.Run, Write: current.MCP.Write,
		AlwaysConfirmSends: current.MCP.AlwaysConfirmSends(),
	}
}

// RevokeAll turns all three capabilities off, which is what makes the kill
// switch final rather than a pause.
func (s *agentBridge) RevokeAll() error {
	current, err := s.settings.Get()
	if err != nil {
		return err
	}
	current.MCP.Enabled = false
	current.MCP.Read, current.MCP.Run, current.MCP.Write = false, false, false
	return s.settings.Set(current)
}

// --- mcpserver.Asker ----------------------------------------------------

// WindowOpen reports whether there is a window to ask in, and a collection for
// the answer to be about.
func (s *agentBridge) WindowOpen() bool {
	// A collection is required whatever the window says: every tool is
	// scoped to one, and a confirmation about a request in a collection that
	// is no longer open is a question nobody can answer honestly.
	if s.collections.Current().Path == "" {
		return false
	}
	if s.windowPresent != nil {
		return s.windowPresent()
	}
	if s.app == nil {
		return false
	}
	return len(s.app.Window.GetAll()) > 0
}

// AskInWindow puts a confirmation in front of the person and blocks.
//
// This is §6.4's authority — the one surface no client preference can
// auto-approve. The window is *told* rather than polled because a tool call is
// waiting on the answer with a deadline running.
func (s *agentBridge) AskInWindow(ctx context.Context, c mcpserver.Confirmation) (bool, error) {
	pending := &waiting{confirmation: c, answer: make(chan bool, 1)}
	s.mu.Lock()
	s.pending[c.ID] = pending
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, c.ID)
		s.mu.Unlock()
		s.emitStatusNow()
	}()

	s.emit(events.MCPConfirm, MCPConfirmation{Confirmation: c})
	s.emitStatusNow()

	select {
	case approve := <-pending.answer:
		s.emit(events.MCPConfirmResolved, MCPResolved{ID: c.ID, Reason: ""})
		return approve, nil
	case <-ctx.Done():
		// The dialog closes itself rather than waiting for a click that has
		// nowhere to go.
		s.emit(events.MCPConfirmResolved, MCPResolved{
			ID: c.ID, Reason: "Nobody answered in time, so Otis refused it.",
		})
		return false, ctx.Err()
	}
}

// MCPConfirmation is the events.MCPConfirm payload.
//
// It wraps mcpserver.Confirmation rather than restating it, so the window and
// the confirmation logic cannot drift about what a person is being told.
type MCPConfirmation struct {
	mcpserver.Confirmation
}

// MCPResolved is the events.MCPConfirmResolved payload.
type MCPResolved struct {
	ID string `json:"id"`
	// Reason is why it resolved without the person, or "" when they answered.
	Reason string `json:"reason,omitempty"`
}

// resolveAll refuses every outstanding confirmation.
func (s *MCPService) resolveAll(reason string) {
	s.mu.Lock()
	all := make([]*waiting, 0, len(s.pending))
	for _, pending := range s.pending {
		all = append(all, pending)
	}
	s.mu.Unlock()
	for _, pending := range all {
		pending.once.Do(func() { pending.answer <- false })
		s.emit(events.MCPConfirmResolved, MCPResolved{ID: pending.confirmation.ID, Reason: reason})
	}
}

// --- helpers ------------------------------------------------------------

func (s *MCPService) repoState() git.State {
	if s.gitState == nil {
		return git.State{}
	}
	state, err := s.gitState.State()
	if err != nil {
		return git.State{}
	}
	return state
}

func (s *MCPService) emit(name string, data any) {
	if s.emitter != nil {
		s.emitter(name, data)
		return
	}
	if s.app == nil {
		return
	}
	s.app.Event.Emit(name, data)
}

func (s *MCPService) emitStatus(status MCPStatus) { s.emit(events.MCPChanged, status) }

func (s *MCPService) emitStatusNow() {
	if status, err := s.Status(); err == nil {
		s.emitStatus(status)
	}
}

func (s *MCPService) logf(format string, args ...any) {
	if s.app == nil {
		return
	}
	s.app.Logger.Warn(fmt.Sprintf(format, args...))
}

// displayEnv is the environment a tool call should resolve against: the one it
// named, or the active one.
//
// An agent naming an environment is allowed and is policy-checked like any
// other, and it does **not** change the app's active environment — an agent
// must not be able to silently repoint the human's next send (§12).
func (s *MCPService) displayEnv(named string) string {
	if named != "" {
		return named
	}
	current, err := s.settings.Get()
	if err != nil {
		return ""
	}
	return current.ActiveEnv
}

// placeholderStore is the store read paths resolve against, so describing a
// request never performs a real keychain lookup.
//
// It is also why a fingerprint holds no credential even indirectly: both
// phases resolve against the same placeholders, so the hash is stable and
// there is nothing secret in it to leak. A change to a secret's *value*
// therefore does not void an intent, which is correct — it is the same
// request, and the value was never what the preview described.
func placeholderStore() secrets.Store { return secrets.Placeholder{} }

// --- mcpserver.Source ---------------------------------------------------

// Collection is the open collection's directory.
func (s *agentBridge) Collection() (string, bool) {
	dir := s.collections.Current().Path
	return dir, dir != ""
}

// ListRequests is the tree, which is the same tree the sidebar draws.
func (s *agentBridge) ListRequests(folder string, includeFolders bool) (mcpserver.Tree, *mcp.Redactor, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return mcpserver.Tree{}, mcp.NoSecrets(), err
	}
	root := loaded.Root
	if folder != "" {
		node := loaded.Find(folder)
		if node == nil || node.Kind != collection.KindFolder {
			return mcpserver.Tree{}, mcp.NoSecrets(), fmt.Errorf("%s is not a folder in the collection", folder)
		}
		root = node
	}

	tree := mcpserver.Tree{Requests: []mcpserver.TreeRequest{}}
	walk(root, func(n *collection.Node) bool {
		switch n.Kind {
		case collection.KindRequest:
			if n.Broken || n.Request == nil {
				return true
			}
			tree.Requests = append(tree.Requests, mcpserver.TreeRequest{
				Path: n.ID, Name: n.Name, Method: n.Method, Folder: parentOf(n.ID),
			})
		case collection.KindFolder:
			if includeFolders && n != root {
				tree.Folders = append(tree.Folders, mcpserver.TreeFolder{
					Path: n.ID, Name: n.Name, HasSettings: n.Settings != nil,
				})
			}
		}
		return true
	})
	// A listing resolves nothing, so there is nothing to mask — but it still
	// goes through a Redactor, because a tool that could be written against a
	// second, unverified path is a tool that eventually is.
	return tree, mcp.NoSecrets(), nil
}

// GetRequest is one request as it would be sent and as it is written.
func (s *agentBridge) GetRequest(path, environment string) (mcpserver.RequestView, *mcp.Redactor, error) {
	doc, err := s.requests.Load(path, s.displayEnv(environment))
	if err != nil {
		return mcpserver.RequestView{}, mcp.NoSecrets(), err
	}

	// Resolved against placeholders, exactly as the editor's read path is, so
	// describing a request performs no real keychain lookup.
	loaded, node, err := s.sends.requestNode(path)
	if err != nil {
		return mcpserver.RequestView{}, mcp.NoSecrets(), err
	}
	res, resErr := s.describeResolved(loaded, node, s.displayEnv(environment))

	view := mcpserver.RequestView{
		Path: doc.Path, Name: doc.Name,
		Headers:   []mcpserver.HeaderView{},
		Variables: []mcpserver.VariableView{},
		Warnings:  append([]string{}, doc.Warnings...),
		Source:    doc.Raw,
		GitStatus: s.statusOf(path),
	}
	if resErr != nil {
		// A request that will not resolve is still worth describing: the
		// agent gets the source and the reason rather than an opaque failure.
		view.Warnings = append(view.Warnings, resErr.Error())
	}
	if res != nil {
		view.Method, view.URL = res.Method, res.Mask(res.URL)
		for _, header := range res.Headers {
			view.Headers = append(view.Headers, mcpserver.HeaderView{
				Name: header.Name, Value: res.Mask(header.Value),
				Source: header.Source.String(), Inherited: header.Source.Path != path,
				Secret: header.Value != res.Mask(header.Value),
			})
		}
		if res.Auth != nil {
			view.Auth = mcpserver.AuthView{
				Kind: string(res.Auth.Kind), Source: res.Auth.Source.String(),
				Secret: res.HasSecrets(),
			}
		}
		view.Body = bodyView(res)
	}
	for _, ref := range doc.Variables {
		view.Variables = append(view.Variables, mcpserver.VariableView{
			Name: ref.Name, Resolved: ref.Value, Origin: string(ref.Origin), Secret: ref.Secret,
		})
	}
	return view, redactorFor(res), nil
}

// ListEnvironments is names and shapes, never a value.
func (s *agentBridge) ListEnvironments() ([]mcpserver.EnvironmentView, *mcp.Redactor, error) {
	list, err := s.environments.List()
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}
	dir := s.collections.Current().Path
	out := make([]mcpserver.EnvironmentView, 0, len(list.Items))
	for _, row := range list.Items {
		view := mcpserver.EnvironmentView{Name: row.Name, Active: row.Name == list.Active}
		// The two $otis fields that change what an agent may do, so an agent
		// is told them rather than discovering them through a refusal.
		if env, err := resolve.LoadEnvironment(dir, row.Name); err == nil {
			view.ConfirmBeforeSend = env.Meta.ConfirmBeforeSend
			view.Agents = string(env.Meta.EffectiveAgentPolicy())
			view.Description = env.Meta.Description
			for _, name := range env.Order {
				value, ok := env.Values[name]
				if !ok {
					continue
				}
				view.Variables = append(view.Variables, struct {
					Name   string `json:"name"`
					Secret bool   `json:"secret"`
				}{Name: name, Secret: value.Secret})
			}
		}
		out = append(out, view)
	}
	// No value of any kind is in this, secret or not, so there is nothing to
	// mask: an environment's non-secret values are still somebody's
	// infrastructure and are simply not returned.
	return out, mcp.NoSecrets(), nil
}

// SessionVariables is what runs have set. A secret's value is withheld.
func (s *agentBridge) SessionVariables(folder string) ([]mcpserver.SessionVariable, *mcp.Redactor, error) {
	values := s.sends.SessionVars()
	out := make([]mcpserver.SessionVariable, 0, len(values))
	for _, value := range values {
		if folder != "" && value.Owner != folder {
			continue
		}
		out = append(out, mcpserver.SessionVariable{
			Name: value.Name, Value: value.Value, Scope: string(value.Scope),
			Owner: value.Owner, SetBy: value.Origin,
			At: value.At.UTC().Format(time.RFC3339),
		})
	}
	return out, mcp.NoSecrets(), nil
}

// LastResponse is a held response, paged.
func (s *agentBridge) LastResponse(sendID string, offset, limit int) (mcpserver.ResponseView, *mcp.Redactor, error) {
	if sendID == "" {
		if sendID = s.sends.newestSendID(); sendID == "" {
			return mcpserver.ResponseView{}, mcp.NoSecrets(),
				fmt.Errorf("nothing has been sent yet in this session")
		}
	}
	meta, err := s.sends.Meta(sendID)
	if err != nil {
		return mcpserver.ResponseView{}, mcp.NoSecrets(), err
	}
	redactor, err := s.sends.redactor(sendID)
	if err != nil {
		return mcpserver.ResponseView{}, mcp.NoSecrets(), err
	}
	chunk, err := s.sends.Lines(sendID, response.Pretty, offset, limit)
	if err != nil {
		return mcpserver.ResponseView{}, redactor, err
	}

	view := mcpserver.ResponseView{
		SendID: sendID, Status: meta.StatusCode, StatusText: meta.Status,
		Headers: []mcpserver.HeaderView{}, Size: meta.Size,
	}
	view.Timing.TotalMs = meta.DurationMs
	for _, header := range meta.Headers {
		view.Headers = append(view.Headers, mcpserver.HeaderView{
			Name: header.Name, Value: header.Value, Secret: header.Secret,
		})
	}
	lines := make([]string, 0, len(chunk.Lines))
	for _, line := range chunk.Lines {
		lines = append(lines, line.Text)
	}
	view.Body = mcpserver.BodyPage{
		Lines: lines, Offset: chunk.From, Total: chunk.Total,
		Truncated: chunk.From+len(lines) < chunk.Total,
	}
	return view, redactor, nil
}

// TestResults is the assertions from a send.
func (s *agentBridge) TestResults(sendID string) (mcpserver.TestsView, *mcp.Redactor, error) {
	if sendID == "" {
		if sendID = s.sends.newestSendID(); sendID == "" {
			return mcpserver.TestsView{}, mcp.NoSecrets(),
				fmt.Errorf("nothing has been sent yet in this session")
		}
	}
	meta, err := s.sends.Meta(sendID)
	if err != nil {
		return mcpserver.TestsView{}, mcp.NoSecrets(), err
	}
	redactor, err := s.sends.redactor(sendID)
	if err != nil {
		redactor = mcp.NoSecrets()
	}
	view := mcpserver.TestsView{SendID: sendID, Tests: []mcpserver.TestView{}}
	for _, result := range meta.Tests {
		view.Tests = append(view.Tests, mcpserver.TestView{
			Name: result.Name, OK: result.Passed, Message: result.Message,
		})
		if result.Passed {
			view.Passed++
		} else {
			view.Failed++
		}
	}
	return view, redactor, nil
}

// --- Source helpers -----------------------------------------------------

// describeResolved resolves a request for *describing*, against placeholders.
//
// The same read path the editor uses, for the same reason: knowing that a
// variable is a secret does not require fetching it, and a describe that
// touched the keychain would be a keychain prompt an agent could trigger.
func (s *agentBridge) describeResolved(
	loaded *collection.Collection, node *collection.Node, envName string,
) (*resolve.Resolved, error) {
	var env *resolve.Environment
	if envName != "" {
		var err error
		if env, err = resolve.LoadEnvironment(loaded.Dir, envName); err != nil {
			return nil, err
		}
	}
	return resolve.InCollection(loaded, node, resolve.Options{
		Env:     env,
		Secrets: placeholderStore(),
		Session: s.sends.varsStore(),
	})
}

// redactorFor is the gate for anything derived from a resolved request.
func redactorFor(res *resolve.Resolved) *mcp.Redactor {
	if res == nil {
		return mcp.NoSecrets()
	}
	return mcp.NewRedactor(res.Mask)
}

// bodyView describes a body without carrying all of it.
func bodyView(res *resolve.Resolved) mcpserver.BodyView {
	view := mcpserver.BodyView{Kind: "none"}
	switch {
	case res.Body.FilePath != "":
		view.Kind = "file"
		view.Preview = res.Body.FilePath
		return view
	case res.Body.Raw == "":
		return view
	}
	view.Kind = "text"
	if header, ok := (&resolve.Effective{Headers: res.Headers}).Header("Content-Type"); ok {
		view.ContentType = header.Value
	}
	body := res.Mask(res.Body.Raw)
	if len(body) > mcpserver.MaxBodyPreview {
		// Truncated is said rather than left to be noticed: an agent that
		// read a cut-off body as the real one and wrote it back with
		// update_request would silently delete the rest of it.
		body = body[:mcpserver.MaxBodyPreview]
		view.Truncated = true
	}
	view.Preview = body
	return view
}

// statusOf is git's verdict on one path, as get_request reports it.
func (s *agentBridge) statusOf(path string) string {
	state := s.repoState()
	if !state.Repository {
		return "no-repository"
	}
	if status, ok := state.Statuses[path]; ok {
		return string(status)
	}
	return string(git.StatusClean)
}

// parentOf is the folder part of a node path, "" at the root.
func parentOf(nodePath string) string {
	if i := strings.LastIndex(nodePath, "/"); i >= 0 {
		return nodePath[:i]
	}
	return ""
}

// --- mcpserver.Sender ---------------------------------------------------

// Prepare resolves a request as far as it can go without sending.
func (s *agentBridge) Prepare(path, environment string) (mcpserver.Prepared, *mcp.Redactor, error) {
	loaded, node, err := s.sends.requestNode(path)
	if err != nil {
		return mcpserver.Prepared{}, mcp.NoSecrets(), err
	}
	envName := s.displayEnv(environment)
	res, err := s.describeResolved(loaded, node, envName)
	if err != nil {
		return mcpserver.Prepared{}, mcp.NoSecrets(), err
	}
	return s.preparedFrom(loaded, node, envName, res), redactorFor(res), nil
}

// preparedFrom builds what the policy and the person need to judge a send.
func (s *agentBridge) preparedFrom(
	loaded *collection.Collection, node *collection.Node, envName string, res *resolve.Resolved,
) mcpserver.Prepared {
	prepared := mcpserver.Prepared{
		Path: node.ID, Name: node.Name,
		Method: res.Method, URL: res.Mask(res.URL),
		Environment:    envName,
		HasEnvironment: envName != "",
		Review:         mcp.ReviewOf(s.repoState(), node.ID),
	}
	// The *names* of the secrets, for §5.1's "Sends apiKey from the
	// keychain." Never the values, which is why this reads Variables rather
	// than anything resolved.
	for _, use := range res.Variables {
		if use.Secret {
			prepared.UsesSecret = true
			prepared.SecretNames = append(prepared.SecretNames, use.Name)
		}
	}
	if res.HasSecrets() {
		prepared.UsesSecret = true
	}
	if envName != "" {
		if env, err := resolve.LoadEnvironment(loaded.Dir, envName); err == nil {
			prepared.Env = env.Meta
		}
	}

	headers := make([]string, 0, len(res.Headers))
	for _, header := range res.Headers {
		headers = append(headers, header.Name+": "+header.Value)
	}
	if res.Auth != nil {
		// The auth directive is in the fingerprint because it decides what
		// credential goes on the wire, and it is not one of the headers yet
		// at this point — the sender adds that at prepare time.
		headers = append(headers, "@auth "+string(res.Auth.Kind)+" "+res.Auth.Source.String())
	}
	session := map[string]string{}
	for _, value := range s.sends.SessionVars() {
		session[value.Name] = value.Value
	}
	prepared.Print = mcp.RequestPrint{
		Method: res.Method, URL: res.URL, Environment: envName,
		Headers: headers, Body: []byte(res.Body.Raw), Session: session,
	}
	return prepared
}

// Send sends one request, through the same code the Send button reaches.
func (s *agentBridge) Send(ctx context.Context, path, environment string) (mcpserver.SendResult, *mcp.Redactor, error) {
	out, err := s.sends.sendNow(ctx, path, s.displayEnv(environment))
	if err != nil {
		return mcpserver.SendResult{}, mcp.NoSecrets(), err
	}
	switch {
	case out.failure != nil:
		return mcpserver.SendResult{}, mcp.NoSecrets(),
			fmt.Errorf("%s: %s", out.failure.Message, out.failure.Detail)
	case out.meta == nil:
		return mcpserver.SendResult{}, mcp.NoSecrets(), errors.New("the send produced no response")
	}
	meta := out.meta

	redactor, err := s.sends.redactor(meta.SendID)
	if err != nil {
		// A held response with no masker is refused rather than described:
		// see SendService.redactor for why that is not defaulted.
		return mcpserver.SendResult{}, mcp.NoSecrets(), err
	}
	result := mcpserver.SendResult{
		SendID: meta.SendID, Status: meta.StatusCode, StatusText: meta.Status,
		DurationMs: meta.DurationMs, Size: meta.Size, ResolvedURL: meta.Request.URL,
	}
	for _, test := range meta.Tests {
		if test.Passed {
			result.Tests.Passed++
		} else {
			result.Tests.Failed++
		}
	}
	return result, redactor, nil
}

// FolderPlan is what a folder run would do, in order.
func (s *agentBridge) FolderPlan(path string) (mcpserver.FolderPlan, *mcp.Redactor, error) {
	loaded, _, requests, err := s.sends.folderRequests(path)
	if err != nil {
		return mcpserver.FolderPlan{}, mcp.NoSecrets(), err
	}
	envName := s.displayEnv("")
	plan := mcpserver.FolderPlan{Path: path}
	for _, node := range requests {
		res, err := s.describeResolved(loaded, node, envName)
		if err != nil {
			// A request that will not resolve is still in the plan, marked by
			// an empty URL. Dropping it would understate what the run does.
			plan.Requests = append(plan.Requests, mcpserver.Prepared{
				Path: node.ID, Name: node.Name, Method: node.Method,
				Environment: envName, HasEnvironment: envName != "",
				Review: mcp.ReviewOf(s.repoState(), node.ID),
			})
			continue
		}
		plan.Requests = append(plan.Requests, s.preparedFrom(loaded, node, envName, res))
	}
	return plan, mcp.NoSecrets(), nil
}

// RunFolder runs a folder's requests in sequence, asking before each.
func (s *agentBridge) RunFolder(
	ctx context.Context, path string, stopOnFailure bool, before mcpserver.Confirm,
) (mcpserver.RunResult, *mcp.Redactor, error) {
	envName := s.displayEnv("")
	hook := func(ctx context.Context, node *collection.Node) error {
		loaded, err := s.collections.Loaded()
		if err != nil {
			return err
		}
		res, err := s.describeResolved(loaded, node, envName)
		if err != nil {
			return err
		}
		return before(ctx, s.preparedFrom(loaded, node, envName, res))
	}

	summary, err := s.sends.runFolderNow(ctx, path, envName, stopOnFailure, hook)
	if err != nil {
		return mcpserver.RunResult{}, mcp.NoSecrets(), err
	}
	result := mcpserver.RunResult{RunID: summary.RunID, Results: []mcpserver.RunItem{}}
	result.Summary.Sent = summary.Passed + summary.Failed
	result.Summary.Passed = summary.Passed
	result.Summary.Failed = summary.Failed
	result.Summary.Refused = len(summary.Refused)
	for _, refused := range summary.Refused {
		result.Results = append(result.Results, mcpserver.RunItem{
			Path: refused.Path, Refused: true, Error: refused.Reason,
		})
	}
	return result, mcp.NoSecrets(), nil
}

// --- mcpserver.Writer ---------------------------------------------------

// CreateRequest goes through RequestService, the only writer of a .http file.
func (s *agentBridge) CreateRequest(folder, name, text string) (mcpserver.Created, *mcp.Redactor, error) {
	path, err := s.requests.Create(folder, name)
	if err != nil {
		return mcpserver.Created{}, mcp.NoSecrets(), err
	}
	if text != "" {
		// SaveText refuses text that does not parse, so a bad `text` leaves
		// the stub the create just wrote rather than a broken file.
		if _, err := s.requests.SaveText(path, s.displayEnv(""), text); err != nil {
			return mcpserver.Created{}, mcp.NoSecrets(),
				fmt.Errorf("%s was created but the text was refused: %w", path, err)
		}
	}
	return mcpserver.Created{Path: path, Slug: collection.Slug(name)}, mcp.NoSecrets(), nil
}

// CreateFolder goes through FolderService, which also writes the
// _folder.http that makes git track the directory.
func (s *agentBridge) CreateFolder(parent, name string) (mcpserver.Created, *mcp.Redactor, error) {
	path, err := s.folders.Create(parent, name)
	if err != nil {
		return mcpserver.Created{}, mcp.NoSecrets(), err
	}
	return mcpserver.Created{Path: path, Slug: collection.Slug(name)}, mcp.NoSecrets(), nil
}

// UpdateRequest replaces a request's text through RequestService.SaveText,
// which refuses text that does not parse.
func (s *agentBridge) UpdateRequest(path, text string) (mcpserver.Updated, *mcp.Redactor, error) {
	if _, err := s.requests.SaveText(path, s.displayEnv(""), text); err != nil {
		return mcpserver.Updated{}, mcp.NoSecrets(), err
	}
	return mcpserver.Updated{Path: path, Status: "modified"}, mcp.NoSecrets(), nil
}

// agentBridge is everything internal/mcpserver may call.
//
// It exists to keep those methods **off the binding surface**. Wails exposes a
// registered service's exported methods to the window, so implementing the
// four boundaries on MCPService itself made `MCPService.Send`,
// `MCPService.AskInWindow`, `MCPService.Grants` and thirteen more callable
// from the frontend — and several of them return a `*mcp.Redactor`, which
// CLAUDE.md says must never join that surface. An unexported type is not
// registered and is not bound, so the window's API is exactly the methods
// above.
//
// It embeds the service rather than copying its fields, so there is one piece
// of state and no question about which copy is current.
type agentBridge struct{ *MCPService }

// bridge is the adapter handed to mcpserver.
func (s *MCPService) bridge() *agentBridge { return &agentBridge{MCPService: s} }

// Compile-time proof that the bridge — and not the service — is what satisfies
// the boundaries. If a method is ever moved back onto MCPService, this still
// compiles, so it is paired with a test that asserts the binding surface.
var (
	_ mcpserver.Source  = (*agentBridge)(nil)
	_ mcpserver.Sender  = (*agentBridge)(nil)
	_ mcpserver.Writer  = (*agentBridge)(nil)
	_ mcpserver.Asker   = (*agentBridge)(nil)
	_ mcpserver.Grantor = (*agentBridge)(nil)
)
