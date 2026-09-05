import { Outlet, createRootRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/shell/app-shell";
import { EmptyState } from "@/components/shell/empty-state";
import { TitleStrip } from "@/components/shell/title-strip";
import { TooltipProvider } from "@/components/ui/tooltip";
import { isMac } from "@/lib/platform";
import { CollectionProvider, useCollection } from "@/state/collection-context";
import { DocumentsProvider } from "@/state/documents-context";
import { DiffProvider } from "@/state/diff-context";
import { LogProvider } from "@/state/log-context";
import { ScriptsProvider } from "@/state/scripts-context";
import { EnvironmentProvider } from "@/state/environment-context";
import { MCPProvider } from "@/state/mcp-context";
import { OrderProvider } from "@/state/order-context";
import { RunProvider } from "@/state/run-context";
import { SendProvider } from "@/state/send-context";
import { SettingsProvider } from "@/state/settings-context";
import { TabsProvider } from "@/state/tabs-context";
import { abbreviatePath, useHomeDir } from "@/state/use-recents";

export const Route = createRootRoute({
  component: RootLayout,
});

/**
 * The window.
 *
 * With a collection open this is the three-pane shell (screen 1a) and the
 * route renders into the centre pane. With none open it is the empty state
 * (screen 2b), which has no sidebar and no panes, whatever the route says —
 * every other route needs a collection to mean anything.
 *
 * `data-file-drop-target` makes the whole window a drop target; Go handles
 * the drop and opens the directory (see CollectionService).
 */
function RootLayout() {
  return (
    <SettingsProvider>
      {/* The activity log is outermost after settings, and takes nothing from
          any of them: everything below it can report a failure, including the
          providers themselves, and a log that could only be written from
          inside the collection would be silent for exactly the failures that
          stop one opening. */}
      <LogProvider>
      <CollectionProvider>
        {/* The agent server sits high and outside everything else: its
            confirmation dialog has to be reachable whatever is open, and it is
            deliberately not below Tabs or Documents — an agent's send is not
            about the active tab, and a dialog that could only appear over one
            screen would be a dialog an agent could get past by asking while
            you were on another. */}
        <MCPProvider>
        {/* Environments sit above Tabs: which one is active decides how every
            document resolves, so both the editor and the sender read it from
            here. */}
        <EnvironmentProvider>
          {/* Ordering sits above the documents and beside the diff: a reorder
              writes a file in the repository, so the tree, the diff and ⌘Z all
              have to be looking at the same last-change. It needs only the
              collection. */}
          <OrderProvider>
          {/* The diff view sits beside the documents rather than inside them:
              it is a view of the whole collection, and a stage or a commit
              changes what every other view says about a file. */}
          <DiffProvider>
              <TabsProvider>
                {/* Documents sit inside Tabs: a draft is what makes a tab dirty,
                    and the veto on closing a dirty tab is installed from here. */}
                <DocumentsProvider>
                  {/* Scripts sit beside the request documents rather than
                      inside them: a `.js` is text, a request is a parsed
                      model, and both are tabs. Both register a close guard,
                      which is why that is a list. */}
                  <ScriptsProvider>
                  {/* Folder runs sit *inside* the documents, not beside them.
                      Their results still outlive whichever tab started a run —
                      DocumentsProvider is one provider for the collection, not
                      one per tab — and being inside is what lets a run write
                      the drafts of the requests it is about to send, the same
                      way a single send does. Nothing above here consumes
                      useRuns; the folder view is its only reader. */}
                  <RunProvider>
                  {/* Sends sit inside Tabs too: the response pane shows whatever
                      the active tab is showing. */}
                  <SendProvider>
                    {/* One provider for every tooltip in the window: the tree's
                        git dots, parse errors and folder-settings markers all use
                        it. */}
                    <TooltipProvider delayDuration={400}>
                      <Window />
                    </TooltipProvider>
                  </SendProvider>
                  </RunProvider>
                  </ScriptsProvider>
                </DocumentsProvider>
              </TabsProvider>
          </DiffProvider>
          </OrderProvider>
        </EnvironmentProvider>
        </MCPProvider>
      </CollectionProvider>
      </LogProvider>
    </SettingsProvider>
  );
}

function Window() {
  const { collection, isOpen } = useCollection();
  const home = useHomeDir();

  return (
    <div
      data-file-drop-target
      className="flex h-screen flex-col overflow-hidden bg-background text-foreground"
    >
      <TitleStrip
        name={isOpen ? collection!.name : null}
        path={isOpen ? abbreviatePath(collection!.path, home) : null}
        reserveTrafficLights={isMac()}
      />

      {isOpen ? (
        <AppShell>
          <Outlet />
        </AppShell>
      ) : (
        <EmptyState />
      )}
    </div>
  );
}
