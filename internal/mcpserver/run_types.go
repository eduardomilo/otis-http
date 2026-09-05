package mcpserver

import (
	"context"

	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/resolve"
)

// The RUN and WRITE boundaries (docs/MCP.md §12).
//
// They are separate interfaces from Source rather than more methods on it,
// because a Server built without a Sender does not register the send tools at
// all. "This build cannot send" is then a fact about what was constructed
// rather than a claim about what the code does.

// A Sender resolves and sends. It is the only route to the network.
type Sender interface {
	// Prepare resolves a request as far as it can go without sending, and
	// returns everything the policy and the person need to judge it.
	//
	// It is phase 1 of every send, and it is also called again at phase 2 —
	// the fingerprint it returns is what proves nothing changed in between,
	// so what the person is shown is what will actually be sent.
	Prepare(path, environment string) (Prepared, *mcp.Redactor, error)

	// Send sends one request. Cancelling ctx stops it, including a request
	// already on the wire, which is what the kill switch relies on.
	Send(ctx context.Context, path, environment string) (SendResult, *mcp.Redactor, error)

	// FolderPlan is what a folder run would do, in order.
	FolderPlan(path string) (FolderPlan, *mcp.Redactor, error)

	// RunFolder runs a folder's requests in sequence.
	//
	// before is called for each request and is where the person is asked; a
	// non-nil error from it means that request is not sent. The hook exists
	// so that what "running a folder" means — the order, the session values
	// flowing between requests, stopOnFailure — stays in one place and is
	// the same for an agent as for the Run button, while the asking stays in
	// one place here. Reimplementing the loop on this side would be a second
	// answer to the first question.
	RunFolder(ctx context.Context, path string, stopOnFailure bool, before Confirm) (RunResult, *mcp.Redactor, error)
}

// A Confirm is asked before one request in a run is sent.
//
// Returning an error means that request is not sent, and the error is what the
// result reports for it. §6.5: a folder run asks **once per request**, not
// once for the folder — a folder run is N sends and the policy is per send.
type Confirm func(ctx context.Context, p Prepared) error

// A Writer creates and edits files. Each method goes through the service
// CLAUDE.md already names as the only writer of that kind of file, so an
// agent's write is subject to the invariants a person's is: it holds the write
// guard, it announces itself, and it does not touch `.order`.
type Writer interface {
	CreateRequest(folder, name, text string) (Created, *mcp.Redactor, error)
	CreateFolder(parent, name string) (Created, *mcp.Redactor, error)
	UpdateRequest(path, text string) (Updated, *mcp.Redactor, error)

	// UpdateDocumentation replaces a folder's README.md. Nothing parses it —
	// a README is text — so review is the whole of the gate, and the reason
	// that is enough is that a README is *rendered* and never injected
	// (docs/MCP.md §12).
	UpdateDocumentation(folder, text string) (Updated, *mcp.Redactor, error)
}

// An Asker puts a confirmation in front of the person in Otis' own window.
//
// This is §6.4's authority: the one surface no client preference can
// auto-approve. It blocks until the person answers, ctx is cancelled, or the
// caller times out.
type Asker interface {
	// AskInWindow returns true if the person approved.
	AskInWindow(ctx context.Context, c Confirmation) (bool, error)

	// WindowOpen reports whether there is a window to ask in. With no window
	// — or no collection — a call needing a §6.4 confirmation fails, because
	// there is nobody to ask (§6.5).
	WindowOpen() bool
}

// Prepared is a resolved request, judged but not sent.
//
// It is the input to both the policy and the confirmation, and the source of
// the fingerprint the intent binds to.
type Prepared struct {
	// Path is the request's node path.
	Path string
	// Name is the request's `# @name`, for the dialog.
	Name   string
	Method string
	// URL is the resolved URL, masked. It is the single most important thing
	// in the confirmation: the host is what distinguishes an exfiltration
	// attempt from ordinary work.
	URL string
	// Environment is the name resolution used, "" for none.
	Environment string
	// HasEnvironment separates "no environment" from "an environment that
	// says nothing" — the same policy, different reasons.
	HasEnvironment bool
	// UsesSecret reports that sending would consume a keychain secret.
	UsesSecret bool
	// SecretNames are the secrets' names, never their values. §5.1 asks the
	// dialog to name them: "Sends apiKey from the keychain."
	SecretNames []string
	// Review is git's verdict on this file.
	Review mcp.Review
	// Env is the environment's committed settings, for the policy.
	Env resolve.EnvMeta
	// Print is what the intent's fingerprint is taken over.
	Print mcp.RequestPrint
}

// SendPreview is phase 1's answer: what would happen, and the intent to spend.
type SendPreview struct {
	Intent      string   `json:"intent"`
	ExpiresAt   string   `json:"expiresAt"`
	Path        string   `json:"path"`
	Method      string   `json:"method"`
	ResolvedURL string   `json:"resolvedUrl"`
	Environment string   `json:"environment"`
	UsesSecret  bool     `json:"usesSecret"`
	Secrets     []string `json:"secrets,omitempty"`
	Reviewed    bool     `json:"reviewed"`
	// WillAsk says who will be asked and where, in words, so an agent can
	// tell a person what is about to happen before spending a turn on it.
	WillAsk string `json:"willAsk"`
	// Blocked is set when the policy will refuse this send outright. Phase 1
	// says so rather than letting the agent spend an intent to find out.
	Blocked bool   `json:"blocked,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// SendResult is phase 2's answer.
type SendResult struct {
	SendID      string  `json:"sendId"`
	Status      int     `json:"status"`
	StatusText  string  `json:"statusText"`
	DurationMs  float64 `json:"durationMs"`
	Size        int64   `json:"size"`
	ResolvedURL string  `json:"resolvedUrl"`
	Tests       struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"tests"`
	SessionVariablesSet []SetVariable `json:"sessionVariablesSet,omitempty"`
}

// SetVariable is one session value a send set. The value is not here: a
// session value can be a token lifted from a response.
type SetVariable struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Owner string `json:"owner,omitempty"`
}

// FolderPlan is what a folder run would do, in order.
//
// It carries Prepared rather than the view type, because "who will be asked
// about this one" is a policy question and policy.go is where those are
// answered — a Source filling in WillAsk would be a second answer to it.
type FolderPlan struct {
	Path     string
	Requests []Prepared
}

// PlanRequest is one request in a plan, as the agent sees it.
type PlanRequest struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Method      string `json:"method"`
	ResolvedURL string `json:"resolvedUrl"`
	UsesSecret  bool   `json:"usesSecret"`
	Reviewed    bool   `json:"reviewed"`
	WillAsk     string `json:"willAsk"`
	Blocked     bool   `json:"blocked,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// FolderPlanView is run_folder's phase 1.
type FolderPlanView struct {
	Intent    string `json:"intent"`
	ExpiresAt string `json:"expiresAt"`
	Path      string `json:"path"`
	// Confirmations is how many times a person will be asked if this runs.
	// It is the number §12 calls the useful part of the plan: it tells an
	// agent how many prompts it is about to put in front of somebody.
	Confirmations int           `json:"confirmations"`
	Requests      []PlanRequest `json:"requests"`
}

// RunResult is run_folder's phase 2.
type RunResult struct {
	RunID   string    `json:"runId"`
	Results []RunItem `json:"results"`
	Summary struct {
		Sent   int `json:"sent"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
		// Refused counts the requests a person declined, which is not a
		// failure of the request and must not be reported as one.
		Refused int `json:"refused"`
	} `json:"summary"`
}

// RunItem is one request's outcome in a run.
type RunItem struct {
	Path   string `json:"path"`
	SendID string `json:"sendId,omitempty"`
	Status int    `json:"status,omitempty"`
	Tests  struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"tests"`
	// Refused and Error are the two ways a request in a run produces no
	// response, kept apart because "the person said no" is not a fault.
	Refused bool   `json:"refused,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Created is create_request's and create_folder's answer.
type Created struct {
	// Path is the node path Go actually used. It may carry a `-2` the agent
	// did not ask for, and the agent must use what comes back.
	Path string `json:"path"`
	Slug string `json:"slug"`
}

// Updated is update_request's answer.
type Updated struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// A Confirmation is what the person is asked.
//
// Every field is here because §6.4 rule 3 says the prompt names the client,
// the tool, the method, the resolved URL, the environment and whether a secret
// is involved — "not 'allow?', the whole value of a confirmation is in what it
// says".
type Confirmation struct {
	// Kind says which question this is, because two different things now ask
	// one: a send, and a session write (docs/MCP.md §8.1). The window draws
	// them differently — a send names a host, a session write names a
	// variable — and a dialog that guessed from which fields were empty
	// would be one field away from drawing the wrong one.
	Kind ConfirmKind `json:"kind"`

	// Tool and Client name who is asking.
	Tool   string `json:"tool"`
	Client string `json:"client"`

	Path        string `json:"path"`
	Name        string `json:"name"`
	Method      string `json:"method"`
	URL         string `json:"url"`
	Environment string `json:"environment"`

	UsesSecret bool     `json:"usesSecret"`
	Secrets    []string `json:"secrets,omitempty"`
	Reviewed   bool     `json:"reviewed"`

	// Danger is §5.1: an unreviewed send that would consume a secret. The
	// window draws this one with the destructive treatment, the diff, the
	// host in the button text and Refuse focused.
	Danger bool `json:"danger"`
	// Reason is why a confirmation is needed, in words.
	Reason string `json:"reason"`

	// Host is the URL's host alone, so the window can put it in the button:
	// "Send to evil.test", never "Send". Muscle memory cannot approve a host
	// it has to read.
	Host string `json:"host"`

	// ID identifies this confirmation across the binding, so the window's
	// answer can be matched to the call that is blocking on it.
	ID string `json:"id"`
	// ExpiresAt is when it refuses itself.
	ExpiresAt string `json:"expiresAt"`

	// Variable, Value and Reaches are the session write's own fields
	// (ConfirmSession). Value is the agent's own literal — nothing from the
	// keychain can reach here — and Reaches is how many requests in that
	// folder and below would resolve the name, which is the blast radius.
	Variable string `json:"variable,omitempty"`
	Value    string `json:"value,omitempty"`
	Reaches  int    `json:"reaches,omitempty"`
}

// ConfirmKind discriminates the two things that ask a person.
type ConfirmKind string

const (
	// ConfirmSend is a request about to go on the wire.
	ConfirmSend ConfirmKind = "send"
	// ConfirmSession is a session variable about to be set (§8.1).
	ConfirmSession ConfirmKind = "session"
)
