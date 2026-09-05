import { useRouterState } from "@tanstack/react-router";

import { ENV_INDEX_ROUTE_ID, FOLDER_ROOT_ROUTE_ID, nodeRoute } from "@/lib/paths";

/** What the current route is showing, if it addresses a document at all. */
export interface RouteDocument {
  kind: "request" | "folder" | "script" | "diff" | "environment";
  /** The node's collection-relative path, or the environment file's path. */
  path: string;
  /**
   * The environment's name, on an environment route only. The path is the
   * file (`env/staging.json`) because that is what the status bar names, and
   * the name is what every binding takes.
   */
  name?: string;
}

const KIND_BY_ROUTE: Record<string, RouteDocument["kind"]> = {
  [nodeRoute("request")]: "request",
  [nodeRoute("folder")]: "folder",
  [nodeRoute("script")]: "script",
  [nodeRoute("diff")]: "diff",
  // The collection root, whose route carries no path param at all.
  [FOLDER_ROOT_ROUTE_ID]: "folder",
};

/**
 * The document the current route addresses, or null on a route that shows
 * none. The status bar names it, which is why this is derived from the route
 * rather than from the tab list: /diff and /env open no tab, and the bar must
 * still say what is on screen.
 */
export function useRouteDocument(): RouteDocument | null {
  return useRouterState({
    select: (state) => {
      for (const match of state.matches) {
        const kind = KIND_BY_ROUTE[match.routeId];
        // The root folder route has no path param, and "" is its node path.
        if (kind) return { kind, path: (match.params as { path?: string }).path ?? "" };
        // The index route names no environment, and "" is what the sidebar
        // reads as "show the list with nothing open".
        if (match.routeId === ENV_INDEX_ROUTE_ID) {
          return { kind: "environment" as const, path: "", name: "" };
        }
        if (match.routeId === "/env/$name") {
          const { name } = match.params as { name: string };
          return { kind: "environment" as const, path: `env/${name}.json`, name };
        }
      }
      return null;
    },
  });
}
