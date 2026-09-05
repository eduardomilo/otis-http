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
export type NodeRouteKind = "request" | "folder" | "script" | "diff";

const ROUTE_BY_KIND = {
  request: "/r/$path",
  folder: "/f/$path",
  // A `.js` file is a row in the tree (docs/FORMAT.md §2.4) and therefore a
  // document with a route of its own. It is not `/r/` — a script is not a
  // request, nothing parses it, and RequestService would refuse it.
  script: "/s/$path",
  diff: "/diff/$path",
} as const;

/**
 * The collection root's folder route.
 *
 * The root is a folder with an empty node path, and an empty dynamic segment
 * does not match `/f/$path`: the router answers Not Found. So it has its own
 * route, and everything that links to a folder has to go through nodeLink,
 * which knows that.
 */
export const FOLDER_ROOT_ROUTE = "/f" as const;

/**
 * The collection-root folder route's *id*, which is not its path.
 *
 * TanStack gives an index route the trailing slash in its id (`/f/`) and
 * drops it from the navigable path (`/f`). Anything matching on `routeId` —
 * the tab list, the status bar's document — needs this one, and anything
 * navigating needs the other. Naming both is cheaper than finding out.
 */
export const FOLDER_ROOT_ROUTE_ID = "/f/" as const;

/**
 * The environment editor with none named, and its route id.
 *
 * Same shape as the folder root above: an index route's id keeps the trailing
 * slash its navigable path drops. This is where "edit environments" goes when
 * a collection has none, which is every collection until somebody makes one.
 */
export const ENV_INDEX_ROUTE = "/env" as const;
export const ENV_INDEX_ROUTE_ID = "/env/" as const;

/**
 * Link options for a node. Pass the raw ID; the router encodes it.
 *
 *     <Link {...nodeLink("request", "orders/create-order.http")}>
 */
export function nodeLink(kind: NodeRouteKind, path: string) {
  // The collection root. See FOLDER_ROOT_ROUTE: an empty dynamic segment is
  // Not Found, so the root has a route of its own and this is the one place
  // that has to know it.
  if (kind === "folder" && path === "") {
    return { to: FOLDER_ROOT_ROUTE } satisfies LinkOptions;
  }
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
