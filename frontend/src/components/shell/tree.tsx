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
import { findNode, flatten, indentOf, isExpanded, type Expansion, type Row } from "@/lib/tree";
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

export function Tree({
  tree,
  filter,
  activePath,
}: {
  tree: CollectionTree;
  filter: ReadonlySet<string> | undefined;
  activePath: string;
}) {
  const [overrides, setOverrides] = useState<Expansion>(() => new Map());
  const [menuTarget, setMenuTarget] = useState<Node | null>(null);
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

  // Keep the selected row on screen when the route changes from somewhere
  // else — a deep link, a tab, the palette.
  const index = rows.findIndex((row) => row.node.path === activePath);
  const scrollToIndex = virtualizer.scrollToIndex;
  useEffect(() => {
    if (index >= 0) scrollToIndex(index, { align: "auto" });
    // Only when the selection moves, not on every re-measure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath]);

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
                  <TreeRow row={row} selected={row.node.path === activePath} onToggle={toggle} />
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
  onToggle,
}: {
  row: Row;
  selected: boolean;
  onToggle: (path: string, depth: number) => void;
}) {
  const navigate = useNavigate();
  const { openTab } = useTabs();
  const { node, depth, expandable, expanded } = row;
  const isFolder = node.kind === "folder";

  const open = useCallback(
    (activate: boolean) => {
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
    [isFolder, expandable, expanded, node.path, depth, onToggle, navigate, openTab],
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
      ) : (
        <span className={cn(methodGutter, methodColor(node.method))} title={node.method}>
          {node.method}
        </span>
      )}

      <span
        className={cn(
          "truncate text-ui",
          isFolder && !selected && "text-fg-muted",
          selected && "font-medium",
        )}
      >
        {node.name}
      </span>

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
        Reveal in {fileManagerName}
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
