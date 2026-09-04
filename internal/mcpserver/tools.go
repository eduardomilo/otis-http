package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/otis-http/otis/internal/mcp"
)

// The tool framework.
//
// Every tool is registered through register, which is the only thing in this
// package that produces a tool result. That is deliberate and is the whole
// design of this file: a handler returns a *value* and the redactor that must
// mask it, and cannot serialize anything itself. So "did this tool remember to
// check the capability / take a rate token / mask its output / write an audit
// line?" is not a question that can be asked per tool — there is one answer
// and it is here.
//
// **Never use mcpgo.NewToolResultStructured.** It marshals the value itself,
// which would go around the redactor completely. `NewToolResultText` with
// bytes this file produced is the only route out, and
// TestNoToolBypassesRedaction asserts the constructor appears nowhere in the
// package.

// A handler is a tool's body.
//
// It returns the value to send, the redactor that must mask it, and an error.
// The redactor should be returned **even alongside an error**, because an
// error can carry a resolved URL and a resolved URL can carry a credential in
// a query parameter. A handler with nothing resolved returns mcp.NoSecrets().
type handler func(ctx context.Context, req mcpgo.CallToolRequest) (any, *mcp.Redactor, error)

// register adds a tool, wrapping it in the checks every tool gets.
//
// capability is what the tool needs granted. The order below is the order of
// authority, the same principle policy.go's Decide is built on: what the user
// has granted, then what the budget allows, then the work, then what may
// leave.
func (s *Server) register(tool mcpgo.Tool, capability mcp.Capability, h handler) {
	s.mcp.AddTool(tool, func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		started := time.Now()
		entry := mcp.Entry{
			At:          started,
			Collection:  s.collectionName(),
			Tool:        tool.Name,
			Target:      req.GetString("path", req.GetString("folder", "")),
			Environment: req.GetString("environment", ""),
			Client:      s.clientName(ctx),
			Surface:     mcp.NotAsked,
		}
		done := func(decision mcp.AuditDecision, status string) {
			entry.Decision = decision
			entry.Status = status
			entry.DurationMs = float64(time.Since(started).Microseconds()) / 1000
			s.record(entry)
		}

		// 1. The capability. All three are off by default and the app ships
		//    with the server itself off (§3), so this is the common refusal
		//    and it has to name what to turn on rather than just say no.
		if !s.grants.Grants().Holds(capability) {
			done(mcp.DeniedByPolicy, "capability-off")
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"Otis' %s capability is off. Turn it on in the agent popover in Otis' title strip.",
				capability)), nil
		}

		// 2. The budget (§10). A read that has been called too fast is
		//    refused here, before it can do any work.
		if !s.limits.Allow(capability) {
			done(mcp.RateLimited, "rate-limited")
			return mcpgo.NewToolResultError(fmt.Sprintf(
				"Too many %s calls. Otis rate-limits agents; wait a moment and try again.",
				capability)), nil
		}

		// 3. There has to be a collection open. Every tool is scoped to it,
		//    and that scope is what bounds them all.
		if _, ok := s.source.Collection(); !ok {
			done(mcp.DeniedByPolicy, "no-collection")
			return mcpgo.NewToolResultError(
				"No collection is open in Otis. Open one and try again."), nil
		}

		value, redactor, err := h(ctx, req)
		if err != nil {
			done(mcp.Refused, failureKind(err))
			return mcpgo.NewToolResultError(safeMessage(err, redactor)), nil
		}

		// 4. The only route out. Marshal refuses rather than emitting bytes
		//    that still carry a secret, and a refusal here is a failed tool
		//    call rather than a leaked one.
		out, err := redactor.Marshal(value)
		if err != nil {
			done(mcp.Refused, "redaction-failed")
			if errors.Is(err, mcp.ErrSecretSurvived) {
				// The agent is told the result was withheld, not what was in
				// it. This is a bug in Otis when it happens, and the honest
				// thing is to say so rather than to send a partial result.
				return mcpgo.NewToolResultError(
					"Otis withheld this result: it could not confirm the result was free of " +
						"secret values. This is a bug in Otis, not something to retry."), nil
			}
			return mcpgo.NewToolResultError("Otis could not encode this result."), nil
		}

		done(mcp.Allowed, "ok")
		return mcpgo.NewToolResultText(string(out)), nil
	})
}

// safeMessage is what an agent is told about a failure.
//
// With a redactor it is the error, masked. Without one it is a fixed string:
// a handler that returns an error and no redactor has given this function no
// way to know whether the message is safe, and guessing on that is exactly the
// trade this whole package refuses. The cost is a vague message in a case that
// should not arise; the alternative is a credential in an error string.
func safeMessage(err error, redactor *mcp.Redactor) string {
	if redactor == nil {
		return "Otis could not complete this call, and withheld the reason " +
			"because it could not confirm the reason was free of secret values."
	}
	return redactor.Text(err.Error())
}

// failureKind is the short code the audit log records for a failure.
//
// Short by design: the audit log's `status` holds a code and never a message,
// because free text is where a URL, and then a query parameter, and then a
// credential ends up (§9).
func failureKind(err error) string {
	switch {
	case errors.Is(err, mcp.ErrNoSuchIntent):
		return "no-such-intent"
	case errors.Is(err, mcp.ErrIntentExpired):
		return "intent-expired"
	case errors.Is(err, mcp.ErrIntentStale):
		return "intent-stale"
	case errors.Is(err, errNotInCollection):
		return "not-in-collection"
	case errors.Is(err, errNoSuchSend):
		return "no-such-send"
	default:
		return "failed"
	}
}

// Errors the tools raise that deserve their own audit code.
var (
	errNotInCollection = errors.New("that path is not in the open collection")
	errNoSuchSend      = errors.New("no response is being held for that send")
)

// readOnly is the annotation set every READ tool carries (§6.1).
//
// The annotations are a *hint to the client*, never a gate: a client is free
// to offer "always allow this tool" on the strength of one, and Otis' own
// gates do not consult them. They are here so a client that shows a person
// what a tool does shows something true.
func readOnly() []mcpgo.ToolOption {
	return []mcpgo.ToolOption{
		mcpgo.WithReadOnlyHintAnnotation(true),
		mcpgo.WithDestructiveHintAnnotation(false),
		mcpgo.WithIdempotentHintAnnotation(true),
		// A read touches only the open collection on this machine.
		mcpgo.WithOpenWorldHintAnnotation(false),
	}
}

// tool builds a tool from a name, a description and options.
func tool(name, description string, extra []mcpgo.ToolOption, opts ...mcpgo.ToolOption) mcpgo.Tool {
	all := append([]mcpgo.ToolOption{mcpgo.WithDescription(description)}, extra...)
	all = append(all, opts...)
	return mcpgo.NewTool(name, all...)
}

// clientName is the connected client, for the audit log.
func (s *Server) clientName(ctx context.Context) string {
	if c, ok := s.source.(Clienter); ok {
		return c.ClientName(ctx)
	}
	if session := mcpsrv.ClientSessionFromContext(ctx); session != nil {
		return session.SessionID()
	}
	return ""
}
