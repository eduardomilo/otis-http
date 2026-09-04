import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";

import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { buildLabel, useBuildInfo } from "@/hooks/use-build-info";
import { match, segments, type Segment } from "@/lib/fuzzy";
import { methodColor, methodGutter } from "@/lib/method";
import { nodeParentPath, nodeRoute } from "@/lib/paths";
import { hint } from "@/lib/platform";
import { formatDuration } from "@/lib/format";
import { relativeTime } from "@/lib/time";
import { cn } from "@/lib/utils";
import { useCollection } from "@/state/collection-context";
import { useDiff } from "@/state/diff-context";
import { useMCP } from "@/state/mcp-context";
import { useEnvironments } from "@/state/environment-context";
import { useSends } from "@/state/send-context";
import { useTabs } from "@/state/tabs-context";
import { AppService, CollectionService } from "@bindings/internal/services";
import type { Node } from "@bindings/internal/services";

/**
 * The command palette (screen 2c).
 *
 * # Sections at scale
 *
 * The sections hold **fixed positions with a per-section cap**, rather than
 * re-ranking against the query. Three reasons, in order of how much they
 * matter:
 *
 *  1. **Every section stays reachable.** A collection of 2,000 requests can
 *     match 400 of them. If sections re-ranked, reaching an environment would
 *     mean scrolling past every request that scored higher — the `@` prefix
 *     would stop being a shortcut and become the only way. With a cap, the
 *     Environment group is always within a screen of the top.
 *  2. **Keyboard navigation stays predictable.** ↓↓↵ has to mean the same
 *     thing from one keystroke to the next. Sections that reorder as you type
 *     make the third row a request one moment and an environment the next,
 *     and the palette is a keyboard surface before it is anything else.
 *  3. **The sections are kinds, not relevance buckets.** "Requests, then
 *     environments, then recents" is a layout somebody learns once. Ranking a
 *     *kind* by how well its best member scored is ranking the wrong thing.
 *
 * The cost of a cap is a hidden match, so the footer counts **total** matches
 * rather than rendered ones (which the design's "4 of 23 requests" already
 * asks for): a capped section says how much it is not showing, and `@`, `>`
 * or `:` lifts the cap by making that section the only one.
 *
 * # What the query filters
 *
 * The typed query filters **requests**. The other sections are listed as they
 * are, which is what their asides mean: "type @ to filter" is only sensible if
 * the plain query does not. Screen 2c is explicit about it — with `ord cre`
 * typed it still shows `local` and `prod` under Environment and two unrelated
 * rows under Recent, so those sections are a standing list you can reach
 * rather than a second set of search results competing with the first.
 *
 * Commands are the exception in the other direction: the design advertises the
 * mode in the input's rail and never draws the section, so commands appear in
 * `>` mode only. A palette that lists eight commands under every search would
 * be spending a third of its height on things nobody was looking for.
 *
 * # Why this is not shadcn's Command
 *
 * DESIGN-NOTES §7.4: `Command` gives filtering, grouping and keyboard
 * navigation but none of the `@`/`>`/`:` modes, the footer, the per-row `↵`
 * hint, or character-level match highlighting — "All four are custom on top."
 * Overriding its filter and its grouping to get section caps is more fighting
 * than owning the list, so the list is owned here: one flat array of rows, one
 * selected index, and the sections drawn as headings between them.
 */

/** Rows per section when no prefix has narrowed the palette to one. */
const CAPS = { request: 12, environment: 6, command: 8, recent: 6 } as const;

type Kind = keyof typeof CAPS;

/** What a row does when it is chosen. */
type Action = "open" | "send" | "reveal";

interface Row {
  kind: Kind;
  /** Stable across renders, for React keys. */
  id: string;
  /** The primary text and its matched character positions. */
  label: string;
  labelAt: number[];
  /** The secondary text — a URL, a description — and its matches. */
  detail: string;
  detailAt: number[];
  /** Right-aligned trailing text: a folder path, an @shortcut, a time. */
  trailing: string;
  /** A request's method, for the gutter. */
  method?: string;
  /** An environment's dot colour, or a recent's status colour. */
  accent?: "accent" | "danger" | "faint" | "modified";
  /** A status code on a recent row. */
  status?: number;
  /** How long the send took, already formatted. */
  elapsed?: string;
  /** What the row says about itself under the label, e.g. "confirms before send". */
  note?: string;
  /**
   * Whether ↵ can land here.
   *
   * A standing row — an environment or a recent, listed while the typed query
   * was filtering requests — is false. The keyboard never selects one, because
   * ↵ acts on wherever the cursor happens to be and a query that missed every
   * request must not leave the cursor sitting on "switch environment". Clicking
   * one still works: a click is aim, a keystroke is not.
   */
  pickable: boolean;
  run: (action: Action) => void;
}

const SECTIONS: { kind: Kind; heading: string; aside?: string }[] = [
  { kind: "request", heading: "Requests" },
  { kind: "environment", heading: "Environment", aside: "type @ to filter" },
  { kind: "command", heading: "Commands" },
  { kind: "recent", heading: "Recent", aside: "type : to filter" },
];

export function CommandPalette({
  open,
  onOpenChange,
  onReveal,
  onCreate,
  onLeaveCollection,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** ⇧↵: show where a request lives, without opening it. */
  onReveal: (path: string) => void;
  /** Opens the create dialog, in the folder the shell decides is current. */
  onCreate: (kind: "request" | "folder") => void;
  /** Opens another collection, or closes this one. Asks first if anything is unsaved. */
  onLeaveCollection: (action: "open" | "close") => void;
}) {
  const navigate = useNavigate();
  const { tree, collection } = useCollection();
  const { environments, active, activate } = useEnvironments();
  const { send, recents, clearSessionVars } = useSends();
  const { openTab } = useTabs();
  const { overview } = useDiff();
  const { status: agents, setEnabled: setAgentsEnabled, disconnect: disconnectAgents } = useMCP();
  const build = useBuildInfo();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  // Reopening starts fresh: a palette that remembers the last query is a
  // palette you have to clear before you can use it.
  useEffect(() => {
    if (open) {
      setQuery("");
      setSelected(0);
    }
  }, [open]);

  const close = useCallback(() => onOpenChange(false), [onOpenChange]);

  /**
   * Every request in the collection, flattened once per tree.
   *
   * Once per tree and not per keystroke: the walk and the string building are
   * the only parts that scale with the collection, and scoring two thousand
   * short strings is microseconds.
   */
  const candidates = useMemo(() => {
    if (!tree) return [];
    const out: { node: Node; folder: string }[] = [];
    walk(tree.root, (node) => {
      if (node.kind === "request" || node.kind === "broken") {
        out.push({ node, folder: nodeParentPath(node.path) });
      }
    });
    return out;
  }, [tree]);

  const commands = useMemo(
    () =>
      buildCommands({
        navigate,
        close,
        collection: collection?.path ?? "",
        hasChanges: (overview?.changes?.length ?? 0) > 0,
        firstEnvironment: environments[0]?.name ?? "",
        clearSessionVars,
        version: buildLabel(build),
        onCreate,
        onLeaveCollection,
        agentsEnabled: agents?.enabled ?? false,
        setAgentsEnabled,
        disconnectAgents,
      }),
    [
      navigate,
      close,
      collection?.path,
      overview?.changes?.length,
      environments,
      clearSessionVars,
      build,
      onCreate,
      onLeaveCollection,
      agents?.enabled,
      setAgentsEnabled,
      disconnectAgents,
    ],
  );

  // The prefix modes of the design's right rail. A mode makes its section the
  // only one, which is also what lifts its cap.
  const mode = modeOf(query);
  const term = mode ? query.slice(1) : query;

  const result = useMemo(() => {
    const built: Record<Kind, Row[]> = { request: [], environment: [], command: [], recent: [] };
    const totals: Record<Kind, number> = { request: 0, environment: 0, command: 0, recent: 0 };

    const want = (kind: Kind) => (mode ? mode === kind : kind !== "command");
    // The query filters requests; the other sections are a standing list and
    // filter only under their own prefix.
    const termFor = (kind: Kind) => (mode === kind || kind === "request" ? term : "");
    // A section the typed term did not filter is context, not a result, so it
    // holds no keyboard selection. With nothing typed everything is pickable:
    // the whole palette is then one list you arrow through.
    const pickable = (kind: Kind) => term === "" || termFor(kind) === term;

    if (want("request")) {
      const scored: { row: Row; score: number }[] = [];
      for (const { node, folder } of candidates) {
        const found = match(termFor("request"), [
          { text: node.name, weight: 2 },
          { text: node.url ?? "", weight: 1 },
        ]);
        if (!found) continue;
        scored.push({
          score: found.score,
          row: {
            kind: "request",
            pickable: pickable("request"),
            id: "r:" + node.path,
            label: node.name,
            labelAt: found.positions[0],
            detail: node.url ?? "",
            detailAt: found.positions[1],
            trailing: folder ? folder + "/" : "",
            method: node.method,
            run: (action) => {
              if (action === "reveal") {
                onReveal(node.path);
                close();
                return;
              }
              openTab(node.path, "request", { activate: true });
              void navigate({ to: nodeRoute("request"), params: { path: node.path } });
              close();
              if (action === "send") void send(node.path);
            },
          },
        });
      }
      scored.sort((a, b) => b.score - a.score || a.row.label.localeCompare(b.row.label));
      totals.request = scored.length;
      built.request = scored.slice(0, mode ? scored.length : CAPS.request).map((s) => s.row);
    }

    if (want("environment")) {
      const scored: { row: Row; score: number }[] = [];
      for (const env of environments) {
        const found = match(termFor("environment"), [
          { text: env.name, weight: 2 },
          { text: env.description ?? "", weight: 1 },
        ]);
        if (!found) continue;
        const isActive = env.name === active;
        scored.push({
          score: found.score,
          row: {
            kind: "environment",
            pickable: pickable("environment"),
            id: "e:" + env.name,
            label: env.name,
            labelAt: found.positions[0],
            detail: isActive ? "the active environment" : "switch environment",
            detailAt: [],
            trailing: "@" + env.name,
            accent: isActive ? "accent" : env.confirmBeforeSend ? "danger" : "faint",
            note: env.error
              ? "does not parse"
              : env.confirmBeforeSend
                ? "confirms before send"
                : undefined,
            run: () => {
              void activate(env.name);
              close();
            },
          },
        });
      }
      // Score-ordered only when `@` made them the search; otherwise the
      // collection's own order, so the list does not shuffle as you type a
      // request's name.
      if (mode === "environment") {
        scored.sort((a, b) => b.score - a.score || a.row.label.localeCompare(b.row.label));
      }
      totals.environment = scored.length;
      built.environment = scored.slice(0, mode ? scored.length : CAPS.environment).map((s) => s.row);
    }

    if (want("command")) {
      const scored: { row: Row; score: number }[] = [];
      for (const command of commands) {
        if (command.hidden) continue;
        const found = match(termFor("command"), [
          { text: command.name, weight: 2 },
          { text: command.detail ?? "", weight: 1 },
        ]);
        if (!found) continue;
        scored.push({
          score: found.score,
          row: {
            kind: "command",
            pickable: pickable("command"),
            id: "c:" + command.name,
            label: command.name,
            labelAt: found.positions[0],
            detail: command.detail ?? "",
            detailAt: found.positions[1],
            trailing: command.shortcut ?? "",
            run: () => {
              command.run();
            },
          },
        });
      }
      scored.sort((a, b) => b.score - a.score || a.row.label.localeCompare(b.row.label));
      totals.command = scored.length;
      built.command = scored.slice(0, mode ? scored.length : CAPS.command).map((s) => s.row);
    }

    if (want("recent")) {
      const scored: { row: Row; score: number }[] = [];
      for (const recent of recents) {
        const found = match(termFor("recent"), [
          { text: recent.name, weight: 2 },
          { text: recent.path, weight: 1 },
        ]);
        if (!found) continue;
        scored.push({
          // Recents are ordered by recency, not by score: "most recent" is
          // the whole reason the section exists. The score only filters.
          score: 0,
          row: {
            kind: "recent",
            pickable: pickable("recent"),
            id: "t:" + recent.path,
            label: recent.name,
            labelAt: found.positions[0],
            detail: recent.message,
            detailAt: [],
            trailing: relativeTime(recent.at),
            method: recent.method,
            status: recent.statusCode,
            elapsed: formatDuration(recent.durationMs),
            accent: recent.statusCode === 0 || recent.statusCode >= 400 ? "danger" : "accent",
            run: (action) => {
              openTab(recent.path, "request", { activate: true });
              void navigate({ to: nodeRoute("request"), params: { path: recent.path } });
              close();
              if (action === "send") void send(recent.path);
            },
          },
        });
      }
      totals.recent = scored.length;
      built.recent = scored.slice(0, mode ? scored.length : CAPS.recent).map((s) => s.row);
    }

    const rows = SECTIONS.flatMap((section) => built[section.kind]);
    return { built, totals, rows };
  }, [
    candidates, commands, environments, recents, term, mode, active,
    activate, close, navigate, openTab, send, onReveal,
  ]);

  // Clamp the selection whenever the results change under it, and never leave
  // it on a standing row: a query that stopped matching requests must move the
  // cursor off "switch environment", not park it there.
  useEffect(() => {
    setSelected((current) => {
      const rows = result.rows;
      if (rows[current]?.pickable) return current;
      const first = rows.findIndex((row) => row.pickable);
      return first < 0 ? -1 : first;
    });
  }, [result.rows]);

  // Keep the selected row on screen. The list scrolls; the selection moves
  // by keyboard, so the browser will not do this on its own.
  useEffect(() => {
    const element = listRef.current?.querySelector<HTMLElement>('[data-selected="true"]');
    element?.scrollIntoView({ block: "nearest" });
  }, [selected, result.rows.length]);

  function onKeyDown(event: React.KeyboardEvent) {
    const rows = result.rows;
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        setSelected((at) => step(rows, at, 1));
        return;
      case "ArrowUp":
        event.preventDefault();
        setSelected((at) => step(rows, at, -1));
        return;
      case "Home":
        event.preventDefault();
        setSelected(step(rows, -1, 1));
        return;
      case "End":
        event.preventDefault();
        setSelected(step(rows, rows.length, -1));
        return;
      case "Enter": {
        event.preventDefault();
        const row = rows[selected];
        // No pickable row means the query matched nothing. ↵ does nothing
        // rather than falling through to whatever is on screen.
        if (!row || !row.pickable) return;
        // ⌘↵ opens and sends; ⇧↵ reveals in the tree; ↵ opens.
        row.run(event.metaKey || event.ctrlKey ? "send" : event.shiftKey ? "reveal" : "open");
        return;
      }
    }
  }

  const footerCount = countLabel(mode, result.totals, result.rows.length, term);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        // sm:max-w-[640px] is not redundant: the shadcn default carries
        // sm:max-w-sm, and a responsive variant survives merging with an
        // unprefixed max-w, which would cap the palette at 384px.
        className="top-[120px] w-[640px] max-w-[640px] translate-y-0 gap-0 overflow-hidden rounded-lg! border-border-strong bg-raised p-0 shadow-[0_16px_48px_rgba(0,0,0,.6)] sm:max-w-[640px]"
        onKeyDown={onKeyDown}
      >
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <DialogDescription className="sr-only">
          Search requests, environments, commands and recent sends.
        </DialogDescription>

        <div className="flex h-11 items-center gap-2 border-b border-border px-4">
          <span className="font-mono text-field text-fg-faint">›</span>
          <input
            autoFocus
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setSelected(0);
            }}
            placeholder="Search requests, or @ > : to narrow"
            aria-label="Search"
            autoComplete="off"
            spellCheck={false}
            className="h-full min-w-0 flex-1 bg-transparent font-mono text-field text-fg-emphasis outline-none placeholder:text-fg-dim"
          />
          {/* The design's right rail: the three prefixes, the active one lit. */}
          <div className="flex shrink-0 items-center gap-3 text-meta">
            <ModeHint prefix="@" label="env" on={mode === "environment"} />
            <ModeHint prefix=">" label="commands" on={mode === "command"} />
            <ModeHint prefix=":" label="recent" on={mode === "recent"} />
          </div>
        </div>

        <div ref={listRef} className="max-h-[420px] min-h-[64px] overflow-auto py-2">
          {result.rows.length === 0 ? (
            <p className="px-4 py-6 text-center text-ui text-fg-dim">
              {emptyMessage(mode, term, candidates.length, recents.length)}
            </p>
          ) : (
            SECTIONS.map((section) => {
              const rows = result.built[section.kind];
              if (rows.length === 0) {
                // The section the term was filtering says so where it would
                // have been: with environments still listed below, an absent
                // Requests heading is not an answer.
                if (!missed(section.kind, mode, term)) return null;
                return (
                  <div key={section.kind}>
                    <Heading label={section.heading} />
                    <p className="px-4 py-2 text-ui text-fg-dim">
                      Nothing matches “{term}”.
                    </p>
                  </div>
                );
              }
              const total = result.totals[section.kind];
              return (
                <div key={section.kind}>
                  <Heading
                    label={section.heading}
                    aside={
                      total > rows.length
                        ? [`${rows.length} of ${total}`, section.aside].filter(Boolean).join(" · ")
                        : section.aside
                    }
                  />
                  {rows.map((row) => {
                    const at = result.rows.indexOf(row);
                    return (
                      <RowView
                        key={row.id}
                        row={row}
                        selected={at === selected}
                        // Hovering a standing row must not arm it either, or
                        // moving the mouse across the palette re-creates the
                        // problem the keyboard rule just fixed.
                        onHover={row.pickable ? () => setSelected(at) : undefined}
                        onChoose={(action) => row.run(action)}
                      />
                    );
                  })}
                </div>
              );
            })
          )}
        </div>

        <div className="flex h-[30px] shrink-0 items-center gap-4 border-t border-border px-4 font-mono text-label text-fg-faint">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>{hint("↵")} open &amp; send</span>
          <span>⇧↵ reveal in tree</span>
          <div className="flex-1" />
          <span>{footerCount}</span>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** One of the three prefix hints in the input's right rail. */
function ModeHint({ prefix, label, on }: { prefix: string; label: string; on: boolean }) {
  return (
    <span className={cn("whitespace-nowrap", on ? "text-primary" : "text-fg-faint")}>
      <span className="font-mono">{prefix}</span> {label}
    </span>
  );
}

/** §8.6's uppercase group heading, with its aside right-aligned. */
function Heading({ label, aside }: { label: string; aside?: string }) {
  return (
    <div className="flex items-baseline gap-2 px-4 pt-2 pb-1">
      <span className="text-label tracking-[.08em] text-fg-dim uppercase">{label}</span>
      <div className="flex-1" />
      {aside ? <span className="text-meta text-fg-faint">{aside}</span> : null}
    </div>
  );
}

/**
 * One result row: 30px, the 48px method gutter, the label and detail with
 * their matched characters lit, and the trailing text (DESIGN-NOTES §4.2,
 * §4.3, §7.4).
 */
function RowView({
  row,
  selected,
  onHover,
  onChoose,
}: {
  row: Row;
  selected: boolean;
  onHover?: () => void;
  onChoose: (action: Action) => void;
}) {
  return (
    <div
      role="option"
      aria-selected={selected}
      data-selected={selected}
      onMouseMove={onHover}
      onClick={(event) => onChoose(event.metaKey || event.ctrlKey ? "send" : "open")}
      className={cn(
        "flex h-[30px] cursor-default items-center gap-2 px-4",
        selected
          ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--accent)]"
          : "text-fg-secondary hover:bg-selected/60",
      )}
    >
      {/* The 48px slot: a method for a request, a dot for an environment, so
          both land on the same axis (DESIGN-NOTES §4.2). */}
      {row.kind === "environment" ? (
        <span className="flex w-[var(--method-gutter-width)] shrink-0 justify-end pr-2">
          <span className={cn("size-1.5 rounded-full", dotClass(row.accent))} />
        </span>
      ) : (
        <span className={cn(methodGutter, methodColor(row.method))} title={row.method}>
          {row.method}
        </span>
      )}

      <span className="shrink-0 font-mono text-result">
        <Highlighted segments={segments(row.label, row.labelAt)} />
      </span>

      {row.status !== undefined && row.status > 0 ? (
        <span className={cn("shrink-0 font-mono text-meta", textClass(row.accent))}>
          {row.status}
        </span>
      ) : null}

      {/* How long it took, beside how it went: a recent row answers "did that
          work, and was it slow" before it answers "when". */}
      {row.elapsed !== undefined ? (
        <span className="shrink-0 font-mono text-meta text-fg-faint">{row.elapsed}</span>
      ) : null}

      {row.detail ? (
        <span className="min-w-0 truncate font-mono text-meta text-fg-dim">
          <Highlighted segments={segments(row.detail, row.detailAt)} />
        </span>
      ) : null}

      {row.note ? (
        <span className={cn("shrink-0 text-meta", textClass(row.accent))}>· {row.note}</span>
      ) : null}

      <div className="flex-1" />

      {row.trailing ? (
        <span className="shrink-0 truncate font-mono text-meta text-fg-faint">{row.trailing}</span>
      ) : null}

      {/* The design puts an ↵ chip on the selected row only. */}
      {selected ? (
        <span className="shrink-0 rounded-sm border border-border-control px-1 font-mono text-label text-fg-faint">
          ↵
        </span>
      ) : null}
    </div>
  );
}

/**
 * Matched characters take the accent at weight 500 with no background, which
 * is what §7.4 specifies and why this is not a `<mark>`.
 */
function Highlighted({ segments }: { segments: Segment[] }) {
  return (
    <>
      {segments.map((segment, at) =>
        segment.matched ? (
          <span key={at} className="font-medium text-primary">
            {segment.text}
          </span>
        ) : (
          <span key={at}>{segment.text}</span>
        ),
      )}
    </>
  );
}

function dotClass(accent: Row["accent"]): string {
  switch (accent) {
    case "accent":
      return "bg-primary";
    case "danger":
      return "bg-destructive";
    case "modified":
      return "bg-modified";
    default:
      return "bg-fg-faint";
  }
}

function textClass(accent: Row["accent"]): string {
  switch (accent) {
    case "accent":
      return "text-primary";
    case "danger":
      return "text-destructive";
    case "modified":
      return "text-modified";
    default:
      return "text-fg-faint";
  }
}

/** The prefix modes of the design's right rail. */
function modeOf(query: string): Kind | null {
  switch (query[0]) {
    case "@":
      return "environment";
    case ">":
      return "command";
    case ":":
      return "recent";
    default:
      return null;
  }
}

/**
 * The footer's count.
 *
 * Total matches, not rendered ones: a capped section has to say how much it is
 * not showing, or the number is a lie about a list that has been trimmed.
 */
function countLabel(
  mode: Kind | null,
  totals: Record<Kind, number>,
  rendered: number,
  term: string,
): string {
  if (mode) {
    const total = totals[mode];
    const noun = mode === "environment" ? "environments" : mode === "command" ? "commands" : mode === "recent" ? "recent" : "requests";
    return `${total} ${noun}`;
  }
  const total = totals.request;
  const shown = Math.min(total, CAPS.request);
  // With a term typed the count is about requests even when none matched: the
  // environments below are listed, not found, and counting them would report
  // four results for a query that found nothing.
  if (total === 0 && term !== "") return "no matches";
  if (total === 0) return `${rendered} results`;
  return `${shown} of ${total} requests`;
}

/**
 * What the list says when it has nothing.
 *
 * Mode-aware, because "nothing matches" is only true when something was
 * typed: `:` on a session with no sends yet has an empty term, and telling
 * somebody their empty query matched nothing would be blaming them for a list
 * that was never going to have rows.
 */
function emptyMessage(mode: Kind | null, term: string, requests: number, recents: number): string {
  if (term !== "") return `Nothing matches “${term}”.`;
  switch (mode) {
    case "recent":
      return recents === 0
        ? "Nothing sent yet this session. Recents are forgotten when the window closes."
        : "No recent sends match.";
    case "environment":
      return "This collection has no environments. A collection does not need one.";
    case "command":
      return "No commands are available here.";
    default:
      return requests === 0 ? "This collection has no requests yet." : "Nothing to show.";
  }
}

/**
 * Whether a section is the one the typed term was filtering and found nothing.
 */
function missed(kind: Kind, mode: Kind | null, term: string): boolean {
  return term !== "" && (mode ? mode === kind : kind === "request");
}

/**
 * The next pickable row from `at`, wrapping, or -1 when there is none.
 *
 * One pass over the list: a palette whose rows are all standing must stop
 * rather than spin.
 */
function step(rows: readonly Row[], at: number, by: 1 | -1): number {
  if (rows.length === 0) return -1;
  for (let i = 1; i <= rows.length; i++) {
    const next = (((at + by * i) % rows.length) + rows.length) % rows.length;
    if (rows[next].pickable) return next;
  }
  return -1;
}

/** Depth-first over the tree, in display order. */
function walk(node: Node, fn: (node: Node) => void) {
  fn(node);
  for (const child of node.children ?? []) walk(child, fn);
}

/** One entry in the `>` mode. */
interface CommandEntry {
  name: string;
  detail?: string;
  shortcut?: string;
  hidden?: boolean;
  run: () => void;
}

/**
 * The `>` commands.
 *
 * The design advertises the mode and never draws it, so the list is ours. It
 * is deliberately short and every entry does something that already exists —
 * a palette full of commands that open a dialog saying "not yet" is worse than
 * one with eight that work.
 */
function buildCommands(context: {
  navigate: ReturnType<typeof useNavigate>;
  close: () => void;
  collection: string;
  hasChanges: boolean;
  firstEnvironment: string;
  clearSessionVars: () => Promise<void>;
  version: string;
  onCreate: (kind: "request" | "folder") => void;
  onLeaveCollection: (action: "open" | "close") => void;
  agentsEnabled: boolean;
  setAgentsEnabled: (on: boolean) => Promise<void>;
  disconnectAgents: () => Promise<void>;
}): CommandEntry[] {
  const {
    navigate,
    close,
    collection,
    hasChanges,
    firstEnvironment,
    clearSessionVars,
    version,
    onCreate,
    onLeaveCollection,
    agentsEnabled,
    setAgentsEnabled,
    disconnectAgents,
  } = context;
  const go = (run: () => void) => () => {
    run();
    close();
  };
  return [
    {
      // First, because it is the only command that makes something rather
      // than navigating somewhere.
      name: "New request",
      detail: "in the folder you are looking at",
      hidden: collection === "",
      run: go(() => onCreate("request")),
    },
    {
      name: "New folder",
      detail: "in the folder you are looking at",
      hidden: collection === "",
      run: go(() => onCreate("folder")),
    },
    {
      name: "Show changes",
      detail: hasChanges ? "the git diff view" : "the git diff view — nothing has changed",
      shortcut: hint("G"),
      run: go(() => void navigate({ to: "/diff" })),
    },
    {
      name: "Open the collection root",
      detail: "its shared settings, documentation and scripts",
      run: go(() => void navigate({ to: nodeRoute("folder"), params: { path: "" } })),
    },
    {
      name: "Edit environments",
      detail: "variables and secrets",
      hidden: firstEnvironment === "",
      run: go(() => void navigate({ to: "/env/$name", params: { name: firstEnvironment } })),
    },
    {
      // How you get to another collection at all. macOS has File › Open
      // Collection… and Windows and Linux have no menu item for it, so
      // without this there is no way to switch on two of three platforms.
      name: "Open a collection…",
      detail: "choose a folder of .http files",
      shortcut: hint("O"),
      run: () => {
        close();
        onLeaveCollection("open");
      },
    },
    {
      // The only way to reach the empty state once something is open — and
      // the empty state is where the recent-collections list lives, so this
      // is also how you get back to one you had open before.
      name: "Close this collection",
      detail: "returns to the start screen, with your recent collections",
      hidden: collection === "",
      run: () => {
        close();
        onLeaveCollection("close");
      },
    },
    {
      name: "Reveal the collection in Finder",
      detail: collection,
      hidden: collection === "",
      run: go(() => void CollectionService.Reveal("")),
    },
    {
      name: "Copy the collection path",
      detail: collection,
      hidden: collection === "",
      run: go(() => void CollectionService.CopyPath("")),
    },
    {
      name: "Reload the collection from disk",
      detail: "re-walks the directory and refreshes git",
      run: go(() => void CollectionService.Refresh()),
    },
    {
      name: "Clear session variables",
      detail: "the values runs set, on this machine only",
      run: go(() => void clearSessionVars()),
    },
    {
      // Turning the server on is a deliberate act and the chip is not drawn
      // until it is on (DESIGN-NOTES §9.22), so this is the only way in. It
      // grants no capability: those are three more switches, in the popover.
      name: agentsEnabled ? "Turn the agent server off" : "Let an agent drive this collection",
      detail: agentsEnabled
        ? "closes the listener and turns every capability off"
        : "starts a local MCP server — nothing is allowed until you say so",
      run: go(() => void setAgentsEnabled(!agentsEnabled)),
    },
    {
      // §10's kill switch, reachable without going through the chip: if
      // something is going wrong, the fastest thing to hand should stop it.
      name: "Disconnect agents",
      detail: "revokes the token, cancels anything in flight, turns all three off",
      hidden: !agentsEnabled,
      run: go(() => void disconnectAgents()),
    },
    {
      // The one place the version is reachable with a collection open. Its
      // detail line shows the version, so ⌘P › ">" both answers "which build
      // is this" and hands it over for a bug report — which is the whole
      // mechanism, since Otis ships no updater (DESIGN-NOTES §9.18).
      //
      // Go writes the clipboard, and what it writes is longer than what is
      // shown here: it names the toolchain and platform too. CopyVersion has
      // the argument.
      name: "Copy version",
      detail: version === "" ? "the running build" : version,
      hidden: version === "",
      run: go(() => void AppService.CopyVersion()),
    },
  ];
}
