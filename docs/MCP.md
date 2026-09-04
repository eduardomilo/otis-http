# The MCP server — proposal

**Status: proposal. Nothing in this document is implemented.** It is written to
be approved, rejected or argued with first, because the feature can send
authenticated requests to production systems and the security design *is* the
feature rather than a wrapper around it.

Read `VISION.md` §3 first. The line it draws under secrets is the constraint
everything here bends around.

---

## 1. What it is, and why it lives inside Otis

An MCP server that lets an agent — Claude Code, or anything else that speaks
the protocol — read a collection and, if you let it, send its requests.

There are already MCP servers that wrap an HTTP client's CLI. This one is
**in-process**, inside the running app, and that is the whole point: it can
reach the things that only exist in a running Otis.

- **Session variables.** A value a post-response script set (`FORMAT.md` §4.5)
  lives in memory in the open collection and is in no file. A CLI wrapper
  starts a fresh process per call and cannot see it, so an agent driving one
  cannot do the thing people actually need — create a resource, then act on the
  id that came back. §7 shows that flow working.
- **The held response.** A response body stays in Go and is paged out a
  screenful at a time (`CLAUDE.md`). The agent reads it the same way, so a
  40 MB body does not become a 40 MB tool result.
- **The human.** A confirmation an agent needs can appear in the window the
  person is already looking at. A CLI wrapper has nowhere to ask.
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

> **Open decision (§13.1).** A port and token that change every launch mean
> re-reading that file each time. The alternative is a stable port, which is a
> worse security posture for a marginal convenience. My recommendation is to
> keep it unstable and ship a `otis mcp config` command that prints the current
> block for pasting.

## 3. Capabilities: READ and RUN

Two switches. **Both off by default, and the app ships with the server itself
off.**

| Capability | Grants | Default |
| --- | --- | --- |
| — | nothing; no listener at all | **off** |
| READ | `list_requests`, `get_request`, `list_environments`, `get_session_variables`, `get_last_response`, `get_test_results` | **off** |
| RUN | `send_request`, `run_folder` | **off** |

RUN without READ is allowed and is not silly: an agent told exactly which
request to send does not need to enumerate the collection.

Enabling either is a click in the app, per-machine, and persisted in
`settings.json` under a new `mcp` key — never in the collection. Whether *you*
let an agent drive your machine is not a fact about the repository, and a
committed switch would be one person deciding it for the whole team.

**Turning RUN on does not grant sending.** It grants the *tool*. Whether a
given call proceeds is §4 and §5.

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

Unknown values are an error naming the key, not a silent fallback — `"agents":
"alow"` must not read as permission.

## 5. The consent model

**Every call that needs confirmation gets its own confirmation. There is no
session-wide approval, no "allow for 10 minutes", and no "always allow".**

The gate lives where the existing one lives — `send-context`, which already
holds the `confirmBeforeSend` gate for the Send button, ⌘↵, the palette and
folder runs, on the argument that "a safety feature with a hole in it is not
one". An agent send is one more caller, and it must not get its own path.

The flow for a call that needs confirmation:

1. The tool handler resolves the request far enough to know **what it would
   do**: method, the resolved URL with secrets masked, the environment, and
   whether a secret would be used.
2. It raises a confirmation in the window and **blocks**.
3. The window shows a modal naming the agent, the tool, and those details.
   Two buttons: **Send once** and **Refuse**.
4. The person answers. Refusal returns an error the agent can read. No answer
   within **60 seconds** is a refusal — an agent must not be able to leave a
   dialog up indefinitely, and a call that hangs forever is worse for the agent
   than one that fails.
5. Only then does the send happen.

Consequences worth stating plainly:

- An agent cannot batch approvals. Ten sends against a `confirm` environment
  are ten dialogs. That is the intended friction; if it is too much, the answer
  is `"allow"` on a safe environment, not a weaker gate.
- `run_folder` against a `confirm` environment asks **once per request in the
  folder**, not once for the folder. A folder run is N sends and the policy is
  per send. A twenty-request folder is therefore impractical to run against
  production from an agent, which is correct.
- If no window is open, or the collection is closed, a call needing
  confirmation fails. There is nobody to ask.

## 6. Secrets

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
  and §14 tests it directly.
- `list_environments` reports that a variable **is** a secret and never its
  value, exactly as `EnvironmentRow` does for the window.
- `get_session_variables` marks a session value secret if it came from one, and
  withholds it.
- No tool takes a secret as an argument. There is no way for an agent to set
  one, and no tool writes to the keychain.

## 7. Session state: the multi-step flow

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

## 8. The audit log

Every tool call is recorded, whatever the outcome:

| Field | |
| --- | --- |
| `at` | when |
| `tool` | which tool |
| `target` | the request or folder node path |
| `environment` | the environment name, or `""` |
| `decision` | `allowed` · `confirmed` · `refused` · `denied-by-policy` · `timed-out` · `rate-limited` |
| `status` | the HTTP status, or the failure kind |
| `duration` | how long it took |
| `client` | the MCP client's declared name and version |

**What is deliberately not in it:** no request body, no response body, no
headers, no secret value, no session variable value. An audit log is a record
of *what was done*, and one that quoted payloads would become the thing you
have to protect.

Inspectable from the UI: a panel listing the calls, newest first, with the
in-app indicator (§10) as its way in.

> **Open decision (§13.2).** In memory for the session, or appended to
> `<config>/otis/mcp-audit.jsonl`? In memory matches how session state works
> and leaves nothing behind. A file is what makes it an audit log rather than a
> readout — but it records which endpoints you called, which is a privacy
> artifact that did not exist before. My recommendation: in memory by default,
> with persistence a setting, off.

## 9. Rate limiting and the kill switch

**Rate limits**, per capability, token bucket:

| | Sustained | Burst |
| --- | --- | --- |
| READ | 10/s | 30 |
| RUN | 1/s | 5 |

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

## 10. The in-app indicator

**When the server is enabled, the title strip carries a chip; when a client is
actually connected, the chip is live.** The design does not draw one, so this
is a `DESIGN-NOTES` §9 item (§13.3), but its content is not in doubt:

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

## 11. Tool surface

Refined from the brief. Every result is masked (§6). Every path is a
collection-relative node path (`FORMAT.md` §2.1).

### READ

**`list_requests`** — the collection as a tree.
`{ folder?: string, includeFolders?: boolean }` →
`{ requests: [{ path, name, method, folder }], folders: [...] }`
The same tree the sidebar draws and `otis ls` prints.

**`get_request`** — one request, as it would be sent.
`{ path: string, environment?: string }` →
`{ path, name, method, url, headers: [{ name, value, source, inherited }],
   auth: { kind, source, secret }, body: { kind, contentType, preview },
   variables: [{ name, resolved, origin, secret }], warnings: [] }`
Effective values with provenance, not the raw file: the agent should see what
will happen, which is the same thing the editor shows a person. `body.preview`
is capped at 4 KB; `resolvedUrl` is masked.

**`list_environments`** —
`{ environments: [{ name, description, active, confirmBeforeSend, agents,
   variables: [{ name, secret }] }] }`
Names and shapes. Never a value, secret or not — an environment's non-secret
values are still somebody's infrastructure.

**`get_session_variables`** — what runs have set (§7).
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

**`send_request`** — send one request.
`{ path: string, environment?: string }` →
`{ sendId, status, statusText, timing, size, resolvedUrl,
   tests: { passed, failed }, sessionVariablesSet: [{ name, scope, owner }] }`
Naming an `environment` is allowed and is policy-checked like any other; it
does not change the app's active environment, because an agent must not be able
to silently repoint the human's next send.

**`run_folder`** — run a folder in order.
`{ path: string, stopOnFailure?: boolean }` →
`{ runId, results: [{ path, status, tests }], summary: { sent, passed, failed } }`
Confirmation is per request (§5).

### Not exposed, on purpose

- **No writes.** No tool creates, edits, renames or deletes a request, folder,
  environment or `.order`. An agent that can rewrite the collection can rewrite
  the request it is about to be allowed to send, which makes every confirmation
  above meaningless.
- **No secret access of any kind**, read or write.
- **No git.** No commit, stage, or discard. `internal/diff` is the only thing
  that writes to a repository and it stays a human affordance.
- **No `clear_session`.** Reaching into the human's session state to destroy it
  is not something an agent needs.
- **No arbitrary URL.** An agent can send *a request in the collection*, not a
  request it composed. This is the boundary that makes the whole thing
  reviewable: what can be sent is what is committed and was reviewed.

That last one is the most important design choice here, and it is worth being
explicit that it is a choice. It means an agent cannot explore an API freely
through Otis. It also means the blast radius of a compromised or confused agent
is bounded by the collection somebody already reviewed.

## 12. What this protects against, and what it does not

**Does:**
- An agent reading a secret value — architecturally, not by policy (§6).
- An agent sending to production without a person seeing it (§4, §5).
- Another process or user on the machine driving it — token, loopback bind.
- A web page driving it through your browser — `Origin` and `Host` checks.
- An agent composing a request nobody reviewed (§11).
- Runaway loops — rate limits, and a kill switch that means it.

**Does not:**
- **A malicious agent within its grant.** If you enable RUN and mark an
  environment `"allow"`, an agent can send those requests as fast as the rate
  limit permits. That is the grant working, not failing.
- **A confused person clicking through confirmations.** A dialog is only as
  good as its reading, which is why the confirmation names the method, the
  resolved URL and the environment rather than asking "allow?".
- **Anything a request itself does.** Otis sends what the collection says. A
  reviewed request that deletes a production table will delete it.
- **Another local process reading `mcp.json`.** It is mode `0600`, which stops
  other *users*, not other processes running as you. A local attacker with your
  uid has your keychain anyway.
- **Prompt injection reaching the agent through a response body.** A response
  can carry text that tries to instruct the agent. Otis cannot prevent that and
  should not pretend to; what it can do is make sure the *next* action still
  needs the same consent as the first, which is exactly why there is no
  session-wide approval.

## 13. Open decisions — for you

1. **Port and token stability** (§2). Recommend: unstable, plus
   `otis mcp config` to print the client block.
2. **Audit log persistence** (§8). Recommend: in memory, persistence an
   off-by-default setting.
3. **The indicator's colour and place** (§10). A `DESIGN-NOTES` §9 item;
   recommend the title strip in amber, and I would add it as §9.22 rather than
   decide it here.
4. **`agents` default.** Recommend `"confirm"`. `"deny"` is safer and makes RUN
   useless until every environment is annotated; `"allow"` is indefensible.
5. **Confirmation timeout** (§5). Recommend 60s. Longer leaves dialogs up;
   shorter makes a person racing a timer.
6. **Should RUN require READ?** Recommend no — an agent told exactly what to
   send should not need to enumerate.

## 14. Verification plan

The brief's list, plus what §6 demands:

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
   (§7), and the value never appears in the first result.
7. Kill switch mid-session → the next call fails authentication, an in-flight
   send is cancelled, `mcp.json` is gone.
8. Rate limit: exceed RUN's bucket → error, logged as `rate-limited`, and a
   refused confirmation does not consume budget.
9. `Origin: https://evil.test` → rejected. No token → rejected. `0.0.0.0`
   never appears in a bind.
10. Audit log contains every one of the above with the right `decision`, and no
    body, header or secret.

Tests may not touch the network (`CLAUDE.md`), so 5 and 6 run against an
`httptest` server.

---

**Nothing above is built.** Approve, change or reject it and I will implement
what survives, in the order §14 tests it — starting with the masking test,
because it is the one whose absence would be unrecoverable.
