package mcp

import (
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/git"
	"github.com/otis-http/otis/internal/resolve"
)

// every combination of the inputs Decide takes, so the rules below are
// exhausted rather than sampled.
func allSends() []Send {
	var out []Send
	for _, policy := range []resolve.AgentPolicy{
		resolve.AgentUnset, resolve.AgentDeny, resolve.AgentConfirm, resolve.AgentAllow,
	} {
		for _, confirmBefore := range []bool{false, true} {
			// "allow" beside confirmBeforeSend is rejected at parse time
			// (resolve.validateAgents), so it cannot reach Decide.
			if policy == resolve.AgentAllow && confirmBefore {
				continue
			}
			for _, review := range []Review{Reviewed, Unreviewed, NoRepository} {
				for _, usesSecret := range []bool{false, true} {
					for _, hasEnv := range []bool{false, true} {
						out = append(out, Send{
							Environment: resolve.EnvMeta{
								Agents:            policy,
								ConfirmBeforeSend: confirmBefore,
							},
							HasEnvironment: hasEnv,
							Review:         review,
							UsesSecret:     usesSecret,
						})
					}
				}
			}
		}
	}
	return out
}

func allGrants() []Grants {
	var out []Grants
	for _, run := range []bool{false, true} {
		for _, always := range []bool{false, true} {
			out = append(out, Grants{Run: run, AlwaysConfirmSends: always})
		}
	}
	return out
}

// RUN off means nothing sends, whatever anything else says.
func TestNothingSendsWithoutTheRunCapability(t *testing.T) {
	for _, s := range allSends() {
		for _, g := range allGrants() {
			if g.Run {
				continue
			}
			d := Decide(g, s)
			if d.Outcome != Deny {
				t.Fatalf("RUN off but outcome = %q for %+v", d.Outcome, s)
			}
		}
	}
}

// The invariant docs/MCP.md §4 rule 4 states: a per-machine setting may only
// tighten. Turning AlwaysConfirmSends on must never turn a Confirm or a Deny
// into a Proceed, and must never change a Surface from window to either.
func TestAlwaysConfirmSendsOnlyTightens(t *testing.T) {
	for _, s := range allSends() {
		loose := Decide(Grants{Run: true, AlwaysConfirmSends: false}, s)
		tight := Decide(Grants{Run: true, AlwaysConfirmSends: true}, s)

		if rank(tight.Outcome) < rank(loose.Outcome) {
			t.Errorf("AlwaysConfirmSends loosened %q -> %q for %+v",
				loose.Outcome, tight.Outcome, s)
		}
		if loose.Surface == SurfaceWindow && tight.Surface != SurfaceWindow {
			t.Errorf("AlwaysConfirmSends moved a window-only send to %q for %+v",
				tight.Surface, s)
		}
		if loose.Danger && !tight.Danger {
			t.Errorf("AlwaysConfirmSends dropped the danger treatment for %+v", s)
		}
	}
}

// rank orders outcomes by strictness, so "only tightens" is checkable.
func rank(o Outcome) int {
	switch o {
	case Proceed:
		return 0
	case Confirm:
		return 1
	case Deny:
		return 2
	}
	return -1
}

// There is no combination in which "allow" gets past the review gate. This is
// the claim §5 rests on: an agent that writes a request cannot send it without
// somebody seeing it, whatever the environment says.
func TestAllowNeverOverridesTheReviewGate(t *testing.T) {
	for _, s := range allSends() {
		if s.Review != Unreviewed {
			continue
		}
		for _, g := range allGrants() {
			if !g.Run {
				continue
			}
			d := Decide(g, s)
			if d.Outcome == Proceed {
				t.Errorf("an unreviewed send proceeded for %+v", s)
			}
			if d.Outcome == Confirm && d.Surface != SurfaceWindow {
				t.Errorf("an unreviewed send was answerable at %q for %+v", d.Surface, s)
			}
		}
	}
}

// An unreviewed send that uses a secret is the §5.1 case: confirmed, in the
// window, with the danger treatment. Never proceeded, and never answerable
// through the client.
func TestUnreviewedSecretSendIsAlwaysTheDangerConfirmation(t *testing.T) {
	for _, s := range allSends() {
		if s.Review != Unreviewed || !s.UsesSecret {
			continue
		}
		for _, g := range allGrants() {
			if !g.Run {
				continue
			}
			d := Decide(g, s)
			if d.Outcome == Deny {
				continue // a "deny" environment refuses first, which is stricter
			}
			if d.Outcome != Confirm || d.Surface != SurfaceWindow || !d.Danger {
				t.Errorf("got %+v, want a danger confirmation in the window, for %+v", d, s)
			}
		}
	}
}

// Production is never answerable through the client, and never proceeds.
func TestConfirmBeforeSendIsAlwaysWindowOnly(t *testing.T) {
	for _, s := range allSends() {
		if !s.Environment.ConfirmBeforeSend {
			continue
		}
		for _, g := range allGrants() {
			if !g.Run {
				continue
			}
			d := Decide(g, s)
			if d.Outcome == Deny {
				continue
			}
			if d.Outcome != Confirm || d.Surface != SurfaceWindow {
				t.Errorf("got %+v for a confirmBeforeSend environment, want a window confirmation: %+v", d, s)
			}
		}
	}
}

// The only way to Proceed. Stated as a whitelist so a future clause that
// widens it has to change this test on purpose.
func TestTheOnlyWayToProceed(t *testing.T) {
	for _, s := range allSends() {
		for _, g := range allGrants() {
			if Decide(g, s).Outcome != Proceed {
				continue
			}
			switch {
			case !g.Run:
				t.Errorf("proceeded without RUN: %+v", s)
			case g.AlwaysConfirmSends:
				t.Errorf("proceeded with AlwaysConfirmSends on: %+v", s)
			case s.Environment.EffectiveAgentPolicy() != resolve.AgentAllow:
				t.Errorf("proceeded on policy %q: %+v", s.Environment.EffectiveAgentPolicy(), s)
			case s.Review == Unreviewed:
				t.Errorf("proceeded on an unreviewed request: %+v", s)
			case s.Environment.ConfirmBeforeSend:
				t.Errorf("proceeded on a confirmBeforeSend environment: %+v", s)
			}
		}
	}
}

// A default-on AlwaysConfirmSends is what ships, so the shipped default must
// ask for everything.
func TestTheShippedDefaultAsksForEverySend(t *testing.T) {
	shipped := Grants{Run: true, AlwaysConfirmSends: true}
	for _, s := range allSends() {
		if d := Decide(shipped, s); d.Outcome == Proceed {
			t.Errorf("the shipped default proceeded without asking: %+v", s)
		}
	}
}

// Every refusal and every confirmation says why. A dialog that reads "allow?"
// is worth nothing (docs/MCP.md §6.4 step 3).
func TestEveryDecisionCarriesAReason(t *testing.T) {
	for _, s := range allSends() {
		for _, g := range allGrants() {
			d := Decide(g, s)
			if d.Outcome == Proceed {
				continue
			}
			if strings.TrimSpace(d.Reason) == "" {
				t.Fatalf("no reason given for %q on %+v", d.Outcome, s)
			}
		}
	}
}

func TestReviewOf(t *testing.T) {
	tests := []struct {
		name  string
		state git.State
		want  Review
	}{
		{"not a repository", git.State{}, NoRepository},
		{"absent from statuses is clean", git.State{Repository: true,
			Statuses: map[string]git.Status{}}, Reviewed},
		{"modified", git.State{Repository: true,
			Statuses: map[string]git.Status{"a.http": git.StatusModified}}, Unreviewed},
		{"untracked", git.State{Repository: true,
			Statuses: map[string]git.Status{"a.http": git.StatusUntracked}}, Unreviewed},
		// Staged is not reviewed: HEAD is what was reviewed, and staging is
		// something you do before reading the diff.
		{"staged", git.State{Repository: true,
			Statuses: map[string]git.Status{"a.http": git.StatusAdded}}, Unreviewed},
		{"deleted", git.State{Repository: true,
			Statuses: map[string]git.Status{"a.http": git.StatusDeleted}}, Unreviewed},
	}
	for _, tc := range tests {
		if got := ReviewOf(tc.state, "a.http"); got != tc.want {
			t.Errorf("%s: ReviewOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// WRITE depends on git for its safety, so it is refused where there is none.
func TestWriteNeedsAGitRepository(t *testing.T) {
	if err := CanGrantWrite(git.State{}); err == nil {
		t.Error("WRITE was grantable outside a git repository")
	}
	if err := CanGrantWrite(git.State{Repository: true}); err != nil {
		t.Errorf("WRITE refused inside a repository: %v", err)
	}
}

// The tests above are mostly of the form "no combination does X". They are
// worth nothing if the decision space is narrower than it looks, so this
// asserts every outcome and surface is actually reachable — otherwise a
// clause could be removed entirely and the suite would still pass.
func TestTheDecisionSpaceIsActuallyExercised(t *testing.T) {
	seen := map[string]int{}
	for _, s := range allSends() {
		for _, g := range allGrants() {
			d := Decide(g, s)
			seen[string(d.Outcome)]++
			if d.Outcome == Confirm {
				seen["surface:"+string(d.Surface)]++
				if d.Danger {
					seen["danger"]++
				}
			}
		}
	}
	for _, want := range []string{
		string(Proceed), string(Confirm), string(Deny),
		"surface:" + string(SurfaceEither), "surface:" + string(SurfaceWindow),
		"danger",
	} {
		if seen[want] == 0 {
			t.Errorf("no combination produced %q, so the tests asserting its absence are vacuous", want)
		}
	}
	t.Logf("decision space: %v", seen)
}
