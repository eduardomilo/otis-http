// Package mcpserver is the MCP protocol surface: the tools, their
// annotations, and the loopback listener that carries them.
//
// It is separate from internal/mcp on purpose. That package decides, records
// and redacts, and it imports no protocol library and no service — only the
// standard library plus internal/git and internal/resolve, both of which it
// needs to answer what an agent may do. So the part of this feature worth
// auditing is one package with no MCP in it, and the protocol library lives
// here instead, where a bug is a bug in plumbing rather than in a decision.
//
// It does not import internal/services either. The tools read and write
// through the Source interface below, which internal/services implements, the
// same arrangement internal/script uses to keep an interpreter away from a
// disk it takes no dependency on.
package mcpserver

import (
	"context"

	"github.com/otis-http/otis/internal/mcp"
)

// A Source is everything the tools can reach.
//
// Each method hands back a *mcp.Redactor alongside its value, and the tool
// framework in tools.go is the only thing that serializes either. That is why
// the redactor is a return value rather than something a handler fetches: a
// handler cannot produce a result without one, so "did this tool remember to
// mask?" is not a question anybody has to ask per tool.
//
// The methods are narrow on purpose. This interface *is* the boundary in
// docs/MCP.md §12's "not exposed, on purpose" list: there is no rename, no
// delete, no environment write, no `.order` write, no secret access and no
// git, and the reason none of those can be reached is that none of them is
// here to call.
type Source interface {
	// Collection reports the open collection's directory, and false when no
	// collection is open. Every other method is scoped to it, and that scope
	// is what bounds them all.
	Collection() (dir string, ok bool)

	// ListRequests is the tree the sidebar draws and `otis ls` prints.
	ListRequests(folder string, includeFolders bool) (Tree, *mcp.Redactor, error)

	// GetRequest is one request as it would be sent and as it is written.
	GetRequest(path, environment string) (RequestView, *mcp.Redactor, error)

	// GetDocumentation is a folder's README.md (docs/MCP.md §12).
	GetDocumentation(folder string) (DocumentationView, *mcp.Redactor, error)

	// ListEnvironments is names and shapes, never a value.
	ListEnvironments() ([]EnvironmentView, *mcp.Redactor, error)

	// SessionVariables is what runs have set (docs/MCP.md §8).
	SessionVariables(folder string) ([]SessionVariable, *mcp.Redactor, error)

	// LastResponse is a held response, paged. An empty sendID means the most
	// recent send.
	LastResponse(sendID string, offset, limit int) (ResponseView, *mcp.Redactor, error)

	// TestResults is the assertions from a send or run.
	TestResults(sendID string) (TestsView, *mcp.Redactor, error)
}

// A Sessions is the boundary for docs/MCP.md 8.1's session write.
//
// Separate from Source for the reason every boundary here is separate: it is
// reachable only while the SESSION capability is granted, and an interface a
// tool cannot see is a tool that cannot be written by accident.
//
// Both methods gather the evidence rule 1 needs and hand it to
// mcp.CheckSessionWrite. Both, and not just the write: the check runs at
// *redeem* against the scope as it stands then, so a name that became defined
// between the two phases is refused.
type Sessions interface {
	// SessionTarget describes what a proposed write would do, without doing
	// it: what it would shadow, and how many requests below would resolve it.
	// Rule 1's refusal comes back as an error when the name is taken.
	SessionTarget(folder, name string) (SessionTargetView, *mcp.Redactor, error)

	// SetSessionVariable writes it, re-checking rule 1 first.
	SetSessionVariable(folder, name, value string) (SessionSetView, *mcp.Redactor, error)
}

// SessionTargetView is set_session_variable's phase 1.
type SessionTargetView struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
	// Reaches is how many requests in that folder and below would resolve the
	// name - the blast radius, and an exact count like every other count in
	// Otis.
	Reaches int `json:"reaches"`
	// Environment is the active environment's name, "" for none. It is what
	// the confirmation names.
	Environment string `json:"environment"`
	// Agents is the environment's committed $otis.agents policy, which is
	// where a "deny" comes from. It travels with the target so the decision
	// stays a pure function over evidence rather than a second lookup.
	Agents string `json:"-"`
}

// SessionSetView is set_session_variable's phase 2.
type SessionSetView struct {
	Set    bool   `json:"set"`
	Folder string `json:"folder"`
	Name   string `json:"name"`
	Scope  string `json:"scope"`
}

// A Grantor answers what the agent is currently allowed to do.
//
// Separate from Source because the grants change while the server runs — the
// user flips a switch in the popover, or throws the kill switch — and a tool
// must read them at call time rather than at registration.
type Grantor interface {
	// Grants is the current state of the three switches plus
	// alwaysConfirmSends.
	Grants() mcp.Grants

	// RevokeAll turns all three capabilities off.
	//
	// The kill switch calls it, because revoking the token is not enough:
	// §10 requires that reconnecting be insufficient and that re-enabling be
	// a deliberate act. It is on this interface rather than being a callback
	// the caller may or may not pass, so that nothing can implement Grantor
	// without providing the step that makes the kill switch final.
	RevokeAll() error
}

// A Clienter names the connected MCP client for the audit log, when the
// protocol told us.
type Clienter interface {
	ClientName(ctx context.Context) string
}

// DocumentationView is `get_documentation`: a folder's README.md.
//
// The file's text verbatim, because that is what `update_documentation`
// replaces — the same read-modify-write `get_request.source` exists for
// (docs/MCP.md §14.8).
type DocumentationView struct {
	// Folder is the node path asked for, "" for the collection root.
	Folder string `json:"folder"`
	// Path is the README's collection-relative path, filled in whether or not
	// the file is there: it is where a write would go.
	Path string `json:"path"`
	// Text is the file's contents, "" when there is none.
	Text string `json:"text"`
	// Exists distinguishes an empty README from an absent one, which is the
	// difference between editing and creating.
	Exists bool `json:"exists"`
}

// Tree is `list_requests`.
type Tree struct {
	Requests []TreeRequest `json:"requests"`
	Folders  []TreeFolder  `json:"folders,omitempty"`
}

// TreeRequest is one request in the tree.
type TreeRequest struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Method string `json:"method"`
	Folder string `json:"folder"`
}

// TreeFolder is one folder in the tree.
type TreeFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// HasSettings says whether a _folder.http is there, which is the one
	// thing about a folder that changes what its requests send.
	HasSettings bool `json:"hasSettings"`
}

// RequestView is `get_request`: effective values with provenance, and the
// file's own text.
type RequestView struct {
	Path    string       `json:"path"`
	Name    string       `json:"name"`
	Method  string       `json:"method"`
	URL     string       `json:"url"`
	Headers []HeaderView `json:"headers"`
	Auth    AuthView     `json:"auth"`
	Body    BodyView     `json:"body"`

	Variables []VariableView `json:"variables"`
	Warnings  []string       `json:"warnings"`

	// Source is the file's own text, unresolved.
	//
	// It exists so update_request can be a read-modify-write on the real
	// bytes rather than a reconstruction from a summary (§14.8). It is
	// masked like everything else, which matters here: a .http file can
	// contain a literal credential somebody committed, and this must not be
	// how an agent reads one.
	Source string `json:"source"`

	// GitStatus decides how a send will be gated (§5), so an agent that can
	// see it can say "this will need your confirmation" before spending a
	// turn finding out.
	GitStatus string `json:"gitStatus"`
}

// HeaderView is one effective header and where it came from.
type HeaderView struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	Inherited bool   `json:"inherited"`
	// Secret marks a value the mask replaced, so an agent can tell a masked
	// value from a literal five-dot one.
	Secret bool `json:"secret,omitempty"`
}

// AuthView is the effective auth directive.
type AuthView struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Secret bool   `json:"secret"`
}

// BodyView describes a body without carrying all of it.
type BodyView struct {
	Kind        string `json:"kind"`
	ContentType string `json:"contentType,omitempty"`
	// Preview is capped at MaxBodyPreview bytes.
	Preview string `json:"preview,omitempty"`
	// Truncated says the preview is not the whole body, so an agent does not
	// read a cut-off body as the real one and then write it back.
	Truncated bool `json:"truncated,omitempty"`
}

// VariableView is one variable used, with provenance. A secret's value is
// never here.
type VariableView struct {
	Name     string `json:"name"`
	Resolved string `json:"resolved,omitempty"`
	Origin   string `json:"origin"`
	Secret   bool   `json:"secret,omitempty"`
}

// EnvironmentView is one environment's name and shape.
type EnvironmentView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Active      bool   `json:"active"`
	// ConfirmBeforeSend and Agents are the two $otis fields that change what
	// an agent may do, so an agent is told them rather than discovering them
	// through a refusal.
	ConfirmBeforeSend bool   `json:"confirmBeforeSend"`
	Agents            string `json:"agents"`
	Variables         []struct {
		Name   string `json:"name"`
		Secret bool   `json:"secret"`
	} `json:"variables"`
}

// SessionVariable is one value a run set.
type SessionVariable struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Scope string `json:"scope"`
	Owner string `json:"owner,omitempty"`
	SetBy string `json:"setBy,omitempty"`
	At    string `json:"at,omitempty"`
	// Secret withholds Value.
	Secret bool `json:"secret,omitempty"`
}

// ResponseView is a held response with a page of its body.
type ResponseView struct {
	SendID     string       `json:"sendId"`
	Status     int          `json:"status"`
	StatusText string       `json:"statusText"`
	Headers    []HeaderView `json:"headers"`
	Timing     struct {
		TotalMs float64 `json:"totalMs"`
	} `json:"timing"`
	Size int64    `json:"size"`
	Body BodyPage `json:"body"`
}

// BodyPage is a screenful of a response body, because a body never crosses a
// boundary whole.
type BodyPage struct {
	Lines     []string `json:"lines"`
	Offset    int      `json:"offset"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
}

// TestsView is `get_test_results`.
type TestsView struct {
	SendID string     `json:"sendId"`
	Passed int        `json:"passed"`
	Failed int        `json:"failed"`
	Tests  []TestView `json:"tests"`
}

// TestView is one assertion.
type TestView struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// MaxBodyPreview caps get_request's body preview (§12).
const MaxBodyPreview = 4 << 10

// Body line paging, as §12 states it.
const (
	// DefaultBodyLines is get_last_response's page size.
	DefaultBodyLines = 200
	// MaxBodyLines is the largest page it will return. The window's own cap
	// is higher; this one is smaller because an agent paying by the token has
	// no use for a thousand lines it did not ask for.
	MaxBodyLines = 1000
)
