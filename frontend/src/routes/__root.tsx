import { Outlet, createRootRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/shell/app-shell";
import { EmptyState } from "@/components/shell/empty-state";
import { TitleStrip } from "@/components/shell/title-strip";
import { TooltipProvider } from "@/components/ui/tooltip";
import { isMac } from "@/lib/platform";
import { CollectionProvider, useCollection } from "@/state/collection-context";
import { DocumentsProvider } from "@/state/documents-context";
import { DiffProvider } from "@/state/diff-context";
import { EnvironmentProvider } from "@/state/environment-context";
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
      <CollectionProvider>
        {/* Environments sit above Tabs: which one is active decides how every
            document resolves, so both the editor and the sender read it from
            here. */}
        <EnvironmentProvider>
          {/* The diff view sits beside the documents rather than inside them:
              it is a view of the whole collection, and a stage or a commit
              changes what every other view says about a file. */}
          <DiffProvider>
            {/* Folder runs sit beside the documents too: a run is a sequence
                of sends against a folder, and its results outlive whichever
                tab happened to start it. */}
            <RunProvider>
              <TabsProvider>
                {/* Documents sit inside Tabs: a draft is what makes a tab dirty,
                    and the veto on closing a dirty tab is installed from here. */}
                <DocumentsProvider>
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
                </DocumentsProvider>
              </TabsProvider>
            </RunProvider>
          </DiffProvider>
        </EnvironmentProvider>
      </CollectionProvider>
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
