package settings

import "testing"

// The shipped state is the safe one, and it has to survive a settings file
// that predates the field, is empty, or is corrupt — all three of which
// produce the zero value (Store.Get treats a bad file as no file).
func TestTheZeroMCPStateIsTheSafeOne(t *testing.T) {
	var zero MCP

	if zero.Enabled {
		t.Error("the server is enabled by default")
	}
	if zero.Read || zero.Run || zero.Write {
		t.Error("a capability is granted by default")
	}
	// The two that default *on*, which is why they are stored inverted.
	if !zero.AlwaysConfirmSends() {
		t.Error("alwaysConfirmSends is off by default; docs/MCP.md §4 rule 4 says on")
	}
	if !zero.PersistAuditLog() {
		t.Error("the audit log is off by default; docs/MCP.md §9.1 says on")
	}
}

// And the inversion works in both directions, or the switch in the UI would
// be a switch that does nothing.
func TestTheInvertedFieldsCanBeTurnedOff(t *testing.T) {
	m := MCP{NeverConfirmSends: true, DoNotPersistAuditLog: true}
	if m.AlwaysConfirmSends() {
		t.Error("NeverConfirmSends did not turn confirmation off")
	}
	if m.PersistAuditLog() {
		t.Error("DoNotPersistAuditLog did not turn the log off")
	}
}
