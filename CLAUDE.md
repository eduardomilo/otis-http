# Otis — file-based HTTP client

Desktop app built with Wails v3 (Go) + React/TypeScript. Go module
`github.com/otis-http/otis`, binary `otis`.

## Required reading

Read these before changing anything they cover. They are the specs; the code
follows them, not the other way round.

- `docs/VISION.md` — what Otis is and why: the thesis, what follows from it,
  the line under secrets, and the order the priorities go in. The tiebreak when
  FORMAT.md and DESIGN-NOTES.md do not settle a decision.
- `docs/FORMAT.md` — the on-disk format and the CLI surface. Authoritative.
- `docs/design/DESIGN-NOTES.md` — the UI design system: color tokens, method
  colors, type scale, spacing, shadcn mapping. Authoritative for anything
  visual. `docs/design/SCREENS.md` covers each screen, and
  `docs/design/screens/*.png` is the offline copy of the design, which
  otherwise lives only in Claude Design.
- `docs/BUILDING.md` — how the binary is built and what each platform's
  artifacts are: the `otis_cli` build tag, version stamping, icons, the `.http`
  file association, single instance, the Windows console caveat, and the
  WebKitGTK floor on Linux. Authoritative for anything in `build/` or the
  Taskfiles.
- `docs/RELEASING.md` — cutting a release, what unsigned costs users on each
  platform, and exactly where signing and notarization would go plus the
  secrets each would need. Authoritative for anything in `.github/workflows/`.
- `docs/MCP.md` — the MCP server's tool surface and consent model.
  **A proposal, not yet implemented and not yet approved.** Do not build any
  part of it until it is; the security design is the feature.

If one of these and the code disagree, that is a bug. §9 of DESIGN-NOTES.md
lists the design decisions that are still open — do not resolve them silently.

## Layout

- `main.go` — the entry point, and it decides one thing: whether this process
  is the desktop app or the `otis` command. Nothing else lives here.
- `gui.go` — the desktop app: the embedded frontend, the Wails options, the
  services, the macOS menu, the file-open handler and the single-instance
  forwarder. Behind `//go:build !otis_cli`, so it and every Wails dependency
  leave the build under that tag; `gui_disabled.go` is the other side and
  explains why anyone would want it (docs/BUILDING.md §2).
- `internal/services/` — all Go services exposed to the frontend. One file per service.
  `request.go` is the request editor's: it loads a `.http` file with its
  inheritance and provenance, and is the only thing that writes one. `send.go`
  is the sender: it resolves, prepares and sends a request, holds the response
  and pages it to the window, and runs a folder's requests in sequence.
  `environment.go` is the environment editor's, and the only thing that puts a
  secret into the keychain or takes one out. `order.go` is the sidebar's drag
  and the folder menu's Manual/Alphabetical, and the only writer of a
  `.order`. `folder.go` is the folder view's:
  it reads a folder's shared settings with their provenance, its documentation
  and scripts, and which descendants opt out of each setting, and is the only
  thing that writes a `_folder.http`.
- `internal/httpfile/` — `.http` parser + serializer.
- `internal/collection/` — the directory walk, `.order` and the node tree,
  including script nodes and the hook/module distinction (docs/FORMAT.md §2.4).
- `internal/resolve/` — inheritance, `{{variable}}` resolution, environments,
  and the in-memory session store (`session.go`, docs/FORMAT.md §4.5).
- `internal/secrets/` — the secret store behind a `{"$secret": "keychain"}`
  reference: `Keyring` (the OS credential store, via go-keyring, pure Go so
  `CGO_ENABLED=0` still works, with a key index beside `settings.json` because
  no keyring enumerates), `Memory`, `Fallback` (read through a list, which is
  how the CLI puts `OTIS_SECRET_*` ahead of the keychain), and `Placeholder`,
  the display-only store read paths resolve against so no real lookup happens
  when the editor only needs to know that a secret exists.
- `internal/httpclient/` — preparing and sending a resolved request, including
  AWS SigV4.
- `internal/response/` — a response body held in Go, formatted and indexed by
  line once, and served to the window a screenful at a time. See the
  binding-boundary constraint below.
- `internal/script/` — the script runtime (docs/FORMAT.md §9): goja, the
  sandbox, `vars`, the opaque secret handle, `test`/`expect`, and the module
  transform. It takes interfaces and touches no disk, which is what keeps the
  interpreter unable to reach one.
- `internal/scriptrun/` — the wiring between that runtime and a collection:
  the hook plan, the module loader, the variable store and the substitution of
  a secret handle into the prepared request. The window and the CLI both use
  it, so `otis run` in CI and the request editor run scripts identically.
- `internal/mcp/` — the MCP server's security core, and nothing that talks
  MCP: `policy.go` decides what an agent may do (docs/MCP.md §§3–6) as pure
  functions over grants, an environment's `$otis.agents` and git's view of the
  tree; `redact.go` is the single gate every agent-facing result passes
  through; `audit.go` is the JSONL record (§9); `auth.go` is the token and the
  `Origin`/`Host` checks (§2); `limit.go` the per-capability token buckets
  (§10); `intent.go` the two-phase gate and the fingerprint it binds to
  (§6.2); `endpoint.go` the `mcp.json` a client is configured from. It imports
  no service and no transport — no MCP library either, which is the point:
  the part of this feature worth auditing is one package with no protocol in
  it. It must keep building under `otis_cli`, since `cmd/otis` imports it for
  `otis mcp config`, and that is also what keeps mcp-go out of the CLI.
- `internal/services/mcp.go` — the agent server as the window sees it. Eight
  exported methods and no more (the popover's switches, the kill switch, and
  `Answer`); every boundary `internal/mcpserver` calls lives on the unexported
  `agentBridge`. It constructs the server out of the *other* services and
  reimplements nothing, which is what makes an agent's write subject to the
  invariants a person's is. It is never given the secret store: no tool reads
  or writes a secret, and the way that is guaranteed is that this service has
  no way to.
- `internal/mcpserver/` — the MCP protocol surface: `source.go` is the `Source`
  interface the tools read and write through (and the `Grantor` whose
  `RevokeAll` is what makes the kill switch final), `tools.go` the framework
  every tool is registered through, `read.go` the READ tools, `server.go` the
  loopback listener's lifecycle. It does not import `internal/services`, the
  same way `internal/script` takes no dependency on a disk: the interface is
  the boundary, and a tool cannot reach what is not on it — which is how
  docs/MCP.md §12's "not exposed, on purpose" list is enforced rather than
  remembered.
- `internal/importer/postman/` — the Postman v2.1 importer.
- `internal/events/` — the name of every Go → frontend event, and the generator
  for the TypeScript mirror. See "Events" below.
- `internal/watch/` — the recursive filesystem watcher behind the live tree,
  and the write guard that keeps Otis' own writes from bouncing back.
- `internal/git/` — read-only git: branch, ahead/behind, per-path status.
- `internal/diff/` — the diff view: working tree against HEAD with per-file
  hunks (`hunks.go`, whose `Apply`/`Reverse` are the one splice behind both
  staging and discarding), the semantic hunk headers a `.http` file allows
  (`label.go`), and the four operations a review performs (`apply.go`).
- `internal/settings/` — the JSON settings file in the OS config dir. The only
  place frontend state persists.
- `internal/buildinfo/` — the build identity: `Version`, `Commit` and `Date`,
  set by the linker, plus the one-line and block renderings `otis --version`
  and the window both use. Its own package rather than a corner of
  `internal/services`, because `cmd/otis` needs it and must not import that
  (Wails, and therefore cgo).
- `cmd/otis/mcp.go` — `otis mcp config`, and the reason there is no
  `otis mcp serve`: the listener belongs to the app, because a confirmation
  needs a window to appear in.
- `cmd/otis/` — the CLI (package `cli`, cobra). Not a `main` package: `main.go`
  dispatches to it when the process has arguments and opens the window when it
  does not. `dispatch.go` owns the rule that decides which — a file
  association arrives looking exactly like a command line — and
  `console_windows.go` is why the packaged Windows binary can print at all.
- `docs/` — the specs listed under Required reading above. Every syntax, semantics or
  visual decision is written there as it is made.
- `frontend/` — Vite + React + TS. Path aliases `@/*` → `frontend/src/*` and
  `@bindings/*` → the generated bindings, both mirrored in `tsconfig.json` and
  `vite.config.ts` (shadcn depends on the first). Inside `frontend/src/`:
  - `routes/` — TanStack file-based routing; `routeTree.gen.ts` is generated by
    the Vite plugin. Route files hold the route and its component, nothing else.
  - `components/ui/` — shadcn components. Ours to edit; note the deviation in a
    comment when you change one away from the shadcn default.
  - `components/shell/` — the window chrome: title strip, panes, tab bar, status
    bar, palette, empty state, `create-dialog` (naming a new request or folder,
    showing the path it will write), `order-strip` (screen 2a's confirmation
    under the tree), `agent-chip` (the MCP indicator and its popover,
    DESIGN-NOTES §9.22) and `agent-confirm-dialog` (the confirmation an
    agent's send blocks on, including §5.1's danger variant with the diff in
    it). One component per file, named after it.
  - `components/editor/` — the CodeMirror 6 setup: the theme and syntax
    colours (`otis-theme`), the `{{variable}}` decoration
    (`variable-decoration`), and the React wrapper (`code-editor`). One
    editor component; the URL field is the same one with `singleLine`.
  - `components/request/` — the centre pane for `/r/$path`: `request-editor`
    and one file per sub-tab (`params-tab`, `headers-tab`, `body-tab`,
    `auth-tab`, `scripts-tab`), plus `url-bar`, `variable-text` and the two
    prompts (`conflict-banner`, `unsaved-changes-dialog`).
    `auth-directive-form` is the scheme select and argument field that edits
    one `# @auth` line, shared with the folder view because the same directive
    is written at both levels — and because the AWS argument hint is the only
    place the five shapes docs/FORMAT.md §3.3 allows are spelled out for the
    user.
  - `components/response/` — the right pane: `response-pane` (status line and
    sub-tabs), `body-view` (the windowed body, whose lines come from Go) and
    `failure-view` (a send that produced no response, by failure kind).
  - `components/diff/` — the centre pane and sidebar for `/diff` and
    `/diff/$path`: `changes-list` (the changes and the commit box, replacing
    the tree), `diff-view` (the header, the hunk headers and their controls)
    and `hunk-view` (one hunk, unified or split).
  - `components/folder/` — the centre pane for `/f/$path` (screen 3a):
    `folder-view` (header, tabs, the `1fr 440px` split, the README's
    Preview/Edit and the live run), `panels` (the Auth, Headers, Variables and
    Scripts panels, each naming where its values come from), `settings-editor`
    (the write half: the Auth, Variables and Headers tabs edit
    `FolderDocument.Settings` and hand it to `FolderService.Save`) and
    `markdown` (the README, rendered).
  - `components/environment/` — the centre pane for `/env/$name`:
    `environment-editor` (the variable table), `secret-detail` (the split panel
    of screen 1c), `secret-value-dialog` (the one place a value travels *in*)
    and `environment-list` (the sidebar, which replaces the tree on this route).
  - `state/` — React context providers, one concern each (`settings-context`,
    `collection-context`, `tabs-context`, `documents-context`,
    `send-context`, `environment-context`, `diff-context`, `run-context`,
    `order-context`),
    each exporting a `useXxx` hook
    that throws outside its provider. Providers are composed in
    `routes/__root.tsx`; `environment-context` sits above `tabs-context`,
    because which environment is active decides how every document resolves;
    `documents-context` sits inside `tabs-context`, because a draft is what
    makes a tab dirty, and `send-context` inside both, because the response
    pane shows whatever the active tab is showing.
  - `hooks/` — reusable hooks that are not providers (`use-keymap`,
    `use-route-document`, `use-response-body`).
  - `lib/` — pure helpers, no React: `paths` (node paths in routes), `platform`
    (macOS vs the rest, keyboard hints), `method` (HTTP method colours), `time`,
    `http-file` (immutable edits to a parsed `.http` model), `variables`
    (`{{name}}` tokenizing and styling), `query` (the URL's query string as
    table rows), `json` (pretty-printing a body that contains references),
    `format` (durations, byte sizes and status-code colours), `json-tokens`
    (colouring a line of JSON that Go already indented), `drag` (where a
    dragged row would land, as pure functions), `fuzzy` (the palette's
    matcher, which returns matched character *positions* rather than a boolean,
    because the highlighting is per-character), `tree` (flattening, the expand
    rule and the filter, as pure functions).
- `frontend/bindings/` — generated by `wails3 generate bindings`. Never edit by hand.
- `build/` — wails3 Taskfiles and packaging assets. `build/config.yml` holds
  product metadata, including the `.http` file association and the bundle
  identifier. `build/appicon.svg` is the icon's source and `build/appicon.png`
  its committed render. `build/homebrew/otis.rb.tmpl` is the formula the
  release workflow renders into the tap. `build/windows/*.ps1` are the two
  steps that have to be PowerShell, and say why. `build/linux/otis.desktop`
  and `build/linux/otis-http.xml` are committed, not generated.
- `.github/workflows/` — `ci.yml` on every push and pull request: vet, race
  tests and typecheck once, then build and package on all three platforms.
  `release.yml` on a `v*` tag: the same checks as a gate, then build, sign
  (a no-op) and package per platform, then one job that collects everything,
  writes a single `SHA256SUMS`, checks each expected artifact is present by
  name, publishes the release, and updates the Homebrew tap.

## Hard constraints — do not violate without asking

- **Router uses hash history** (`createHashHistory()` from `@tanstack/history`).
  Never switch to browser history. Wails serves the packaged app from a custom
  scheme and browser history breaks there.
- **Never use `localStorage` or `sessionStorage`.** Use React state or Go.
- **Go → frontend streaming goes over the Wails event system**
  (`app.Event.Emit` / `Events.On`), not fetch/SSE/WebSocket. Bindings are
  request/response only.
- **Never change the Vite `build.outDir`.** `main.go` embeds `frontend/dist`.
- **Any SQLite work uses `modernc.org/sqlite`.** Keep `CGO_ENABLED=0` possible for
  Windows builds. No cgo dependencies without asking.
- **A CodeMirror rule that "does not take" is probably losing to the base
  theme.** `@codemirror/view`'s base theme uses long, specific selectors —
  `&dark.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground`
  for one — which outrank the short ones a theme naturally reaches for. The
  selection colour was set for two years' worth of reading and never applied to
  a focused editor. Mirror the base theme's selector shape; a theme outranks a
  base theme at equal specificity. DESIGN-NOTES §9.29.
- **`--bg-selected` is a selected *row*, never selected text.** The tree, the
  changes list, the environment list and the palette. Editor text selection is
  `--bg-text-selection`, which is deliberately neutral: colour means something
  everywhere else in this design and a selection means nothing. Do not reach
  for `--accent-wash` either — that is the background behind a `{{variable}}`,
  and the URL bar is made of those.
- **Build UI with shadcn components and Tailwind utilities.** No hand-rolled styled
  divs, no hardcoded hex colors — use the theme tokens (`bg-background`,
  `text-muted-foreground`, etc.). Tailwind v4 is CSS-first: theme lives in
  `frontend/src/index.css`, there is no `tailwind.config.js`. Dark theme is the
  default (`<html class="dark">`), and the design ships no light palette.
  Token values come from `docs/design/DESIGN-NOTES.md`; do not invent colors,
  sizes or radii, and do not add drop shadows (the design has exactly two).
- **Verify with `wails3 build`, not just `wails3 dev`.** Dev runs on localhost and
  hides packaging bugs (asset paths, hash routing). Launch the built binary from `bin/`.
- **Event names are never literals.** They live as constants in
  `internal/events`, and `go generate ./internal/events` writes the TypeScript
  mirror `frontend/src/lib/events.gen.ts`. A test fails if the two drift. Every
  name must also be registered in `main.go` with `application.RegisterEvent[T]`;
  use `application.Void` for an event with no payload — registering `any` and
  emitting `nil` panics inside Wails.
- **The frontend never touches disk, network, git or secrets.** If the window
  needs something from the machine, it goes through a Go service.
- **A README is rendered, never injected.** `components/folder/markdown` uses
  `react-markdown`, which builds a React element tree; there is no
  `dangerouslySetInnerHTML` anywhere in the app and there must not be. A
  README is repository content written by whoever wrote the branch, and the
  window it renders in is the one holding the collection. Links are not
  navigable and images are not loaded, for the same reason: neither should be
  a request the collection made without being asked.
- **A script gets a JavaScript realm and nothing else.** No filesystem, no
  process, no network, no timers (docs/FORMAT.md §9.3). goja provides none of
  them, and `internal/script`'s `forbidden` map defines the dangerous names as
  throwing stubs so a script that reaches for `fetch` gets a message rather
  than "not defined" — and so wiring one in means deleting a line there first.
  Each phase has a hard-killed budget: `vm.Interrupt` stops an infinite loop.
- **A resolved secret value never leaves Go.** Not across a binding, not in a
  log line, not in an error message, not in `settings.json`. Where the window
  has to show that a secret exists it gets a reference and a masked
  placeholder, never the value — see `resolve.Use`, whose `Value` is cleared
  whenever the expansion consumed a secret (a file variable that resolves to
  one *is* that secret), and `secrets.Placeholder`, the display-only store the
  editor resolves against so no real lookup happens on a read path.
  `EnvironmentRow.Value` is empty for a secret rather than holding a mask: a
  field that sometimes holds dots and sometimes a value is one refactor from
  shipping the value, so the window is told `secret: true` and draws the dots
  itself. Values travel *in* only — the user types one and it goes window → Go
  → keychain — and the design's `Reveal` is `CopySecretValue`, which writes the
  system clipboard from Go (DESIGN-NOTES §9.12). The key index beside
  `settings.json` holds keys and nothing else; a test asserts it.
  A script sees a `script.Handle`, never a string: all three JavaScript
  coercion hooks — `toString`, `valueOf`, `Symbol.toPrimitive` — return
  `[secret:name]`, every property is non-enumerable, and the value lives in a
  Go map the interpreter cannot reach. `Reveal()` is Go-only and the sender
  calls it once, after every script has run, registering the value with the
  masker so what the window is *shown* stays masked.
  `TestSecretHandleCannotBeExfiltrated` tries twenty-nine paths; add to it
  rather than trusting the invariant.
- **Go's serializer is the only writer of a `.http` file.** The editor edits
  the parsed model and hands it back to `RequestService.Save`, which
  serializes it. A second formatter in the frontend would mean two answers to
  what canonical form is (docs/FORMAT.md §1.13), and the round-trip guarantee
  is what keeps an edit out of somebody's whitespace diff.
- **A response body never crosses the binding boundary whole.** It stays in
  `internal/response`, which formats and indexes it by line once, and the
  window asks for the lines it is about to draw (`SendService.Lines`). A 40 MB
  body marshalled through one call is a 40 MB JSON string built in Go, parsed
  in the webview and handed to the DOM, and all three of those block. For the
  same reason pretty-printing is Go's job, not the window's.
- **`MCPService`'s exported methods are its binding surface, so the
  boundaries live on `agentBridge`.** Wails binds every exported method of a
  registered service to the window. Implementing `internal/mcpserver`'s four
  interfaces on the service itself put nineteen more methods there —
  `Send`, `AskInWindow`, `Grants`, `RevokeAll`, `UpdateRequest` — several
  returning a `*mcp.Redactor`, which must never join that surface.
  `TestTheAgentServiceExposesOnlyTheWindowsAPI` locks the list, because the
  `var _ mcpserver.Source = (*agentBridge)(nil)` assertions keep compiling if
  a method moves back: promotion through the embedded pointer satisfies them
  from either side.
- **`CollectionService` fires its `OnClose` hooks from `Open` as well as
  `Close`**, because opening the first collection of a session is a change
  from "none". A hook that tears something down must therefore check that a
  collection *was* open — `Current().Path != ""`, which still holds the
  outgoing collection while the hooks run. The MCP service's hook did not, and
  so disconnected at every launch the server `ServiceStartup` had just
  restored. Only running the built app found it; every test until then opened
  the collection before the service existed, so the launch order was never
  exercised. `TestOpeningTheFirstCollectionDoesNotDisconnect` is the
  regression.
- **A test that starts the MCP server must isolate the config directory.** The
  endpoint file and the audit log live under `os.UserConfigDir()`, so a test
  that does not set `HOME`/`XDG_CONFIG_HOME`/`AppData` writes to — and
  `Disconnect` *deletes* — the developer's real `~/…/otis/mcp.json`, pulling
  the endpoint out from under any Otis actually running, and leaves fake
  entries in their real audit log. Both happened before the isolation went in.
- **The `Origin` and `Host` checks are the DNS-rebinding defence, and a valid
  token is not a substitute.** Without them any web page the user visits can
  drive the MCP server through their browser: an attacking page points its own
  hostname at `127.0.0.1`, and the browser then sends *that* hostname in both
  headers — which is the only thing distinguishing it from a real local
  client, since the page cannot forge either header. So `Auth.Guard` checks
  them **before** the token, both because they answer "who is calling" without
  touching the credential and so a probing page cannot tell from a rejection
  whether its token was looked at. `localhost` counts as loopback *there* while
  §2 forbids binding to it — not a contradiction: binding to a name that might
  resolve off-loopback would expose the listener, whereas no rebinding attack
  can make a browser claim `localhost` for a page served elsewhere. A rejection
  never echoes what was presented, or the token lands in the caller's log.
- **One place serializes a tool result, and a handler cannot.** A tool's
  handler returns a *value* and the `*mcp.Redactor` that must mask it;
  `mcpserver.register` is the only thing that turns either into bytes, and it
  is also where the capability check, the rate token, the collection check and
  the audit line live. So "did this tool remember to mask / check / record?"
  is not a question that can be asked per tool. **Never call mcp-go's
  `NewToolResultStructured`** (or `…StructuredOnly`): it marshals the value
  itself and would go straight around the redactor.
  `TestNoToolBypassesRedaction` walks the package AST for those calls — text
  matching finds the doc comment warning against them and reports a violation
  that is not there. A handler returns its redactor **even alongside an
  error**, because an error can carry a resolved URL and a resolved URL can
  carry a credential in a query parameter; with no redactor the framework
  substitutes a fixed message rather than guessing that the error was safe.
- **Nothing sends on one tool call, and the intent is bound to a fingerprint.**
  `send_request` and `run_folder` without an intent preview and send nothing;
  with the intent handed back they go on to ask a person. Being universal is
  what makes it a property rather than a configuration (docs/MCP.md §6.2). The
  fingerprint covers method, URL, headers, body, environment and the session
  values consumed, each length-prefixed so no field's contents can imitate the
  next — without the body in it, two phases would be a hole: preview something
  harmless, edit it with `update_request`, spend the intent on what the preview
  never described. An intent is single-use and is spent **on every outcome
  including a stale one**, so an agent cannot retry the check to search for a
  matching request. And redeeming grants nothing: `Redeem` returns an intent
  and the caller still has to go through `Decide` and then a person. An agent
  will echo an intent back without a thought.
- **There is no headless MCP server, and `otis mcp serve` must not be added.**
  The listener lives in the app because everything that makes it safe is
  there: the window a confirmation appears in (§6.4), the open collection, the
  keychain. A CLI server would be this design with its one safety removed.
  `otis mcp config` prints the client block, which is the whole of the CLI's
  half; the port is OS-assigned and the token minted per launch, so both
  change every start (§14.1) and the command is what makes that cost a paste.
- **Everything an agent is shown goes through `mcp.Redactor.Marshal`, and it
  fails closed.** A secret reaching an agent is the one failure in the MCP
  design that cannot be undone: a bad send can be re-run and a bad write is in
  git, but a transcript is retained, replayed and sent to a model provider. So
  redaction is not per-field — a tool that masks the three fields it knows
  about is one new field away from a leak, added by whoever was not thinking
  about secrets — it walks the whole JSON tree, keys and numbers included
  (a digits-only secret arrives as a number, which is why `decode` uses
  `UseNumber`). It then **re-parses the bytes it is about to return and
  refuses if any secret survived**, returning `ErrSecretSurvived` *instead of*
  the bytes, so a caller that ignores the error emits nothing. That check does
  not trust the walk above it; the walk is the thing being checked. It works by
  masking a second time and treating any change as a survivor, which is how it
  checks for values this package is not allowed to hold — and
  `TestMaskingIsIdempotentWhichIsWhatVerifyRelies0n` pins the property it
  rests on.
- **The masker travels with the held response, and a missing one is refused.**
  `stored.mask` is set from the request's `*resolve.Resolved` at send time,
  because the response body is the one part of a send Otis cannot mask in
  advance: an endpoint that echoes a request header sends the credential back
  in its own body, and no care taken over the request prevents it. The window
  is shown that body verbatim — the person looking at it owns the credential —
  and an agent is not. `SendService.redactor` therefore **errors** rather than
  falling back to `mcp.NoSecrets()` when the masker is absent: that fallback is
  permissive, so forgetting to thread it through would redact nothing and tell
  nobody. It is unexported because it returns a value closing over secret
  strings, which must never join the binding surface.
- **The audit log has nowhere to put a payload, and that is a property of the
  type.** `mcp.Entry`'s fields are names, codes, times and numbers; there is
  none for a body, a header, a URL or a file's contents, and `target` is a
  node path precisely because a URL can carry a credential in a query
  parameter. `TestTheAuditEntryHasNowhereToPutAPayload` locks the json field
  set against docs/MCP.md §9's table, so widening what the log records is a
  decision made in a diff rather than incidentally while building a tool. It
  is `0600`, in the config directory, **never in the collection** — a log in
  the repository gets committed, and then everyone on the branch holds a
  record of which endpoints one person called.
- **`internal/git` is read-only, and "not a repository" is a normal state**,
  never an error: a collection is a directory of files and works perfectly
  well outside version control. It answers "what does git think" for the tree
  dots and the status bar and never writes.
- **`internal/diff` is the only place Otis writes to a repository**, and only
  the writes a review needs: the index, and a commit on the current branch. It
  does not push, pull, fetch, merge, rebase, cherry-pick or move HEAD — those
  are git's job and the terminal's. Discarding is the one operation in Otis
  that destroys work git cannot get back, so the confirmation is a *parameter*
  (`confirm bool`, refused with `diff.ErrNoConfirm`) and the method has a
  distinct name: a second caller must not reach it by picking the wrong method
  off the service, and the dialog in front of it is a courtesy on top, not the
  safety.
- **Every write to a collection goes inside `CollectionService.Guard()`**
  (`release := guard.Writing(path); defer release()`), or the watcher reports
  Otis' own save as an external change and the window re-walks on every
  keystroke it just persisted.
- **`.order` is never rewritten except by an explicit reorder**
  (docs/FORMAT.md §2.2). Adding a request must not touch it: the new file is
  unlisted, so it sorts alphabetically after the listed ones, and that is the
  whole mechanism. `internal/services/order.go` is the only writer and has no
  other callers, which is what makes that checkable rather than hoped for;
  `TestAddingARequestDoesNotTouchTheOrderFile` asserts the file is
  *byte*-identical, not equivalent, because a rewrite that produced the same
  order would still put the file in somebody's diff and would have lost their
  comments on the way. Switching a folder to alphabetical deletes the file —
  the absence of it *is* "this folder is alphabetical", so there is no second
  representation to keep in step.
- **An ordering change is undoable, and that is the only safety in front of
  it.** `OrderService.Undo` restores the previous *bytes* of every `.order`
  the change touched, so a hand-written file comes back as written rather than
  as Otis' rendering of the same order, and refuses when the file no longer
  holds what the reorder left (`ErrChangedOnDisk`). Because ⌘Z is there, none
  of these operations has a confirmation dialog — unlike `internal/diff`'s
  discard, which destroys work git cannot get back and takes `confirm bool` as
  a parameter.
- **The palette's typed query filters requests only.** Environment, Commands
  and Recent are a standing list that filters under its own prefix (`@`, `>`,
  `:`), which is what screen 2c draws and what the section asides say.
  Sections hold fixed positions with a per-section cap rather than re-ranking,
  so ↓↓↵ means the same thing between keystrokes and 400 request matches
  cannot bury the environments. **A standing row never takes the keyboard
  selection**: with a term typed, only rows that term filtered are selectable.
  Without that rule a request query matching nothing leaves ↵ on "switch
  environment", and typing a name that turns out not to exist silently changes
  where every send goes. Clicking one still works — a click is aim, a
  keystroke is not. DESIGN-NOTES §7.4 carries the reasoning.
- **A send writes the draft first, and that also lives in `send-context`.**
  Go resolves a send from the collection *on disk*, so a request with unsaved
  edits used to send its last saved version silently — the editor showing one
  request and Send running another. `persist` writes the draft before
  anything leaves, **before the confirmation** (which names a URL Go resolves
  from disk, so asking first would describe the version being replaced), and a
  failed save stops the send. A folder run does the same for the drafts under
  that folder and no others. `RunProvider` is inside `DocumentsProvider` for
  this reason; it used to sit above, where drafts are unreachable.
  DESIGN-NOTES §9.28.
- **The confirm-before-send gate lives in `send-context`, not in a caller.**
  An environment with `$otis.confirmBeforeSend` has to stop the Send button,
  the shell's ⌘↵, the palette's ⌘↵ and anything added later; a check in one
  caller is a gate the next caller forgets.

- **One binary, and `cli.WindowPath` is the only thing that decides which
  half of it runs.** With no arguments Otis opens the window; with arguments it
  is a CLI — except that a `.http` file double-clicked on Windows or Linux
  arrives as `argv[1]` and so looks exactly like a command line. The rule is
  one argument, not a flag, not the name of a command, and it exists on disk;
  every clause is load-bearing and every one has a test
  (`TestWindowPath`). Never inline a variant of this rule: a wrong answer
  either breaks the file association or turns `otis run` into a window.
  `WindowPathIn` is the same rule with an explicit working directory, which
  the single-instance forwarder needs because a relative path in a forwarded
  command line means the file beside the *sending* terminal.
- **`CollectionService.OpenPath` is the one entry point for a path that came
  from outside the window** — a file opened from the desktop, a path on the
  command line, a second launch's arguments, a file dropped on the window. All
  four mean "show me this" and all four must agree about which collection that
  is, which is why they share the method rather than each working out a
  directory themselves. It finds the root with `collection.FindRoot`, the same
  walk `otis run` uses (docs/FORMAT.md §8), so a request resolves against the
  same root from the desktop as from the terminal.
- **The window *pulls* the node to open; Go does not push it.**
  `TakePendingOpen` transfers it and clears as it reads. Wails raises its
  runtime-ready event before the React tree has mounted, so a target emitted
  then is silently lost — which is what happened the first time a `.http` file
  was double-clicked: the collection opened and the centre pane stayed empty.
  `events.OpenNode` is only a nudge for a window that is already up, and
  carries no payload for exactly that reason.
- **`appIdentifier` in `main.go` must not change.** It is both the macOS bundle
  identifier and the single-instance lock's ID, deliberately one string
  because both answer "which app is this". macOS keys the `.http` association
  and the user's default-application choice on it, so changing it reads as a
  different app and orphans the old registration. Nothing of the *user's* is
  keyed on it — settings and the key index live under
  `os.UserConfigDir()/otis`, keychain entries under
  `<collection>/<env>/<name>` — so the cost is a re-registration, but it is a
  cost with no upside once there are installs.
- **`build/appicon.svg` is the icon's source of truth**, `build/appicon.png` is
  its committed render, and every `.icns`/`.ico` is generated from the PNG and
  git-ignored. Its two colours are deliberately *not* the DESIGN-NOTES tokens
  (§9.18): an app icon is brand, not chrome. Do not "fix" them.
- **`build/linux/otis.desktop` and `build/linux/otis-http.xml` are committed,
  not generated.** `wails3 generate .desktop` writes a `MimeType` line only
  for custom URL *protocols*, so regenerating the desktop entry silently drops
  `MimeType=text/x-http;` — the one line that makes double-clicking a `.http`
  file reach Otis. `linux:generate:dotdesktop` asserts the line is there
  instead of producing the file. Note also that
  `wails3 task common:update:build-assets` **patches** generated files rather
  than rewriting them, so a stale key survives a config change that should
  have removed it; delete the file and re-run to clear one.
- **The `otis_cli` build tag must keep working, and CI asserts it.** With
  `gui.go` tagged out, the whole CLI compiles for darwin, linux and windows on
  both architectures with `CGO_ENABLED=0`. That is the documented
  `go install` path *and* the proof of the layering below: nothing outside
  `internal/services` and `gui.go` may depend on Wails. If a core package
  gains a Wails import, this is the check that fails.
- **`frontend/dist/.gitkeep` is tracked on purpose.** `gui.go` embeds
  `frontend/dist`, the bundle is generated and never committed, and a Go embed
  pattern matching no files is a compile error — so without the placeholder,
  a plain `go install` of the module fails to build. With it, the binary is a
  working CLI whose window declines to open and says why
  (`assetsPresent`). Do not remove it, and do not commit the bundle.
- **Build, sign and package are separate steps, and the sign steps are
  no-ops.** Otis ships unsigned and un-notarized by deliberate deferral
  (docs/RELEASING.md). `sign:darwin`, `sign:windows` and `sign:linux` exist so
  that adding signing later is an edit to one task rather than a rework of the
  release tasks. Do not fold signing into a build or package task.
- **On Windows the packaged app and the CLI are two links of the same source.**
  `otis.exe` in the installer is `-H windowsgui`, without which a console
  flashes behind every launch; such a process is never waited for by cmd.exe,
  so the exit code is lost and `otis run` cannot gate a CI step. The release
  therefore also ships a console-subsystem build (`windows:build:cli`), which
  is what goes on PATH and what `go install` produces.
  `cmd/otis/console_windows.go` makes the GUI binary print at all, and honours
  a redirect or a pipe over the console. This is the one place "the CLI is the
  same binary" needs an asterisk; docs/BUILDING.md §9 carries it.

- **`.order` stays untouched when something is created.** `RequestService.Create`
  and `FolderService.Create` are the two paths that add an entry to a folder,
  and neither may write `.order`: the new entry is unlisted, so it sorts
  alphabetically after the listed ones, and that is the whole mechanism
  (docs/FORMAT.md §2.2). `order.go` remains the only writer.
  `TestCreatingARequestOrFolderDoesNotTouchTheOrderFile` asserts the file is
  *byte*-identical through both.
- **A new folder always gets a `_folder.http`.** Git does not track an empty
  directory, so a folder created without a file in it vanishes on the next
  clone or checkout and the collection silently differs between two people.
  The importer already does this for the same reason (docs/FORMAT.md §7).
- **One implementation of the slug rules, in `internal/collection`.** `Slug`
  and `UniqueName` name a file for both the Postman importer and anything
  created in the app, because the same request arriving by either route has to
  land on the same file name. `frontend/src/lib/slug.ts` mirrors them for the
  create dialog's preview *only* — Go decides what is written, and the caller
  navigates to the path Go returns rather than to the preview, since only Go
  can see the collisions.

- **A folder's settings are edited in the folder view, never as a request.**
  `_folder.http` is settings and not a node in the tree (docs/FORMAT.md §2.1),
  so `/r/<folder>/_folder.http` cannot load — `RequestService` reports "not in
  the collection", which is exactly what the folder view's Edit and Add links
  used to do. `components/folder/settings-editor` is the editor: it changes
  `FolderDocument.Settings`, the parsed file `FolderService.Load` already
  returns for the purpose, and hands it back to `FolderService.Save`. Do not
  route the folder file to the request editor to fix a missing affordance.
  DESIGN-NOTES §9.20 has the whole account.

- **A layout that lives inside a resizable pane uses a container query, never
  a viewport breakpoint.** The folder view's `1fr 440px` split was `xl:`, a
  *viewport* rule, so on a 1512px window it split into two columns while the
  centre pane was 735px wide — leaving the left column ~275px and clipping its
  fields. It is `@container` plus `@min-[800px]:` now. Only the shadcn
  primitives in `components/ui/` may use `sm:`/`md:`/`lg:`, and only because a
  dialog is positioned against the viewport rather than inside a pane.

- **A Postman import plans before it writes, and lands in one of two places.**
  `internal/services/import.go` is the window's half of
  `internal/importer/postman`, and it exists as a service rather than a
  binding onto the importer because it holds a *plan* between the preview and
  the write — the importer's `Plan`/`Write` split is what makes the dialog
  possible. With no collection open an import becomes a new collection beside
  the export; with one open it becomes a folder **inside** it, which means an
  import is a write to somebody's collection and gets the same treatment as
  any other: inside `CollectionService.Guard()`, announced with `Refresh()`,
  and **the parent's `.order` untouched** so the new folder sorts
  alphabetically (a test asserts it byte for byte). A destination with files in
  it is refused rather than merged — `--force` is a CLI escape hatch for
  someone who typed a path, not a button — and Go re-checks the destination on
  every read of the plan, including inside `Commit`, because a directory can
  gain files while a dialog is open. DESIGN-NOTES §9.25.
- **`CollectionService.Open` emits `CollectionOpened` without the tree**, and
  returns the tree to its caller instead. That is enough for the window, which
  asked; it is not enough for anything in Go that opens a collection itself —
  the sidebar stays empty until something re-walks. Such a caller follows
  `Open` with `Refresh()`, which is the same "a write Otis makes announces
  itself" mechanism used everywhere else.
- **Leaving a collection goes through one guarded path.** Opening another
  collection or closing this one drops every open tab, and a draft lives only
  in the window, so both ask first when anything is unsaved
  (`collection-switch-dialog`). The macOS File menu therefore emits
  `events.OpenCollectionRequested` rather than calling `CollectionService.Open`
  itself — Go cannot know whether a draft would be lost, and opening from there
  made ⌘O a quieter way to lose work than the command palette's own entry. ⌘O
  is bound in `useKeymap` **only off macOS**, because a menu accelerator is
  handled before the key reaches the window and binding both would open two
  directory dialogs. The command palette carries "Open a collection…" and
  "Close this collection" because it is the one surface identical on all three
  platforms; without the second, screen 2b's start screen — and the
  recent-collections list on it — was unreachable once anything was open.
  DESIGN-NOTES §9.24.
- **An entry point that names a document needs a route for "there is not one
  yet".** Two have been found: the collection root below, and the environment
  editor — `/env/$name` needs a name, and a collection with no `env/*.json`
  has none, so "Edit environments" was hidden in the palette and went to `/`
  from the title strip, leaving no way to create the first one.
  `routes/env.index.tsx` serves `/env`, replacing itself with the first
  environment when there is one so callers need not know which case they are
  in. When adding a command that opens a named thing, check what it does
  before the first one exists. DESIGN-NOTES §9.27.
- **The collection root is a folder with an empty node path, and it has its
  own route.** An empty dynamic segment does not match `/f/$path` — the router
  answers Not Found — so `routes/f.index.tsx` serves the root and
  **`nodeLink` is the only place that knows it**. Build folder links with
  `nodeLink(kind, path)`, never `{ to: nodeRoute(kind), params: { path } }`:
  that form cannot express the root, and every site using it was a route to a
  404, including the palette's "Open the collection root" and the Auth tab's
  "Edit in the collection root". A root `_folder.http` is where a whole
  collection's auth lives and is what the Postman importer writes, so this was
  not a corner. Note also that a route's **id is not its path**: an index
  route's id keeps the trailing slash (`/f/`) and its navigable path does not
  (`/f`), so `FOLDER_ROOT_ROUTE` and `FOLDER_ROOT_ROUTE_ID` are both named.
  Anything reading `match.params.path` must default it to `""`, because the
  root route has no path param — two places assumed otherwise and one of them
  failed three frames away from the cause. DESIGN-NOTES §9.26.
- **A sidebar that replaces the request tree carries the way back.** The
  environment list and the changes list each swap the tree out, and neither
  screen in the design draws anything that returns — so with no document tab
  open there was no way back to the requests view at all. Both headers render
  `components/shell/back-to-requests`, which navigates to the *active
  document* when there is one so returning restores what you were looking at,
  and to the collection root otherwise. A third replaced navigator needs it in
  its header too, and nothing checks that for you: DESIGN-NOTES §9.23 records
  why it lives in the lists rather than in `sidebar.tsx`.
- **A tab whose file is gone closes itself, unless it is dirty.** The tree is
  the authority on what exists, so a request deleted in another editor or by a
  branch switch takes its tab with it — otherwise the tab cannot load, shows no
  method, and comes back on the next launch because the open paths are
  persisted. A *dirty* tab is kept on purpose: its edits live only in the
  draft, and with the file gone, saving is the only way to get them back and
  that needs the tab to still be there.

- **The tab strip spans everything right of the sidebar, and it can still
  overflow.** Two nested `ResizablePanelGroup`s in `AppShell` are what make
  that layout possible — the sidebar divides the outer one, the strip sits
  below that divide, the centre and response panes divide the inner one — which
  splits pane-size persistence over two layout callbacks that each see half the
  geometry. `AppShell`'s `geometry` ref holds the whole of it so neither
  callback overwrites the other's half; do not go back to reading the other
  half out of settings, because the save is debounced and two drags can land
  before it does. Because the strip overflows, `TabBar` scrolls the active tab
  into view — activation comes from four places and the scroll belongs to the
  bar, not to each caller. DESIGN-NOTES §9.19 records the whole deviation from
  screen 1a.

- **One keyboard handler.** `useKeymap` in `AppShell` owns every shortcut;
  components do not bind their own. A shortcut that needs the platform
  modifier fires wherever focus is; one that does not is suppressed in a text
  field, and nothing fires inside the command palette or a dialog. The single
  exception is CodeMirror, which handles keys before the window sees them: the
  request editor passes ⌘S and ⌘↵ to the editor as a CodeMirror keymap calling
  the same functions the shell's map calls.

## Conventions

- Go services: a struct with exported methods, constructed via `NewXxxService()`,
  registered in `main.go` with `application.NewService(...)`. Implement
  `ServiceStartup` if you need `*application.App`, via `application.Get()`.
- After changing any Go service signature, regenerate bindings
  (`wails3 generate bindings -ts -i -clean=true`, also run automatically by the
  build). Frontend imports them from `frontend/bindings/...`.
- Register custom events with `application.RegisterEvent[T](events.Name)` in
  `main.go` so the TS bindings get a typed payload.
- Frontend styling uses the theme tokens defined in `frontend/src/index.css`,
  which carries the DESIGN-NOTES token → Tailwind utility mapping at the top.
  Two names differ from the design's on purpose: the design's `--accent`
  (emerald) is shadcn's `primary`, and shadcn's own `accent` is the `--bg-control`
  hover surface. Sizes come from the `text-micro`…`text-display` scale, not from
  Tailwind's default `text-sm`/`text-base`.
- **`lib/platform` exports functions, not constants.** `System.IsMac()` reads
  `window._wails.environment`, which the Wails runtime fills in after the
  bundle may already have evaluated, so a value captured at import time is
  `false` for the life of the window whenever the race goes the wrong way —
  and that silently costs every ⌘ shortcut at once. The lookup is therefore
  deferred, cached only once the runtime answers, and falls back to the user
  agent until then.
- **A write Otis makes announces itself.** Every writer holds the write guard
  so the watcher does *not* report the save as an external change, which means
  the save has to tell the window itself: `CollectionService.Refresh()`
  re-walks, re-caches and emits `events.CollectionChanged`.
- `CollectionService` caches the last walk (`Loaded()`), so the services that
  need a node and its ancestors — the request editor, and the sender and
  variable index that follow — do not each re-walk the directory.
- **An open decision in DESIGN-NOTES §9 stays open in the UI too.** §9.5 has no
  on-disk form for a disabled *local* header, so the Headers tab renders that
  checkbox and disables it with a title saying why; `!inherit` is not
  borrowed for it, because §3.2 gives that value one meaning. The Params tab
  has no checkbox at all for the same reason, and the environment table takes
  the Headers tab's treatment — §9.5 names that table too.
- A node's collection-relative path travels in routes as a single dynamic
  segment; build links with `nodeLink`/`nodeRoute` from `@/lib/paths` and let the
  router do the percent-encoding.
- **The sidebar tree is virtualized, so a row must stay cheap.** No Radix
  component per row: the context menu is one instance for the whole tree, and
  the git dots use a plain `title` (which is what DESIGN-NOTES §6 specifies for
  them). Moving those off the row took a scroll step from 18ms to 1ms at 2,000
  requests. Tree flattening, the expand rule and the filter live in
  `@/lib/tree` as pure functions.
- `_folder.http` is settings, not a request (docs/FORMAT.md §2.1), so it is not
  a tree row: it hangs off its folder as `Node.Settings`. The tree the sidebar
  draws and the tree `otis ls` prints are the same tree, and a test asserts it.
- The build identity is injected at build time via `-ldflags -X` into
  `internal/buildinfo` — `Version`, `Commit` and `Date`, all three, from
  `VERSION`/`COMMIT`/`BUILD_DATE` in `Taskfile.yml`. Both halves of the binary
  read that package directly, so `otis --version` and the version the window
  shows cannot disagree. An unstamped build reports `dev` with
  `commit unknown`, which is the honest answer and is asserted by a test.
- The core packages must keep building with `CGO_ENABLED=0` (only `internal/services`
  and `main.go` may depend on Wails, which needs cgo on macOS).

## Commands

```bash
go test -race ./...     # everything; no test may touch the network
go vet ./...
npm --prefix frontend run typecheck        # tsc --noEmit
otis ls | otis run | otis import postman   # the CLI (see docs/FORMAT.md §8)
wails3 dev              # dev mode with HMR (Vite on 127.0.0.1:9245)
wails3 build            # production build → bin/otis
wails3 package          # the host platform's installers
wails3 build VERSION=x  # override the injected version

# Release artifacts, into dist/. See docs/BUILDING.md.
wails3 task release:darwin      # universal .app + DMG + tar.gz (macOS only)
wails3 task release:windows     # NSIS installer + console-CLI zip
wails3 task release:linux       # AppImage + deb + rpm + tar.gz (needs Linux
                                # for the AppImage; Docker for the binary)
wails3 task release:checksums   # SHA256SUMS over dist/
wails3 task setup:docker        # the wails-cross image, for Linux builds

# The command line alone: no cgo, no frontend, no platform toolkit.
go install -tags otis_cli github.com/otis-http/otis@latest
```

Releasing is a tag: bump `info.version` in `build/config.yml`, commit, then
`git push origin v0.2.0`. docs/RELEASING.md is the checklist and names the
secrets the deferred signing steps would need — do not add a credential to a
workflow without reading it.
