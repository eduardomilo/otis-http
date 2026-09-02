import { Folder, X } from "lucide-react";

import { methodColor } from "@/lib/method";
import { nodeDisplayName } from "@/lib/paths";
import { findNode } from "@/lib/tree";
import { cn } from "@/lib/utils";
import { useCollection } from "@/state/collection-context";
import type { Tab } from "@/state/tabs-context";
import type { Node } from "@bindings/internal/services";
import { useTabs } from "@/state/tabs-context";

/**
 * The document tab bar: 34px, one tab per open request or folder
 * (DESIGN-NOTES §4.1, screen 1a).
 *
 * Hand-rolled rather than Radix Tabs, which models one-of-N panel switching
 * and cannot do closeable, overflowing document tabs (DESIGN-NOTES §7.3).
 *
 * The active tab takes --bg and an accent underline; the inactive ones sit on
 * the strip. A dirty tab shows an amber dot *in place of* the close ×, as in
 * screen 1a — nothing marks a tab dirty until the editor lands in Phase C.
 */
export function TabBar() {
  const { tabs, activePath, closeTab, openTab } = useTabs();
  const { tree } = useCollection();

  return (
    <div className="flex h-[var(--tab-bar-height)] shrink-0 items-stretch overflow-x-auto border-b border-border bg-background">
      {tabs.map((tab) => (
        <TabButton
          key={tab.path}
          tab={tab}
          // The method and the display name come from the tree rather than
          // from the path: docs/FORMAT.md §2.1 prefers the @name directive
          // over the file name, and only the parsed file knows it.
          node={tree ? findNode(tree.root, tab.path) : undefined}
          active={tab.path === activePath}
          onActivate={() => openTab(tab.path, tab.kind)}
          onClose={() => void closeTab(tab.path)}
        />
      ))}
    </div>
  );
}

function TabButton({
  tab,
  node,
  active,
  onActivate,
  onClose,
}: {
  tab: Tab;
  node: Node | undefined;
  active: boolean;
  onActivate: () => void;
  onClose: () => void;
}) {
  const label = node?.name ?? nodeDisplayName(tab.path);
  return (
    <div
      role="tab"
      aria-selected={active}
      tabIndex={-1}
      onClick={onActivate}
      // Middle-click closes, the way every editor does it. The auxclick
      // listener is on the tab rather than the close button so the whole tab
      // is the target.
      onAuxClick={(event) => {
        if (event.button === 1) {
          event.preventDefault();
          onClose();
        }
      }}
      className={cn(
        "group flex min-w-0 shrink-0 cursor-default items-center gap-2 border-r border-border px-3 select-none",
        active
          ? "bg-background text-fg-emphasis shadow-[inset_0_-1px_0_var(--accent)]"
          : "text-fg-muted hover:bg-control",
      )}
    >
      {tab.kind === "folder" ? (
        <Folder className="size-3 shrink-0 text-fg-muted" />
      ) : (
        <span
          className={cn(
            "shrink-0 font-mono text-label font-medium tracking-[.02em]",
            methodColor(node?.method),
          )}
        >
          {node?.method}
        </span>
      )}

      <span className={cn("truncate text-ui", active && "font-medium")}>{label}</span>

      {tab.dirty ? (
        // Screen 1a: an amber dot *in place of* the close ×.
        <span
          aria-label="Unsaved changes"
          title="Unsaved changes"
          className="size-1.5 shrink-0 rounded-full bg-modified"
        />
      ) : (
        <button
          type="button"
          aria-label={`Close ${label}`}
          onClick={(event) => {
            event.stopPropagation();
            onClose();
          }}
          className="shrink-0 text-fg-faint hover:text-fg-emphasis"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}
