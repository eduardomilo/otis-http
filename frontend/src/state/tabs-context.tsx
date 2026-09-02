import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useNavigate, useRouterState } from "@tanstack/react-router";

import { nodeRoute } from "@/lib/paths";
import { useCollection } from "@/state/collection-context";
import { useSettings } from "@/state/settings-context";

/**
 * Document tabs.
 *
 * A tab exists for every request or folder the user has opened; navigating to
 * /r/$path or /f/$path opens one, and closing the last tab returns to /. Tab
 * state is in memory — only the list of paths and which one is active are
 * persisted, so a restart reopens the same documents but rebuilds everything
 * about them from disk.
 *
 * Radix Tabs models one-of-N panel switching, not an editor tab bar with
 * close, overflow and a dirty marker, so this is hand-rolled
 * (DESIGN-NOTES §7.3).
 */

export type TabKind = "request" | "folder";

export interface Tab {
  /** The node's collection-relative ID (docs/FORMAT.md §2.1). */
  path: string;
  kind: TabKind;
  /** Unsaved changes, set by the documents provider (increment 10). */
  dirty: boolean;
}

/**
 * A veto on closing one tab, installed by whoever knows whether closing it
 * would lose work. Returning false leaves the tab open.
 *
 * The guard lives here rather than in the tab bar because ⌘W, the close ×, a
 * middle click and closing a collection all have to consult it, and there is
 * exactly one of them.
 */
export type CloseGuard = (path: string) => boolean | Promise<boolean>;

interface TabsContextValue {
  tabs: Tab[];
  /** The active tab's path, or "" when no document is open. */
  activePath: string;
  /** Opens a tab and, unless activate is false, navigates to it. */
  openTab: (path: string, kind: TabKind, options?: { activate?: boolean }) => void;
  closeTab: (path: string) => void;
  closeActive: () => void;
  /** Reopens the most recently closed tab. */
  reopenLastClosed: () => void;
  /** Marks a tab as having unsaved changes. */
  setDirty: (path: string, dirty: boolean) => void;
  /** Installs (or, with null, removes) the veto consulted before a close. */
  setCloseGuard: (guard: CloseGuard | null) => void;
}

const TabsContext = createContext<TabsContextValue | null>(null);

/** The route a tab kind lives at, and the reverse mapping. */
const KIND_BY_ROUTE: Record<string, TabKind> = {
  [nodeRoute("request")]: "request",
  [nodeRoute("folder")]: "folder",
};

export function TabsProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const { settings, saveTabs } = useSettings();
  const { collection } = useCollection();
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [activePath, setActivePath] = useState("");
  const closed = useRef<Tab[]>([]);
  const restoredFor = useRef<string | null>(null);
  const closeGuard = useRef<CloseGuard | null>(null);

  // The document the current route addresses, if any.
  const current = useRouterState({
    select: (state) => {
      for (const match of state.matches) {
        const kind = KIND_BY_ROUTE[match.routeId];
        if (kind) return { kind, path: (match.params as { path: string }).path };
      }
      return null;
    },
  });

  // Restore the persisted tabs, once per collection.
  useEffect(() => {
    const path = collection?.path ?? "";
    if (!settings || !path || restoredFor.current === path) return;
    restoredFor.current = path;
    const open = settings.tabs.open ?? [];
    setTabs(open.map((p) => ({ path: p, kind: kindOf(p), dirty: false })));
    setActivePath(settings.tabs.active ?? "");
  }, [settings, collection?.path]);

  // Closing a collection closes its documents with it.
  useEffect(() => {
    if (collection && !collection.path) {
      restoredFor.current = null;
      setTabs([]);
      setActivePath("");
      closed.current = [];
    }
  }, [collection]);

  // Navigating to a document opens its tab and makes it active. This is the
  // only place a tab becomes active from a route, so deep links, palette
  // results and tree clicks all behave identically.
  useEffect(() => {
    if (!current) return;
    setTabs((existing) =>
      existing.some((t) => t.path === current.path)
        ? existing
        : [...existing, { path: current.path, kind: current.kind, dirty: false }],
    );
    setActivePath(current.path);
  }, [current?.path, current?.kind]);

  // Persist the paths whenever the set or the active one changes.
  useEffect(() => {
    if (!settings || !collection?.path) return;
    const open = tabs.map((t) => t.path);
    const same =
      (settings.tabs.active ?? "") === activePath &&
      sameOrder(settings.tabs.open ?? [], open);
    if (same) return;
    saveTabs({ open, active: activePath });
  }, [tabs, activePath, settings, collection?.path, saveTabs]);

  const goTo = useCallback(
    (tab: Tab) => {
      void navigate({ to: nodeRoute(tab.kind), params: { path: tab.path } });
    },
    [navigate],
  );

  const openTab = useCallback<TabsContextValue["openTab"]>(
    (path, kind, options) => {
      setTabs((existing) =>
        existing.some((t) => t.path === path)
          ? existing
          : [...existing, { path, kind, dirty: false }],
      );
      if (options?.activate === false) return;
      goTo({ path, kind, dirty: false });
    },
    [goTo],
  );

  const closeTab = useCallback(
    async (path: string) => {
      // The guard first, and before any state moves: a tab that is not going
      // to close must not lose its place in the list or its selection.
      if (closeGuard.current && !(await closeGuard.current(path))) return;
      setTabs((existing) => {
        const index = existing.findIndex((t) => t.path === path);
        if (index === -1) return existing;
        closed.current = [existing[index], ...closed.current].slice(0, 10);
        const remaining = existing.filter((t) => t.path !== path);
        if (path === activePath) {
          // Activate the neighbour on the right, or the new last tab.
          const next = remaining[index] ?? remaining[remaining.length - 1];
          if (next) {
            goTo(next);
          } else {
            setActivePath("");
            void navigate({ to: "/" });
          }
        }
        return remaining;
      });
    },
    [activePath, goTo, navigate],
  );

  const closeActive = useCallback(() => {
    if (activePath) void closeTab(activePath);
  }, [activePath, closeTab]);

  const reopenLastClosed = useCallback(() => {
    const [tab, ...rest] = closed.current;
    if (!tab) return;
    closed.current = rest;
    openTab(tab.path, tab.kind);
  }, [openTab]);

  const setDirty = useCallback((path: string, dirty: boolean) => {
    setTabs((existing) =>
      existing.some((t) => t.path === path && t.dirty !== dirty)
        ? existing.map((t) => (t.path === path ? { ...t, dirty } : t))
        : existing,
    );
  }, []);

  const setCloseGuard = useCallback((guard: CloseGuard | null) => {
    closeGuard.current = guard;
  }, []);

  const value = useMemo<TabsContextValue>(
    () => ({
      tabs,
      activePath,
      openTab,
      closeTab,
      closeActive,
      reopenLastClosed,
      setDirty,
      setCloseGuard,
    }),
    [tabs, activePath, openTab, closeTab, closeActive, reopenLastClosed, setDirty, setCloseGuard],
  );

  return <TabsContext.Provider value={value}>{children}</TabsContext.Provider>;
}

export function useTabs(): TabsContextValue {
  const value = useContext(TabsContext);
  if (!value) throw new Error("useTabs must be used inside a TabsProvider");
  return value;
}

/**
 * Only paths are persisted, so a restored tab's kind is read off the path.
 * Request IDs end in `.http` and folder IDs are directory paths, which never
 * do — a folder's settings file is `orders/_folder.http`, but the folder's own
 * ID is `orders`.
 */
function kindOf(path: string): TabKind {
  return path.endsWith(".http") ? "request" : "folder";
}

function sameOrder(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}
