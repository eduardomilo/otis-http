package mcpserver

import (
	"context"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/otis-http/otis/internal/mcp"
)

// The RUN tools (docs/MCP.md §12, §6.2).
//
// Both are two-phase on every client, always. A call without an intent
// describes what would happen and sends nothing; a call with the intent handed
// back goes on to ask a person. There is no shape of call that skips phase 1,
// and that is a property of these two functions rather than a configuration.
//
// Two phases are not consent. An agent will echo an intent back without a
// thought, so phase 2 is where a *person* is asked — an implementation in
// which a returned intent skipped that would be a bug of the worst kind.

// sendAnnotations is what the send tools declare (§6.1).
//
// Not read-only, not idempotent, and open-world: a send leaves the machine.
// These are hints to the client and Otis' own gates do not consult them —
// a client may offer "always allow this tool" on the strength of one, which
// is exactly why the gates are elsewhere.
func sendAnnotations() []mcpgo.ToolOption {
	return []mcpgo.ToolOption{
		mcpgo.WithReadOnlyHintAnnotation(false),
		// Destructive because a request can be a DELETE, and because a send
		// cannot be taken back whatever it was.
		mcpgo.WithDestructiveHintAnnotation(true),
		mcpgo.WithIdempotentHintAnnotation(false),
		mcpgo.WithOpenWorldHintAnnotation(true),
	}
}

func (s *Server) registerRun() {
	s.register(tool("send_request",
		"Send one request from the open Otis collection. This takes TWO calls and there is no "+
			"way to do it in one. Call it without `intent` first: nothing is sent, and you get "+
			"back what would happen — the resolved URL, the environment, whether a secret is "+
			"involved, and who will be asked to confirm. Show the person that, then call again "+
			"passing the `intent` you were given. A person is asked before anything leaves the "+
			"machine, and they may refuse.",
		sendAnnotations(),
		mcpgo.WithString("path", mcpgo.Required(),
			mcpgo.Description("The request's collection-relative path.")),
		mcpgo.WithString("environment",
			mcpgo.Description("Send against this environment instead of the active one. This does "+
				"not change which environment Otis is using, and an intent taken for one "+
				"environment cannot be spent against another.")),
		mcpgo.WithString("intent",
			mcpgo.Description("Omit to preview. Pass the intent from a preview to actually send.")),
	), mcp.CapRun, spendAfterApproval, s.sendRequest)

	s.register(tool("run_folder",
		"Run every request in a folder, in the folder's order. Two calls, like send_request: "+
			"without `intent` you get the plan and every request's preview, including how many "+
			"times a person will be asked to confirm — which is worth reading before you spend "+
			"the intent, because a person is asked once PER REQUEST, not once for the folder.",
		sendAnnotations(),
		mcpgo.WithString("path", mcpgo.Required(),
			mcpgo.Description("The folder's collection-relative path.")),
		mcpgo.WithBoolean("stopOnFailure",
			mcpgo.Description("Stop the run at the first request that fails.")),
		mcpgo.WithString("intent",
			mcpgo.Description("Omit to get the plan. Pass the intent from a plan to run it.")),
	), mcp.CapRun, spendAfterApproval, s.runFolder)
}

// sendRequest is both phases of one send.
func (s *Server) sendRequest(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}
	environment := req.GetString("environment", "")

	prepared, redactor, err := s.sender.Prepare(path, environment)
	if err != nil {
		return nil, redactor, err
	}
	// What it actually resolved against, which is not the argument unless the
	// agent named one.
	c.environment = prepared.Environment

	intentID := req.GetString("intent", "")
	if intentID == "" {
		// Phase 1. Nothing is sent and nobody is asked. Preparing costs real
		// work, so it costs a rate token.
		if !c.spend() {
			return nil, redactor, fmt.Errorf("too many run calls; wait a moment and try again")
		}
		preview, err := s.preview(prepared, s.clientCanBeAsked(ctx))
		if err != nil {
			return nil, redactor, err
		}
		return preview, redactor, nil
	}

	// Phase 2. The fingerprint is taken over what was *just* re-resolved, so
	// a request edited between the phases voids the intent rather than being
	// sent under a preview that never described it.
	//
	// Verified rather than spent, because asking a person is a round trip
	// (§6.3): this handler runs again when the answer arrives, and an intent
	// spent on the first pass would leave the retry nothing to spend. A
	// failed check still voids it, so the anti-search property is unchanged.
	if _, err := s.intents.Verify("send_request", intentID, prepared.Print.Fingerprint()); err != nil {
		return nil, redactor, err
	}

	decision := mcp.Decide(s.grants.Grants(), sendOf(prepared))
	if err := s.ask(ctx, req, decision, s.confirmationFor("send_request", ctx, prepared), intentID, c); err != nil {
		return nil, redactor, err
	}
	if c.inputRequired != nil {
		// Waiting on the person. Nothing is sent and the intent is still
		// there for the retry.
		return nil, redactor, nil
	}
	c.confirmed = decision.Outcome == mcp.Confirm

	// Now it is really happening, so the intent is spent — once, whatever
	// happens next.
	if _, err := s.intents.Redeem("send_request", intentID, prepared.Print.Fingerprint()); err != nil {
		return nil, redactor, err
	}

	// The budget is spent here, after approval (§10).
	if !c.spend() {
		return nil, redactor, fmt.Errorf("too many run calls; wait a moment and try again")
	}

	result, sendRedactor, err := s.sender.Send(ctx, path, environment)
	if err != nil {
		return nil, orRedactor(sendRedactor, redactor), err
	}
	c.status = fmt.Sprint(result.Status)
	return result, orRedactor(sendRedactor, redactor), nil
}

// runFolder is both phases of a folder run.
func (s *Server) runFolder(ctx context.Context, req mcpgo.CallToolRequest, c *call) (any, *mcp.Redactor, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}
	stopOnFailure := req.GetBool("stopOnFailure", false)

	plan, redactor, err := s.sender.FolderPlan(path)
	if err != nil {
		return nil, redactor, err
	}

	intentID := req.GetString("intent", "")
	if intentID == "" {
		if !c.spend() {
			return nil, redactor, fmt.Errorf("too many run calls; wait a moment and try again")
		}
		return s.planView(ctx, plan)
	}

	// One intent covers the run, because the plan is what was previewed. It
	// is bound to the plan's shape, so a request added to the folder — or a
	// URL changed in one — voids it.
	if _, err := s.intents.Redeem("run_folder", intentID, planPrint(plan).Fingerprint()); err != nil {
		return nil, redactor, err
	}

	// The person is asked per request (§6.5). The agent previewing the folder
	// once does not confirm anything on anybody's behalf, so this hook runs
	// inside the run and is where each request's own decision is made.
	// A folder run's confirmations are answered in Otis' window, never
	// through the client.
	//
	// Not a policy difference — mcp.Decide still decides each request on its
	// own — but a mechanical one: asking through the client is a round trip
	// that returns from the tool call, and a run is a loop inside one call.
	// Returning from the middle of it would abandon the requests already
	// sent. §6.5 already says a folder run against a confirming environment
	// is impractical from an agent, "which is correct"; this is where that
	// bites, and the plan's `confirmations` count is what warns an agent
	// before it starts.
	var refusedAny bool
	before := func(ctx context.Context, prepared Prepared) error {
		c.environment = prepared.Environment
		decision := mcp.Decide(s.grants.Grants(), sendOf(prepared))
		inner := &call{spend: c.spend}
		if err := s.askInWindowOrRefuse(ctx, decision, s.confirmationFor("run_folder", ctx, prepared), inner); err != nil {
			refusedAny = true
			return err
		}
		if inner.surface != mcp.NotAsked {
			// The last surface asked is what the run's audit line records:
			// the run is the call, and each request's own send is recorded by
			// the sender.
			c.surface = inner.surface
		}
		if decision.Outcome == mcp.Confirm {
			c.confirmed = true
		}
		if !c.spend() {
			return fmt.Errorf("Otis' run budget is exhausted; the rest of this folder was not sent")
		}
		return nil
	}

	result, runRedactor, err := s.sender.RunFolder(ctx, path, stopOnFailure, before)
	if err != nil {
		return nil, orRedactor(runRedactor, redactor), err
	}
	if refusedAny {
		c.status = "partly-refused"
	} else {
		c.status = fmt.Sprintf("%d sent", result.Summary.Sent)
	}
	return result, orRedactor(runRedactor, redactor), nil
}

// preview mints an intent and describes what phase 2 would do.
//
// A send the policy will refuse outright is said so here, rather than letting
// the agent spend an intent to find out: the point of phase 1 is that the
// resolved target and its consequences land in the agent's context — and so in
// a transcript a person may be reading — before anything happens.
func (s *Server) preview(p Prepared, clientCanAsk bool) (SendPreview, error) {
	decision := mcp.Decide(s.grants.Grants(), sendOf(p))
	view := SendPreview{
		Path:        p.Path,
		Method:      p.Method,
		ResolvedURL: p.URL,
		Environment: p.Environment,
		UsesSecret:  p.UsesSecret,
		Secrets:     p.SecretNames,
		Reviewed:    p.Review != mcp.Unreviewed,
		WillAsk:     s.willAsk(decision, clientCanAsk),
	}
	if decision.Outcome == mcp.Deny {
		view.Blocked = true
		view.Reason = decision.Reason
		// No intent: there is nothing to spend it on.
		return view, nil
	}

	intent, err := s.intents.Issue("send_request", p.Path, p.Environment, p.Print.Fingerprint())
	if err != nil {
		return SendPreview{}, err
	}
	view.Intent = intent.ID
	view.ExpiresAt = intent.ExpiresAt.Format("2006-01-02T15:04:05Z")
	return view, nil
}

// planView is run_folder's phase 1: the plan, every request's preview, and
// the number of times a person is about to be asked.
func (s *Server) planView(ctx context.Context, plan FolderPlan) (any, *mcp.Redactor, error) {
	view := FolderPlanView{Path: plan.Path, Requests: []PlanRequest{}}
	grants := s.grants.Grants()
	// A folder run's confirmations are window-only whatever the surface would
	// otherwise be (see runFolder), so the plan says so rather than promising
	// the client's prompt.
	const clientCanAsk = false
	for _, prepared := range plan.Requests {
		decision := mcp.Decide(grants, sendOf(prepared))
		view.Requests = append(view.Requests, PlanRequest{
			Path:        prepared.Path,
			Name:        prepared.Name,
			Method:      prepared.Method,
			ResolvedURL: prepared.URL,
			UsesSecret:  prepared.UsesSecret,
			Reviewed:    prepared.Review != mcp.Unreviewed,
			WillAsk:     s.willAsk(decision, clientCanAsk),
			Blocked:     decision.Outcome == mcp.Deny,
			Reason:      decision.Reason,
		})
		// The count §12 calls the useful part of the plan: how many prompts
		// this run is about to put in front of somebody.
		if decision.Outcome == mcp.Confirm {
			view.Confirmations++
		}
	}
	intent, err := s.intents.Issue("run_folder", plan.Path, "", planPrint(plan).Fingerprint())
	if err != nil {
		return nil, mcp.NoSecrets(), err
	}
	view.Intent = intent.ID
	view.ExpiresAt = intent.ExpiresAt.Format("2006-01-02T15:04:05Z")
	return view, mcp.NoSecrets(), nil
}

// planPrint is the fingerprint a folder run's intent binds to.
//
// It covers each request's path, method and resolved URL, so adding a request
// to the folder or repointing one of them voids the intent. The bodies are not
// in it: a folder plan does not resolve them, and a fingerprint that claimed
// to cover something it never saw would be worse than one that says what it
// covers.
func planPrint(plan FolderPlan) mcp.RequestPrint {
	lines := make([]string, 0, len(plan.Requests))
	for _, item := range plan.Requests {
		lines = append(lines, item.Path+" "+item.Method+" "+item.URL)
	}
	return mcp.RequestPrint{Method: "RUN", URL: plan.Path, Headers: lines}
}

// willAsk says who will be asked and where, in words.
//
// It names the actual surface rather than the possibilities. An earlier
// version said "in your client or in Otis' window, whichever answers first",
// which was true of the design and is not true of the code: the two surfaces
// cannot be raced, because asking through the client returns from the tool
// call while asking in the window blocks inside it (docs/MCP.md §6.4). An
// agent that tells a person to watch the wrong place has warned them of
// nothing, so this takes clientCanAsk and commits to an answer.
func (s *Server) willAsk(decision mcp.Decision, clientCanAsk bool) string {
	switch decision.Outcome {
	case mcp.Deny:
		return "nobody — Otis will refuse this: " + decision.Reason
	case mcp.Proceed:
		return "nobody — this environment allows agent sends without confirmation"
	}

	windowOpen := s.asker != nil && s.asker.WindowOpen()
	if decision.Surface == mcp.SurfaceWindow {
		where := "the person, in Otis' own window"
		if !windowOpen {
			where += " — and no Otis window is open, so this will fail"
		}
		return where + " — " + decision.Reason
	}
	switch {
	case clientCanAsk:
		return "the person, in your client — " + decision.Reason
	case windowOpen:
		return "the person, in Otis' own window — " + decision.Reason
	default:
		return "nobody can be asked: no Otis window is open and this client cannot ask " +
			"you questions, so this will fail — " + decision.Reason
	}
}

// sendOf is the policy's view of a prepared request.
func sendOf(p Prepared) mcp.Send {
	return mcp.Send{
		Environment:    p.Env,
		HasEnvironment: p.HasEnvironment,
		Review:         p.Review,
		UsesSecret:     p.UsesSecret,
	}
}

// confirmationFor is what a person is shown.
func (s *Server) confirmationFor(toolName string, ctx context.Context, p Prepared) Confirmation {
	return Confirmation{
		Tool:        toolName,
		Client:      s.clientName(ctx),
		Path:        p.Path,
		Name:        p.Name,
		Method:      p.Method,
		URL:         p.URL,
		Environment: p.Environment,
		UsesSecret:  p.UsesSecret,
		Secrets:     p.SecretNames,
		Reviewed:    p.Review != mcp.Unreviewed,
	}
}

// orRedactor prefers the first non-nil redactor.
//
// A send produces its own — the response body is masked against the secrets
// that request used — and it is the one that matters. The prepared request's
// is the fallback for the paths where the send never happened.
func orRedactor(first, second *mcp.Redactor) *mcp.Redactor {
	if first != nil {
		return first
	}
	if second != nil {
		return second
	}
	return mcp.NoSecrets()
}
