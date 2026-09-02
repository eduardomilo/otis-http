/**
 * Collection-relative paths in routes.
 *
 * Every node in a collection has a stable ID: its path relative to the
 * collection root, "/"-separated, with no trailing slash (docs/FORMAT.md
 * §2.1) — `orders/create-order.http`, `orders`, or `""` for the root. Routes
 * carry that ID in a single dynamic segment (`/r/$path`, `/f/$path`,
 * `/diff/$path`), so its separators must be percent-encoded or the segment
 * would split.
 *
 * TanStack Router already does exactly that: it encodes a param value with
 * encodeURIComponent when building a URL, and decodes each segment after
 * splitting on "/" when matching. So route params take the *raw* ID and the
 * round trip is lossless even for a file name containing a literal "%".
 * Nothing outside this file should hand-roll that encoding — use the link
 * helpers below.
 */

import type { LinkOptions } from "@tanstack/react-router";

/** The routes that address a node by path. */
export type NodeRouteKind = "request" | "folder" | "diff";

const ROUTE_BY_KIND = {
  request: "/r/$path",
  folder: "/f/$path",
  diff: "/diff/$path",
} as const;

/**
 * Link options for a node. Pass the raw ID; the router encodes it.
 *
 *     <Link {...nodeLink("request", "orders/create-order.http")}>
 */
export function nodeLink(kind: NodeRouteKind, path: string) {
  return { to: ROUTE_BY_KIND[kind], params: { path } } satisfies LinkOptions;
}

/** The route pattern a node kind navigates to. */
export function nodeRoute(kind: NodeRouteKind): (typeof ROUTE_BY_KIND)[NodeRouteKind] {
  return ROUTE_BY_KIND[kind];
}

/**
 * Percent-encodes a node ID for a URL written by hand rather than built by
 * the router — a hash href, say. Prefer nodeLink.
 */
export function encodeNodePath(id: string): string {
  return encodeURIComponent(id);
}

/** The inverse of encodeNodePath. A malformed escape is returned unchanged. */
export function decodeNodePath(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

/** The last segment of a node ID: `orders/create-order.http` -> `create-order.http`. */
export function nodeBaseName(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? id : id.slice(cut + 1);
}

/** Everything before the last segment, or "" for a top-level node. */
export function nodeParentPath(id: string): string {
  const cut = id.lastIndexOf("/");
  return cut === -1 ? "" : id.slice(0, cut);
}

/**
 * A node's display name, as far as a path alone can tell: the file name
 * without its `.http` extension (docs/FORMAT.md §2.1 prefers the `@name`
 * directive and then the `###` title, both of which need the parsed file —
 * increment 9 supplies them from the tree).
 */
export function nodeDisplayName(id: string): string {
  const base = nodeBaseName(id);
  return base.endsWith(".http") ? base.slice(0, -".http".length) : base;
}
