import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronRight, GripVertical, List, Plus, TriangleAlert } from "lucide-react";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuRadioGroup,
  ContextMenuRadioItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { dropAt, parentOf, reorderedPaths, type Drop } from "@/lib/drag";
import { methodColor, methodGutter } from "@/lib/method";
import { canManage, type NodeAction } from "@/components/shell/node-actions";
import { nodeLink, nodeParentPath } from "@/lib/paths";
import { fileManagerName } from "@/lib/platform";
import {
  expandFolder,
  expandTo,
  findNode,
  flatten,
  indentOf,
  isExpanded,
  type Expansion,
  type Row,
} from "@/lib/tree";
import { cn } from "@/lib/utils";
import type { Node, Tree as CollectionTree } from "@bindings/internal/services";
import { CollectionService, LogService } from "@bindings/internal/services";
import { useOrder } from "@/state/order-context";
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
 *
 * Dragging (screen 2a) is built on pointer events rather than HTML5
 * drag-and-drop. The design's ghost has to be ours to place: the browser draws
 * its drag image directly under the cursor, which sits on top of the row the
 * drop is aimed at, and the design review called that out. Ours is pinned just
 * outside the sidebar at the pointer's height, so the insertion line and the
 * row under it stay visible for the whole drag.
 */

const ROW_HEIGHT = 24;
const NODE_PATH_ATTRIBUTE = "data-node-path";
/** Pixels the pointer must travel before a press becomes a drag, not a click. */
const DRAG_THRESHOLD = 4;
/** How close to an edge starts auto-scrolling, and by how much per frame. */
const EDGE = 24;
const SCROLL_STEP = 6;

/** A drag in flight. */
interface DragState {
  path: string;
  node: Node;
  /** False until the pointer has moved far enough to mean a drag. */
  armed: boolean;
  /** The pointer now, in client coordinates, for the ghost. */
  x: number;
  y: number;
  /** Where the press started, for the threshold. */
  fromX: number;
  fromY: number;
  drop: Drop | null;
}

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
  onCreate,
  onManage,
}: {
  tree: CollectionTree;
  filter: ReadonlySet<string> | undefined;
  activePath: string;
  /** Filled in with the reveal handle, for the palette's ⇧↵. */
  revealRef?: React.RefObject<TreeHandle | null>;
  /** Opens the create dialog, in the folder the menu was opened on. */
  onCreate: (kind: "request" | "folder", folder: string) => void;
  onManage: (action: NodeAction, node: Node) => void;
}) {
  const [overrides, setOverrides] = useState<Expansion>(() => new Map());
  const [menuTarget, setMenuTarget] = useState<Node | null>(null);
  const [revealed, setRevealed] = useState<string | null>(null);
  const [drag, setDrag] = useState<DragState | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const { reorder, move, busy } = useOrder();

  const rows = useMemo(() => flatten(tree.root, overrides, filter), [tree, overrides, filter]);

  // A filtered tree is not the order on disk — it is a subset of it — so a
  // drop inside one would write a `.order` listing only what matched. Dragging
  // is off while the filter is on, and the row says why.
  const canDrag = filter === undefined && !busy;

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

  // ---- Dragging (screen 2a) --------------------------------------------
  //
  // One set of handlers on the scroller rather than per row, for the reason
  // the context menu is shared: a virtualized tree cannot afford three
  // listeners and a piece of state on every row.

  const begin = useCallback(
    (event: React.PointerEvent, node: Node) => {
      if (!canDrag || event.button !== 0) return;
      // Only a real drag counts. A click has to keep working, so nothing
      // happens until the pointer has moved a few pixels (`armed` below).
      setDrag({
        path: node.path,
        node,
        armed: false,
        x: event.clientX,
        y: event.clientY,
        fromX: event.clientX,
        fromY: event.clientY,
        drop: null,
      });
    },
    [canDrag],
  );

  const onPointerMove = useCallback(
    (event: React.PointerEvent) => {
      setDrag((current) => {
        if (!current) return current;
        const moved =
          Math.abs(event.clientX - current.fromX) + Math.abs(event.clientY - current.fromY) > DRAG_THRESHOLD;
        if (!current.armed && !moved) return current;
        const box = scroller.current?.getBoundingClientRect();
        let drop: Drop | null = null;
        if (box) {
          const y = event.clientY - box.top + (scroller.current?.scrollTop ?? 0);
          const index = Math.floor(y / ROW_HEIGHT);
          const offset = (y % ROW_HEIGHT) / ROW_HEIGHT;
          // Below the last row is a real target: the end of the root's list,
          // which is how a row is dragged out of a folder without aiming at
          // another one.
          drop = dropAt(tree.root, rows, current.path, index < rows.length ? index : -1, offset);
        }
        return { ...current, armed: true, x: event.clientX, y: event.clientY, drop };
      });
    },
    [rows, tree.root],
  );

  const finish = useCallback(() => {
    setDrag((current) => {
      if (current?.armed && current.drop) {
        const { folder, index } = current.drop;
        if (folder === parentOf(current.path)) {
          void reorder(folder, reorderedPaths(tree.root, folder, current.path, index));
        } else {
          // The destination has to be open, or the row lands somewhere
          // invisible and the drag reads as having lost the file.
          setOverrides((expansion) => expandFolder(expansion, folder));
          void move(current.path, folder, index);
        }
      }
      return null;
    });
  }, [reorder, move, tree.root]);

  // Auto-scroll while a drag is held near an edge, so a row can be dragged
  // out of a folder taller than the sidebar.
  useEffect(() => {
    if (!drag?.armed) return;
    const element = scroller.current;
    if (!element) return;
    const box = element.getBoundingClientRect();
    const above = drag.y - box.top;
    const below = box.bottom - drag.y;
    const by = above < EDGE ? -SCROLL_STEP : below < EDGE ? SCROLL_STEP : 0;
    if (by === 0) return;
    const timer = window.setInterval(() => {
      element.scrollTop += by;
    }, 16);
    return () => window.clearInterval(timer);
  }, [drag?.armed, drag?.y]);

  // Escape abandons a drag. It is bound here rather than in useKeymap because
  // it only exists while a drag is in flight, which is the one case that map's
  // "one handler" rule is about avoiding: this is not a shortcut, it is the
  // gesture's own cancel.
  useEffect(() => {
    if (!drag) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setDrag(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drag]);

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
          onPointerMove={onPointerMove}
          onPointerUp={finish}
          onPointerCancel={() => setDrag(null)}
          onLostPointerCapture={finish}
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
                    dragging={drag?.armed === true && drag.path === row.node.path}
                    line={lineFor(drag?.drop ?? null, row.node.path)}
                    into={drag?.drop?.into === row.node.path}
                    onGrab={canDrag ? begin : undefined}
                  />
                </div>
              );
            })}
          </div>
        </div>
      </ContextMenuTrigger>

      <RowMenu node={menuTarget} onCreate={onCreate} onManage={onManage} />
      {drag?.armed ? <DragGhost node={drag.node} x={drag.x} y={drag.y} /> : null}
    </ContextMenu>
  );
}

/** Which edge of a row the single insertion line sits on, if either. */
function lineFor(drop: Drop | null, path: string): "above" | "below" | null {
  if (!drop?.line || drop.line.path !== path) return null;
  return drop.line.below ? "below" : "above";
}

/**
 * The 216×24 ghost of screen 2a: a grip glyph, the method label and the name.
 *
 * It follows the pointer, offset down and to the right.
 *
 * It used to be pinned just outside the sidebar at the pointer's height,
 * moving only vertically — an answer to the design review's finding that a
 * preview under the cursor hides the rows the drop is aimed at, including the
 * insertion line. That is a real problem and this was the wrong fix: a ghost
 * that does not track the pointer horizontally reads as a rendering bug, not
 * as a considered choice. It was reported as one — "the dragged item is not
 * positioned where the mouse pointer is, showing like 100px to the right".
 *
 * The offset is the actual answer to the original finding, and it is what
 * every file manager does: down and right of the cursor leaves the pointer
 * tip, the row it is over and the boundary above it uncovered, while the ghost
 * still reads as attached to the hand moving it. `pointer-events-none` so it
 * can never eat the drop.
 *
 * A portal because the scroller has `overflow-y: auto`: a fixed element inside
 * it is still clipped by it.
 */
/** How far the ghost trails the pointer. Enough to leave the cursor tip and
 *  the row boundary above it readable; small enough to stay attached. */
const GHOST_OFFSET_X = 14;
const GHOST_OFFSET_Y = 12;
const GHOST_WIDTH = 216;
const GHOST_HEIGHT = 24;

function DragGhost({ node, x, y }: { node: Node; x: number; y: number }) {
  // Clamped so a drag near the right or bottom edge does not push it off the
  // window, where it would look like it had vanished mid-drag.
  const left = Math.min(x + GHOST_OFFSET_X, window.innerWidth - GHOST_WIDTH - 8);
  const top = Math.min(y + GHOST_OFFSET_Y, window.innerHeight - GHOST_HEIGHT - 8);
  return createPortal(
    <div
      aria-hidden
      style={{ left, top }}
      className="pointer-events-none fixed z-50 flex h-6 w-[216px] items-center gap-1.5 rounded-sm border border-border-control bg-raised px-1.5 shadow-[0_8px_24px_rgba(0,0,0,.5)]"
    >
      <GripVertical className="size-3 shrink-0 text-fg-ghost" />
      {node.kind === "folder" ? (
        <span className="w-2 shrink-0" />
      ) : node.kind === "hook" || node.kind === "module" ? (
        <span className={cn(methodGutter, "text-fg-dim")}>js</span>
      ) : (
        <span className={cn(methodGutter, methodColor(node.method))}>{node.method}</span>
      )}
      <span className="truncate text-ui text-fg-emphasis">{node.name}</span>
    </div>,
    document.body,
  );
}

const TreeRow = memo(function TreeRow({
  row,
  selected,
  revealed,
  onToggle,
  dragging,
  line,
  into,
  onGrab,
}: {
  row: Row;
  selected: boolean;
  /** Briefly marked by the palette's ⇧↵, so "it is over there" is visible. */
  revealed: boolean;
  onToggle: (path: string, depth: number) => void;
  /** This row is the one being dragged: dimmed, per screen 2a. */
  dragging: boolean;
  /** The single insertion line, when it belongs on this row's edge. */
  line: "above" | "below" | null;
  /** This folder row is the drop destination. */
  into: boolean;
  /** Undefined while dragging is off — a filtered tree, or a write in flight. */
  onGrab?: (event: React.PointerEvent, node: Node) => void;
}) {
  const navigate = useNavigate();
  const { openTab } = useTabs();
  const { node, depth, expandable, expanded } = row;
  const isFolder = node.kind === "folder";
  const isScript = node.kind === "hook" || node.kind === "module";

  const open = useCallback(
    (activate: boolean) => {
      const kind = isScript ? "script" : isFolder ? "folder" : "request";
      if (!activate) {
        openTab(node.path, kind, { activate: false });
        return;
      }
      // Opening a folder document also reveals what is in it. It expands
      // rather than toggling: collapsing the folder you just clicked on, while
      // its document opens beside you, reads as the click having gone wrong.
      if (isFolder && expandable && !expanded) onToggle(node.path, depth);
      void navigate(nodeLink(kind, node.path));
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
      onClick={() => (dragging ? undefined : open(true))}
      // The whole row is the grab handle. A dedicated grip would be a 12px
      // target in a 24px row, and the design draws the grip on the ghost —
      // the thing that follows the cursor — not on the row.
      onPointerDown={(event) => onGrab?.(event, node)}
      // Middle-click opens a tab without leaving the current document, the way
      // every editor and browser does it.
      onAuxClick={(event) => {
        if (event.button !== 1) return;
        event.preventDefault();
        open(false);
      }}
      className={cn(
        // The transparent borders are reserved, not decorative: the insertion
        // line replaces one of them, so a row's height never changes when the
        // line appears and the rows below do not jump mid-drag
        // (DESIGN-NOTES §7.7).
        "flex h-[var(--row-height)] cursor-default items-center border-y border-transparent pr-2 select-none",
        line === "above" && "border-t-primary",
        line === "below" && "border-b-primary",
        into && "bg-control ring-1 ring-primary ring-inset",
        dragging
          ? "bg-inset text-fg-faint opacity-40"
          : selected
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

      {/* A folder whose order is manual carries a list glyph (screen 2a), so
          the arrangement being deliberate is visible without opening a menu.
          A plain title, like the git dots: this is a virtualized row. */}
      {node.ordered ? (
        <List
          className="ml-1.5 size-2.5 shrink-0 text-fg-ghost"
          aria-label="Manually ordered"
        >
          <title>Manually ordered by {node.path === "" ? ".order" : node.path + "/.order"}</title>
        </List>
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
 * The tree's context menu.
 */
/**
 * Sends a failed menu action to the activity log.
 *
 * It used to go to `console.error`, in a webview whose console nobody opens,
 * with a comment saying there was nowhere else for it — a clipboard or file
 * manager that refuses should not fail in complete silence. There is
 * somewhere now (`components/shell/activity-log`).
 */
function report(work: Promise<unknown>, what: string): void {
  void work.catch((err: unknown) =>
    LogService.Record("error", "window", `Could not ${what}`, String(err)),
  );
}

function RowMenu({
  node,
  onCreate,
  onManage,
}: {
  node: Node | null;
  onCreate: (kind: "request" | "folder", folder: string) => void;
  onManage: (action: NodeAction, node: Node) => void;
}) {
  const { setMode } = useOrder();
  const folder = node?.kind === "folder" ? node : null;
  // Creating from a *request* row means creating beside it, which is what
  // aiming at it implies. From a folder row it means inside it.
  const target = node === null ? "" : folder ? folder.path : nodeParentPath(node.path);
  return (
    // The design never draws this menu (DESIGN-NOTES §6); it takes the app's
    // 12px default rather than shadcn's 14px.
    <ContextMenuContent className="w-56 text-ui *:data-[slot=context-menu-item]:text-ui">
      {/* First, because creating is the thing you came to the menu for most
          often. The label names where it will land, so aiming at a request
          row and aiming at its folder read differently. */}
      <ContextMenuItem onSelect={() => onCreate("request", target)}>
        New request in {target === "" ? "the root" : `${target}/`}…
      </ContextMenuItem>
      <ContextMenuItem onSelect={() => onCreate("folder", target)}>
        New folder in {target === "" ? "the root" : `${target}/`}…
      </ContextMenuItem>
      <ContextMenuSeparator />
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
      {/* Screen 2a's Folder options. It lives in the folder's own menu rather
          than in the folder view, because `.order` is not a shared setting: it
          does not live in `_folder.http`, it does not cascade, and a panel
          beside Auth and Headers would imply it did. This is also where the
          drag that writes it happens. */}
      {folder ? (
        <>
          <ContextMenuSeparator />
          <ContextMenuRadioGroup
            value={folder.ordered ? "manual" : "alphabetical"}
            onValueChange={(value) => {
              if (value === "manual" || value === "alphabetical") void setMode(folder.path, value);
            }}
          >
            <ContextMenuRadioItem value="manual" className="text-ui">
              Manual order
            </ContextMenuRadioItem>
            <ContextMenuRadioItem value="alphabetical" className="text-ui">
              Alphabetical
            </ContextMenuRadioItem>
          </ContextMenuRadioGroup>
        </>
      ) : null}
      <ContextMenuSeparator />
      {/* Disabled for a script row and for the collection root: a `.js` file
          beside a folder is not a request, and the root is the directory
          somebody chose in a file dialog — neither is Otis' to rename or
          remove. `canManage` is the same check the service makes, said once
          here so the menu does not offer what Go will refuse. */}
      <ContextMenuItem disabled={!canManage(node)} onSelect={() => node && onManage("rename", node)}>
        Rename…
      </ContextMenuItem>
      <ContextMenuItem
        disabled={!canManage(node)}
        onSelect={() => node && onManage("duplicate", node)}
      >
        Duplicate
      </ContextMenuItem>
      <ContextMenuItem
        disabled={!canManage(node)}
        onSelect={() => node && onManage("delete", node)}
        className="text-destructive data-[disabled]:text-destructive"
      >
        Delete…
      </ContextMenuItem>
    </ContextMenuContent>
  );
}
