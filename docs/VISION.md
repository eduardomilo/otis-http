# What Otis is, and why

This document is the *why*. `FORMAT.md` is the what — the on-disk format and
the CLI — and `design/DESIGN-NOTES.md` is the how it looks. When a decision
has to be made and those two do not settle it, this is the tiebreak.

> **Written from the evidence.** Every claim below is one the code already
> makes: each is traceable to a constraint in `CLAUDE.md`, a rule in
> `FORMAT.md`, or a resolved decision in `DESIGN-NOTES.md` §9. Nothing here is
> aspiration. §7's priorities are the exception — they are a reading of what
> the work so far implies, and are the part most worth arguing with.

## 1. The thesis

**An HTTP request is source code, so it should live where source code lives.**

A request that exercises your API is a fact about your API. It has the same
lifecycle as the endpoint it calls: it is written when the endpoint is written,
it changes when the endpoint changes, it breaks when someone breaks the
endpoint, and it should be reviewed by whoever reviews the endpoint. Every one
of those is something version control already does well and nothing else does
at all.

So a collection in Otis is **a directory of files in your repository**. Not an
export from one. Not a sync target. The directory *is* the collection, the
files *are* the requests, and `git log` is the history. `otis ls` and the
sidebar print the same tree, and a test asserts it.

## 2. What follows from that

The thesis is only worth holding if it survives contact with the details. Most
of Otis' constraints are that survival.

**The files have to be readable by a person.** A format that is technically
text but practically a serialisation blob buys nothing — you cannot review it,
so it is a database with extra steps. So Otis writes the format the JetBrains
HTTP Client and the VS Code REST Client already read, and adds nothing they
reject: its extensions live in comment directives (`# @name`) and `{% %}`
script blocks, which both tools already tolerate (`FORMAT.md` §1). You can
adopt Otis without leaving them, and leave Otis without exporting anything.
That is deliberate: a format nobody else can read is a lock-in, whatever the
licence says.

**The files have to survive being edited by Otis.** A tool that reformats a
file on save puts every colleague's whitespace in your diff, and after that
nobody reviews the diff. So parsing a canonical file and writing it back
produces *identical bytes* (`FORMAT.md` §1.13), a body is preserved verbatim
down to its indentation (§1.8), and `.order` is never rewritten except by an
explicit reorder — a test asserts the file is byte-identical, not equivalent,
because a rewrite producing the same order would still land in somebody's diff
and would still have eaten their comments.

**One writer per kind of file.** `request.go` is the only thing that writes a
`.http`, `order.go` the only thing that writes a `.order`, `folder.go` the only
thing that writes a `_folder.http`, `environment.go` the only thing that touches
the keychain. Not tidiness: it is what makes "does anything else write this?"
a question you can answer by reading one file instead of trusting a convention.

**Review is a first-class screen.** Otis has a git diff view because a request
collection that changes without review is how a team ends up with six versions
of the same endpoint and no idea which one is right. `internal/git` is
read-only and "not a repository" is a normal state — a directory of files works
perfectly well outside version control. `internal/diff` is the only thing that
writes to a repository, and only the index and a commit; push, pull, merge and
rebase are git's job and the terminal's.

## 3. The line under secrets

**A resolved secret value never leaves Go.** Not across a binding, not in a log
line, not in an error message, not into a script, not into `settings.json`.

This is the one constraint with no trade to make. A file-based HTTP client is
useless if it cannot authenticate, and a collection that carries credentials is
a liability the moment it is pushed. So the collection holds a *reference*
(`{"$secret": "keychain"}`) and the value lives in the OS keychain, and the
architecture is arranged so the value has nowhere else to go: the window is
told `secret: true` and draws the dots itself rather than being handed a mask
that could one day be a value; a script sees an opaque handle whose three
coercion hooks all return `[secret:name]`; the design's "Reveal" became
**Copy**, because copying can be done by Go writing the clipboard while
revealing means handing the string to a webview
(`DESIGN-NOTES` §9.12).

`TestSecretHandleCannotBeExfiltrated` tries twenty-nine routes out. When
somebody finds a thirtieth, the test grows — the invariant is not something to
take on trust.

## 4. What the window is for

The CLI and the app are the same binary because they are the same product seen
from two distances. `otis run` in CI and the request editor resolve, prepare
and send through the same code, so a request that passes locally passes in the
pipeline for the same reasons.

What the window adds is **the things a file cannot tell you by looking at it**:

- **Where an effective value came from.** An inherited header names the file it
  came from and the script that set it; auth names its file; a variable names
  its storage. The resolver already computes that provenance, so showing it
  costs nothing and not showing it makes inheritance a guessing game
  (`DESIGN-NOTES` §8.1).
- **What a write will do before it happens.** "Override copies the header into
  this file", "writes `@auth none`", "Order saved to `orders/.order`". The UI
  tells you what the diff will look like (§8.2).
- **Exact counts, everywhere.** `7 sent · 4 local · 3 inherited`,
  `inherited by 6 requests`, `+4 −2 · 2 hunks`. A count that is approximate is
  a count you have to go and check (§8.5).

## 5. What Otis is not

- **Not a collaboration platform.** There are no accounts, no workspaces and no
  sync. Sharing is `git push`; access control is the repository's. Which
  environment is *active* is per-machine and deliberately not in the collection
  — one person works against staging while another is on local, from the same
  branch.
- **Not a mock server, not a test framework, not an API designer.** It sends
  requests and tells you what came back. `test`/`expect` exist so a response
  can be asserted on (`FORMAT.md` §9.9), not to become a test runner.
- **Not a place to put a value nobody reviewed.** A session variable is scratch
  state between requests and is written nowhere (§4.5). Anything a colleague
  needs belongs in `_folder.http` or an environment, where it is reviewable.
  The tool should make the reviewable path the easy one.
- **Not a sandbox escape.** A script gets a JavaScript realm and nothing else:
  no filesystem, no process, no network, no timers, with the dangerous names
  defined as throwing stubs so reaching for `fetch` gets a message rather than
  `undefined` — and so that wiring one in means deleting a line first.

## 6. How decisions get made

Three habits, all of them visible in the repository:

**Write the decision down where the next person will look for it.** Every
syntax rule is in `FORMAT.md` as it is made; every visual decision in
`DESIGN-NOTES.md`; every constraint that would be easy to violate by accident
in `CLAUDE.md`, with the reason. If the code and one of those disagree, that is
a bug in the code.

**Leave open questions open.** `DESIGN-NOTES` §9 lists the decisions that are
not made yet, and the rule is that they are not resolved silently. Where the
format has no answer — a disabled *local* header has no on-disk form (§9.5) —
the UI shows the control disabled with the reason in its tooltip rather than
inventing syntax to fill the gap. Inventing it is the expensive mistake,
because once written to somebody's file it cannot be taken back.

**Prefer the constraint that can be checked.** "Nothing outside
`internal/services` depends on Wails" is a good intention; the `otis_cli`
build tag that fails CI when it stops being true is a constraint. Same for the
byte-identical `.order` test, the event-name mirror test, and the secret
exfiltration test. When a rule matters, find the check.

## 7. Priorities

*This section is a reading of what the work so far implies. It is the part to
argue with.*

In order:

1. **Correctness of what is written to disk.** A tool that mangles a file loses
   trust it cannot earn back. Round-trip fidelity, one writer per file kind,
   and never touching a file the user did not ask to change.
2. **The secret line.** See §3. It does not bend for a feature.
3. **Legibility of inheritance.** The reason a request collection rots is that
   nobody can tell what a request will actually send. Provenance everywhere is
   the answer, and it has to keep working as folders, environments, scripts and
   overrides multiply.
4. **The CLI staying equal to the app.** The moment CI and the window disagree
   about what a request means, the format stops being the product.
5. **Speed at real sizes.** A collection of two thousand requests is not
   exotic. The tree is virtualised, a response body never crosses the binding
   whole, and pretty-printing is Go's job — all three because the naive version
   was measurably too slow, not because it might be.
6. **Everything else.** Convenience, polish, breadth of protocol support.

What is deliberately *not* being built yet, and why:

- **Code signing and notarization** — deferred until there are enough users to
  justify the certificates. `RELEASING.md` says exactly where they slot in and
  which secrets they need, so the deferral costs a day, not a rewrite.
- **An auto-updater** — `brew upgrade` and a download are the update path. This
  is why `otis --version` reports the commit and build date, and why the window
  shows its version at all: with no updater, "which build is this" has to be
  answerable by hand.
- **Anything that needs an account.** See §5.
