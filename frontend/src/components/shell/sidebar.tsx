import { forwardRef, useMemo, useState } from "react";

import { ChangesList } from "@/components/diff/changes-list";
import { EnvironmentList } from "@/components/environment/environment-list";
import { OrderStrip } from "@/components/shell/order-strip";
import { type NodeAction } from "@/components/shell/node-actions";
import { Tree, type TreeHandle } from "@/components/shell/tree";
import { Input } from "@/components/ui/input";
import { hint } from "@/lib/platform";
import { filterTree } from "@/lib/tree";
import { verbatimText } from "@/lib/text-input";
import { useCollection } from "@/state/collection-context";
import type { Node } from "@bindings/internal/services";

/**
 * The sidebar: a filter input and the request tree (DESIGN-NOTES §4.1, 10px
 * of horizontal padding).
 *
 * On an environment route it is the environment list (screen 1c) and on a
 * diff route the changes list (screen 1b): the sidebar is the navigator for
 * whatever kind of document the centre pane is showing, and there is no
 * request tree to filter while you are reading a diff or editing
 * `env/staging.json`.
 */
export const Sidebar = forwardRef<
  HTMLInputElement,
  {
    activePath: string;
    environment?: string | null;
    diff?: boolean;
    /** Filled in with the tree's reveal handle, for the palette's ⇧↵. */
    revealRef?: React.RefObject<TreeHandle | null>;
    /** Opens the create dialog for a folder in the tree. */
    onCreate: (kind: "request" | "folder", folder: string) => void;
    onManage: (action: NodeAction, node: Node) => void;
  }
>(function Sidebar({ activePath, environment, diff, revealRef, onCreate, onManage }, filterRef) {
  const { tree } = useCollection();
  const [query, setQuery] = useState("");

  const filter = useMemo(() => (tree ? filterTree(tree.root, query) : undefined), [tree, query]);

  if (diff) {
    return <ChangesList activePath={activePath} />;
  }
  if (environment !== null && environment !== undefined) {
    return <EnvironmentList activeName={environment} />;
  }

  return (
    <div className="flex h-full flex-col bg-background px-2.5">
      <div className="flex h-12 shrink-0 items-center">
        <div className="relative w-full">
          <Input
            {...verbatimText}
            ref={filterRef}
            type="text"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            // Escape clears the filter. This is the one key handled outside
            // useKeymap, because it is a text field's own behaviour rather
            // than a shortcut — and useKeymap deliberately never fires while
            // focus is in a field.
            onKeyDown={(event) => {
              if (event.key !== "Escape") return;
              event.preventDefault();
              if (query === "") event.currentTarget.blur();
              else setQuery("");
            }}
            placeholder="Filter requests"
            aria-label="Filter requests"
            // The webview offers its own autofill list over the tree
            // otherwise; this is a filter, not a form field.
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
            // The dark: and md: overrides are the shadcn Input's own
            // defaults; the design has one palette and one 12px size.
            className="h-[26px] rounded-md border-border-control bg-inset pr-12 text-ui md:text-ui dark:bg-inset placeholder:text-fg-dim"
          />
          <span className="pointer-events-none absolute inset-y-0 right-2 flex items-center rounded-sm border border-border-control px-1 font-mono text-label text-fg-faint">
            {hint("P")}
          </span>
        </div>
      </div>

      {tree ? (
        <Tree
          tree={tree}
          filter={filter}
          activePath={activePath}
          revealRef={revealRef}
          onCreate={onCreate}
          onManage={onManage}
        />
      ) : (
        <p className="px-1 py-2 text-meta text-fg-faint">Reading the collection…</p>
      )}

      {/* Screen 2a's confirmation, below the tree: "Order saved to
          orders/.order" with Undo. It renders nothing when there is nothing to
          report, so the tree keeps the full height the rest of the time. */}
      <OrderStrip />
    </div>
  );
});
