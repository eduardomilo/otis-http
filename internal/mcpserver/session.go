package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/mcp"
	"github.com/otis-http/otis/internal/resolve"
)

// The SESSION tool (docs/MCP.md §8.1).
//
// One tool, and it is the most carefully gated one here — not because a
// session value is dangerous in itself but because of where it sits.
// docs/FORMAT.md §4.2 interleaves the session layer with the committed one,
// and at one level a session value beats the file: a value set for a folder
// outranks that folder's `_folder.http`, every `_folder.http` above it, and
// the active environment. An unconstrained setter would therefore be an
// environment write by another name, and it would defeat §5's review gate
// without touching a file — a committed, clean, credential-bearing request
// would go wherever the agent pointed it, with `git status` reporting nothing
// at all.
//
// So: rule 1 refuses a name any of those scopes already declares
// (mcp.CheckSessionWrite, decided in internal/mcp with the evidence gathered
// by the Sessions boundary), and rule 2 asks a person in Otis' own window
// every time (mcp.DecideSessionWrite). Neither is skippable by an environment
// marked "allow", because a value in no file is one nobody has reviewed.

// sessionAnnotations is what the tool declares (§6.1). They are a hint to a
// client and never a gate — the gate is in the handler.
func sessionAnnotations() []mcpgo.ToolOption {
	return []mcpgo.ToolOption{
		mcpgo.WithReadOnlyHintAnnotation(false),
		// Not destructive: nothing on disk changes, and the value is gone
		// when the collection closes.
		mcpgo.WithDestructiveHintAnnotation(false),
		// Setting the same name to the same value twice is the same state.
		mcpgo.WithIdempotentHintAnnotation(true),
		// It touches only this machine's memory.
		mcpgo.WithOpenWorldHintAnnotation(false),
	}
}

func (s *Server) registerSession() {
	s.register(tool("set_session_variable",
		"Set a session variable for a folder in the open Otis collection, so the next request "+
			"can resolve it as {{name}} — this is how you chain a flow when no script sets the "+
			"value for you. It lives in memory until the collection closes and is written to no "+
			"file.\n\n"+
			"This takes TWO calls, like send_request. Call it without `intent` first: nothing "+
			"is set, and you get back how many requests would resolve the name. Then call again "+
			"with the `intent`, and a person is asked in Otis' own window. They may refuse.\n\n"+
			"A name the collection already defines is REFUSED, whichever phase you are in — a "+
			"session value outranks a folder's settings and the environment, so setting one "+
			"named `apiHost` or `token` would redirect requests somebody already reviewed. The "+
			"refusal tells you where the name is defined. Use names the collection does not "+
			"define: the ids and tokens a flow produces.",
		sessionAnnotations(),
		mcpgo.WithString("folder", mcpgo.Required(),
			mcpgo.Description("The folder the value is scoped to, as a collection-relative path. "+
				"Every request in it and below resolves the name. Empty string for the "+
				"collection root, which reaches everything.")),
		mcpgo.WithString("name", mcpgo.Required(),
			mcpgo.Description("The variable's name, as a request would write it inside {{...}}.")),
		mcpgo.WithString("value", mcpgo.Required(),
			mcpgo.Description("The value, used literally. It is not a template: {{...}} inside "+
				"it is not expanded.")),
		mcpgo.WithString("intent",
			mcpgo.Description("Omit to preview. Pass the intent from a preview to actually set it.")),
	), mcp.CapSession, spendAfterApproval, s.setSessionVariable)
}

// setSessionVariable is both phases of one session write.
func (s *Server) setSessionVariable(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
	folder := req.GetString("folder", "")
	name, err := req.RequireString("name")
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}
	value, err := req.RequireString("value")
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}

	// Rule 1 runs here, in both phases. In phase 1 it is what turns a
	// refusal into something the agent can act on before spending a turn; in
	// phase 2 it is the check that matters, because it reads the scope as it
	// stands *now* rather than as it stood at the preview.
	target, redactor, err := s.sessions.SessionTarget(folder, name)
	if err != nil {
		return nil, redactor, err
	}
	c.target = folder
	c.environment = target.Environment

	fingerprint := sessionFingerprint(folder, name, value)
	intentID := req.GetString("intent", "")
	if intentID == "" {
		if !c.spend() {
			return nil, redactor, fmt.Errorf("too many session calls; wait a moment and try again")
		}
		intent, err := s.intents.Issue("set_session_variable", folder, target.Environment, fingerprint)
		if err != nil {
			return nil, redactor, err
		}
		return sessionPreview{
			Intent:      intent.ID,
			Folder:      folder,
			Name:        name,
			Reaches:     target.Reaches,
			Environment: target.Environment,
			Note: "Not set. Call again with the intent, and a person will be asked in " +
				"Otis' window.",
		}, redactor, nil
	}

	// Verified rather than spent: asking a person is a round trip, this
	// handler runs again when the answer arrives, and an intent spent on the
	// first pass would leave the retry nothing to spend. A failed check
	// still voids it, so the anti-search property is unchanged.
	if _, err := s.intents.Verify("set_session_variable", intentID, fingerprint); err != nil {
		return nil, redactor, err
	}

	decision := mcp.DecideSessionWrite(s.grants.Grants(), resolve.EnvMeta{Agents: resolve.AgentPolicy(target.Agents)})
	confirmation := Confirmation{
		Kind:        ConfirmSession,
		Tool:        "set_session_variable",
		Client:      s.clientName(ctx),
		Path:        folder,
		Name:        name,
		Environment: target.Environment,
		Variable:    name,
		Value:       value,
		Reaches:     target.Reaches,
	}
	if err := s.ask(ctx, req, decision, confirmation, intentID, c); err != nil {
		return nil, redactor, err
	}
	if c.inputRequired != nil {
		return nil, redactor, nil
	}
	c.confirmed = decision.Outcome == mcp.Confirm

	if _, err := s.intents.Redeem("set_session_variable", intentID, fingerprint); err != nil {
		return nil, redactor, err
	}
	if !c.spend() {
		return nil, redactor, fmt.Errorf("too many session calls; wait a moment and try again")
	}

	set, setRedactor, err := s.sessions.SetSessionVariable(folder, name, value)
	if err != nil {
		return nil, orRedactor(setRedactor, redactor), err
	}
	c.status = "set"
	return set, orRedactor(setRedactor, redactor), nil
}

// sessionPreview is phase 1's result.
type sessionPreview struct {
	Intent      string `json:"intent"`
	Folder      string `json:"folder"`
	Name        string `json:"name"`
	Reaches     int    `json:"reaches"`
	Environment string `json:"environment,omitempty"`
	Note        string `json:"note"`
}

// sessionFingerprint binds an intent to the whole of what was previewed.
//
// Folder, name and value, each length-prefixed so no field's contents can
// imitate the next — the same construction RequestPrint.Fingerprint uses and
// for the same reason. Without the value in it, two phases would be a hole:
// preview `orderId` = "1", redeem it as something the preview never described.
func sessionFingerprint(folder, name, value string) string {
	h := sha256.New()
	for _, part := range []struct{ label, s string }{
		{"folder", folder}, {"name", name}, {"value", value},
	} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part.s)))
		h.Write([]byte(part.label))
		h.Write(n[:])
		h.Write([]byte(part.s))
	}
	return hex.EncodeToString(h.Sum(nil))
}
