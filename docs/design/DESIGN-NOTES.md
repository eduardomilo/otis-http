# Otis — design notes

The durable spec for the Otis UI. Every value here was read out of the design
document, not inferred. Where the design leaves something undefined or says
two things at once it is listed in [§9 Unresolved](#9-unresolved), not
silently decided.

**Source.** Claude Design project `5320e379-595b-407b-831f-87f80b73f614`
("Desktop API client design"), file `API Client.dc.html`. The project also
contains `support.js`, which is the generated canvas runtime (`dc-runtime`)
and carries no design information — it holds four hex values, none of them
part of this system. The design lives in the cloud; the PNGs in `screens/`
are the offline copy of record, rendered from that document at 2× (2880×1800
for a 1440×900 artboard).

Screen-by-screen content is in [SCREENS.md](SCREENS.md).

---

## 1. Fonts

Two families, loaded from Google Fonts.

| Role | Family | Weights used |
| --- | --- | --- |
| Sans (UI) | `Instrument Sans`, fallback `system-ui, sans-serif` | 400, 500, 600 |
| Mono | `IBM Plex Mono`, fallback `monospace` | 400, 500 |

The split is semantic, not decorative. **Mono means "this is a literal string
from the user's repo or the wire."** Sans means "this is Otis talking."

Mono is used for: file and folder paths, HTTP method labels, URLs, header
names and values, request and response bodies, variable references
(`{{apiKey}}`), branch names, environment names, JSON snippets, script
source, keyboard hints (`⌘P`, `⌘↵`), status codes, timings, sizes, and diff
text.

Sans is used for: labels, section headings, prose, button text, tab names,
badge text, and explanatory copy. Sans is applied explicitly *inside* mono
rows where a non-literal label appears mid-row — for example the `AUTH` tag
and the source annotation on an inherited header both re-declare
`Instrument Sans` inside a mono grid row.

Weight 600 appears exactly three times: the Send button. Weight 500 carries
every other emphasis (method labels, active tab names, selected rows,
headings). There is no bold text.

---

## 2. Color

Dark only. The design ships no light palette.

### 2.1 Background layers

Six surfaces, near-black, separated by 1–2 points of lightness. Depth is
communicated by borders, not by large jumps in value.

| Token | Hex | Use |
| --- | --- | --- |
| `--canvas` | `#070708` | The design page behind the artboards. **Not part of the app**; it is only visible as the rounded corners outside the window. |
| `--bg` | `#09090b` | App window background. Also the background of the *active* document tab. |
| `--bg-raised` | `#0c0c0e` | Command palette body, the selected auth card, the dashed session-variables box. |
| `--bg-inset` | `#0f0f11` | Anything recessed: text inputs, the method and URL fields, code blocks, the current line in the editor, row hover, diff hunk headers. |
| `--bg-selected` | `#141417` | The selected row in the tree, the changes list, the environment list, and the highlighted palette item. Also the palette row hover. |
| `--bg-control` | `#18181b` | Raised controls: buttons, the environment chip, segmented-control active segment, inline code in prose, the drag ghost, tree row hover. |

`#141417` does double duty as a hairline color; see §2.2.

### 2.2 Borders

| Token | Hex | Use |
| --- | --- | --- |
| `--border-hairline` | `#141417` | Row separators *inside* a table or list. The quietest line in the system. |
| `--border` | `#1f1f23` | Structural dividers: pane edges, title bar, tab bar, status bar, section separators. The most common border by far. |
| `--border-control` | `#27272a` | Around anything interactive or card-like: buttons, inputs, chips, badges, cards, code blocks, the scrollbar thumb. |
| `--border-strong` | `#3f3f46` | Raised or focused edges: the command palette outline, unselected radio and checkbox borders, macOS traffic lights. |
| `--border-danger` | `#3f1d1d` | The "Discard changes…" button only. |
| `--border-secret` | `#3a2f0e` | The `Secret · OS keychain` badge only. |

Dashed `1px dashed #27272a` marks two things, both meaning *"this is a
different kind of thing from what is above it"*: the divider before the
inherited-headers group, and the box around session variables.

### 2.3 Text levels

Six levels. Read them as a confidence ladder: the more the text matters right
now, the lighter it is.

| Token | Hex | Use |
| --- | --- | --- |
| `--fg-emphasis` | `#fafafa` | Selected row, active tab, headings, the value you are looking at. |
| `--fg` | `#e4e4e7` | Default body text inside the app; header keys; branch name. |
| `--fg-secondary` | `#d4d4d8` | Unselected tree rows, header values, prose. |
| `--fg-muted` | `#a1a1aa` | Folder names, labels, secondary metadata, inherited header keys. |
| `--fg-dim` | `#71717a` | Placeholders, table headings, units, punctuation in JSON, inherited values. |
| `--fg-faint` | `#52525b` | Hints, timestamps, source annotations, the `×` on a tab, disabled dots. |
| `--fg-ghost` | `#3f3f46` | Line numbers and the `···` row-menu affordance. Barely present until hovered. |

`#e5e5e5` appears once, as the design page's own body color. It is not an app
token.

### 2.4 Accent

| Token | Hex | Notes |
| --- | --- | --- |
| `--accent` | `#34d399` | Emerald. **Themeable** — the document exposes it as a prop with options `#34d399`, `#38bdf8`, `#fbbf24`, `#a78bfa`. See §9.1 for why those options are a problem. |
| `--accent-hover` | `#6ee7b7` | Send button hover, link hover. |
| `--accent-on` | `#052e1f` | Text *on* the accent (the Send button label). |
| `--accent-wash` | `rgba(52,211,153,.10)` | Background behind a `{{variable}}` token. |

The accent carries five jobs: the Send button, the selection edge, active tab
underlines, resolved `{{variable}}` tokens, and "good" states (200 OK,
`clean`, untracked-file dot, passing tests).

**Selection is drawn as an inset bar, never a fill alone.**

- Tree, palette, changes list, environment list: `inset 2px 0 0 var(--accent)`
  on the left edge, plus `--bg-selected`.
- Active document tab: `inset 0 -1px 0 var(--accent)` on the bottom edge, plus
  `--bg` (which is *lighter* than the inactive tabs' transparent-on-`--bg`
  strip only because the tab strip sits on the same color — the underline is
  what actually reads).
- Active sub-tab (Params / Headers / Body …): `border-bottom: 1px solid
  var(--accent)` with `margin-bottom: -1px` so it sits on the strip's own
  bottom border.

### 2.5 HTTP method colors

Defined once, as a map, and used identically in the tree, the document tabs,
the command palette, the method selector, and the drag ghost.

| Method | Hex | Tailwind-ish name |
| --- | --- | --- |
| `GET` | `#38bdf8` | sky-400 |
| `POST` | `#fbbf24` | amber-400 |
| `PUT` | `#a78bfa` | violet-400 |
| `PATCH` | `#f472b6` | pink-400 |
| `DELETE` | `#f87171` | red-400 |

`HEAD`, `OPTIONS`, `TRACE`, `CONNECT` and custom methods have **no defined
color** (§9.2).

Method labels are always: mono, 10px, weight 500, `letter-spacing: .02em`,
right-aligned in a fixed 48px gutter with `padding-right: 8px`.

### 2.6 Semantic colors

| Meaning | Foreground | Background | Where |
| --- | --- | --- | --- |
| Added | `--accent` `#34d399` (sign), `#d1fae5` (text) | `rgba(52,211,153,.08)` | Diff `+` lines |
| Removed | `#f87171` (sign), `#fecaca` (text) | `rgba(248,113,113,.08)` | Diff `−` lines |
| Diff context | `#a1a1aa` | transparent | Unchanged diff lines |
| Diff hunk header | `#71717a` | `#0f0f11` | `@@ … @@` lines |
| Modified (git `M`) | `#fbbf24` | — | Tree dot, changes list status, tab dirty dot, status-bar `M` |
| Untracked (git `U`) | `--accent` | — | Tree dot, changes list status |
| Clean | `--accent` | — | Status bar `clean` |
| Secret | `#fbbf24` | row `rgba(251,191,36,.03)`, badge `#1a1506` on `#3a2f0e` | Environment editor, keychain lock icon |
| Inherited | `#a1a1aa` key / `#71717a` value | transparent | Inherited header rows |
| Warning / attention | `#fbbf24` | — | Same amber as modified and secret (§9.3) |
| Danger | `#f87171` | `rgba(248,113,113,.08)` hover | Discard, Remove from keychain, prod environment dot |

Ahead/behind counts and inactive environment dots use `#52525b`.

### 2.7 JSON syntax colors

Used in both the request body editor and the response viewer.

| Token | Hex |
| --- | --- |
| Key | `#93c5fd` |
| String | `#d4d4d8` |
| Number | `#fbbf24` |
| Boolean / null | `#c084fc` |
| Punctuation, braces, colons | `#71717a` |
| `{{variable}}` | `--accent` on `--accent-wash` |

---

## 3. Type scale

Nine sizes. The system is dense: 11px and 12px carry almost everything.

| Size | Weight | Family | Use |
| --- | --- | --- | --- |
| 9px | 400 | sans | Micro tags: `AUTH`, `HOOK`, `LIB` |
| 10px | 500 | mono | Method labels, tab counts, keyboard hints, `js` marker |
| 10px | 400 | sans | Uppercase section labels (`THIS REQUEST`, `INHERITED`, `Requests`), source annotations |
| 11px | 400 | both | Status bar, metadata, secondary labels, explanatory copy, `+n`/`−n` counts |
| 12px | 400 | both | **The default.** Tree rows, header tables, buttons, tabs, body and response code, form fields |
| 13px | 400 | sans | Palette result names, documentation prose, artboard captions |
| 14px | 400 | mono | Command palette input |
| 15px | 500 | mono | Folder title (`orders/`) |
| 16px | 500 | sans | Documentation `h1`; also the `+` new-tab and `+` add glyphs |
| 20px | 500 | sans | Empty-state headline, `letter-spacing: -.01em` |

`letter-spacing` is used sparingly: `.02em` on method labels, `.06em` on
uppercase micro-labels, `.08em` on palette group headings, `-.01em` on the
one 20px headline, and `2px` on masked secret values (`••••`) to make the
dots read as a field rather than a word.

Line heights: `16px` for 11px copy, `18px` for 11–12px prose, `20px` for all
code and for 12–13px prose, `24px` for tree rows, `14px` for micro badges.

---

## 4. Spacing and metrics

### 4.1 Window and panes

| Element | Size |
| --- | --- |
| Artboard (window) | 1440 × 900 |
| Title bar | 38px tall |
| Traffic lights | 3 × 12px circles, 8px gap, 52px container |
| Sidebar | **260px**, fixed (`flex-shrink: 0`) |
| Response pane | **480px**, fixed (`flex-shrink: 0`) |
| Center pane | `flex: 1`, `min-width: 0` |
| Document tab bar | 34px tall |
| Response header bar | 34px tall |
| Status bar (bottom) | 26px tall |
| Sidebar git footer | 30px tall |
| Folder view split | `1fr 440px` |
| Diff / ordering split | `1fr 1fr` |

Pane padding is `0 16px` in the center and response panes, `0 10px` in the
sidebar. Section blocks in the folder view use `12px 16px`.

### 4.2 The method gutter

The single most repeated measurement in the design, and the reason the tree
reads as a column rather than a list:

```
width: 48px;  flex-shrink: 0;  text-align: right;  padding-right: 8px;
font: 500 10px 'IBM Plex Mono';  letter-spacing: .02em;
```

Identical in the sidebar tree, the folder-view tree, the drag tree, the
command palette results, and the palette's recent list. In the palette's
environment rows the same 48px slot holds a right-aligned 6px status dot, so
the dot lands on the same axis as the method text above it.

### 4.3 Rows

| Row | Height |
| --- | --- |
| Tree row (request, folder, script) | 24px |
| Environment list row, changes list row | 24px |
| Folder variable row | 24px |
| Session variable row | 22px |
| Header table row | 28px |
| Header table heading row | 26px |
| Environment variable row | 36px |
| Environment table heading row | 28px |
| Command palette row | 30px |
| Script API row | 20px |
| Code line (body, response, diff) | 20px line-height |

Tree indent is `10 + depth × 14` px on a leading spacer span, so depth 0 sits
at 10px and each level adds 14px.

### 4.4 Controls

| Control | Metrics |
| --- | --- |
| Filter input | 26px tall, `padding: 0 8px`, `--bg-inset` on `--border-control`, radius 4px |
| Method selector, URL field, Send | 30px tall, radius 4px |
| Send button | `padding: 0 14px`, accent fill, `#052e1f` text, weight 600 |
| Environment chip | 24px tall, `padding: 0 8px 0 10px`, `--bg-control` |
| Small button (Stage, Add variable, Run folder) | 24–26px tall, `padding: 0 10px` |
| Inline chip (Override, Off, Reveal, Replace) | `padding: 0–1px 5–6px`, 10–11px, radius 3px, `--border-control` |
| Segmented control (Pretty/Raw, Unified/Split, Preview/Edit) | `padding: 2–3px 6–8px`, radius 3px; active gets `--bg-control` + `--border-control`, inactive gets `transparent` border |
| Keyboard hint | mono 10px, `padding: 0 4px`, radius 3px, `--border-control`, `line-height: 16px` |
| Checkbox | 12px square, radius 3px, `1px solid #3f3f46`; checked fills with `--accent` and draws an 8px check stroked in `#09090b` |
| Radio | 12px circle; unselected `1px solid #3f3f46`, selected `1px solid var(--accent)` with a 6px accent dot |
| Status / git dot | 6px circle |

### 4.5 Tables

| Table | `grid-template-columns` |
| --- | --- |
| Request headers | `24px 190px 1fr 56px` |
| Environment variables | `28px 220px 1fr 120px 60px` |
| Folder variables | `120px 1fr` |
| Session variables | `110px 1fr auto` |
| Folder headers | `150px 1fr` |
| Inherit-card detail | `90px 1fr` |
| Folder auth detail | `80px 1fr` |
| Script API | `172px 1fr` |

### 4.6 Code gutters

| Context | Line number | Extra |
| --- | --- | --- |
| Request body editor | 44px, `padding-right: 14px` | — |
| Response viewer | 40px, `padding-right: 8px` | 14px fold column (`▾`/`▸`) |
| Diff | 44px + 44px (old, new), `padding-right: 10px`, divider `1px solid #1f1f23` after the second | 24px centered sign column |

Line numbers are `#3f3f46` and `user-select: none`.

### 4.7 Overlays

| Element | Metrics |
| --- | --- |
| Command palette | 640px wide, `top: 120px`, centered; 44px input, rows 30px, list `max-height: 420px`, 30px footer |
| Palette scrim | `rgba(9,9,11,.55)`, covers everything below the title bar |
| Dimmed background content | `opacity: .35` |
| Drag ghost | 216 × 24px, `--bg-control`, `1px solid var(--accent)`, radius 3px, `opacity: .92` |
| Empty state column | 720px wide, 28px gaps; starter cards in a 2-column grid, 10px gap, `padding: 14px 16px` |

---

## 5. Radii, elevation, scrollbars

**Radii.** Four values, applied by size of thing:

| Radius | Applies to |
| --- | --- |
| 6px | The window itself, the command palette |
| 4px | Buttons, inputs, cards, panels, the environment chip |
| 3px | Chips, badges, code blocks, variable tokens, checkboxes, segmented segments, the drag ghost |
| 50% | Dots, radios, traffic lights |
| 2px | Scrollbar thumb |

**Elevation.** The design is deliberately flat. There are exactly two drop
shadows in the entire document:

- Command palette: `0 16px 48px rgba(0,0,0,.6)`
- Drag ghost: `0 4px 12px rgba(0,0,0,.5)`

Everything else is separated by a 1px border and a 1–2 point background
shift. `box-shadow` elsewhere is always `inset` and always means *selection*,
never depth. **Do not add shadows to cards, panels, dropdowns or tabs.**

**Scrollbars.** 8px wide, thumb `#27272a`, radius 2px, no track. This is a
`::-webkit-scrollbar` treatment on natively scrolling elements.

---

## 6. Component mapping to shadcn/ui

The design was drawn as static HTML, not against a component library. This is
the mapping to build against, with the deviations called out.

| Design element | shadcn primitive | Notes |
| --- | --- | --- |
| Three-pane layout | `Resizable` (`ResizablePanelGroup`) | Design shows **fixed** 260 / flex / 480 with no handles drawn. See §7.1. |
| Sidebar tree | — | **No shadcn tree primitive.** Custom. `Collapsible` per folder is possible but fights virtualization. See §7.2. |
| Document tab bar | — | `Tabs` cannot do closeable, reorderable, overflowing document tabs. See §7.3. |
| Request sub-tabs (Params/Headers/Body/Auth/Scripts) | `Tabs` | Counts are `Badge` or a plain mono span. |
| Response sub-tabs (Body/Headers/Cookies/Tests) | `Tabs` | Same. Tests count takes the accent color when passing. |
| Folder tabs (Overview/Auth/Variables/Scripts/Headers) | `Tabs` | Same. |
| Command palette | `Command` in `CommandDialog` | `CommandGroup` per section, `CommandItem` per row. Footer and the `@`/`>`/`:` mode prefixes are custom. See §7.4. |
| Environment selector | `Select` | Custom `SelectTrigger` (dot + name + chevron). |
| Method selector | `Select` | Trigger text takes the method color. |
| Right-click on tree / tab | `ContextMenu` | Implied by the design, never drawn. |
| Row `···` menu | `DropdownMenu` | Trigger is `#3f3f46` text, not a button. |
| Header and variable tables | `Table` | Design uses CSS grid with fixed columns; `Table` is structurally fine, the grid template is what matters. |
| Enable/disable checkbox | `Checkbox` | Checked color is the accent, check stroke is `--bg`. |
| Auth mode selection | `RadioGroup` | Selected option expands to show detail; `RadioGroup` does not do that on its own. See §7.5. |
| Ordering mode (Manual / Alphabetical) | `RadioGroup` | Straightforward. |
| Pretty/Raw, Unified/Split, Preview/Edit | `ToggleGroup` (`type="single"`) | Active segment gets `--bg-control` + `--border-control`. |
| Scrollable panes | `ScrollArea` | Design uses native `overflow: auto` with a styled 8px webkit scrollbar. See §7.6. |
| Filter input | `Input` | With a trailing `⌘P` hint slot. |
| Commit message | `Textarea` | 56px tall in the design. |
| Buttons | `Button` | Send is a custom accent variant; Discard maps to `variant="outline"` recolored, not `destructive` (which fills). |
| `Discard changes…` | `AlertDialog` | The ellipsis implies a confirmation step. |
| Secret / status badges | `Badge` | `Secret · OS keychain` needs a custom amber variant. |
| Tooltips on git dots | `Tooltip` | The design sets `title="Modified"`. |
| Pane dividers | `Separator` | Most dividers are plain borders; use `Separator` only for standalone rules. |
| Keyboard hints | — | No shadcn `Kbd`. Custom span. |
| Drag to reorder | — | No primitive. Needs `dnd-kit` or equivalent. See §7.7. |
| Diff viewer | — | Custom. |
| Body / response code | — | Custom editor. See §7.8. |

---

## 7. Where the design exceeds the primitives

**7.1 Resizable panes are implied, not shown.** Sidebar (260px) and response
pane (480px) are `flex-shrink: 0` with no handles drawn anywhere in nine
screens. A desktop client this dense will want drag-to-resize, but the design
does not specify handle appearance, min/max widths, or whether widths persist.
Using `Resizable` means inventing the handle; using flex means shipping fixed
panes. Decide before building the shell.

**7.2 There is no shadcn tree.** The sidebar is the most-used surface in the
app and has no primitive behind it: rows carry a disclosure chevron, a 48px
method gutter, an ellipsised name, a git dot, drag handles, selection edges,
and three row kinds (folder, request, script). It must be built by hand, and
it should be virtualized — the design shows 16+ rows in a small tree and real
collections will be larger.

**7.3 Document tabs are not `Tabs`.** They close, they show a dirty dot in
place of the close `×`, they carry a method label, they overflow horizontally
(`overflow: hidden` on a 34px strip), and there is a `+` affordance. Radix
`Tabs` models one-of-N panel switching, not an editor tab bar. Build custom;
keep `Tabs` for the sub-tab strips, where it fits exactly.

**7.4 The palette has modes and a footer.** `Command` gives filtering,
grouping, and keyboard navigation, but not: the `@` / `>` / `:` prefix modes
advertised in the input's right rail, the persistent footer with four
shortcuts and a result count, per-row trailing `↵` hints, or the
character-level match highlighting the design shows (matched characters take
the accent at weight 500, not a background). All four are custom on top.

**7.5 The auth radio group expands.** The selected option renders a detail
panel inside its own card (type, token, what gets sent, an "Edit in orders/"
action), the unselected ones are single rows. `RadioGroup` handles state;
the expansion is conditional rendering, and the selected card also changes
border color to the accent and background to `--bg-raised`.

**7.6 `ScrollArea` changes the scrollbar.** The design specifies native
scrollbars styled through `::-webkit-scrollbar` (8px, `#27272a` thumb).
Radix `ScrollArea` replaces them with its own overlay scrollbars, which
behave differently on trackpads and need their own styling to match. Either
skip `ScrollArea` for the code panes or restyle it to these values.

**7.7 Drag-to-reorder is fully specified visually but has no primitive.** The
source row goes to `opacity: .4` on `--bg-inset` with `#52525b` text; the drop
target gets a `1px solid var(--accent)` top border; a 216×24 ghost follows the
cursor with a 6-dot grip glyph, the method label, and a drop shadow. Rows
reserve `1px solid transparent` top and bottom borders so the drop indicator
does not shift layout.

**7.8 The body and response panes are real editors.** The design renders them
as static colored spans. Shipping needs syntax highlighting, folding (the
response shows `▾`/`▸` and a `2 items` collapsed chip), a current-line
highlight (`--bg-inset`), and a cursor with `Ln/Col` reporting in the status
bar. Note the constraint from CLAUDE.md: this runs in a Wails webview from a
custom scheme, so a bundler-friendly editor without web workers loaded from
absolute paths is the safer choice.

---

## 8. Conventions worth preserving

These are patterns the design repeats deliberately. They are cheap to keep
and expensive to retrofit.

1. **Every effective value shows its source.** Inherited headers name the file
   they came from (`orders/_folder.http`) and, when a script set the value,
   the script too (`· set by orders/_pre.js`). Auth names its file. Variables
   name their storage. This is the provenance the resolver already computes.
2. **Every write to disk is announced before it happens.** "Override copies
   the header into this file"; "writes an `@auth` block into
   create-order.http"; "writes `@auth none`"; "Order saved to
   `orders/.order`"; "Auto-written on reorder". The UI tells you what the diff
   will look like.
3. **Secrets are always three things at once**: an amber lock icon, a masked
   value with `letter-spacing: 2px`, and a storage label saying `Keychain`.
   Never show a secret value without all three.
4. **The status bar always names the file and its git state.** Left: path and
   `M`/`clean`. Right: a one-line summary of the current view's context
   (`Inherits from orders/ · 1 level`, `Auth: inherited · orders/`,
   `Referenced by 23 requests`).
5. **Counts are everywhere and are always exact.** `7 sent · 4 local · 3
   inherited`, `inherited by 6 requests`, `4 of 23 requests`, `6 variables · 2
   secrets`, `+4 −2 · 2 hunks`. Budget for computing them.
6. **Uppercase micro-labels group table sections** (`THIS REQUEST`,
   `INHERITED`): 10px sans, `#71717a`, `letter-spacing: .06em`, with the
   relevant file path beside them in mono `#52525b`.

---

## 9. Unresolved

Read this section before making any of these decisions yourself.

**9.1 The themeable accent collides with the method colors.** The accent prop
offers `#34d399`, `#38bdf8`, `#fbbf24`, `#a78bfa` — and the last three *are*
the GET, POST and PUT colors exactly. Choosing sky makes every GET label look
like the selection edge; choosing amber makes POST, the modified-file marker,
the dirty-tab dot and the accent all the same color. Either the accent options
need to be disjoint from the method palette, or method colors need to shift
with the accent. The design does not say which.

**9.2 Four HTTP methods have no color.** The map covers GET, POST, PUT, PATCH
and DELETE. `HEAD`, `OPTIONS`, `TRACE`, `CONNECT` and custom methods (the
parser accepts any uppercase token) are undefined. The 48px gutter is also
sized for at most 6 characters at 10px mono; `OPTIONS` is 7 and would clip.

**9.3 Amber `#fbbf24` carries four unrelated meanings.** POST, git-modified,
secret, and dirty-tab. On the `create-order` tab in screen 1a all three of the
first, second and fourth appear in one row: an amber `POST` label, an amber
dirty dot, and an amber `M` in the status bar for the same file. Similarly
`#f87171` means DELETE, removed-line, production environment, and destructive
action. Both are legible in the mock because context separates them, but a
color legend or a second channel (icon, weight) may be needed.

**9.4 The session variable scope — resolved.** Screen 3a showed a **session**
scope ("set by scripts · this machine only", written by `vars.folder.set(k, v)`)
that `FORMAT.md` §4.2 did not have. It is now specified: `FORMAT.md` §4.5
defines the two scopes (folder and environment), where they sit in resolution
(§4.2, interleaved with the committed layer rather than stacked above it), that
the value is literal and never written anywhere, and that every one records the
request that set it and when. Implemented in Increment 11; the writer arrives
with the script engine.

**9.5 Disabled rows have no on-disk representation.** Both the request headers
table and the environment variables table put a checkbox on every row,
including local (non-inherited) ones. `FORMAT.md` defines no way to write a
disabled header into a `.http` file or a disabled key into an environment
JSON. Unchecking a local header currently has nowhere to be saved. (Unchecking
an *inherited* header is well defined — it writes `Header: !inherit`.)

Both tables therefore render the checkbox checked and **disabled**, with the
reason in its `title`, and offer removal in the row menu instead — an
operation the format does have. That keeps the decision visibly open rather
than resolving it by inventing syntax.

**9.6 Two request counts disagree in wording.** Screen 3a's folder header says
"5 requests · 1 subfolder" while the same screen's auth panel says "inherited
by 6 requests" and screen 4b says "changes apply to all 6 requests". Both are
arguably right — 5 direct children, 6 including `fixtures/seed-order` — but
the labels do not distinguish direct from recursive. Pick one and say which.

**9.7 The folder "has shared settings" icon is a plus sign.** In screen 3a the
marker next to `auth` and `orders` is a `+` glyph (`M6 1.5v9M1.5 6h9`), which
reads as an add-item affordance sitting exactly where an add button would go.
The intended meaning is "this folder has a `_folder.http`".

**9.8 Ordering of scripts versus resolution is asserted but not specified.**
Screen 4a shows an inherited header `Idempotency-Key: {{idemKey}}` annotated
"set by `orders/_pre.js`". That requires the pre-request script to run *before*
header and variable resolution, and to be able to write a variable that a
folder-level header then references. The current sender resolves variables
before preparing the request; the hook order needs defining.

**9.9 Two features in the empty state are not in any phase.** "Clone
repository" (git clone with credentials) and "Start fresh" (`mkdir` + `git
init`) appear as first-class entry points on screen 2b but are not in the
A–E plan.

**9.10 Minor: dead values in the source.** Two ternaries in the document
resolve to the same value on both branches — the `lib` folder color in 3a
(`active ? '#fafafa' : (lib ? '#a1a1aa' : '#a1a1aa')`) and the git flag lookup
in 1a — suggesting an abandoned intent to style `lib/` differently from other
folders. Screen 1a also sets `color: #52525b` on the response body line
container, which is inert because every inner span sets its own color. None of
these affect the rendered design; they are noted so a future reader does not
mistake them for intent.

**9.11 Screen 1c shows an open dropdown that is not drawn — resolved.** The
environment chip has an accent border and an up-chevron, indicating the menu is
open, but no menu is rendered, so the switcher's popover had no design. It is
now a `DropdownMenu` of radio items rather than the `Select` §6 maps the
selector to, for two reasons: the surface has to carry an action ("Edit
environments…") as well as a choice, and "no environment" is a real option that
Radix `Select` cannot represent, since its item values may not be empty. The
radio items keep the one-of-N semantics `Select` would have given. Implemented
in Increment 12.

**9.12 The `Reveal` chip has no implementation that is allowed — resolved as
Copy.** Screen 1c puts `Reveal` beside every masked secret. Revealing means
putting the value on screen, which means handing it to the webview, where it
lives in a React tree, a DOM node and any devtools session — and CLAUDE.md's
hard constraint is that a resolved secret value never crosses the binding, not
once, not for display. The chip therefore reads **Copy** and copies: Go reads
the keychain and writes the system clipboard, and the value never enters the
window's process at all. The user still gets at their own credential; it simply
never becomes pixels. The "Remove from keychain" confirmation says so, because
the value being unrecoverable-by-reading is the thing that makes removal
different from every other destructive action in the app.

**9.14 Screen 1b draws no per-hunk controls, and heads its two hunks two
different ways.** The diff view's file-level controls are drawn exactly —
`Stage`, a vertical rule, `Discard changes…` in red — but the per-hunk Stage
and Discard the view needs appear nowhere, so their placement was ours:
`Stage` is an inline chip at the right of the hunk header and Discard is in
the `···` overflow menu after it, marked destructive. Deliberately not
adjacent, and deliberately not hover-only — a control that appears only under
the pointer is one a keyboard user never finds.

The screen also heads its first hunk `@@ -1,9 +1,11 @@  POST {{baseUrl}}/v2/orders`
and its second `@@ tests @@` — offsets plus a label in one, label alone in the
other. Otis renders the label alone wherever it can derive one, since that is
the point of deriving it, and keeps the offsets in the row's `title` so nothing
is lost. A file the format has nothing to say about — an `.order` list, an
environment JSON, a README — keeps the offsets, as does a `.http` file that
does not parse: its line map cannot be trusted, and a confidently wrong header
is worse than an honest offset.

Split view is named in the segmented control and never drawn. It pairs a
removed line with the added line that replaced it, padding the shorter run,
which is what every split diff does.

**9.13 "Set on this machine · Aug 28" has no source.** The secret detail panel
dates a stored secret. No OS keyring reports when an entry was written, and
Otis' key index deliberately holds nothing but keys — adding a date would be
the first exception to that, and the promise is worth more than the line. The
panel says whether a value is stored here, which is the half that changes what
you do next. If the design wants the date, the index is where it would have to
go, and §9's next reader should know that is the trade.
