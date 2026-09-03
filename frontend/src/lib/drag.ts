import type { Node } from "@bindings/internal/services";

import { findNode, type Row } from "@/lib/tree";

/**
 * Where a dragged row would land (screen 2a).
 *
 * Pure, and separate from the tree component, for the same reason
 * `lib/tree`'s flattening is: the arithmetic of "which row is the pointer
 * over, and does that mean above it, below it, or inside it" is the part worth
 * testing, and it has nothing to do with React.
 *
 * The design specifies one insertion line and one drag ghost, so this returns
 * one line and one folder to highlight — never both, and never two lines.
 */

/** How much of a folder row's height means "into this folder". */
const INTO_BAND = 0.4;

export interface Drop {
  /** The folder the entry ends up in, as a node path; "" is the root. */
  folder: string;
  /** Its position among that folder's children, after the move. */
  index: number;
  /**
   * Where the single insertion line goes: above this row, or below it when
   * `below` is true. Null when the drop is into a folder rather than between
   * two rows.
   */
  line: { path: string; below: boolean } | null;
  /** The folder row to outline, when dropping into it. Null otherwise. */
  into: string | null;
}

/**
 * Resolves a pointer position over the flattened rows into a drop.
 *
 * `offset` is the pointer's position inside the row it is over, 0 at the top
 * edge and 1 at the bottom. A collapsed or empty folder's middle band means
 * "inside"; everything else means "between these two rows", which is what the
 * accent top border in the design marks.
 *
 * Returns null when the drop would do nothing or cannot be done: onto the
 * dragged row itself, into its own subtree, or back where it already is.
 */
export function dropAt(
  root: Node,
  rows: readonly Row[],
  dragged: string,
  rowIndex: number,
  offset: number,
): Drop | null {
  if (rowIndex < 0 || rowIndex >= rows.length) {
    // Past the last row: the end of the root's list, which is how a row is
    // dragged out of a folder without aiming at another one.
    return normalize(root, dragged, { folder: "", index: childCount(root, ""), line: null, into: null });
  }

  const row = rows[rowIndex];
  const target = row.node;
  if (isSelfOrDescendant(dragged, target.path)) return null;

  const isFolder = target.kind === "folder";
  const inside = isFolder && offset > (1 - INTO_BAND) / 2 && offset < 1 - (1 - INTO_BAND) / 2;
  if (inside) {
    // Into the folder, at the end: a folder's own row has no position within
    // itself to aim at, and appending is what dropping "on" a folder means
    // everywhere else.
    return normalize(root, dragged, {
      folder: target.path,
      index: childCount(root, target.path),
      line: null,
      into: target.path,
    });
  }

  const parent = parentOf(target.path);
  const siblings = childrenOf(root, parent);
  const at = siblings.findIndex((child) => child.path === target.path);
  if (at < 0) return null;
  const below = offset > 0.5;
  return normalize(root, dragged, {
    folder: parent,
    index: below ? at + 1 : at,
    line: { path: target.path, below },
    into: null,
  });
}

/**
 * Drops a no-op and refuses an impossible one.
 *
 * A no-op is the row's current position expressed two ways — the line below
 * the row above it, and the line above the row below it — and both have to
 * come back null, or the strip would report an order that was saved without
 * anything having changed.
 */
function normalize(root: Node, dragged: string, drop: Drop): Drop | null {
  if (isSelfOrDescendant(dragged, drop.folder)) return null;
  const siblings = childrenOf(root, drop.folder);
  const from = siblings.findIndex((child) => child.path === dragged);
  if (from < 0) return drop; // a different folder: always a real move
  if (drop.index === from || drop.index === from + 1) return null;
  return drop;
}

/** True when `path` is `dragged` itself or somewhere inside it. */
function isSelfOrDescendant(dragged: string, path: string): boolean {
  return path === dragged || path.startsWith(dragged + "/");
}

/**
 * The order a folder's children would be in after the drop, as node paths —
 * what `OrderService.Reorder` wants. The dragged row is taken out first, so
 * the index is a position in the list as the drop leaves it.
 */
export function reorderedPaths(root: Node, folder: string, dragged: string, index: number): string[] {
  const paths = childrenOf(root, folder).map((child) => child.path);
  const from = paths.indexOf(dragged);
  if (from < 0) return paths;
  const without = paths.filter((path) => path !== dragged);
  // An index counted in the list *including* the dragged row is one too high
  // once the row is removed from above the target.
  const to = index > from ? index - 1 : index;
  without.splice(Math.max(0, Math.min(to, without.length)), 0, dragged);
  return without;
}

/** A node path's parent folder path; "" for a top-level entry. */
export function parentOf(path: string): string {
  const cut = path.lastIndexOf("/");
  return cut < 0 ? "" : path.slice(0, cut);
}

function childrenOf(root: Node, folder: string): readonly Node[] {
  const node = folder === "" ? root : findNode(root, folder);
  return node?.children ?? [];
}

function childCount(root: Node, folder: string): number {
  return childrenOf(root, folder).length;
}
