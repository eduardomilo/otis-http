import { useEffect } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";

import { hint } from "@/lib/platform";
import { nodeLink } from "@/lib/paths";
import { useSettings } from "@/state/settings-context";
import { useTabs } from "@/state/tabs-context";

export const Route = createFileRoute("/")({
  component: Home,
});

/**
 * The route with no document.
 *
 * With no collection open the root layout renders the empty state instead of
 * this, so reaching here means a collection is open but nothing is selected.
 * If the last session left a document open, go straight back to it.
 */
function Home() {
  const navigate = useNavigate();
  const { settings } = useSettings();
  const { tabs } = useTabs();

  useEffect(() => {
    const active = settings?.tabs.active;
    if (!active) return;
    const tab = tabs.find((t) => t.path === active);
    if (!tab) return;
    void navigate({ ...nodeLink(tab.kind, tab.path), replace: true });
  }, [settings?.tabs.active, tabs, navigate]);

  return (
    <div className="flex h-full items-center justify-center">
      <p className="text-ui text-fg-faint">
        Open a request from the sidebar, or press {hint("K")}.
      </p>
    </div>
  );
}
