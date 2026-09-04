package resolve

import (
	"strings"
	"testing"
)

// An environment that says nothing about agents gets a person in the loop.
// Opting out is the deliberate act (docs/MCP.md §4 rule 1).
func TestAgentPolicyDefaultsToConfirm(t *testing.T) {
	env, err := ParseEnvironment("dev", []byte(`{"baseUrl":"https://x.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Meta.EffectiveAgentPolicy(); got != AgentConfirm {
		t.Errorf("EffectiveAgentPolicy() = %q, want %q", got, AgentConfirm)
	}
	// And an environment with no $otis at all still writes none back.
	if !env.Meta.IsZero() {
		t.Error("an unset policy should leave the meta zero, so no $otis key is written")
	}
}

func TestAgentPolicyRoundTrips(t *testing.T) {
	for _, policy := range []AgentPolicy{AgentDeny, AgentConfirm, AgentAllow} {
		src := `{"$otis":{"agents":"` + string(policy) + `"},"baseUrl":"https://x.test"}`
		env, err := ParseEnvironment("dev", []byte(src))
		if err != nil {
			t.Fatalf("%s: %v", policy, err)
		}
		if env.Meta.Agents != policy {
			t.Errorf("parsed agents = %q, want %q", env.Meta.Agents, policy)
		}
		// Canonical form: writing it back produces the same bytes
		// (docs/FORMAT.md §4.3).
		out := env.Marshal()
		again, err := ParseEnvironment("dev", out)
		if err != nil {
			t.Fatalf("%s: reparse: %v", policy, err)
		}
		if again.Meta.Agents != policy {
			t.Errorf("after a round trip agents = %q, want %q", again.Meta.Agents, policy)
		}
	}
}

// "alow" must not read as permission (docs/MCP.md §4).
func TestUnknownAgentPolicyIsAnError(t *testing.T) {
	for _, bad := range []string{"alow", "yes", "true", "Allow", "ALLOW", "denied"} {
		src := `{"$otis":{"agents":"` + bad + `"}}`
		if _, err := ParseEnvironment("dev", []byte(src)); err == nil {
			t.Errorf("agents %q was accepted; an unknown value must be an error", bad)
		}
	}
}

// A convenience setting must not be able to cancel a safety one
// (docs/MCP.md §4 rule 2).
func TestAllowCannotDowngradeConfirmBeforeSend(t *testing.T) {
	src := `{"$otis":{"confirmBeforeSend":true,"agents":"allow"}}`
	_, err := ParseEnvironment("prod", []byte(src))
	if err == nil {
		t.Fatal(`"allow" beside confirmBeforeSend was accepted; it must be an error`)
	}
	// "confirm" and "deny" beside it are fine — they do not weaken anything.
	for _, ok := range []AgentPolicy{AgentConfirm, AgentDeny} {
		src := `{"$otis":{"confirmBeforeSend":true,"agents":"` + string(ok) + `"}}`
		if _, err := ParseEnvironment("prod", []byte(src)); err != nil {
			t.Errorf("%q beside confirmBeforeSend should be allowed: %v", ok, err)
		}
	}
}

// An unknown field inside $otis is still preserved, so a newer Otis writing
// one is not silently dropped by an older one (docs/FORMAT.md §4.3).
func TestAgentsDoesNotDisturbUnknownMetaFields(t *testing.T) {
	src := `{"$otis":{"agents":"deny","futureThing":42}}`
	env, err := ParseEnvironment("dev", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out := env.Marshal()
	if !strings.Contains(string(out), "futureThing") {
		t.Errorf("futureThing was dropped:\n%s", out)
	}
}
