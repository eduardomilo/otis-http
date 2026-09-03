import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronRight, Plus, TriangleAlert } from "lucide-react";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { methodColor, methodGutter } from "@/lib/method";
import { nodeRoute } from "@/lib/paths";
import { fileManagerName } from "@/lib/platform";
import { expandTo, findNode, flatten, indentOf, isExpanded, type Expansion, type Row } from "@/lib/tree";
import { cn } from "@/lib/utils";
import type { Node, Tree as CollectionTree } from "@bindings/internal/services";
import { CollectionService } from "@bindings/internal/services";
import { useTabs } from "@/state/tabs-context";

/**
 * The request tree (screen 1a).
 *
 * There is no shadcn tree primitive (DESIGN-NOTES §7.2) and the sidebar is the
 * most-used surface in the app, so this is built by hand and virtualized: real
 * collections run to thousands of requests and every row is a fixed 24px,
 * which is exactly the case a virtualizer is for.
 *
 * Two things keep scrolling cheap at that size. There is one context menu for
 * the whole tree rather than one per row — sixty Radix menus re-rendering on
 * every scroll frame is the difference between smooth and not — and the git
 * dots use a plain `title`, which is what the design specifies for them
 * (DESIGN-NOTES §6) and costs nothing.
 */

const ROW_HEIGHT = 24;
const NODE_PATH_ATTRIBUTE = "data-node-path";

/** What the shell can ask the tree to do. */
export interface TreeHandle {
  /** Opens the ancestors of a path, scrolls to it and marks it. */
  reveal: (path: string) => void;
}

export function Tree({
  tree,
  filter,
  activePath,
  revealRef,
}: {
  tree: CollectionTree;
  filter: ReadonlySet<string> | undefined;
  activePath: string;
  /** Filled in with the reveal handle, for the palette's ⇧↵. */
  revealRef?: React.RefObject<TreeHandle | null>;
}) {
  const [overrides, setOverrides] = useState<Expansion>(() => new Map());
  const [menuTarget, setMenuTarget] = useState<Node | null>(null);
  const [revealed, setRevealed] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const rows = useMemo(() => flatten(tree.root, overrides, filter), [tree, overrides, filter]);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
  });

  const toggle = useCallback((path: string, depth: number) => {
    setOverrides((current) => {
      const next = new Map(current);
      next.set(path, !isExpanded(current, path, depth));
      return next;
    });
  }, []);

  // A selected row inside a closed folder is a selected row nobody can see,
  // so the ancestors open. This is what makes a deep link, a restored tab and
  // the palette all land somewhere visible, and it is why the palette needs no
  // expansion plumbing of its own for ↵.
  useEffect(() => {
    if (!activePath) return;
    setOverrides((current) => expandTo(current, activePath));
  }, [activePath]);

  // Keep the selected row on screen when the route changes from somewhere
  // else — a deep link, a tab, the palette.
  const index = rows.findIndex((row) => row.node.path === activePath);
  const scrollToIndex = virtualizer.scrollToIndex;
  useEffect(() => {
    if (index >= 0) scrollToIndex(index, { align: "auto" });
    // Only when the selection moves, not on every re-measure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath]);

  // Reveal is the palette's ⇧↵: open the ancestors, put the row on screen and
  // mark it, without opening a document. Imperative because it is an event
  // rather than a state — revealing the same path twice has to work.
  useEffect(() => {
    if (!revealRef) return;
    revealRef.current = {
      reveal(path: string) {
        setOverrides((current) => expandTo(current, path));
        setRevealed(path);
      },
    };
    return () => {
      revealRef.current = null;
    };
  }, [revealRef]);

  // The revealed row is marked until the next reveal or selection change, so
  // "it is over there" is visible for longer than the scroll takes.
  useEffect(() => {
    if (!revealed) return;
    const at = rows.findIndex((row) => row.node.path === revealed);
    if (at >= 0) scrollToIndex(at, { align: "center" });
    const timer = window.setTimeout(() => setRevealed(null), 2000);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revealed, rows.length]);

  // Right-clicking anywhere in the tree finds the row under the pointer, so
  // one menu serves every row.
  const onContextMenu = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      const row = (event.target as HTMLElement).closest(`[${NODE_PATH_ATTRIBUTE}]`);
      const path = row?.getAttribute(NODE_PATH_ATTRIBUTE);
      setMenuTarget(path === null || path === undefined ? null : (findNode(tree.root, path) ?? null));
    },
    [tree],
  );

  if (rows.length === 0) {
    return (
      <div className="flex-1 px-1 py-2">
        <p className="text-meta text-fg-faint">
          {filter ? "No requests match." : "No requests in this collection."}
        </p>
      </div>
    );
  }

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          ref={scroller}
          role="tree"
          aria-label="Requests"
          onContextMenu={onContextMenu}
          className="min-h-0 flex-1 overflow-y-auto py-1"
        >
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const row = rows[item.index];
              return (
                <div
                  key={row.node.path}
                  className="absolute top-0 left-0 w-full"
                  style={{ height: item.size, transform: `translateY(${item.start}px)` }}
                >
                  <TreeRow
                    row={row}
                    selected={row.node.path === activePath}
                    revealed={row.node.path === revealed}
                    onToggle={toggle}
                  />
                </div>
              );
            })}
          </div>
        </div>
      </ContextMenuTrigger>

      <RowMenu node={menuTarget} />
    </ContextMenu>
  );
}

const TreeRow = memo(function TreeRow({
  row,
  selected,
  revealed,
  onToggle,
}: {
  row: Row;
  selected: boolean;
  /** Briefly marked by the palette's ⇧↵, so "it is over there" is visible. */
  revealed: boolean;
  onToggle: (path: string, depth: number) => void;
}) {
  const navigate = useNavigate();
  const { openTab } = useTabs();
  const { node, depth, expandable, expanded } = row;
  const isFolder = node.kind === "folder";
  const isScript = node.kind === "hook" || node.kind === "module";

  const open = useCallback(
    (activate: boolean) => {
      // A script has no document of its own yet: the editor arrives with the
      // script engine (Increment 15). Until then it opens the folder that
      // owns it, whose Scripts panel shows the file.
      if (isScript) {
        const folder = node.path.includes("/") ? node.path.replace(/\/[^/]*$/, "") : "";
        openTab(folder, "folder", { activate });
        if (activate) void navigate({ to: nodeRoute("folder"), params: { path: folder } });
        return;
      }
      const kind = isFolder ? "folder" : "request";
      if (!activate) {
        openTab(node.path, kind, { activate: false });
        return;
      }
      // Opening a folder document also reveals what is in it. It expands
      // rather than toggling: collapsing the folder you just clicked on, while
      // its document opens beside you, reads as the click having gone wrong.
      if (isFolder && expandable && !expanded) onToggle(node.path, depth);
      void navigate({ to: nodeRoute(kind), params: { path: node.path } });
    },
    [isFolder, isScript, expandable, expanded, node.path, depth, onToggle, navigate, openTab],
  );

  return (
    <div
      role="treeitem"
      {...{ [NODE_PATH_ATTRIBUTE]: node.path }}
      aria-selected={selected}
      aria-expanded={expandable ? expanded : undefined}
      aria-level={depth + 1}
      tabIndex={-1}
      onClick={() => open(true)}
      // Middle-click opens a tab without leaving the current document, the way
      // every editor and browser does it.
      onAuxClick={(event) => {
        if (event.button !== 1) return;
        event.preventDefault();
        open(false);
      }}
      className={cn(
        "flex h-[var(--row-height)] cursor-default items-center pr-2 select-none",
        selected
          ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--accent)]"
          : revealed
            ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--border-strong)]"
            : "text-fg-secondary hover:bg-control",
      )}
    >
      <span className="shrink-0" style={{ width: indentOf(depth) }} />

      {expandable ? (
        // The chevron only opens and closes; it never navigates, so a folder
        // can be explored without opening its document.
        <button
          type="button"
          aria-label={expanded ? `Collapse ${node.name}` : `Expand ${node.name}`}
          tabIndex={-1}
          onClick={(event) => {
            event.stopPropagation();
            onToggle(node.path, depth);
          }}
          className="flex size-3 shrink-0 items-center justify-center"
        >
          <ChevronRight
            className={cn("size-3 text-fg-dim transition-transform", expanded && "rotate-90")}
          />
        </button>
      ) : (
        <span className="size-3 shrink-0" />
      )}

      {isFolder ? (
        <span className="w-2 shrink-0" />
      ) : isScript ? (
        // "js" in the method gutter, so a script sits on the same axis as
        // every other row (DESIGN-NOTES §4.2) and reads as not-a-request.
        <span className={cn(methodGutter, "text-fg-dim")}>js</span>
      ) : (
        <span className={cn(methodGutter, methodColor(node.method))} title={node.method}>
          {node.method}
        </span>
      )}

      <span
        className={cn(
          "truncate text-ui",
          isFolder && !selected && "text-fg-muted",
          isScript && !selected && "text-fg-muted",
          selected && "font-medium",
        )}
      >
        {node.name}
      </span>

      {/* HOOK or LIB. The badge is the point: a reader must be able to tell
          whether a script runs on its own without knowing that "_pre.js" is
          special and "idempotency.js" is not (docs/FORMAT.md §2.4). */}
      {isScript ? <ScriptBadge kind={node.kind} hookOf={node.hookOf} /> : null}

      {/* A folder that carries shared settings. The design draws a plus here
          (DESIGN-NOTES §9.7, unresolved); it means "this folder has a
          _folder.http", not "add something". */}
      {node.settings ? (
        <Plus
          className="ml-1.5 size-2.5 shrink-0 text-fg-ghost"
          aria-label="Has shared settings"
        >
          <title>Shared settings in {node.settings.path}</title>
        </Plus>
      ) : null}

      {node.kind === "broken" ? <BrokenMarker error={node.error} /> : null}

      <GitDot node={node} />
    </div>
  );
});

/**
 * The HOOK / LIB badge of screen 3a: 9px sans, uppercase, `.06em` tracking
 * (DESIGN-NOTES §3's micro tag).
 *
 * A plain `title` rather than a Tooltip, for the same reason the git dots use
 * one: the tree is virtualized and a Radix component per row is what took a
 * scroll step from 18ms to 1ms at 2,000 requests.
 */
function ScriptBadge({ kind, hookOf }: { kind: string; hookOf?: string }) {
  const hook = kind === "hook";
  return (
    <span
      title={
        hook
          ? hookOf
            ? `Runs around ${hookOf}`
            : "Runs around every request in this folder and below"
          : "A plain ES module: nothing runs it unless a hook imports it"
      }
      className={cn(
        "ml-1.5 shrink-0 rounded-sm border px-1 text-micro tracking-[.06em] uppercase",
        hook
          ? "border-border-control text-fg-muted"
          : "border-border-control text-fg-faint",
      )}
    >
      {hook ? "hook" : "lib"}
    </span>
  );
}

/**
 * A file that did not parse. This one keeps a real tooltip: the parse error is
 * a sentence that has to wrap, and broken files are rare enough that the extra
 * components cost nothing.
 */
function BrokenMarker({ error }: { error?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="ml-1.5 flex size-3 shrink-0 items-center" aria-label="Parse error">
          <TriangleAlert className="size-3 text-warning" />
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-80">{error || "This file could not be parsed."}</TooltipContent>
    </Tooltip>
  );
}

/**
 * The 6px status dot (DESIGN-NOTES §4.4). A file shows its own status; a
 * folder shows a modified dot when anything below it has changed, which is the
 * only way a collapsed folder can say something inside needs attention.
 *
 * A plain `title` rather than a Radix tooltip: it is what the design specifies
 * for these (DESIGN-NOTES §6), and it keeps a scrolling tree free of sixty
 * mounted tooltip components.
 */
function GitDot({ node }: { node: Node }) {
  const status = node.gitStatus;
  if (!status && !(node.kind === "folder" && node.modified)) {
    return <span className="ml-auto" />;
  }
  const untracked = status === "U" || status === "A";
  const label =
    status === "U"
      ? "Untracked"
      : status === "A"
        ? "Added"
        : status === "D"
          ? "Deleted"
          : "Modified";
  return (
    <span
      title={label}
      aria-label={label}
      className={cn(
        "ml-auto size-1.5 shrink-0 rounded-full",
        untracked ? "bg-primary" : "bg-modified",
      )}
    />
  );
}

/**
 * The tree's context menu. Reveal and Copy path work; the rest are the
 * design's implied items, disabled until the write path exists — Otis does not
 * write to a collection before Phase C.
 */
/**
 * Logs a failed menu action. There is nowhere to show it yet — the design has
 * no toast — but a clipboard or file manager that refuses should not fail in
 * complete silence.
 */
function report(work: Promise<unknown>, what: string): void {
  void work.catch((err: unknown) => console.error(`[otis] could not ${what}:`, err));
}

function RowMenu({ node }: { node: Node | null }) {
  return (
    // The design never draws this menu (DESIGN-NOTES §6); it takes the app's
    // 12px default rather than shadcn's 14px.
    <ContextMenuContent className="w-56 text-ui *:data-[slot=context-menu-item]:text-ui">
      <ContextMenuItem
        disabled={!node}
        onSelect={() => node && report(CollectionService.Reveal(node.path), "reveal")}
      >
        Reveal in {fileManagerName()}
      </ContextMenuItem>
      <ContextMenuItem
        disabled={!node}
        onSelect={() => node && report(CollectionService.CopyPath(node.path), "copy the path")}
      >
        Copy path
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem disabled>Rename…</ContextMenuItem>
      <ContextMenuItem disabled>Duplicate</ContextMenuItem>
      <ContextMenuItem disabled>Delete…</ContextMenuItem>
    </ContextMenuContent>
  );
}
