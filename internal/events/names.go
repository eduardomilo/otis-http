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

	// SettingsChanged is emitted when Go changes the persisted settings on
	// its own initiative — opening a collection updates the recents list, for
	// example. The frontend re-reads settings; there is no payload.
	SettingsChanged = "settings:changed"
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
	{"SettingsChanged", SettingsChanged, "Emitted when Go changed the persisted settings itself. No payload; re-read the settings."},
}
