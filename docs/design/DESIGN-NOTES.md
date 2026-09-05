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

Built in Increment 16 as `frontend/src/lib/fuzzy.ts` plus
`components/shell/command-palette.tsx`. `Command` is not used: its filtering
returns a boolean per item, and the design needs the matched character
positions, so the matcher is ours and the list is a plain scroller. Three
things the design leaves open, decided there:

- **The typed query filters requests. It does not filter the other
  sections.** Screen 2c shows `local` and `prod` listed under ENVIRONMENT
  while `ord cre` is typed in the input, which only makes sense if those rows
  are a standing list — which is also what the section asides (`type @ to
  filter`, `type : to filter`) say. Under a prefix, that section is the only
  one and the term filters it.
- **Sections hold fixed positions with a per-section cap** (12 requests, 6
  environments, 8 commands, 6 recents), rather than re-ranking against the
  query. At 2,000 requests a re-ranking palette buries the environments under
  400 matches, and "requests, then environments, then recents" is a layout
  worth learning once; ↓↓↵ has to mean the same thing between keystrokes. A
  capped section says what it is hiding in its heading aside (`12 of 50`), and
  the footer counts total matches, not rendered rows.
- **A standing row never takes the keyboard selection.** With a term typed,
  only rows that term filtered are selectable; the listed environments and
  recents stay visible and clickable but the cursor skips them. Without this,
  a request query that matches nothing leaves ↵ sitting on "switch
  environment" — typing a name that turned out not to exist would silently
  change where every send goes. A click is aim and still works; a keystroke is
  not. When the searched section matches nothing it says so in its own place,
  so the standing sections below are not mistaken for results.

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

Built in Increment 17. `dnd-kit` was not used: the whole interaction is a
pointer position resolved against a list of fixed-height rows, the tree is
virtualized so the rows the library would attach to do not all exist, and the
ghost has to be ours to place (below). `lib/drag.ts` is the arithmetic —
"which row is the pointer over, and does that mean above it, below it, or
inside it" — as pure functions; the component is listeners and styling.

Three things the design leaves open, decided there:

- **The ghost trails the pointer, down and to the right** — 14px across, 12px
  down, clamped so a drag near an edge cannot push it off the window.

  It was *pinned outside the sidebar* at first, moving only vertically. The
  design review's finding was real — a preview under the cursor covers the
  rows the drop is aimed at, including the insertion line, which is the only
  thing saying where the row will land — but that was the wrong answer to it.
  A ghost that does not track the pointer horizontally reads as a rendering
  bug rather than a considered choice, and it was reported as one: "the
  dragged item is not positioned where the mouse pointer is, showing like
  100px to the right, almost aligned to the beginning of the center panel."

  The offset is the answer, and it is what every file manager does. Down and
  right leaves the cursor tip, the row under it and the boundary above it
  uncovered — the insertion line sits above the ghost's top edge and reads
  clearly — while the ghost still looks attached to the hand moving it.

  HTML5 drag-and-drop could do neither: the browser draws its drag image under
  the cursor and does not offer the choice, which is why the interaction is
  built on pointer events.
- **One indicator at a time.** Between two rows it is the accent top border;
  on a folder row's middle 40% it is an accent ring on that folder and *no*
  line, because the drop appends into it rather than landing at a position.
  Never both, and never two lines.
- **Dragging is off while the sidebar filter is on.** A filtered tree is a
  subset of the order, not the order, so a drop inside one would write a
  `.order` listing only what matched.

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

**9.6 Two request counts disagree in wording — resolved.** Screen 3a's folder
header says "5 requests · 1 subfolder" while the same screen's auth panel says
"inherited by 6 requests" and screen 4b says "changes apply to all 6
requests". Both are right — 5 direct children, 6 including
`fixtures/seed-order` — and the labels did not distinguish direct from
recursive.

The rule: **a count describing a folder's *contents* is direct; a count
describing the *reach* of its settings is recursive**, and the label says
which. So the header reads "2 requests · 1 subfolder · 3 below in all" —
naming both, because a folder whose settings reach further than its own
listing is the normal case and hiding that is what made the two numbers look
like a contradiction. An inheritance line ("inherited by N requests") is
always recursive, and subtracts the descendants that override, since counting
a request that opted out would be the same untruth in a different place.
`FolderCounts` carries both and names them `Requests`/`Subfolders` and
`Below`. Implemented in Increment 14.

**9.7 The folder "has shared settings" icon is a plus sign — and the collision
is now real.** In screen 3a the marker next to `auth` and `orders` is a `+`
glyph (`M6 1.5v9M1.5 6h9`), which reads as an add-item affordance sitting
exactly where an add button would go. The intended meaning is "this folder has
a `_folder.http`".

Creating requests and folders now exists (§9.21), so the tab strip has a `+`
that means **add** and a folder row has a `+` that means **has settings**, a
few hundred pixels apart. Nothing has been changed about the folder marker,
because resolving this is a design decision and §9 items are not resolved
silently — but it is no longer theoretical, and it is the first thing to settle
next time this document is opened. The obvious candidates: give the folder
marker a different glyph, or move it out of the row's trailing slot where an
add button would sit.

**9.8 Ordering of scripts versus resolution — resolved.**
Screen 4a shows an inherited header `Idempotency-Key: {{idemKey}}` annotated
"set by `orders/_pre.js`". That requires the pre-request script to run *before*
header and variable resolution, and to be able to write a variable that a
folder-level header then references. The current sender resolves variables
before preparing the request; the hook order needs defining.

Increment 14 made this observable rather than theoretical: running the
design's own `orders/` folder failed every request that did not override
`Idempotency-Key`, because the header referenced a value only the pre-request
script sets and nothing ran the script.

Increment 15 answers it. **Pre-request hooks run before `{{variable}}`
resolution** (`FORMAT.md` §9.2), so the design's arrangement works exactly as
drawn. The consequence, which the spec states plainly, is that a pre-request
hook sees the *template*: `request.url` and the header values still carry
their `{{...}}`. A hook that wants a resolved value calls `vars.get`, which
resolves — so a script and a `{{reference}}` never disagree about a name.

The scope the design called `folder` is `vars.session`, for the reason §9.4
gives: `_folder.http` declares committed variables, and `vars.folder.set`
reads as setting one of those. Screen 3a's Script API table is therefore one
row different from what shipped.

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

**9.15 Screen 3a's tabs and its right column say the same things.** The screen
draws Overview with the README on the left and all four panels — Auth,
Variables, Scripts, Headers — stacked on the right, *and* a tab for each of
those four. What the other tabs show is never drawn.

Resolved as: the right column is the folder's settings at a glance and is the
same on every tab; the left column is what the tab chooses. On Overview that
is the README, which is why there is no Docs tab — the documentation *is* the
overview. The duplication is deliberate: dropping the panels from the other
tabs would mean losing the glance while editing one of them, which is the
thing the screen is for.

On the other four the left column was that same panel again at full width, and
on three of them it is now the **editor** for what the panel shows (Auth,
Variables, Headers). At full width there is room to change the thing, which is
a better use of the column than showing it twice, and the panel opposite still
carries the glance. Scripts keeps the panel: a folder's scripts are files
beside it rather than lines in `_folder.http`, so there is nothing there for
this editor to write. §9.20 is why the editor had to exist at all.

The run's results have no design at all — screen 3a shows only "Run folder"
and a status-bar "Last run: 6/6 passed". They render as a fifth panel above
the others, with every row drawn as soon as the plan arrives and filled in as
each request finishes: a run that reveals itself one row at a time says
nothing about how far through it is.

**9.16 Screen 2a's centre pane is documentation, and is not built — resolved.**
The screen fills its centre with an explanation of the on-disk format ("How
the order is stored"), the diff of `orders/.order`, a `git diff --stat` block
("What a reviewer sees"), and a Folder options radio group. Three of those four
already exist elsewhere and the fourth is prose:

- The **diff** is the diff view (screen 1b). Reordering leaves `.order` in the
  changes list like any other write, and ⌘G is how you look at it. A second
  diff renderer that only ever shows one file would be the same view with less
  in it.
- **`git diff --stat`** is the same information as 1b's `+1 −1 · 1 hunk`
  footer, which is already there.
- **Folder options** is in the folder's own context menu instead. `.order` is
  not a shared setting: it does not live in `_folder.http`, it does not
  cascade, and a panel beside Auth and Headers would imply it did. The menu is
  also where the drag that writes it happens.
- The **explanation** is `docs/FORMAT.md` §2.2. A centre pane that teaches the
  format on the one screen where you are least likely to be reading is the
  wrong home for it; a `.order` that shows up in a diff and reads as a plain
  list of filenames is the design's actual argument, and that survives without
  the prose.

What the screen specifies that *is* built: the drag itself, the ghost, the
insertion line, the manual-order glyph on a folder row, and the confirmation
strip under the tree with `Undo ⌘Z` — the strip's two-line layout is the
design's, and it is also the only one that fits a 302px sidebar without
truncating the file name to `orders/.…`.

**9.17 What `Undo ⌘Z` reverts, and whether Alphabetical prompts — resolved.**
SCREENS.md flagged both. ⌘Z reverts the *write*: the previous bytes of every
`.order` the change touched, and the file back to its old folder for a move.
The previous bytes rather than a re-derivation of the previous order, so a
hand-written file with comments and a bare `create` line comes back as it was
written and not as Otis' rendering of the same order. The tree follows because
it is a view of the files.

It refuses when what it would overwrite is no longer what the reorder left —
something edited the file since, or a checkout moved it — because ⌘Z means
"take back what I just did", not "restore this to a state it has since left".
The stack is twenty deep and belongs to the open collection.

Switching to Alphabetical does **not** prompt. It deletes the file, and it is
undoable by the same ⌘Z; one mechanism for taking back an ordering change is
better than a dialog plus a mechanism. This is deliberately unlike Increment
13's discard, where the confirmation is a *parameter* — that destroys work git
cannot get back, and this does not.

**9.18 The app icon, and where the version appears — both resolved.** Neither
was in the design: `docs/design/` carries the nine screens and no icon at all,
and no screen has a version anywhere on it.

*The icon* is an elevator call button — a ring with an up and a down triangle
inside it, green on near-black — which is the pun the product name already
makes, and reads as request-and-response besides. `build/appicon.svg` is the
source of truth: a rounded rectangle (corner radius 0.221 of its width), a
stroked circle, and two filleted triangles, all as plain geometry so it stays
crisp at every size and can be edited by hand.

Its two colours are **not** the tokens in §2. The ground is `#1E2128` where
`--bg` is `#09090b`, and the mark is `#27BD62` where `--accent` is `#34d399`.
That is deliberate, not drift: an app icon is brand rather than chrome, it is
seen next to other applications' icons rather than next to Otis' own
surfaces, and the more saturated green holds up better in a Dock. Do not
"fix" these to the tokens — the divergence is the decision.

The one thing the redraw changed from the artwork it came from is the gap
between the triangles, opened from 51 to 62 on a 1024 grid. At 20px and 32px
the original merged them into a single diamond; at the wider gap they stay
two. At 16px they merge regardless, which is where the ring does the work.

There is no `.http` **document** icon yet — the association currently shows
the app icon, which is honest but wrong: a document icon is the mark on a
page, not a second copy of the app sitting in Finder. `docs/BUILDING.md` §4
says where it slots in.

*The version* appears in two places, because Otis ships no auto-updater and
so "which version am I on" is a question the user has to be able to answer
without one:

- The **empty state's footer** (screen 2b) reads `version · commit` after the
  existing line. That screen is where every launch without a collection
  lands, and it had room.
- **⌘P › `>` › "Copy version"** is the only route with a collection open. Its
  detail line shows the version, so the palette both answers the question and
  hands the answer over — and what it copies is longer than what it shows,
  naming the toolchain and platform too, because the complete form is for
  pasting into a bug report. Go writes the clipboard (`AppService.CopyVersion`),
  for the reason §9.12 gives about `CollectionService.CopyPath`.

Deliberately *not* in the status bar: its three slots are branch, file and
view context (§8.4), and a version is none of those and never changes. On
macOS the native About panel carries it as well, for free, from
`CFBundleShortVersionString`.

**9.19 The tab strip spans more than screen 1a draws it over — deliberate.**
Screen 1a puts the document tabs over the centre pane only, with the response
pane's `200 OK · 184 ms · 1.2 KB` status line level with them, and that is what
shipped. It does not survive real use: with a 260px sidebar and a 480px
response pane, seven tabs already overflow a strip that has less than half the
window to work with, while the width beside it sits empty.

So the strip now spans **everything right of the sidebar**, and the response
pane's status line sits below it rather than beside it. The argument for it is
not only space: the response pane shows the response *of the active tab's
request*, so it is part of that document, and the strip that names the document
should span the document. The sidebar is the exception because the tree is
collection-level — it does not belong to any one tab.

Two nested `ResizablePanelGroup`s are what make that possible: the sidebar
divides the outer one, the strip sits below that divide, and the centre and
response panes divide the inner one underneath it. The cost is that pane-size
persistence is split over two layout callbacks, neither of which sees the whole
geometry — `AppShell`'s `geometry` ref is what keeps them from overwriting each
other's half.

Three things follow from a strip that can still overflow:

- **Its scrollbar is hidden.** §5's 8px bar is right for a pane; inside a 34px
  strip it takes a quarter of the height and draws a second line under tabs
  that already have a border. The strip still scrolls.
- **Activating a tab scrolls it into view.** Without that, clicking a request
  in the tree activates a tab that is off-screen, and the click reads as having
  done nothing. Activation arrives from four places — the tree, the palette, a
  file opened from the desktop, and the tab itself — so it belongs in the tab
  bar, keyed on which tab is active, not in each caller.
- **A dirty tab keeps a close control.** Screen 1a draws the amber dot *in
  place of* the `×`, and taken literally that leaves the one tab you most want
  to think twice about as the only one the mouse cannot close. The dot is the
  resting state and hovering the tab swaps in the `×`, both in one fixed-size
  box so the tab's width never changes under the pointer. SCREENS.md already
  lists "tab close" among the interactions the static design cannot show.

Tab **reordering** is built, and borrows this document's existing drag
vocabulary rather than inventing one: the dragged tab dims to `--bg-inset` at
40% and a single accent line marks the edge it would land on, exactly as a
dragged tree row does (§7.7, screen 2a). Both live in one fixed-size box per
tab so the strip does not shift mid-drag. The order is the persisted
`tabs.open` array, so a tab dragged to the front stays there across a
relaunch.

Still not built from what §1a draws: the **`+` new-tab glyph** after the last
tab. It is not a missing affordance but a missing *feature* — there is no way
to create a request in Otis at all, in the UI or in Go, and building one means
deciding where the file lands, what it is named, and how that interacts with
§2.2's rule that adding a request must not touch `.order`. A `+` that opened a
dialog saying "not yet" would be worse than no `+`.

**9.20 A folder's settings had no editor at all, and now do.** The design
draws screen 3a's panels with an `Edit` on Auth and an `Add` on Variables, and
never draws what they open. What shipped sent both to
`/r/<folder>/_folder.http` — the request editor, addressing the folder file as
if it were a request. It is not: `_folder.http` is settings and deliberately
not a node in the tree (`FORMAT.md` §2.1), so the request editor answered
*"orders/_folder.http is not in the collection"* and the link was a dead end.
Folder-level auth could be read and never changed, which is worst for the
scheme that most belongs at that level — AWS SigV4 applies to a whole API, not
to one request.

So the three editable tabs edit it in place (§9.15), against
`FolderDocument.Settings` — the parsed file Go was already handing over for
this purpose — and hand it back to `FolderService.Save`. Go's serializer stays
the only thing that writes a `.http` file.

Three decisions the design does not cover:

- **An explicit Save, with Revert beside it**, rather than the request
  editor's dirty tab and ⌘S. A folder has no document tab to mark, the README
  in the same view already works this way, and what is being saved cascades to
  every request below — so the count of them sits under the form.
- **Auth is three choices, not a field**: inherit from above (the default,
  which writes nothing), declare one here, or `@auth none`. That is the
  request editor's shape one level up, and it is the only way to express
  "stop a parent's auth applying below" without inventing syntax.
- **A `_folder.http` that does not parse is not editable.** Saving would
  rewrite the file from a model missing whatever failed to parse, so the
  section says so and sends you to a text editor. §3.4 already says a broken
  folder file still opens; this is what "still opens" means here.

While wiring it up, the argument field turned out to have been **impossible to
type a space into** — the form derived its value with a trailing `.trim()`, so
`profile=dev ` came back as `profile=dev` and the next keystroke landed against
the previous word. `profile=dev region=eu-west-1` could only ever be pasted.
That bug was in the request editor's Override form too, since this form was
extracted from it.

**9.21 Creating a request or a folder, which the design half-draws.** Screen
1a puts a `+` after the last document tab and never says what it opens; §3
sizes "the `+` new-tab and `+` add glyphs" and stops there. Nothing else in the
design covers making a request, and until now nothing in the product did
either — there was no writer for a new file in Go and no affordance in the UI.

Three entry points, because the answer to "which folder does it go in?" is
different at each and the design gives none of them:

- **The tab strip's `+`** makes a request in the folder you are looking at: the
  active document's own folder, or the folder itself when a folder document is
  open. It is pinned to the right of the strip rather than scrolling away with
  the tabs, so with twenty documents open it is still where you left it.
- **A tree row's context menu** offers both, and the label names the
  destination — "New request in orders/…" — because aiming at a *request* row
  means creating beside it and aiming at a folder row means creating inside it,
  and a menu that did not say which would be a coin flip.
- **The palette** (`⌘K` › `>`) offers both, using the same folder as the `+`.

The dialog **shows the path it will write** as you type. This is §8.2's rule
("every write to disk is announced before it happens") applied to the one case
that most needs it: the file is named for the slug of what you type while the
typed name is kept as the `# @name` directive, so "Create order" becomes
`create-order.http` and still reads as "Create order" everywhere. A name that
silently becomes a different file name is exactly the surprise that rule
exists to prevent.

The preview is computed in the window and can be one character behind the
truth — Go resolves collisions against what is actually on disk and may answer
`create-order-2.http` — so the navigation follows the path Go returns, never
the preview. `lib/slug.ts` says so, and its rules are pinned to Go's.

A new folder gets a `_folder.http` with a comment and nothing else. Two
reasons: git does not track an empty directory, so without it the folder would
vanish on the next clone; and a new folder should inherit everything from above
and declare nothing, which is what an empty settings file expresses.

**9.13 "Set on this machine · Aug 28" has no source.** The secret detail panel
dates a stored secret. No OS keyring reports when an entry was written, and
Otis' key index deliberately holds nothing but keys — adding a date would be
the first exception to that, and the promise is worth more than the line. The
panel says whether a value is stored here, which is the half that changes what
you do next. If the design wants the date, the index is where it would have to
go, and §9's next reader should know that is the trade.

**9.22 The agent indicator, which the design does not draw.** `docs/MCP.md`
§11 needs a chip in the title strip saying that Otis' MCP server is on and
whether something is connected. The design has no such element, so its colour
and place were left to §9 (`MCP.md` §14.3) and are decided here.

**The title strip, right of the collection name, left of the environment
selector.** It belongs in the chrome rather than in a pane because it is a
property of the *window*, not of whatever is open in it — the same reason the
environment selector lives there. Right of the collection name, because the
two together read as the sentence that matters: which collection, and who else
can reach it.

**Amber (`--modified`, `#fbbf24`), and this is deliberately a fifth meaning for
it.** §9.3 flags amber as already carrying four — POST, git-modified, secret,
dirty-tab — and adding to that list needs a reason rather than a shrug. The
reason is that the other four are not colours in the title strip: no method
label, no git marker, no secret and no dirty dot appears in that region, so the
fifth meaning does not have to be told apart from the others in the same
glance. What amber *means* across all five is consistent, which is the part
that matters: something is in a state you should know about. It is not an error
and not a success.

Neither of the alternatives works. The accent (emerald) means "good" in this
design (§2.4), and an agent holding your credentials is not an achievement.
`#f87171` means destruction and would read as a fault, which an enabled server
is not — it would also be the fifth meaning of *that* colour, in a region where
"Discard changes…" already lives.

The states, which were never in doubt:

| | |
| --- | --- |
| Off | Nothing at all. A feature that is off should not occupy the chrome |
| Enabled, nothing connected | `agent · idle` in `--fg-dim`, no dot |
| Connected | `agent · <client>` with a live amber dot |
| A confirmation waiting | the chip counts it: `agent · 1 waiting` |

**The count is exact, like every other count in Otis** (§8.5). A chip reading
"agent · 2 waiting" and a popover listing three confirmations is worse than no
chip, because the number is the only reason to look.

Clicking it opens the popover: the three capability switches, the connected
client, the recent audit entries, and *Disconnect agents*. That control is the
kill switch (`MCP.md` §10), so it takes the destructive treatment — `#f87171`
text, no fill — and unlike §9.17's ordering operations it has no undo, because
its whole purpose is that there is no way back to the old token.

**9.23 Neither replaced sidebar had a way back, and now both do.** The
environment editor (screen 1c) and the diff view (screen 1b) each swap the
request tree out for their own navigator — that is what the design draws, and
it is right: there is no tree to filter while you are reading a diff or editing
`env/staging.json`. What neither screen draws is anything that returns, and
with no document tab open there was nothing in the window that did. ⌘K still
worked, and the tab strip works whenever a tab is open, but a keyboard shortcut
is not an affordance and an empty tab strip is not a hint. The report that
found this was "can't go back to the requests view from the environments view
or the git view".

**A chevron at the left of the navigator's own title**, sharing that row rather
than adding one. The heading stays where the design puts it, and the control is
the standard master-detail idiom, which is what a replaced navigator is.

**It goes to the active document, not to the root**, so returning restores what
you were looking at instead of dropping you on "Open a request from the
sidebar". With nothing open it does go to the root, which is where a fresh
launch lands anyway. The tooltip names the destination either way, because a
bare chevron is the one control where "back to *what*" is worth stating.

`components/shell/back-to-requests` is one component used by both lists rather
than two implementations, and the reason it is not in `sidebar.tsx` — which is
where the decision to replace the tree is made, and would be the tidier place —
is that both lists own their own header row. Injecting a control into a header
from outside would have meant either a second stacked header or plumbing an
element through both components. If a third navigator is ever added, it needs
this in its header too; there is no structural check for that, which is the
cost of the choice.

**9.24 Nothing reached the empty state, and two platforms could not switch
collections at all.** Screen 2b is the start screen — Open folder, the three
`soon` cards, and the recent-collections list — and `SCREENS.md` describes it
as the state with no collection open. It was reachable on first launch and
never again: `CollectionService.Close()` existed, `collection-context` exposed
`close()`, and **nothing in the window called it**. So the recents list, which
is the natural way to hop between two repositories, could not be got to once
anything was open.

Switching was worse. macOS had File › Open Collection… with ⌘O; Windows and
Linux keep Wails' default menu, which has no such item, and nothing in the
window offered one — so on two of three platforms there was no way to open a
different collection at all. The report was "how do I get to the empty screen?
or how do I switch to a different repo or folder with my collections?", which
is both halves of this.

**The command palette carries both**, because it is the one surface that exists
identically on all three platforms and already holds the other
collection-scoped commands — Reveal in Finder, Copy the collection path, Reload
from disk. "Open a collection…" and "Close this collection", the second hidden
when nothing is open. ⌘O is bound in `useKeymap` only off macOS, where the File
menu's accelerator wins before the key reaches the window; binding it in both
places would open two directory dialogs.

**The macOS menu item now emits `events.OpenCollectionRequested` instead of
opening a collection itself.** That is the part worth keeping: leaving a
collection closes every tab, and a draft lives only in the window, so the
window is the only place that knows whether anything would be lost. Opening
directly from Go bypassed the confirmation, which made ⌘O a quieter way to lose
work than the palette entry beside it. One guarded path now, whichever gesture
started it.

**The confirmation is `collection-switch-dialog`, styled as
`unsaved-changes-dialog` is**, because it is the same question about the same
kind of loss — a draft that is in no file yet. It has no **Save** option, which
that dialog does have: "save all" is not an operation Otis has, each document
is written against its own file, and a bulk write is not a thing to invent
behind a confirmation. Cancel is how you keep them.

Not built, and worth stating: the palette has no recent-*collections* section,
so hopping between two repositories is close → pick from the start screen
rather than one step. The recents are one keystroke further away than they
could be, and adding a fourth palette section is a bigger decision than this
fix.

**9.25 Where a Postman import lands, which the design does not say.** Screen
2b's third card is "Import from Postman · `⌘I` · collection.json → *.http" and
§9.9 pointedly does *not* list it with Clone and Start fresh as out of the A–E
plan — the converter has existed in `internal/importer/postman` all along, with
`otis import postman --out` driving it. What the design does not say is where
an import goes, and the CLI's answer is a required flag, which a card cannot
have.

**Two destinations, chosen by whether a collection is open:**

| | |
| --- | --- |
| Nothing open | a new folder beside the export, named for the collection — `~/Downloads/Acme API.postman_collection.json` becomes `~/Downloads/acme-api/`, and that becomes the open collection. This is the start screen's case: importing is how you *get* a collection |
| A collection open | a new folder **inside** it, so an export can be pulled into a collection you already have. `⌘I` and the palette's "Import from Postman…" reach this; the card cannot, because the card is only on screen when nothing is open |

The second is the one worth stating, because it makes an import a write to
somebody's existing collection. It behaves like anything else added to a
folder: `.order` is written into the new directory, which is fresh so there is
nothing to preserve (`FORMAT.md` §2.2), and **the parent's `.order` is not
touched**, so the imported folder sorts alphabetically like a new request does.
The write is held inside `CollectionService.Guard()` and announced with
`Refresh()`, exactly as every other write Otis makes.

**It plans before it writes, and shows the plan.** The importer already
separates `Plan` from `Write`; this is what that separation is for. The dialog
names the export, the collection, the counts, the destination as a path, and
what the conversion had to skip or flag — and only then offers a button. §8.2
asks every write to announce itself, and this is the largest write Otis makes:
a directory of files somebody then has to review.

**There is no "overwrite anyway".** A destination with files in it is refused
with what is in the way named, and the fix is to choose elsewhere. The CLI's
`--force` is for a person who typed a path and meant it, which is not the same
as a button next to a folder full of a colleague's work. Go re-checks the
destination on every read of the plan, including inside the commit, because a
directory can gain files while a dialog is open — so the disabled button is a
courtesy and the refusal is the safety.

Environment exports are not wired up yet. `postman.Options` takes `EnvFiles`
and the CLI's `--env` passes them, but the dialog offers no way to add one, so
an import through the window converts the collection and not the environments
beside it. The card's own description mentions "or environment export", so this
is a gap with a promise in front of it.

**9.26 The collection root's settings were unreachable, on both routes to
them.** A root `_folder.http` is where auth for a whole collection lives, and
it is exactly what the Postman importer writes when an export has
collection-level auth. It was not openable in Otis.

The cause is one line of routing. The root is a folder whose node path is the
**empty string** — `lib/tree.ts` says so plainly: "The root itself is not a
row; the collection's name is in the title strip" — and an empty dynamic
segment does not match `/f/$path`. `navigate({ to: "/f/$path", params: { path:
"" } })` produces `/f/`, and the router answers **Not Found**. Both documented
ways in went there:

- the command palette's "Open the collection root", which `SCREENS.md` lists
  as the way to reach it, and
- the Auth tab's **"Edit in the collection root"** button, which is the one a
  person actually finds — it is right there under the inherited directive on
  every request that inherits it.

So the root has its own route now (`routes/f.index.tsx`), and `nodeLink` is the
single place that knows the root is a different route rather than an empty
segment. Every folder navigation goes through it; the hand-built
`{ to: nodeRoute(kind), params: { path } }` form is gone from the call sites,
because that form cannot express the root and each site would have had to
remember.

Two things this turned up that are worth keeping:

**A route's id is not its path.** TanStack gives an index route the trailing
slash in its id (`/f/`) and drops it from the navigable path (`/f`). Anything
matching on `routeId` — the tab list, the status bar's document — needs the
first; anything navigating needs the second. `paths.ts` names both, because
finding that out costs an afternoon.

**`params.path` was assumed present in two places**, and the root route has no
path param at all. `use-route-document` and `tabs-context` each read
`(match.params as { path: string }).path`, which is `undefined` there — and the
second threw "Cannot read properties of undefined (reading 'lastIndexOf')" from
a path helper three frames down, which is a long way from the cause. Both
default to `""` now, which is the root's real node path.

The root is still not a tree row, and that stays as the design has it: the
collection's name is in the title strip, and the palette and the Auth tab are
the ways in. What was wrong was that neither worked.

**9.27 "Edit environments" led nowhere when there were none.** A collection
with no `env/*.json` had no way to make its first environment. The command
palette's "Edit environments" was `hidden` in exactly that case, and the title
strip's "Edit environments…" navigated to `/` — which reads, from the outside,
as a menu item that does nothing. The sidebar's `+` was the only affordance,
and the sidebar only shows the environment list on an `/env/*` route, which
neither caller could reach.

The shape is §9.26's, one route over: an entry point that assumes a *named*
document, in a state where there is not one yet. `/env/$name` needs a name, and
there is no name until somebody makes one.

**`/env` is now a route of its own** and is where both callers go. With
environments present it replaces itself with the first, so a caller does not
have to know which case it is in; with none it is the empty state that explains
what an environment is and offers **New environment**. The palette's command is
no longer hidden — that is precisely when somebody needs it — and its detail
line says "none yet — create the first" so the row is honest about what it will
do.

The reason to spell out what an environment *is* on that screen rather than
just offering the button: it is the one screen a person reaches while not
knowing whether they need one. The copy says a collection does not need an
environment and requests that name no variables work without it, which is true
and is the answer for most people who land there by curiosity.

`NewEnvironmentDialog` moved from private to exported for this, and that is the
whole change to the list: the dialog is the same one the `+` opens, so there is
one way to name an environment rather than two that could disagree about what
a valid name is.

**9.28 Sending a request with unsaved edits sent the last saved version.**
`SendService.Send` resolves the node from `collections.Loaded()` — the
collection **as parsed from disk** — and a draft lives only in the window until
⌘S. So a dirty request sent its previous contents, silently: the editor showed
one request and Send ran another, with nothing on screen saying so. It surfaced
as a 404 on a URL that was visibly correct in the editor, which is about the
worst way to find out.

**A send now writes the draft first.** Chosen over the alternatives — a
"Save & Send" button label, or refusing a dirty send — because the file *is*
the request in this product, so asking to send one is asking to send what is in
its file, and the only coherent way to do that is to put the edit there. It is
also what "Run" does in every editor that has one.

Three things follow, and they are the reason this is not simply an autosave:

- **It lives in `send-context`**, not in the Send button, for the reason the
  confirm-before-send gate does (`CLAUDE.md`): the button, the shell's ⌘↵, the
  palette's ⌘↵ and anything added later all have to be covered, and a check in
  one caller is a gate the next caller forgets.
- **It happens before the confirmation, not after.** §6's confirm-before-send
  dialog names the resolved URL, and Go resolves that from disk — so asking
  first would have described the version being replaced. The person is asked
  about what will actually go out.
- **A failed save stops the send.** Sending anyway would be the original bug
  with an error message in front of it.

A folder run does the same for the drafts **inside that folder**, and no
others: a run is not a reason to write a request it is not going to send. That
required moving `RunProvider` inside `DocumentsProvider` — it sat above, where
the drafts are not reachable. Its results still outlive whichever tab started a
run, which was the reason given for its old position: `DocumentsProvider` is
one provider for the collection, not one per tab. Nothing above consumed
`useRuns`; the folder view is its only reader.

The write is not announced in advance, which §8.2 would normally ask for. The
justification is that it is not a change to anything — it is the file catching
up with what is already on screen, and it announces itself the moment it lands:
the tab's dirty dot clears, the tree gains its git dot, the status bar says `M`
and `⌘G` shows the diff.

**9.29 Text selection had no colour of its own, and the rule that set it never
applied.** Two separate faults, reported together as "the selection colour is
not always visible, it looks like nothing is selected". The word *always* was
the clue.

**The rule.** `otis-theme` carried
`&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection`
with a comment claiming it covered the focused and unfocused cases. It did not
cover the focused one. `@codemirror/view`'s base theme has

```
&dark.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground
  { background: #233 }
```

which outranks a two-class selector, so **every selection in a focused editor
was CodeMirror's own dim teal** — and `#233` on `#0f0f11` reads as nothing at
all. Unfocused, the theme's own rule won and the colour changed. Mirroring the
base theme's selector shape wins it back: a theme outranks a base theme at
equal specificity. Worth remembering the next time a CodeMirror rule "does not
take" — the base theme is more specific than it looks.

**The colour.** The rule was reaching for `--bg-selected`, which §2.1 assigns
to the selected **row** in the tree, the changes list, the environment list and
the palette. At `#141417` on the editor's `#0f0f11` that is about five points a
channel, so even where the rule did apply it was close to invisible.

So `--bg-text-selection` is a new token, and neither of the two that looked
close would do. `--bg-selected` is a row token. `--accent-wash` is "the
background behind a `{{variable}}` token" — and the URL bar is made of those,
so a selected variable would have been indistinguishable from an unselected
one.

`rgba(255,255,255,.14)`, and **neutral on purpose**: every other colour in this
design means something, and a text selection means nothing — it is a UI state,
not a status, so it gets a plain lift that cannot be read as a variable, a diff
line or a result. Alpha rather than a flat value so the one token works on
`--bg`, `--bg-raised` and `--bg-inset` alike, and so syntax colours read
through it.

One token, everywhere. The editors set it themselves — CodeMirror draws
selection as a layer of its own rather than letting the browser paint it — but
most selectable text in Otis is *not* in an editor. A response body is plain
virtualized DOM, and so are the tree, the header tables and every piece of
prose. Those had no rule at all and took the browser's default, which is the
one selection colour the design never chose. A bare `::selection` in
`@layer base` covers them, and it sets the background only: leaving the text
colour alone is what lets a JSON body keep its syntax colours through a
selection, and what keeps the response body's line numbers — which are
`select-none` — out of a copy.

**A third fault, found because the URL field still showed nothing.** The
current-line highlight was painting over the selection. CodeMirror's
`highlightActiveLine` marks the line under **every** range's head whether or
not that range is empty, §4.3's treatment for that line is an opaque
`--bg-inset`, and the selection layer sits at `z-index: -2` — *behind* the
content. So an opaque line background covers the selection completely.

In the body editor that hid the selection on the cursor's own line and left it
on the others, which is why a three-line selection looked fine and a
within-one-line selection did not. In the URL field it hid it **always**: a
single-line editor's one line is permanently the active line, so selecting a
word there looked exactly like selecting nothing.

`highlightCurrentLine` replaces it and stands down whenever any range is
non-empty. That is also right on its own terms rather than only as a fix: the
current-line highlight exists to say where the cursor is, and when there is a
selection the selection says it better — two overlapping washes say it worse
than either alone. It stands down for a multiple selection's other cursors too,
which would otherwise paint over one end of it.

Worth keeping as a shape: three separate faults produced one symptom, and each
was invisible while the others stood. The colour was wrong, the rule that set
it never applied, and a fourth rule painted over the result.

**9.30 The URL field had a scrollbar.** A single-line CodeMirror scrolls
horizontally when the URL is longer than the field, and the theme declares 8px
webkit scrollbars for `.cm-scroller` — so a bar appeared inside the 30px
control of §4.4, taking a quarter of its height and making a text field look
like a pane. It is hidden now and still scrolls, the same treatment the tab
strip already gets (`.no-scrollbar`), declared against `.cm-scroller` because a
utility class cannot reach an element CodeMirror owns.

**9.31 macOS rewrote what the user typed, and the app had no opinion about
it.** Typing `qa3` into New environment produced `Qa3`: the window is a
WKWebView, and macOS' text substitutions apply inside one exactly as they do
in a note-taking app. So the file written was not the file the dialog had
previewed one line above, and no `{{...}}` written against `qa3` matched it.

The fix is an attribute set, not a fix to that dialog:
`autocapitalize="none" autocorrect="off" autocomplete="off" spellcheck=false`,
exported from `lib/text-input` as `verbatimText` and spread into every field
in the app. Every one of them holds a name, a value, a URL or a filter term —
things that mean what was typed — and the substitution reaches all of them
equally: a header name, an environment variable, a query parameter, the
palette's query.

The one field that does **not** take it is the commit message in the changes
list, which is prose and wants its spellchecker.

It is spread rather than defaulted inside `components/ui/input`, because half
the fields in the app are plain `<input>` elements inside table rows: a
default on the shadcn component would have covered the dialogs and quietly
missed the tables, which is the same shape of bug one layer down.

**9.32 Rename, Duplicate and Delete, which the design draws and Phase B could
not build.** The tree's context menu has carried the three of them, disabled,
since the menu existed: Otis did not write to a collection then. They work
now, and three decisions had to be made that the design does not cover.

**A rename changes both halves of a request's identity.** A request's name
lives in two places — the `# @name` directive and the file name — and this
changes both, because they are two views of one thing: a rename that moved
only one would leave a `place-order.http` reading "Create order" in the tree,
which nobody meant. It is also exactly what Create does with a typed name, so
the two are symmetric rather than each having their own rule. The dialog shows
both lines (`Renames orders/create-order.http → orders/place-order.http` /
`Sets # @name Place order`) rather than explaining the rule, which is §8.2's
form. A folder has no `@name`: its name is the directory's (FORMAT.md §2.1),
so there is one line to show.

**A duplicate names itself once.** "<name> copy", then "<name> copy 2", with
the file named for the slug of whichever it settled on — the label and the
file name are computed together, so they cannot drift. Naming the file with a
`-2` suffix while the label stayed "copy" would put two rows reading the same
thing in the tree, which is the one thing a duplicate most needs not to do.

**The delete dialog says whether git can bring it back.** That is the whole
difference between an inconvenience and a loss, and the tree already carries
each node's git status for its dot, so it costs nothing to say and would be
conspicuous to leave out. An untracked file gets "This file is not in git yet,
so this cannot be undone"; a tracked one gets the `git checkout --` line that
restores it. A folder gets "Anything in it that git has not seen is gone for
good", because the statuses inside it differ and a summary that averaged them
would be worse than the honest general case.

Note what this delete is *not*: `internal/diff`'s discard takes its
confirmation as a **parameter** (`confirm bool`), because it is one of four
things a row of buttons does and had to be unreachable by picking the wrong
method. `Delete` is not that — the method's name is the whole of what it does
— so here the dialog is the safety rather than a courtesy on top of one.

Both operations keep `.order` in step by editing the single line that named
the entry, which FORMAT.md §2.2 now specifies.

**9.33 The activity log, because a failure had nowhere to go.** The design
draws no toast and no console (§6 lists every overlay it uses and neither is
among them), so anything Otis tried and could not do failed in silence: a
clipboard write that refused, a file it could not reveal, a watcher that
stopped. `tree.tsx` said so in a comment above the helper that swallowed
them — "There is nowhere to show it yet".

It is a popover off the status bar's right edge, not a toast. A toast
interrupts and then vanishes, which is the wrong shape for this: these
failures are noticed *after* the fact, when something turns out not to have
worked, and what you want then is a list to go and look at. So the trigger is
the word `log` in `--fg-ghost` — quieter than anything else in the bar — and
it takes a count in `--destructive` when something has failed, which is the
only thing in the status bar that changes colour on its own. Opening it is
what marks the entries read.

The list lives in Go (`LogService`) so that the window's failures and Go's own
land in one place in one order, and so that a failure early enough to stop the
window rendering is still recorded. It is in memory and per-session: a log on
disk would be a second audit trail with none of the care `internal/mcp`'s has,
and nothing here is worth that.

**What it may hold is a property of the type.** `LogEntry` has a message, a
source and the underlying error, and nothing else — no URL, no header, no
body. Same reasoning as `mcp.Entry`'s: a resolved URL can carry a credential
in a query parameter, and a log is the one artefact that gets copied into a
bug report and pasted into a chat. Everything in it is text the window was
already shown, or text that would otherwise have gone to a stderr nobody has.
