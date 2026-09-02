import { forwardRef } from "react";

import { Input } from "@/components/ui/input";
import { hint } from "@/lib/platform";

/**
 * The sidebar: a filter input and the request tree (DESIGN-NOTES §4.1, 10px
 * of horizontal padding).
 *
 * Increment 8 builds the frame only. The tree itself — rows with a method
 * gutter, git dots, virtualization — is increment 9.
 */
export const Sidebar = forwardRef<HTMLInputElement, { collectionName: string }>(
  function Sidebar({ collectionName }, filterRef) {
    return (
      <div className="flex h-full flex-col bg-background px-2.5">
        <div className="flex h-12 shrink-0 items-center">
          <div className="relative w-full">
            <Input
              ref={filterRef}
              type="text"
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

        <div className="min-h-0 flex-1 overflow-y-auto py-1">
          <p className="px-1 py-2 text-meta text-fg-faint">
            {collectionName} has no tree yet.
          </p>
        </div>
      </div>
    );
  },
);
