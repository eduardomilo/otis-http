import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { Layout, PanelImperativeHandle } from "react-resizable-panels";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { Events } from "@wailsio/runtime";

import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CommandPalette } from "@/components/shell/command-palette";
import { AgentConfirmDialog } from "@/components/shell/agent-confirm-dialog";
import {
  CollectionSwitchDialog,
  type CollectionAction,
} from "@/components/shell/collection-switch-dialog";
import { CreateDialog, type CreateKind } from "@/components/shell/create-dialog";
import { ResponsePane } from "@/components/response/response-pane";
import { Sidebar } from "@/components/shell/sidebar";
import type { TreeHandle } from "@/components/shell/tree";
import { StatusBar } from "@/components/shell/status-bar";
import { TabBar } from "@/components/shell/tab-bar";
import { useKeymap } from "@/hooks/use-keymap";
import { useRouteDocument } from "@/hooks/use-route-document";
import { OtisEvent } from "@/lib/events.gen";
import { isMac } from "@/lib/platform";
import { CollectionService } from "@bindings/internal/services";
import { nodeParentPath, nodeRoute } from "@/lib/paths";
import { relativeTime } from "@/lib/time";
import { findNode } from "@/lib/tree";
import { useCollection } from "@/state/collection-context";
import { useDocuments } from "@/state/documents-context";
import { useDiff } from "@/state/diff-context";
import { useOrder } from "@/state/order-context";
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
 *
 * **The tab strip spans everything right of the sidebar**, which is a
 * deliberate deviation from screen 1a — the design draws it over the centre
 * pane only, with the response pane's status line level with it. Two nested
 * panel groups are what make that possible: the sidebar divides the outer one,
 * the tab strip sits below that divide, and the centre and response panes
 * divide the inner one underneath it. The reason is that the tabs kept
 * overflowing while half the window's width sat unused beside them, and the
 * response pane belongs to the active tab's request anyway — so the strip that
 * names the document should span the document. DESIGN-NOTES §9.19 records it.
 */

const SIDEBAR_DEFAULT = 260;
const SIDEBAR_MIN = 200;
const SIDEBAR_MAX = 420;
const CENTER_MIN = 320;
const RESPONSE_MIN = 280;
/** The response pane's share of what is left after the sidebar, on first run. */
const RESPONSE_DEFAULT_FRACTION = 0.4;

export function AppShell({ children }: { children: ReactNode }) {
  const { settings, savePanes } = useSettings();
  const { closeActive, reopenLastClosed, openTab, tabs } = useTabs();
  const { saveActive } = useDocuments();
  const { send } = useSends();
  const { tree, openViaDialog, close: closeCollection } = useCollection();
  const { environments } = useEnvironments();
  const { overview } = useDiff();
  const { undo: undoOrder } = useOrder();
  const { runFor, start } = useRuns();
  const routeDocument = useRouteDocument();
  // Read inside rememberResponse, which is registered once and must not be
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
  // One wrapper per group, so each layout callback can measure its own group
  // and only its own separator. A single ref around both would find the other
  // group's handle too and mis-measure by a pixel every time.
  const innerGroup = useRef<HTMLDivElement>(null);
  const sidebarPanel = useRef<PanelImperativeHandle>(null);
  const responsePanel = useRef<PanelImperativeHandle>(null);
  const sidebarPane = useRef<HTMLDivElement>(null);
  const centerPane = useRef<HTMLDivElement>(null);
  const responsePane = useRef<HTMLDivElement>(null);
  const filterInput = useRef<HTMLInputElement>(null);
  // The tree's reveal handle, for the palette's ⇧↵.
  const treeHandle = useRef<TreeHandle | null>(null);
  // Leaving a collection closes every tab, and a draft lives only in the
  // window, so both ways out ask first when anything is unsaved.
  const [leaving, setLeaving] = useState<CollectionAction | null>(null);
  const dirtyCount = tabs.filter((tab) => tab.dirty).length;

  const runLeave = useCallback(
    async (action: CollectionAction) => {
      if (action === "open") await openViaDialog();
      else await closeCollection();
    },
    [openViaDialog, closeCollection],
  );

  // The gate, in one place rather than in each caller: an unsaved draft has
  // to be as safe from the palette as it is from the ⌘O menu item.
  const leaveCollection = useCallback(
    (action: CollectionAction) => {
      if (dirtyCount === 0) {
        void runLeave(action);
        return;
      }
      setLeaving(action);
    },
    [dirtyCount, runLeave],
  );

  const [paletteOpen, setPaletteOpen] = useState(false);
  // What the create dialog is making, and where. Null when it is closed.
  const [creating, setCreating] = useState<{ kind: CreateKind; folder: string } | null>(null);
  // The whole pane geometry, held here because it is now spread over two
  // nested groups: the sidebar's callback never sees the response pane's size
  // and vice versa, so each writes its own half in and then saves all of it.
  // Reading the other half back out of settings instead would lose a drag,
  // since the save is debounced and two drags can land before it lands.
  //
  // A collapsed pane is 0 wide, and recording that as its width would lose the
  // size to expand back to, so only a real width is ever written.
  const geometry = useRef({
    sidebar: 0,
    response: 0,
    sidebarCollapsed: false,
    responseCollapsed: false,
  });

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

  /**
   * The folder a new request or folder goes in when nothing names one: the
   * active document's own folder, which is where you were looking. A folder
   * document counts as itself rather than its parent — having just opened
   * `orders/`, "New request" means one in `orders/`.
   */
  const folderForNew = () => {
    if (!routeDocument) return "";
    if (routeDocument.kind === "folder") return routeDocument.path;
    if (routeDocument.kind === "request") return nodeParentPath(routeDocument.path);
    return "";
  };

  const openCreate = useCallback((kind: CreateKind, folder: string) => {
    setCreating({ kind, folder });
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

  /**
   * A node the operating system asked for: a .http file double-clicked in
   * Finder or Explorer, a path on the command line (`otis .`), a second
   * launch forwarding its arguments, or a file dropped on the window.
   *
   * Go has already opened the collection by the time we get here, so all that
   * is left is to show the node. This lives in the shell because navigation
   * does; CollectionService owns everything up to the point where a route has
   * to change.
   *
   * Taken on mount *and* on the event, and the two cannot both act because
   * TakePendingOpen clears as it reads. Both are needed: at launch the target
   * exists before this component does — Wails raises its runtime-ready event
   * before React has mounted, so a listener alone silently misses every
   * double-click that started the app — and with the window already open
   * nothing is going to mount, so a mount-time call alone would miss every
   * open after the first.
   */
  const showPending = useCallback(async () => {
    const target = await CollectionService.TakePendingOpen().catch(() => null);
    if (!target || !target.kind) return;
    const kind = target.kind === "request" ? "request" : "folder";
    openTab(target.node, kind, { activate: true });
    void navigate({ to: nodeRoute(kind), params: { path: target.node } });
  }, [navigate, openTab]);

  useEffect(() => {
    void showPending();
    return Events.On(OtisEvent.OpenNode, () => void showPending());
  }, [showPending]);

  // The macOS File menu emits this rather than opening a collection itself,
  // so the confirmation in front of unsaved drafts is the same one the palette
  // gets. Menu key equivalents are handled before the webview sees them, which
  // is also why ⌘O is not in the keymap below on macOS.
  useEffect(() => {
    const off = Events.On(OtisEvent.OpenCollectionRequested, () => leaveCollection("open"));
    return () => off();
  }, [leaveCollection]);

  useKeymap([
    { key: "k", mod: true, run: () => setPaletteOpen(true) },
    // ⌘O / Ctrl+O. Bound here only where nothing else claims it: on macOS the
    // File menu's accelerator wins before the key reaches the window, and
    // binding it in both places would open two directory dialogs.
    ...(isMac() ? [] : [{ key: "o", mod: true, run: () => leaveCollection("open") }]),
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
    // ⌘Z takes back the last reorder (screen 2a's strip offers the same
    // thing). It is the shell's, not the tree's, so it works with focus
    // anywhere; nothing to undo is nothing happening, not a refusal. It does
    // not undo an edit in the request editor — CodeMirror handles ⌘Z itself
    // and consumes it before the window sees it, which is exactly the
    // separation we want: undo in a text field is the field's.
    { key: "z", mod: true, run: () => void undoOrder() },
    { key: "1", mod: true, run: () => focusPane(sidebarPanel.current, sidebarPane.current) },
    { key: "2", mod: true, run: () => centerPane.current?.focus() },
    { key: "3", mod: true, run: () => focusPane(responsePanel.current, responsePane.current) },
  ]);

  // Restore the collapsed panes once, after the panels exist, and seed the
  // geometry ref with what was saved — otherwise the first drag of one pane
  // would save a zero for the other and collapse it on the next launch.
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !settings || !groupWidth) return;
    restored.current = true;
    geometry.current = {
      sidebar: settings.panes.sidebarWidth,
      response: settings.panes.responseWidth,
      sidebarCollapsed: settings.panes.sidebarCollapsed,
      responseCollapsed: settings.panes.responseCollapsed,
    };
    if (settings.panes.sidebarCollapsed) sidebarPanel.current?.collapse();
    if (settings.panes.responseCollapsed) responsePanel.current?.collapse();
  }, [settings, groupWidth]);

  // Persisting the layout hangs off each group's "layout changed" callback
  // rather than each panel's onResize, which does not fire when a separator is
  // what moved. The sizes come from the callback's own argument and not from
  // the panel handles: getSize() still reports the previous layout at this
  // point, which persists every change one step behind.
  const persist = useCallback(() => {
    const g = geometry.current;
    savePanes({
      sidebarWidth: g.sidebar,
      responseWidth: g.response,
      sidebarCollapsed: g.sidebarCollapsed,
      responseCollapsed: g.responseCollapsed,
    });
  }, [savePanes]);

  const rememberSidebar = useCallback(
    (layout: Layout) => {
      const pixels = panelPixels(group.current, layout, "sidebar");
      if (pixels === null) return;
      geometry.current.sidebarCollapsed = pixels === 0;
      if (pixels > 0) geometry.current.sidebar = pixels;
      persist();
    },
    [persist],
  );

  const rememberResponse = useCallback(
    (layout: Layout) => {
      // On the diff route the response pane is not mounted at all, so the
      // layout has nothing to say about it. Persisting it here would record a
      // width of zero and the pane would come back collapsed.
      if (onDiffRef.current) return;
      const pixels = panelPixels(innerGroup.current, layout, "response");
      if (pixels === null) return;
      geometry.current.responseCollapsed = pixels === 0;
      if (pixels > 0) geometry.current.response = pixels;
      persist();
    },
    [persist],
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
          onLayoutChanged={rememberSidebar}
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
                revealRef={treeHandle}
                onCreate={openCreate}
              />
            </div>
          </ResizablePanel>

          <PaneHandle />

          {/* Everything right of the sidebar: the tab strip, then the panes it
              names. The minimum has to cover both inner panes, or a wide
              sidebar could squeeze the inner group below their own minimums —
              which the inner group cannot then honour. */}
          <ResizablePanel id="documents" minSize={onDiff ? CENTER_MIN : CENTER_MIN + RESPONSE_MIN}>
            <div className="flex h-full min-h-0 flex-col">
              <TabBar onNewRequest={() => openCreate("request", folderForNew())} />

              <div ref={innerGroup} className="min-h-0 flex-1">
                <ResizablePanelGroup
                  orientation="horizontal"
                  className="h-full"
                  onLayoutChanged={rememberResponse}
                >
                  <ResizablePanel id="center" minSize={CENTER_MIN}>
                    <div
                      ref={centerPane}
                      tabIndex={-1}
                      className="flex h-full flex-col outline-none"
                    >
                      <div className="min-h-0 flex-1 overflow-auto">{children}</div>
                    </div>
                  </ResizablePanel>

                  {/* Screen 1b has no response pane: the diff takes the full
                      width beside the sidebar. The panel is unmounted rather
                      than collapsed, so its saved width survives — a collapse
                      would persist as one. */}
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
            </div>
          </ResizablePanel>
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

      {/* An agent's confirmation. Mounted here beside the other dialogs so it
          appears over whatever screen is open: a Go tool call is blocked on
          it, and a dialog that only existed on one route would be one an
          agent could get past by asking while you were on another. */}
      <AgentConfirmDialog />

      <CreateDialog
        kind={creating?.kind ?? null}
        folder={creating?.folder ?? ""}
        onClose={() => setCreating(null)}
        // Go returns the node path it actually used, which may carry a -2 the
        // dialog's preview could not know about, so the navigation follows
        // Go rather than the preview.
        onCreated={(nodePath, kind) => {
          openTab(nodePath, kind, { activate: true });
          void navigate({ to: nodeRoute(kind), params: { path: nodePath } });
        }}
      />

      <CollectionSwitchDialog
        action={leaving}
        dirtyCount={dirtyCount}
        onAnswer={(proceed) => {
          const action = leaving;
          setLeaving(null);
          if (proceed && action) void runLeave(action);
        }}
      />

      <CommandPalette
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        onReveal={(path) => {
          // ⇧↵ shows where a request lives without opening it: the sidebar
          // expands to it and the row is marked, and the centre pane is left
          // alone.
          sidebarPanel.current?.expand();
          treeHandle.current?.reveal(path);
        }}
        onCreate={(kind) => openCreate(kind, folderForNew())}
        onLeaveCollection={leaveCollection}
      />
    </>
  );
}

/**
 * The divider between two panes: the 1px --border the design already draws
 * between panes, widened to a 7px hit area and lightened to --border-strong
 * while it is being used.
 */
/**
 * A panel's width in pixels, from a group's layout percentages.
 *
 * A layout percentage is a share of what the panels have between them, which
 * is the group minus its separators. Measuring the separators rather than
 * assuming 1px keeps the round trip exact: without it the saved width drifts a
 * pixel per restart.
 *
 * `wrapper` is the div wrapping one group, and the query is scoped to that
 * group's own direct children — the shell nests two groups, and a loose
 * selector would count the other one's separator as well.
 */
function panelPixels(wrapper: HTMLDivElement | null, layout: Layout, id: string): number | null {
  const group = wrapper?.querySelector(':scope > [data-slot="resizable-panel-group"]');
  if (!(group instanceof HTMLElement)) return null;
  let total = group.clientWidth;
  for (const separator of group.querySelectorAll(
    ':scope > [data-slot="resizable-handle"]',
  )) {
    total -= separator.getBoundingClientRect().width;
  }
  if (total <= 0) return null;
  return Math.round(((layout[id] ?? 0) / 100) * total);
}

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
