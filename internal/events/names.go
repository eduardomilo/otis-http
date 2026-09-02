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
	{"CollectionChanged", CollectionChanged, "Emitted when the collection directory changed on disk. Payload: the whole Tree."},
	{"GitChanged", GitChanged, "Emitted when the repository's HEAD or index changed. Payload: the git State."},
	{"SettingsChanged", SettingsChanged, "Emitted when Go changed the persisted settings itself. No payload; re-read the settings."},
	{"SendStarted", SendStarted, "Emitted once a request is on the wire. Payload: SendStarted, keyed by send ID."},
	{"SendComplete", SendComplete, "Emitted when a response has been read in full. Payload: ResponseMeta; the body stays in Go."},
	{"SendError", SendError, "Emitted when a send produced no response. Payload: SendFailure, with a masked message."},
	{"SessionVarsChanged", SessionVarsChanged, "Emitted when the variables a run set changed. No payload; re-read them."},
}
