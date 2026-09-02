import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { Layout, PanelImperativeHandle } from "react-resizable-panels";
import { useNavigate, useRouterState } from "@tanstack/react-router";

import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CommandPalette } from "@/components/shell/command-palette";
import { ResponsePane } from "@/components/response/response-pane";
import { Sidebar } from "@/components/shell/sidebar";
import { StatusBar } from "@/components/shell/status-bar";
import { TabBar } from "@/components/shell/tab-bar";
import { useKeymap } from "@/hooks/use-keymap";
import { useRouteDocument } from "@/hooks/use-route-document";
import { relativeTime } from "@/lib/time";
import { findNode } from "@/lib/tree";
import { useCollection } from "@/state/collection-context";
import { useDocuments } from "@/state/documents-context";
import { useDiff } from "@/state/diff-context";
import { useEnvironments } from "@/state/environment-context";
import { useRuns } from "@/state/run-context";
import { useSends } from "@/state/send-context";
import { useSettings } from "@/state/settings-context";
import { useTabs } from "@/state/tabs-context";

/**
 * The three-pane shell (screen 1a).
 *
 * Pane geometry comes from DESIGN-NOTES §4.1: a 260px sidebar and a 480px
 * response pane, both fixed in the design. The design draws no resize handles
 * and specifies no minimums (§7.1, unresolved), so the constraints here are
 * ours: 200–420px for the sidebar, at least 280px for the response pane, and
 * both collapsible. The handle is the pane border it already sits on, with a
 * wider invisible hit area and --border-strong on hover; it introduces no new
 * visual language.
 *
 * Widths are in pixels rather than percentages so a restored layout is the
 * layout that was saved, whatever size the window is now.
 */

const SIDEBAR_DEFAULT = 260;
const SIDEBAR_MIN = 200;
const SIDEBAR_MAX = 420;
const RESPONSE_MIN = 280;
/** The response pane's share of what is left after the sidebar, on first run. */
const RESPONSE_DEFAULT_FRACTION = 0.4;

export function AppShell({ children }: { children: ReactNode }) {
  const { settings, savePanes } = useSettings();
  const { closeActive, reopenLastClosed } = useTabs();
  const { saveActive } = useDocuments();
  const { send } = useSends();
  const { tree } = useCollection();
  const { environments } = useEnvironments();
  const { overview } = useDiff();
  const { runFor, start } = useRuns();
  const routeDocument = useRouteDocument();
  // Read inside rememberLayout, which is registered once and must not be
  // re-created every time the route changes.
  const onDiffRef = useRef(false);
  const document = routeDocument && tree ? findNode(tree.root, routeDocument.path) : undefined;

  // On an environment route the sidebar shows the environment list instead of
  // the tree (screen 1c), and the status bar names the file rather than a node.
  const environment = routeDocument?.kind === "environment" ? routeDocument.name : null;
  const onDiff = useRouterState({ select: (state) => state.location.pathname.startsWith("/diff") });
  onDiffRef.current = onDiff;

  // Screen 3a's status-bar summary: "Last run: 6/6 passed · 2h ago".
  const folderRun =
    routeDocument?.kind === "folder" ? lastRun(runFor(routeDocument.path)) : null;
  const environmentRow = environment
    ? (environments.find((e) => e.name === environment) ?? null)
    : null;

  const navigate = useNavigate();
  const sidebarPanel = useRef<PanelImperativeHandle>(null);
  const responsePanel = useRef<PanelImperativeHandle>(null);
  const sidebarPane = useRef<HTMLDivElement>(null);
  const centerPane = useRef<HTMLDivElement>(null);
  const responsePane = useRef<HTMLDivElement>(null);
  const filterInput = useRef<HTMLInputElement>(null);

  const [paletteOpen, setPaletteOpen] = useState(false);
  // The last non-zero width of each collapsible pane, so collapsing does not
  // overwrite the size it should expand back to.
  const widths = useRef({ sidebar: 0, response: 0 });

  // The response pane's first-run width is a fraction of what is left after
  // the sidebar, so the group has to be measured before it can be rendered.
  const [groupWidth, setGroupWidth] = useState(0);
  const group = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const element = group.current;
    if (!element) return;
    setGroupWidth(element.clientWidth);
  }, []);

  const togglePanel = useCallback((panel: PanelImperativeHandle | null) => {
    if (!panel) return;
    if (panel.isCollapsed()) panel.expand();
    else panel.collapse();
  }, []);

  const toggleDiff = useCallback(() => {
    if (onDiffRef.current) {
      void navigate({ to: "/", replace: false });
      return;
    }
    void navigate({ to: "/diff" });
  }, [navigate]);

  const focusFilter = useCallback(() => {
    sidebarPanel.current?.expand();
    filterInput.current?.focus();
    filterInput.current?.select();
  }, []);

  useKeymap([
    { key: "k", mod: true, run: () => setPaletteOpen(true) },
    { key: "p", mod: true, run: focusFilter },
    { key: "b", mod: true, run: () => togglePanel(sidebarPanel.current) },
    { key: "j", mod: true, run: () => togglePanel(responsePanel.current) },
    { key: "w", mod: true, run: closeActive },
    // CodeMirror consumes ⌘S before the window sees it, so the request editor
    // binds this same call as an editor keymap too (see useKeymap).
    { key: "s", mod: true, run: () => void saveActive() },
    // ⌘↵ sends from anywhere in the request view; the editors bind it too.
    {
      key: "Enter",
      mod: true,
      run: () => {
        if (routeDocument?.kind === "request") void send(routeDocument.path);
      },
    },
    { key: "t", mod: true, shift: true, run: reopenLastClosed },
    // ⌘⇧↵ runs the folder on screen (screen 3a puts the hint on the button).
    {
      key: "Enter",
      mod: true,
      shift: true,
      run: () => {
        if (routeDocument?.kind === "folder") void start(routeDocument.path, false);
      },
    },
    // ⌘G shows what changed. SCREENS.md notes the design never says how you
    // get into or out of this view; the status bar's branch is the other way
    // in, and pressing it again goes back to the document that was open.
    { key: "g", mod: true, run: toggleDiff },
    { key: "1", mod: true, run: () => focusPane(sidebarPanel.current, sidebarPane.current) },
    { key: "2", mod: true, run: () => centerPane.current?.focus() },
    { key: "3", mod: true, run: () => focusPane(responsePanel.current, responsePane.current) },
  ]);

  // Restore the collapsed panes once, after the panels exist.
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !settings || !groupWidth) return;
    restored.current = true;
    if (settings.panes.sidebarCollapsed) sidebarPanel.current?.collapse();
    if (settings.panes.responseCollapsed) responsePanel.current?.collapse();
  }, [settings, groupWidth]);

  // Persisting the layout hangs off the group's "layout changed" callback
  // rather than each panel's onResize, which does not fire when a separator
  // is what moved. The sizes come from the callback's own argument, not from
  // the panel handles: the handles still report the previous layout at this
  // point, which persists every change one step behind.
  //
  // A collapsed pane is 0 wide, and recording that as its width would lose the
  // size to expand back to, so only a real width is remembered.
  const rememberLayout = useCallback(
    (layout: Layout) => {
      const element = group.current;
      if (!element) return;
      // On the diff route the response pane is not mounted at all, so the
      // layout has nothing to say about it. Persisting it here would record a
      // width of zero and the pane would come back collapsed.
      if (onDiffRef.current) return;
      // A layout percentage is a share of what the panels have between them,
      // which is the group minus its separators. Measuring the separators
      // rather than assuming 1px keeps the round trip exact: without it the
      // saved width drifts a pixel per restart.
      let total = element.clientWidth;
      for (const separator of element.querySelectorAll('[data-slot="resizable-handle"]')) {
        total -= separator.getBoundingClientRect().width;
      }
      if (total <= 0) return;
      const pixels = (id: string) => Math.round(((layout[id] ?? 0) / 100) * total);
      const sidebarPixels = pixels("sidebar");
      const responsePixels = pixels("response");
      savePanes({
        sidebarCollapsed: sidebarPixels === 0,
        responseCollapsed: responsePixels === 0,
        sidebarWidth: sidebarPixels > 0 ? sidebarPixels : widths.current.sidebar,
        responseWidth: responsePixels > 0 ? responsePixels : widths.current.response,
      });
      if (sidebarPixels > 0) widths.current.sidebar = sidebarPixels;
      if (responsePixels > 0) widths.current.response = responsePixels;
    },
    [savePanes],
  );

  if (!settings) {
    // One frame at most: the settings read is a local file.
    return <div ref={group} className="min-h-0 flex-1" />;
  }
  if (!groupWidth) {
    return <div ref={group} className="min-h-0 flex-1" />;
  }

  const sidebarWidth = settings.panes.sidebarWidth || SIDEBAR_DEFAULT;
  const responseWidth =
    settings.panes.responseWidth ||
    Math.max(RESPONSE_MIN, Math.round(RESPONSE_DEFAULT_FRACTION * (groupWidth - sidebarWidth)));

  return (
    <>
      <div ref={group} className="min-h-0 flex-1">
        <ResizablePanelGroup
          orientation="horizontal"
          className="h-full"
          onLayoutChanged={rememberLayout}
        >
          <ResizablePanel
            id="sidebar"
            panelRef={sidebarPanel}
            defaultSize={sidebarWidth}
            minSize={SIDEBAR_MIN}
            maxSize={SIDEBAR_MAX}
            collapsible
            collapsedSize={0}
          >
            <div ref={sidebarPane} tabIndex={-1} className="h-full outline-none">
              <Sidebar
                ref={filterInput}
                activePath={routeDocument?.path ?? ""}
                environment={environment}
                diff={onDiff}
              />
            </div>
          </ResizablePanel>

          <PaneHandle />

          <ResizablePanel id="center" minSize={320}>
            <div ref={centerPane} tabIndex={-1} className="flex h-full flex-col outline-none">
              <TabBar />
              <div className="min-h-0 flex-1 overflow-auto">{children}</div>
            </div>
          </ResizablePanel>

          {/* Screen 1b has no response pane: the diff takes the full width
              beside the sidebar. The panel is unmounted rather than collapsed,
              so its saved width survives — a collapse would persist as one. */}
          {onDiff ? null : (
            <>
              <PaneHandle />
              <ResizablePanel
                id="response"
                panelRef={responsePanel}
                defaultSize={responseWidth}
                minSize={RESPONSE_MIN}
                collapsible
                collapsedSize={0}
              >
                <div ref={responsePane} tabIndex={-1} className="h-full outline-none">
                  <ResponsePane />
                </div>
              </ResizablePanel>
            </>
          )}
        </ResizablePanelGroup>
      </div>

      <StatusBar
        git={tree?.git ?? null}
        file={routeDocument?.path ?? null}
        gitStatus={document?.gitStatus ?? null}
        totals={
          onDiff && overview?.repository
            ? { adds: overview.adds, dels: overview.dels, hunks: overview.hunks }
            : null
        }
        onShowChanges={toggleDiff}
        // §8.4: the right slot is a one-line summary of the current view.
        // Screen 1c's is "Referenced by 23 requests", which Go counts.
        context={
          onDiff
            ? lastCommit(overview)
            : environmentRow
              ? referencedBy(environmentRow.referencedBy)
              : folderRun
                ? folderRun
                : null
        }
      />

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </>
  );
}

/**
 * The divider between two panes: the 1px --border the design already draws
 * between panes, widened to a 7px hit area and lightened to --border-strong
 * while it is being used.
 */
function PaneHandle() {
  return (
    <ResizableHandle className="w-px bg-border transition-colors after:w-[7px] hover:bg-border-strong data-[resizing]:bg-border-strong" />
  );
}

/**
 * Screen 1b's status-bar summary: `Last commit: "add expand param" · 2h ago ·
 * you`. The author is named as written rather than as "you", because the bar
 * cannot know whose name is in git's config.
 */
function lastCommit(overview: { lastCommit?: { subject: string; author: string; when: string } | null } | null) {
  const commit = overview?.lastCommit;
  if (!commit) return null;
  return `Last commit: “${commit.subject}” · ${relativeTime(commit.when)} · ${commit.author}`;
}

/**
 * Screen 3a's status-bar summary. A run still going says how far it has got,
 * because "Last run" for something that has not finished would be a lie.
 */
function lastRun(run: ReturnType<ReturnType<typeof useRuns>["runFor"]>): string | null {
  if (!run) return null;
  if (run.error) return "Last run: could not start";
  const summary = run.summary;
  if (!summary) {
    const done = run.rows.filter((row) => row.result).length;
    return `Running: ${done}/${run.rows.length}`;
  }
  const state =
    summary.state === "stopped"
      ? ` · stopped, ${summary.skipped} skipped`
      : summary.state === "cancelled"
        ? " · cancelled"
        : "";
  return `Last run: ${summary.passed}/${summary.total} passed${state} · ${relativeTime(summary.at)}`;
}

/** Screen 1c's status-bar summary. The count is exact (DESIGN-NOTES §8.5). */
function referencedBy(count: number): string {
  return `Referenced by ${count} ${count === 1 ? "request" : "requests"}`;
}

/** Expands a collapsed pane before focusing it, so ⌘1/⌘3 always land. */
function focusPane(panel: PanelImperativeHandle | null, element: HTMLElement | null) {
  panel?.expand();
  element?.focus();
}
