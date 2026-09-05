package mcp

import (
	"fmt"
	"strings"

	"github.com/otis-http/otis/internal/resolve"
)

// The session write of docs/MCP.md §8.1, and the one rule that makes it safe.
//
// The decision lives here, as a pure function over evidence the caller
// gathers, for the reason the package doc gives: the parts that *decide* are
// separate from the parts that talk to a network, and this is the decision
// that would be unrecoverable to get wrong.

// SessionWrite is a proposed session variable: a name and a value, for one
// folder's scope (docs/FORMAT.md §4.5).
type SessionWrite struct {
	// Folder is the node path the value is scoped to; "" is the collection
	// root.
	Folder string
	Name   string
	Value  string
}

// Definition is one committed declaration a session write might outrank: a
// `@var` in a `_folder.http` at or above the target folder, or a value in the
// active environment. Source is how it is named to a person —
// `orders/_folder.http:3`, `env/Qa3.json`.
type Definition struct {
	Name   string
	Source string
}

// ErrWouldShadow is rule 1: the name is already declared by a committed scope
// this session value would outrank.
//
// A refusal and not a confirmation, deliberately. §13 says a dialog is only as
// good as its reading, and this is the case with no floor under a misread: the
// name that matters is the one in a request's authority or its auth argument,
// and overriding *that* turns a reviewed, credential-bearing request into a
// send to a host the agent chose — with git still reporting the file clean, so
// §5's gate never fires. It is also nearly free to refuse: a flow chains on
// ids the collection does not otherwise define.
type ErrWouldShadow struct {
	Name   string
	Source string
}

func (e *ErrWouldShadow) Error() string {
	return fmt.Sprintf(
		"%q is defined by %s; a session value would override it. "+
			"Set it there, or use a name the collection does not define.",
		e.Name, e.Source)
}

// ErrBadSessionName is a name that could never be referenced as {{name}}.
type ErrBadSessionName struct{ Name string }

func (e *ErrBadSessionName) Error() string {
	return fmt.Sprintf("%q is not a name a request can reference as {{...}}", e.Name)
}

// CheckSessionWrite decides whether a session write may proceed.
//
// definitions must be every committed declaration the write could outrank:
// the `@var`s of the target folder's own `_folder.http` and of every
// `_folder.http` above it, plus the active environment's values. Those three
// scopes are exactly what §4.2 puts *below* a session value at that folder —
// a request file's own `@var` and a `_folder.http` nearer the request both
// outrank it, so neither belongs in the list and neither is refused.
//
// The caller gathers them at redeem time rather than at preview, so a name
// that became defined in between is refused.
func CheckSessionWrite(w SessionWrite, definitions []Definition) error {
	name := strings.TrimSpace(w.Name)
	if name == "" || name != w.Name || strings.ContainsAny(name, "{} \t\n") {
		return &ErrBadSessionName{Name: w.Name}
	}
	// Reserved, the same way an environment reserves it (docs/FORMAT.md §4.3):
	// $otis is settings, not a variable, and a session value named for it
	// would be a value nothing reads and everything is confused by.
	if strings.HasPrefix(name, "$") {
		return &ErrBadSessionName{Name: w.Name}
	}
	for _, d := range definitions {
		if d.Name == name {
			return &ErrWouldShadow{Name: name, Source: d.Source}
		}
	}
	return nil
}

// DecideSessionWrite is rule 2 of docs/MCP.md §8.1: who, if anyone, is asked
// before a session variable is set.
//
// It is short because there is only one answer worth giving. A session value
// is in no file, so git cannot vouch for it, so it is **permanently
// unreviewed** — and §5's rule is that anything unreviewed asks a person in
// Otis' own window, whatever the environment's policy says. There is no
// "allow" path here for the same reason there is none for an unreviewed send:
// `"allow"` is the team's judgement about the requests they committed, and
// this is not one of those.
//
// `"deny"` still denies, as everywhere, and needs nobody's attention.
func DecideSessionWrite(grants Grants, env resolve.EnvMeta) Decision {
	if !grants.Session {
		return Decision{
			Outcome: Deny,
			Reason:  "the SESSION capability is not enabled in Otis",
		}
	}
	if env.EffectiveAgentPolicy() == resolve.AgentDeny {
		return Decision{
			Outcome: Deny,
			Reason:  "this environment is marked $otis.agents: \"deny\"",
		}
	}
	return Decision{
		Outcome: Confirm,
		Surface: SurfaceWindow,
		Reason: "a session value is in no file, so nobody has reviewed it; " +
			"it outranks the environment for every request in this folder",
	}
}
