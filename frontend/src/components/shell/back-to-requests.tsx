import { useNavigate } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";

import { nodeLink } from "@/lib/paths";
import { useTabs } from "@/state/tabs-context";

/**
 * The way back to the request tree from a sidebar that has replaced it.
 *
 * The environment editor (screen 1c) and the diff view (screen 1b) both swap
 * the tree out for their own navigator, and neither screen in the design draws
 * anything that returns — so with no document tab open there was no way back
 * at all. ⌘K could still get you out, but a keyboard shortcut is not an
 * affordance, and "I cannot get back to my requests" is the report that found
 * this. DESIGN-NOTES §9.23 records the decision.
 *
 * It goes to the **active document** when there is one, so the way back
 * restores what you were looking at rather than dropping you on an empty
 * centre pane. With nothing open it goes to the collection root, which is the
 * tree plus "Open a request from the sidebar" — the same place a fresh launch
 * lands.
 */
export function BackToRequests() {
  const navigate = useNavigate();
  const { tabs, activePath } = useTabs();
  const active = tabs.find((tab) => tab.path === activePath);

  return (
    <button
      type="button"
      aria-label="Back to requests"
      title={active ? `Back to ${active.path}` : "Back to requests"}
      onClick={() => {
        if (active) {
          void navigate(nodeLink(active.kind, active.path));
          return;
        }
        void navigate({ to: "/" });
      }}
      className="flex size-6 shrink-0 items-center justify-center rounded-sm text-fg-faint hover:bg-selected hover:text-fg-emphasis"
    >
      <ChevronLeft className="size-4" aria-hidden />
    </button>
  );
}
