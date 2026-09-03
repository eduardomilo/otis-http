/**
 * Turning the collection tree into the flat list of rows the sidebar renders.
 *
 * The sidebar is virtualized, which means it draws a window onto a flat array
 * rather than a nested set of elements. Everything here is pure so the
 * flattening, the expand rule and the filter can be reasoned about — and
 * tested — without a DOM.
 */

import type { Node } from "@bindings/internal/services";

/** One rendered row: a node and how deep it sits. */
export interface Row {
  node: Node;
  /** 0 for the root's children, 1 for their children, and so on. */
  depth: number;
  /** True for a folder with children, which is what gets a chevron. */
  expandable: boolean;
  expanded: boolean;
}

/**
 * Whether a folder is open.
 *
 * Rather than a set of expanded paths, this is a set of *overrides* on top of
 * a default. A folder the user has never touched follows the rule — the
 * collection's top-level folders are open, everything deeper is closed — so a
 * folder that appears while the app is running gets the right state without
 * anyone having to notice it appeared.
 */
export type Expansion = ReadonlyMap<string, boolean>;

const DEFAULT_OPEN_DEPTH = 0;

export function isExpanded(overrides: Expansion, path: string, depth: number): boolean {
  return overrides.get(path) ?? depth === DEFAULT_OPEN_DEPTH;
}

/**
 * The overrides needed to make every ancestor of a path open.
 *
 * Returns the same map when nothing had to change, so a caller can use the
 * identity to decide whether to set state at all — the tree is virtualized and
 * a new map every time the route changes would re-flatten it for nothing.
 */
export function expandTo(overrides: Expansion, path: string): Expansion {
  const segments = path.split("/");
  if (segments.length < 2) return overrides;
  let next: Map<string, boolean> | null = null;
  let ancestor = "";
  // Every ancestor folder, root-most first, so its depth is its index.
  for (let depth = 0; depth < segments.length - 1; depth++) {
    ancestor = ancestor === "" ? segments[depth] : ancestor + "/" + segments[depth];
    if (isExpanded(overrides, ancestor, depth)) continue;
    next ??= new Map(overrides);
    next.set(ancestor, true);
  }
  return next ?? overrides;
}

/**
 * Opens a folder itself, and every ancestor above it.
 *
 * `expandTo` opens the ancestors of a path, which is what a selected row
 * needs; a drop *into* a folder needs that folder open as well, or the row
 * lands somewhere invisible and the drag reads as having lost the file.
 */
export function expandFolder(overrides: Expansion, folder: string): Expansion {
  if (folder === "") return overrides;
  const depth = folder.split("/").length - 1;
  const opened = expandTo(overrides, folder);
  if (isExpanded(opened, folder, depth)) return opened;
  const next = new Map(opened);
  next.set(folder, true);
  return next;
}

/**
 * Flattens the tree into the visible rows.
 *
 * When `visible` is given (a filter is active) only nodes in it are rendered,
 * and every folder is treated as open so a match is never hidden inside a
 * closed ancestor.
 */
export function flatten(
  root: Node,
  overrides: Expansion,
  visible?: ReadonlySet<string>,
): Row[] {
  const rows: Row[] = [];
  const walk = (node: Node, depth: number) => {
    const children = node.children ?? [];
    const expandable = node.kind === "folder" && children.length > 0;
    const expanded = visible ? true : isExpanded(overrides, node.path, depth);

    if (!visible || visible.has(node.path)) {
      rows.push({ node, depth, expandable, expanded });
    }
    if (!expandable || !expanded) return;
    for (const child of children) walk(child, depth + 1);
  };
  // The root itself is not a row; the collection's name is in the title strip.
  for (const child of root.children ?? []) walk(child, 0);
  return rows;
}

/**
 * A subsequence match, the same shape of matching every fuzzy finder uses:
 * "ordcre" matches "orders/create-order". Case-insensitive.
 */
export function fuzzyMatches(query: string, text: string): boolean {
  if (query === "") return true;
  const haystack = text.toLowerCase();
  let at = 0;
  for (const char of query.toLowerCase()) {
    at = haystack.indexOf(char, at);
    if (at === -1) return false;
    at++;
  }
  return true;
}

/**
 * The nodes a filter should show: everything that matches on its name or its
 * path, plus the ancestors needed to reach them. A folder that matches shows
 * as a row, but does not drag its whole subtree in with it — the query is
 * still what decides which requests appear.
 *
 * An empty query returns undefined, meaning "no filter", which is how the
 * caller tells filtering from a filter that matched nothing.
 */
export function filterTree(root: Node, query: string): ReadonlySet<string> | undefined {
  const trimmed = query.trim();
  if (trimmed === "") return undefined;

  const visible = new Set<string>();
  const walk = (node: Node, ancestors: string[]) => {
    if (fuzzyMatches(trimmed, node.name) || fuzzyMatches(trimmed, node.path)) {
      visible.add(node.path);
      for (const ancestor of ancestors) visible.add(ancestor);
    }
    const next = [...ancestors, node.path];
    for (const child of node.children ?? []) walk(child, next);
  };
  for (const child of root.children ?? []) walk(child, []);
  return visible;
}

/** The indent of a row, in pixels: 10 + depth × 14 (DESIGN-NOTES §4.3). */
export function indentOf(depth: number): number {
  return 10 + depth * 14;
}

/** Finds a node by its path, or undefined. */
export function findNode(root: Node, path: string): Node | undefined {
  if (root.path === path) return root;
  for (const child of root.children ?? []) {
    const found = findNode(child, path);
    if (found) return found;
  }
  return undefined;
}
