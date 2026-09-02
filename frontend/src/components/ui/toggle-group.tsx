import * as React from "react";
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * The segmented controls: Pretty/Raw in the response, Unified/Split in the
 * diff, Preview/Edit in the folder view (DESIGN-NOTES §6).
 *
 * Deviation from the shadcn default, which is a bordered group of `Button`s
 * with joined corners. The design's segmented control is smaller and quieter:
 * `padding: 2–3px 6–8px`, radius 3px, the active segment taking `--bg-control`
 * with a `--border-control` edge and the inactive ones a *transparent* border
 * so nothing shifts when the selection moves (§4.4).
 *
 * The `toggle` primitive shadcn installs alongside this is not used; only the
 * group is.
 */
function ToggleGroup({
  className,
  ...props
}: React.ComponentProps<typeof ToggleGroupPrimitive.Root>) {
  return (
    <ToggleGroupPrimitive.Root
      data-slot="toggle-group"
      className={cn("flex items-center gap-0.5", className)}
      {...props}
    />
  );
}

function ToggleGroupItem({
  className,
  ...props
}: React.ComponentProps<typeof ToggleGroupPrimitive.Item>) {
  return (
    <ToggleGroupPrimitive.Item
      data-slot="toggle-group-item"
      className={cn(
        "cursor-default rounded-sm border border-transparent px-1.5 py-0.5 text-ui text-fg-dim outline-none transition-colors",
        "hover:text-fg-secondary focus-visible:border-border-strong",
        "data-[state=on]:border-border-control data-[state=on]:bg-control data-[state=on]:text-fg-emphasis",
        className,
      )}
      {...props}
    />
  );
}

export { ToggleGroup, ToggleGroupItem };
