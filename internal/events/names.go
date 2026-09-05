// Package events is the single source of truth for the names of the events
// Otis pushes from Go to the frontend.
//
// Wails bindings are request/response only; anything Go needs to push to the
// window goes over the Wails event system (app.Event.Emit / Events.On). Both
// sides of that channel must agree on a name, so no name is ever written as a
// literal: Go uses the constants below and TypeScript uses the mirror in
// frontend/src/lib/events.gen.ts, which `go generate ./internal/events` writes
// from the Registry below. TestTypeScriptMirrorIsCurrent fails if the two
// drift apart.
//
// Every name here must also be registered in main.go with
// application.RegisterEvent[T] so the generated Wails bindings carry the
// payload type.
package events

//go:generate go run ./gen

const (
	// AppReady is emitted once per window as soon as the Wails runtime in
	// that window is ready. Payload: the version string.
	AppReady = "app:ready"

	// CollectionOpened is emitted whenever the current collection changes,
	// including when it is closed. Payload: services.CollectionInfo, whose
	// Path is empty when no collection is open.
	CollectionOpened = "collection:opened"

	// OpenNode is emitted when something outside the window asked for a node
	// to be shown: a .http file double-clicked in Finder or Explorer, a path
	// on the command line (`otis .`), a second launch forwarding its
	// arguments, or a file dropped on the window. No payload.
	//
	// No payload because this is a nudge, not the delivery. The target is
	// held in Go and collected with CollectionService.TakePendingOpen, which
	// the window also calls once on mount — an event is the only way to reach
	// a window that is already open, and a call is the only way to reach one
	// that has not mounted yet, and a launch is the second of those. Whichever
	// arrives first takes it; TakePendingOpen clears as it reads.
	//
	// The collection is already open by the time this arrives:
	// CollectionOpened fired first.
	OpenNode = "open:node"

	// CollectionChanged is emitted when the collection directory changed on
	// disk. Payload: services.Tree, the whole tree.
	//
	// The whole tree, not a diff: a tree is a few hundred small nodes, and a
	// diff protocol would have to be right about renames, reorders and the
	// git status of every ancestor before it saved anything. If a collection
	// large enough to feel it turns up, the diff goes here, keyed by node
	// path, with the walker reporting which subtrees it re-read.
	CollectionChanged = "collection:changed"

	// GitChanged is emitted when the repository's HEAD or index changed — a
	// commit, a stage, or a branch switch. Payload: git.State.
	GitChanged = "git:changed"

	// SettingsChanged is emitted when Go changes the persisted settings on
	// its own initiative — opening a collection updates the recents list, for
	// example. The frontend re-reads settings; there is no payload.
	SettingsChanged = "settings:changed"

	// SendStarted is emitted once the request is on the wire, which is after
	// it has been resolved and prepared — so a send that fails to resolve
	// never starts. Payload: services.SendStarted, keyed by send ID.
	//
	// A send reports itself through events rather than through the return of
	// SendService.Send because it can take thirty seconds, and a binding call
	// that blocks for thirty seconds blocks the window. Send answers with an
	// ID immediately; everything after that arrives here.
	SendStarted = "send:started"

	// SendComplete is emitted when a response has been read in full. Payload:
	// services.ResponseMeta — everything but the body, which stays in Go and
	// is paged through SendService.Lines.
	SendComplete = "send:complete"

	// SendError is emitted when a send produced no response: it did not
	// resolve, it could not be prepared, the transport failed, it timed out,
	// or it was cancelled. Payload: services.SendFailure, whose message is
	// masked and never names a secret's value.
	SendError = "send:error"

	// SessionVarsChanged is emitted when the variables a run set changed — a
	// run set one, or they were cleared (docs/FORMAT.md §4.5). The frontend
	// re-reads them; there is no payload.
	SessionVarsChanged = "session-vars:changed"

	// ScriptTest is emitted as each test a post-response script declared
	// finishes (docs/FORMAT.md §9.9). Payload: services.ScriptTest, keyed by
	// send ID and by the test's index in the phase.
	//
	// Streamed rather than only summarised, because a suite of thirty
	// assertions that appears all at once at the end tells you nothing while
	// it runs. The complete set is on ResponseMeta too, so a tab reopened
	// later still has them.
	ScriptTest = "script:test"

	// ScriptConsole is emitted for each console call a script makes. Payload:
	// services.ScriptConsole, already masked — a secret handle is its mask
	// here as everywhere.
	ScriptConsole = "script:console"

	// RunStarted is emitted when a folder run begins, carrying the whole
	// plan: every request it will send, in the order it will send them.
	// Payload: services.RunStarted, keyed by run ID.
	//
	// The plan up front rather than a row per result as they arrive, because
	// the window can then draw the sequence immediately and fill it in — a
	// run of twenty requests that reveals itself one row at a time tells you
	// nothing about how far through it is.
	RunStarted = "run:started"

	// RunResult is emitted as each request in a folder run finishes.
	// Payload: services.RunResult, keyed by run ID and by the request's
	// index in the plan.
	RunResult = "run:result"

	// RunComplete is emitted when a folder run ends, whether it finished,
	// stopped on a failure, or was cancelled. Payload: services.RunComplete.
	RunComplete = "run:complete"

	// EnvironmentsChanged is emitted when the environments or the active one
	// changed: Otis wrote an env/*.json, somebody else did, or a different
	// environment was activated. Payload: services.Environments, the whole
	// list.
	//
	// It is a separate event from CollectionChanged because env/ is not part
	// of the tree (docs/FORMAT.md §2.1), so the tree payload has nothing to
	// say about it. The whole list rather than a delta, for the same reason
	// the tree is whole: there are a handful of environments, and a delta
	// protocol would have to be right about renames before it saved anything.
	//
	// The payload names environments, variable names and secret *references*.
	// It never carries a secret value.
	EnvironmentsChanged = "environments:changed"

	// OpenCollectionRequested asks the window to run its "open another
	// collection" flow. No payload.
	//
	// The macOS File menu emits it rather than opening a collection itself,
	// because leaving a collection closes every open tab and a draft lives
	// only in the window — so the window is the only place that knows
	// whether anything would be lost, and the confirmation has to be in
	// front of the person before the directory dialog appears. One guarded
	// path, whether the gesture was ⌘O, the menu, or the command palette.
	OpenCollectionRequested = "collection:open-requested"

	// MCPConfirm asks the window to put an agent's operation in front of the
	// person (docs/MCP.md §6.4). Payload: services.MCPConfirmation.
	//
	// The Go side is *blocked* on the answer, which is why this is an event
	// and not a poll: a tool call is waiting, with a 60-second deadline, and
	// the window has to be told the moment there is something to ask rather
	// than on its next tick. The answer comes back through
	// MCPService.Answer, matched on the confirmation's id.
	//
	// The payload carries a masked URL and the *names* of any secrets, never
	// a value.
	MCPConfirm = "mcp:confirm"

	// MCPConfirmResolved tells the window a confirmation no longer needs an
	// answer, so the dialog closes itself. Payload: services.MCPResolved.
	//
	// It exists because the deadline can pass, or the kill switch can be
	// thrown, while a dialog is up — and a dialog whose answer no longer goes
	// anywhere is worse than no dialog, because the next thing the person
	// does is click it.
	MCPConfirmResolved = "mcp:confirm-resolved"

	// LogAppended is emitted when a line was added to the activity log, or
	// the log was cleared. Payload: services.LogEntry, zero-valued for a
	// clear.
	//
	// The whole entry rather than a nudge, because the point of the log is
	// that a failure is noticed *after* it happened: the count in the status
	// bar has to move the moment one arrives, and re-reading the list to find
	// that out would mean a binding call per failure at exactly the moment
	// something is going wrong.
	LogAppended = "log:appended"

	// CloneProgress is emitted for each line git writes while cloning a
	// repository into a new collection. Payload: the line, verbatim.
	//
	// Its own event and not the activity log: the log is where a failure
	// goes and is per-session, while this is a running commentary that stops
	// meaning anything the moment the dialog closes. Nothing is kept — the
	// window shows the last lines and drops them with the dialog.
	CloneProgress = "clone:progress"

	// MCPChanged is emitted when the agent server's state changed: enabled,
	// disabled, a capability flipped, a client connected, or a call recorded.
	// Payload: services.MCPStatus.
	//
	// It drives the indicator chip (DESIGN-NOTES §9.22), whose whole job is
	// to say that something other than the person can currently drive this
	// window. A chip that lags is a chip that lies, so every change to that
	// state emits.
	MCPChanged = "mcp:changed"
)

// Entry is one event in the Registry.
type Entry struct {
	// Ident is the TypeScript member name in the generated mirror.
	Ident string
	// Name is the wire name, and must equal the matching constant above.
	Name string
	// Doc is the comment written above the member in the mirror.
	Doc string
}

// Registry lists every event name in the order it is written to the
// TypeScript mirror. Adding a constant above without adding it here is a
// silent omission, so keep the two together.
var Registry = []Entry{
	{"AppReady", AppReady, "Emitted once per window when its Wails runtime is ready. Payload: the version string."},
	{"CollectionOpened", CollectionOpened, "Emitted whenever the current collection changes, including on close. Payload: CollectionInfo, with an empty path when nothing is open."},
	{"OpenNode", OpenNode, "Emitted when something outside the window asked for a node to be shown \u2014 a .http file opened from the desktop, or a second launch forwarding its arguments. No payload; collect the target with CollectionService.TakePendingOpen."},
	{"CollectionChanged", CollectionChanged, "Emitted when the collection directory changed on disk. Payload: the whole Tree."},
	{"GitChanged", GitChanged, "Emitted when the repository's HEAD or index changed. Payload: the git State."},
	{"SettingsChanged", SettingsChanged, "Emitted when Go changed the persisted settings itself. No payload; re-read the settings."},
	{"SendStarted", SendStarted, "Emitted once a request is on the wire. Payload: SendStarted, keyed by send ID."},
	{"SendComplete", SendComplete, "Emitted when a response has been read in full. Payload: ResponseMeta; the body stays in Go."},
	{"SendError", SendError, "Emitted when a send produced no response. Payload: SendFailure, with a masked message."},
	{"SessionVarsChanged", SessionVarsChanged, "Emitted when the variables a run set changed. No payload; re-read them."},
	{"EnvironmentsChanged", EnvironmentsChanged, "Emitted when the environments or the active one changed. Payload: Environments; never a secret value."},
	{"OpenCollectionRequested", OpenCollectionRequested, "Emitted when something outside the window asked to open another collection \u2014 the macOS File menu. No payload; the window runs its own guarded flow, which asks before discarding unsaved drafts."},
	{"MCPConfirm", MCPConfirm, "Emitted when an agent's operation needs a person's confirmation in Otis' own window. Payload: MCPConfirmation, with a masked URL and secret names only. A tool call is blocked on the answer; reply with MCPService.Answer."},
	{"MCPConfirmResolved", MCPConfirmResolved, "Emitted when a confirmation no longer needs an answer \u2014 it timed out, or the kill switch was thrown \u2014 so the dialog closes itself. Payload: MCPResolved."},
	{"MCPChanged", MCPChanged, "Emitted when the agent server's state changed: enabled, disabled, a capability flipped, or a call recorded. Payload: MCPStatus."},
	{"ScriptTest", ScriptTest, "Emitted as each test a post-response script declared finishes. Payload: ScriptTest."},
	{"ScriptConsole", ScriptConsole, "Emitted for each console call a script makes. Payload: ScriptConsole, already masked."},
	{"RunStarted", RunStarted, "Emitted when a folder run begins. Payload: RunStarted, carrying every request it will send in order."},
	{"RunResult", RunResult, "Emitted as each request in a folder run finishes. Payload: RunResult."},
	{"RunComplete", RunComplete, "Emitted when a folder run ends, however it ended. Payload: RunComplete."},
	{"LogAppended", LogAppended, "Emitted when a line was added to the activity log, or it was cleared. Payload: LogEntry, zero-valued for a clear."},
	{"CloneProgress", CloneProgress, "Emitted for each line git writes while cloning a repository into a new collection. Payload: the line, verbatim."},
}
