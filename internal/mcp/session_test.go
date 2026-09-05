package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/otis-http/otis/internal/resolve"
)

// Rule 1 (docs/MCP.md §8.1), which is the whole safety of the SESSION grant:
// a session value may not be given a name a committed scope already declares,
// because §4.2 would let it outrank that scope — and the name that matters is
// the one holding a host or a credential.
func TestSessionWriteMayNotShadowACommittedName(t *testing.T) {
	defs := []Definition{
		{Name: "SERVICES_HOST", Source: "env/Qa3.json"},
		{Name: "awsSecretKey", Source: "env/Qa3.json"},
		{Name: "apiBase", Source: "orders/_folder.http:3"},
		{Name: "tenant", Source: "_folder.http:1"},
	}

	for _, name := range []string{"SERVICES_HOST", "awsSecretKey", "apiBase", "tenant"} {
		err := CheckSessionWrite(SessionWrite{Folder: "orders", Name: name, Value: "https://evil.test"}, defs)
		var shadow *ErrWouldShadow
		if !errors.As(err, &shadow) {
			t.Fatalf("%s: err = %v, want ErrWouldShadow", name, err)
		}
		// The refusal has to name where it is defined, or the agent cannot
		// tell a person what to change.
		if shadow.Source == "" || !strings.Contains(err.Error(), shadow.Source) {
			t.Errorf("%s: refusal does not name the source: %v", name, err)
		}
	}
}

// And the names a flow actually chains on are free: nothing in the collection
// declares them, which is the case the grant exists for.
func TestSessionWriteAllowsANameNothingDeclares(t *testing.T) {
	defs := []Definition{{Name: "SERVICES_HOST", Source: "env/Qa3.json"}}
	for _, name := range []string{"claActiveTaskId", "claOfferId", "orderId", "signRequestId"} {
		if err := CheckSessionWrite(SessionWrite{Folder: "flow", Name: name, Value: "x"}, defs); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The list is complete rather than cautious: a request file's own @var and a
// _folder.http nearer the request both outrank a session value (§4.2), so
// neither is passed in and neither is refused. Refusing them would block a
// session `petId` because one request declares `@petId = 42`, which is
// friction bought for no safety.
func TestSessionWriteIsNotRefusedByScopesItCannotOutrank(t *testing.T) {
	// Only the scopes a session value at `orders` can outrank are supplied.
	defs := []Definition{{Name: "apiBase", Source: "orders/_folder.http:3"}}
	if err := CheckSessionWrite(SessionWrite{Folder: "orders", Name: "petId", Value: "7"}, defs); err != nil {
		t.Errorf("a request-level @var refused a session write: %v", err)
	}
}

func TestSessionWriteRejectsANameNoRequestCouldReference(t *testing.T) {
	for _, name := range []string{"", " ", "has space", "with{brace}", "$otis", "$uuid", "trailing "} {
		err := CheckSessionWrite(SessionWrite{Folder: "", Name: name, Value: "x"}, nil)
		var bad *ErrBadSessionName
		if !errors.As(err, &bad) {
			t.Errorf("%q: err = %v, want ErrBadSessionName", name, err)
		}
	}
}

// Every capability is refusable on its own, and SESSION is not implied by RUN.
func TestSessionIsItsOwnGrant(t *testing.T) {
	if (Grants{Run: true}).Holds(CapSession) {
		t.Error("RUN granted SESSION")
	}
	if (Grants{Write: true}).Holds(CapSession) {
		t.Error("WRITE granted SESSION")
	}
	if !(Grants{Session: true}).Holds(CapSession) {
		t.Error("SESSION did not grant itself")
	}
	if (Grants{Session: true}).Holds(CapRun) {
		t.Error("SESSION granted RUN")
	}
}

// Rule 2: a person is always asked, in the window, and `"allow"` does not skip
// it — a session value is in no file, so it is permanently unreviewed and §5's
// rule applies. `"deny"` still denies with nobody asked.
func TestSessionWriteAlwaysAsksInTheWindow(t *testing.T) {
	grants := Grants{Session: true}

	for _, policy := range []resolve.AgentPolicy{"", resolve.AgentConfirm, resolve.AgentAllow} {
		d := DecideSessionWrite(grants, resolve.EnvMeta{Agents: policy})
		if d.Outcome != Confirm {
			t.Errorf("agents=%q: outcome = %v, want Confirm", policy, d.Outcome)
		}
		if d.Surface != SurfaceWindow {
			t.Errorf("agents=%q: surface = %v, want the window", policy, d.Surface)
		}
	}

	if d := DecideSessionWrite(grants, resolve.EnvMeta{Agents: resolve.AgentDeny}); d.Outcome != Deny {
		t.Errorf("deny: outcome = %v, want Deny", d.Outcome)
	}
	if d := DecideSessionWrite(Grants{}, resolve.EnvMeta{}); d.Outcome != Deny {
		t.Errorf("without the grant: outcome = %v, want Deny", d.Outcome)
	}
}
