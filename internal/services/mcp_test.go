package services

import (
	"reflect"
	"sort"
	"testing"
)

// The window's API is eight methods and no more.
//
// Wails binds a registered service's *exported* methods, so anything exported
// on MCPService is callable from the frontend. Implementing
// internal/mcpserver's four boundaries on the service itself put nineteen more
// methods there — `Send`, `AskInWindow`, `Grants`, `RevokeAll`,
// `UpdateRequest` — several of which return a `*mcp.Redactor`, which CLAUDE.md
// says must never join the binding surface. They live on the unexported
// agentBridge now.
//
// This is the check for that, because the `var _ mcpserver.Source =
// (*agentBridge)(nil)` assertions next door keep compiling if a method is
// moved back: promotion through the embedded pointer would satisfy them from
// either side.
func TestTheAgentServiceExposesOnlyTheWindowsAPI(t *testing.T) {
	allowed := []string{
		// What the indicator and its popover need.
		"Status", "SetEnabled", "SetCapability", "SetAlwaysConfirmSends",
		"SetPersistAuditLog", "Disconnect", "Answer", "ClientBlock",
		// Wails' own lifecycle hooks.
		"ServiceStartup", "ServiceShutdown",
	}
	sort.Strings(allowed)

	typ := reflect.TypeOf(&MCPService{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, allowed) {
		t.Fatalf("MCPService's exported methods have changed.\n got: %v\nwant: %v\n\n"+
			"Wails binds every one of these to the window. A method internal/mcpserver "+
			"calls belongs on agentBridge, not here — several return a *mcp.Redactor, "+
			"and the window must never be handed one.", got, allowed)
	}
}

// And the bridge really is what implements the boundaries, with the methods
// the tools reach.
func TestTheBridgeCarriesTheBoundaries(t *testing.T) {
	typ := reflect.TypeOf(&agentBridge{})
	for _, name := range []string{
		"Collection", "ListRequests", "GetRequest", "ListEnvironments",
		"SessionVariables", "LastResponse", "TestResults",
		"Prepare", "Send", "FolderPlan", "RunFolder",
		"CreateRequest", "CreateFolder", "UpdateRequest",
		"WindowOpen", "AskInWindow", "Grants", "RevokeAll",
	} {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("agentBridge has no %s", name)
		}
	}
}
