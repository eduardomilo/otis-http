// Package mcp is the MCP server: the capabilities an agent may hold, the
// decisions that gate every call, the record of what it did, and the tools
// themselves.
//
// docs/MCP.md is the design and is authoritative. The security design is the
// feature rather than a wrapper around it, so the parts that *decide* live
// here as pure functions with exhaustive tests, separately from the parts that
// talk to a network.
package mcp

import (
	"fmt"

	"github.com/otis-http/otis/internal/git"
	"github.com/otis-http/otis/internal/resolve"
)

// Capability is one of the four switches, all off by default
// (docs/MCP.md §3).
type Capability string

const (
	CapRead  Capability = "read"
	CapRun   Capability = "run"
	CapWrite Capability = "write"
	// CapSession grants set_session_variable and nothing else (§8.1). It is
	// separate from CapRun although chaining a flow is what it is for: a
	// session value outranks the committed layer it sits beside
	// (docs/FORMAT.md §4.2), so granting it is a different decision from
	// granting sends, and refusable on its own.
	CapSession Capability = "session"
)

// Grants is what the person has turned on, per machine. It is never read from
// the collection: whether *you* let an agent drive your machine is not a fact
// about the repository.
type Grants struct {
	Read    bool
	Run     bool
	Write   bool
	Session bool
	// AlwaysConfirmSends makes every send ask, including against an
	// environment marked "allow". On by default (docs/MCP.md §4 rule 4).
	//
	// It decides *whether* a person is asked, never *where*: the surface is
	// Surface's business. That separation is what stops a default from making
	// every send window-only and leaving elicitation unreachable.
	AlwaysConfirmSends bool
}

// Holds reports whether a capability is granted.
func (g Grants) Holds(c Capability) bool {
	switch c {
	case CapRead:
		return g.Read
	case CapRun:
		return g.Run
	case CapWrite:
		return g.Write
	case CapSession:
		return g.Session
	}
	return false
}

// Review is what git says about the file a send would use (docs/MCP.md §5).
type Review string

const (
	// Reviewed means git reports no difference from HEAD for that path.
	Reviewed Review = "reviewed"
	// Unreviewed means anything else: untracked, modified, or staged but not
	// committed. Staged counts, because HEAD is what was reviewed and staging
	// is something you do *before* reading the diff.
	Unreviewed Review = "unreviewed"
	// NoRepository means the collection is not under git, so there is no
	// notion of reviewed at all. WRITE cannot be granted here (docs/MCP.md
	// §3), but READ and RUN can, and a send is treated as reviewed because
	// the alternative — treating every request in a perfectly ordinary
	// non-git collection as suspect — would make the server useless for one.
	NoRepository Review = "no-repository"
)

// ReviewOf classifies one request path against the repository state.
func ReviewOf(state git.State, nodePath string) Review {
	if !state.Repository {
		return NoRepository
	}
	switch state.Statuses[nodePath] {
	case git.StatusClean:
		return Reviewed
	default:
		// Modified, untracked, staged-added and deleted all mean the file on
		// disk is not the file HEAD holds.
		return Unreviewed
	}
}

// Surface is where a person is asked (docs/MCP.md §6.3, §6.4).
type Surface string

const (
	// SurfaceEither may be answered through the client or in the window,
	// first answer winning.
	SurfaceEither Surface = "either"
	// SurfaceWindow may only be answered in Otis' own window. It is the one
	// place no client preference can auto-approve.
	SurfaceWindow Surface = "window"
)

// Outcome is what the policy decided about one send.
type Outcome string

const (
	// Proceed means no confirmation is needed.
	Proceed Outcome = "proceed"
	// Confirm means ask a person.
	Confirm Outcome = "confirm"
	// Deny means refuse without asking anybody.
	Deny Outcome = "deny"
)

// Decision is the whole answer for one send: whether it may happen, who is
// asked, where, and how loudly.
type Decision struct {
	Outcome Outcome
	Surface Surface
	// Danger marks the confirmation docs/MCP.md §5.1 describes: an unreviewed
	// send that would consume a secret. It is the one case where a mistaken
	// click cannot be taken back, so the dialog carries the destructive
	// treatment, the diff, and the destination in its button.
	Danger bool
	// Reason is shown to the person and returned to the agent on a refusal.
	// It names *why*, because a confirmation that says "allow?" is worth
	// nothing.
	Reason string
}

// Send is what the policy needs to know about one proposed send.
type Send struct {
	// Environment is the environment's committed settings. The zero value is
	// "no environment selected", which is treated as AgentConfirm: with no
	// environment there is no way to reason about where a request points.
	Environment resolve.EnvMeta
	// HasEnvironment distinguishes "no environment" from "an environment that
	// says nothing", which are the same policy but different reasons.
	HasEnvironment bool
	// Review is git's verdict on the request file.
	Review Review
	// UsesSecret reports that resolving the request would consume a keychain
	// secret.
	UsesSecret bool
}

// Decide is the one place a send's fate is settled.
//
// One function, exhaustively tested, because the alternative is the same
// reasoning spread over each tool handler — and a safety feature with a hole
// in it is not one. The tools call this and do what it says.
//
// The order of the clauses is the order of authority: a denial beats
// everything, the review gate beats the environment's own policy, and the
// per-machine setting can only tighten what the collection committed.
func Decide(grants Grants, s Send) Decision {
	if !grants.Run {
		return Decision{
			Outcome: Deny,
			Reason:  "the RUN capability is not enabled in Otis",
		}
	}

	policy := s.Environment.EffectiveAgentPolicy()

	// A denial is final and needs nobody's attention.
	if policy == resolve.AgentDeny {
		return Decision{
			Outcome: Deny,
			Reason:  "this environment is marked $otis.agents: \"deny\"",
		}
	}

	// The review gate, which no environment policy can override
	// (docs/MCP.md §5). Checked before the environment's own policy precisely
	// so that "allow" cannot get past it.
	if s.Review == Unreviewed {
		if s.UsesSecret {
			return Decision{
				Outcome: Confirm,
				Surface: SurfaceWindow,
				Danger:  true,
				Reason: "this request is not committed and would send a secret; " +
					"nobody has reviewed where that credential goes",
			}
		}
		return Decision{
			Outcome: Confirm,
			Surface: SurfaceWindow,
			Reason:  "this request is not committed, so nobody has reviewed it",
		}
	}

	// Production, as marked by the flag the whole team shares.
	if s.Environment.ConfirmBeforeSend {
		return Decision{
			Outcome: Confirm,
			Surface: SurfaceWindow,
			Reason:  "this environment confirms before every send",
		}
	}

	if policy == resolve.AgentConfirm {
		return Decision{
			Outcome: Confirm,
			Surface: SurfaceEither,
			Reason:  reasonForConfirm(s),
		}
	}

	// policy is AgentAllow. The per-machine setting can still tighten it.
	if grants.AlwaysConfirmSends {
		return Decision{
			Outcome: Confirm,
			Surface: SurfaceEither,
			Reason:  "Otis is set to confirm every agent send on this machine",
		}
	}
	return Decision{Outcome: Proceed, Surface: SurfaceEither}
}

func reasonForConfirm(s Send) string {
	if !s.HasEnvironment {
		return "no environment is selected, so where this request points cannot be checked"
	}
	if s.Environment.Agents == resolve.AgentUnset {
		return "this environment does not say whether agents may send, so Otis asks"
	}
	return "this environment is marked $otis.agents: \"confirm\""
}

// CanGrantWrite reports whether WRITE may be enabled, and why not when it may
// not (docs/MCP.md §3).
//
// WRITE is safe because of the review gate, and that gate is git status. With
// no git there is no notion of reviewed and therefore no gate, so the
// capability that depends on it is refused rather than silently weakened.
func CanGrantWrite(state git.State) error {
	if !state.Repository {
		return fmt.Errorf(
			"this collection is not a git repository, so Otis cannot tell a reviewed " +
				"request from one an agent just wrote; WRITE needs that distinction")
	}
	return nil
}
