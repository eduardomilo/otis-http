package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/otis-http/otis/internal/mcp"
)

// Asking a person (docs/MCP.md §6.3, §6.4).
//
// Two surfaces. The client's own prompt is the everyday one, because the
// person is already looking at the client. Otis' window is the authority: it
// is the one place no client preference can auto-approve, and it is where the
// diff is.
//
// Which surface is not decided here — mcp.Decide decides it and this file does
// what it says. What is here is the asking, the timeout, and the rule that
// nothing proceeds by inattention.
//
// **How the client is asked changed under the design.** §6.3 was written for
// server-initiated elicitation, where the handler blocks and asks. From
// protocol version 2026-07-28 that is gone: a server asks by *returning*
// `ResultTypeInputRequired` with the questions it needs answered, and the
// client retries the whole tool call with the answers (SEP-2322). mcp-go
// refuses the old call outright, so this is not a choice. The round trip is
// what askThroughClient implements, and it is why the handler has to be
// resumable — which is in turn why intents are checked with Verify and only
// spent at the send. A client on an older protocol is bridged by mcp-go, so
// one handler serves both eras. docs/MCP.md §6.3 records the consequence:
// "first answer wins" is gone, because the two surfaces can no longer be
// raced.

// ConfirmTimeout is how long a person has (§14.5, decided: 60s).
//
// A timeout is a **refusal**, never a default-yes. It bounds the window's own
// dialog directly; on the client surface the bound is the intent's TTL, which
// is the same 60 seconds — the answer arrives on a retry, and a retry after
// that has nothing left to spend.
const ConfirmTimeout = 60 * time.Second

// confirmInputID keys the confirmation among the input requests a handler may
// make. Only one is ever asked, but the protocol is a map and the key is what
// the retry is matched on.
const confirmInputID = "otis.confirm"

// errRefused is what a tool returns when a person said no.
var errRefused = errors.New("refused")

// refusal is a person's answer, with enough detail to audit it honestly.
type refusal struct {
	// decision is what the audit log records: refused, timed-out, or
	// denied-by-policy.
	decision mcp.AuditDecision
	// surface is where the person was asked, or NotAsked.
	surface mcp.AuditSurface
	// message is what the agent is told.
	message string
}

func (r refusal) Error() string { return r.message }
func (r refusal) Unwrap() error { return errRefused }

// ask puts one operation in front of a person, if the policy says to.
//
// It returns the surface an answer came from — which is the audit field that
// shows §6.4 was honoured — or, on the client surface, sets c.inputRequired
// and returns no answer yet.
func (s *Server) ask(
	ctx context.Context,
	req mcpgo.CallToolRequest,
	decision mcp.Decision,
	confirmation Confirmation,
	state string,
	c *call,
) error {
	switch decision.Outcome {
	case mcp.Deny:
		return refusal{
			decision: mcp.DeniedByPolicy,
			surface:  mcp.NotAsked,
			message:  "Otis refused this: " + decision.Reason + ".",
		}
	case mcp.Proceed:
		return nil
	}

	confirmation.Reason = decision.Reason
	confirmation.Danger = decision.Danger
	confirmation.Host = hostOf(confirmation.URL)
	confirmation.ID = confirmationID()
	confirmation.ExpiresAt = time.Now().Add(ConfirmTimeout).UTC().Format(time.RFC3339)

	if decision.Surface == mcp.SurfaceWindow {
		return s.askWindowOnly(ctx, confirmation, c)
	}
	return s.askEither(ctx, req, confirmation, state, c)
}

// askWindowOnly is §6.4: the two cases that may only be answered in Otis' own
// window — production, and an unreviewed send.
//
// The client is *notified* rather than asked, because the person is looking at
// it and a call that blocks for a minute with no explanation is
// indistinguishable from a hang. A notification cannot collect an answer,
// which is exactly the property this case needs.
func (s *Server) askWindowOnly(ctx context.Context, confirmation Confirmation, c *call) error {
	if s.asker == nil || !s.asker.WindowOpen() {
		// §6.5: with no window there is nobody to ask, so the call fails. It
		// does not fall back to the client — that would turn the one surface
		// a client cannot auto-approve into one it can.
		return refusal{
			decision: mcp.Refused,
			surface:  mcp.NotAsked,
			message: "Otis needs to ask a person in its own window before this can be sent (" +
				confirmation.Reason + "), and no Otis window is open to ask in.",
		}
	}

	s.notifyClient(ctx, confirmation)

	ctx, cancel := context.WithTimeout(ctx, ConfirmTimeout)
	defer cancel()
	ok, err := s.asker.AskInWindow(ctx, confirmation)
	c.surface = mcp.InWindow
	return answered(ok, err, confirmation)
}

// askEither is everything else: the default "confirm" on an ordinary
// environment.
//
// The client if it can be asked, and Otis' window if it cannot. Not both: the
// protocol's confirmation is a round trip, so the client's question and the
// window's dialog cannot be raced against each other any more — one returns
// from the call and the other blocks inside it. Preferring the client is
// §6.3's own reasoning: the person is already looking at it.
func (s *Server) askEither(
	ctx context.Context,
	req mcpgo.CallToolRequest,
	confirmation Confirmation,
	state string,
	c *call,
) error {
	if s.clientCanBeAsked(ctx) {
		return s.askThroughClient(req, confirmation, state, c)
	}
	if s.asker != nil && s.asker.WindowOpen() {
		ctx, cancel := context.WithTimeout(ctx, ConfirmTimeout)
		defer cancel()
		ok, err := s.asker.AskInWindow(ctx, confirmation)
		c.surface = mcp.InWindow
		return answered(ok, err, confirmation)
	}
	return refusal{
		decision: mcp.Refused,
		surface:  mcp.NotAsked,
		message: "Otis needs a person to confirm this (" + confirmation.Reason +
			"), and there is nowhere to ask: no Otis window is open and this client does " +
			"not support asking you questions.",
	}
}

// askThroughClient is the round trip.
//
// On the first pass there is no answer in the request, so it records the
// question for the framework to return. On the retry the client has put the
// answer in InputResponses and this reads it.
func (s *Server) askThroughClient(
	req mcpgo.CallToolRequest,
	confirmation Confirmation,
	state string,
	c *call,
) error {
	if answer := mcpsrv.ElicitationResponse(req.Params.InputResponses, confirmInputID); answer != nil {
		c.surface = mcp.OnClient
		switch answer.Action {
		case mcpgo.ElicitationResponseActionAccept:
			// An accept whose content says no is a no. The action says the
			// person engaged with the form; the field says what they chose.
			if proceedField(answer.Content) {
				return nil
			}
			return refusal{
				decision: mcp.Refused,
				surface:  mcp.OnClient,
				message:  "A person refused this. " + capitalize(confirmation.Reason) + ".",
			}
		case mcpgo.ElicitationResponseActionDecline:
			return refusal{
				decision: mcp.Refused,
				surface:  mcp.OnClient,
				message:  "A person declined this. " + capitalize(confirmation.Reason) + ".",
			}
		default:
			// cancel — the prompt went away. Logged distinctly from a
			// decline, because "the person said no" and "the prompt went
			// away" are different facts and only one means they saw it.
			return refusal{
				decision: mcp.TimedOut,
				surface:  mcp.OnClient,
				message: "The confirmation was dismissed without an answer, so Otis refused " +
					"it. Nothing was sent.",
			}
		}
	}

	c.inputRequired = mcpsrv.NewInputRequestBuilder(state).
		Elicit(confirmInputID, mcpgo.ElicitationParams{
			Mode:            mcpgo.ElicitationModeForm,
			Message:         confirmMessage(confirmation),
			RequestedSchema: proceedSchema(),
		}).ToolResult()
	return nil
}

// notifyClient tells the client a person is being asked in Otis' window.
//
// Fire and forget, and deliberately a notification rather than a question: for
// the §6.4 cases the answer may only come from the window, so the client must
// have no way to supply one.
func (s *Server) notifyClient(ctx context.Context, confirmation Confirmation) {
	_ = s.mcp.SendNotificationToClient(ctx, "notifications/message", map[string]any{
		"level":  "info",
		"logger": "otis",
		"data": "Otis is waiting for your confirmation in its own window: " +
			confirmSummary(confirmation) + ". " + capitalize(confirmation.Reason) +
			", so this one can only be answered in Otis.",
	})
}

// answered turns the window's answer into a result.
func answered(ok bool, err error, confirmation Confirmation) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return refusal{
			decision: mcp.TimedOut,
			surface:  mcp.InWindow,
			message: fmt.Sprintf(
				"Nobody answered Otis' confirmation within %s, so it was refused. Nothing was sent.",
				ConfirmTimeout),
		}
	case err != nil:
		return refusal{
			decision: mcp.Refused,
			surface:  mcp.InWindow,
			message:  "Otis could not ask a person to confirm this: " + err.Error(),
		}
	case !ok:
		return refusal{
			decision: mcp.Refused,
			surface:  mcp.InWindow,
			message:  "A person refused this. " + capitalize(confirmation.Reason) + ".",
		}
	}
	return nil
}

// proceedSchema is the one boolean a confirmation asks for.
func proceedSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"proceed": map[string]any{
				"type": "boolean",
				"description": "Send this request. Answering no refuses it and nothing " +
					"is sent.",
			},
		},
		"required": []string{"proceed"},
	}
}

// clientCanBeAsked reports whether the connected client declared elicitation.
//
// A client that did not is not given a weaker gate — it is simply not asked
// there, and Otis' window becomes the only place a person can answer (§6.3).
func (s *Server) clientCanBeAsked(ctx context.Context) bool {
	session := mcpsrv.ClientSessionFromContext(ctx)
	if session == nil {
		return false
	}
	info, ok := session.(mcpsrv.SessionWithClientInfo)
	if !ok {
		return false
	}
	return info.GetClientCapabilities().Elicitation != nil
}

// proceedField reads the one boolean out of an elicitation response.
//
// A missing or unreadable field is a refusal. The whole point of this gate is
// that nothing proceeds by default, and a malformed answer is not consent.
func proceedField(content any) bool {
	fields, ok := content.(map[string]any)
	if !ok {
		return false
	}
	proceed, ok := fields["proceed"].(bool)
	return ok && proceed
}

// confirmMessage is the question, and it names everything §6.4 rule 3 asks
// for: the client, the tool, the method, the resolved URL, the environment and
// whether a secret is involved. Not "allow?" — the whole value of a
// confirmation is in what it says.
func confirmMessage(c Confirmation) string {
	var b strings.Builder
	b.WriteString("Otis: ")
	b.WriteString(confirmSummary(c))
	b.WriteString("?")
	if c.Client != "" {
		fmt.Fprintf(&b, "\n\nAsked by: %s, via %s.", c.Client, c.Tool)
	}
	if c.UsesSecret {
		if len(c.Secrets) > 0 {
			fmt.Fprintf(&b, "\nSends %s from the keychain.", strings.Join(c.Secrets, ", "))
		} else {
			b.WriteString("\nSends a secret from the keychain.")
		}
	}
	if !c.Reviewed {
		b.WriteString("\nThis request is not committed, so nobody has reviewed it.")
	}
	if c.Reason != "" {
		fmt.Fprintf(&b, "\nWhy you are being asked: %s.", c.Reason)
	}
	return b.String()
}

// confirmSummary is the one-line form: the method, the URL, the environment.
func confirmSummary(c Confirmation) string {
	var b strings.Builder
	b.WriteString("send ")
	if c.Method != "" {
		b.WriteString(c.Method + " ")
	}
	b.WriteString(c.URL)
	if c.Environment != "" {
		b.WriteString(" against " + c.Environment)
	}
	return b.String()
}

// hostOf is the URL's host, for the button text.
//
// A URL that will not parse yields "", and the window then falls back to a
// plain label rather than printing something misleading — a button claiming a
// host it guessed at would be worse than one that does not name it.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// confirmationID matches the window's answer to the call blocking on it.
func confirmationID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// A timestamp is a weaker id, but this is a correlation handle inside
		// one process rather than a credential, and failing the send over it
		// would be the wrong trade.
		return fmt.Sprintf("c_%d", time.Now().UnixNano())
	}
	return "c_" + hex.EncodeToString(buf)
}

// askInWindowOrRefuse asks in Otis' window whatever surface the policy would
// have allowed, and refuses when there is no window.
//
// A folder run uses it. The client surface is a round trip that returns from
// the tool call, and a run is a loop inside one call — so a request in a run
// can only be confirmed somewhere that blocks. Nothing is loosened by this:
// the window is the stricter of the two surfaces, and a policy that would have
// denied still denies.
func (s *Server) askInWindowOrRefuse(
	ctx context.Context,
	decision mcp.Decision,
	confirmation Confirmation,
	c *call,
) error {
	switch decision.Outcome {
	case mcp.Deny:
		return refusal{
			decision: mcp.DeniedByPolicy,
			surface:  mcp.NotAsked,
			message:  "Otis refused this: " + decision.Reason + ".",
		}
	case mcp.Proceed:
		return nil
	}
	confirmation.Reason = decision.Reason
	confirmation.Danger = decision.Danger
	confirmation.Host = hostOf(confirmation.URL)
	confirmation.ID = confirmationID()
	confirmation.ExpiresAt = time.Now().Add(ConfirmTimeout).UTC().Format(time.RFC3339)
	return s.askWindowOnly(ctx, confirmation, c)
}
