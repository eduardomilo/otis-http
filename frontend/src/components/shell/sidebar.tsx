import { forwardRef, useMemo, useState } from "react";

import { EnvironmentList } from "@/components/environment/environment-list";
import { Tree } from "@/components/shell/tree";
import { Input } from "@/components/ui/input";
import { hint } from "@/lib/platform";
import { filterTree } from "@/lib/tree";
import { useCollection } from "@/state/collection-context";

/**
 * The sidebar: a filter input and the request tree (DESIGN-NOTES §4.1, 10px
 * of horizontal padding).
 *
 * On an environment route it is the environment list instead, which is what
 * screen 1c shows: the sidebar is the navigator for whatever kind of document
 * the centre pane is showing, and there is no request tree to filter while you
 * are editing `env/staging.json`.
 */
export const Sidebar = forwardRef<
  HTMLInputElement,
  { activePath: string; environment?: string | null }
>(function Sidebar({ activePath, environment }, filterRef) {
  const { tree } = useCollection();
  const [query, setQuery] = useState("");

  const filter = useMemo(() => (tree ? filterTree(tree.root, query) : undefined), [tree, query]);

  if (environment !== null && environment !== undefined) {
    return <EnvironmentList activeName={environment} />;
  }

  return (
    <div className="flex h-full flex-col bg-background px-2.5">
      <div className="flex h-12 shrink-0 items-center">
        <div className="relative w-full">
          <Input
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
        <Tree tree={tree} filter={filter} activePath={activePath} />
      ) : (
        <p className="px-1 py-2 text-meta text-fg-faint">Reading the collection…</p>
      )}
    </div>
  );
});
