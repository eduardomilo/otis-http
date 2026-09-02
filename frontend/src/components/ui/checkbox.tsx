import * as React from "react";
import { Checkbox as CheckboxPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * Deviation from the shadcn default: the design specifies a 12px square with
 * radius 3px and a `--border-strong` edge when unchecked, filling with the
 * accent and drawing an 8px check stroked in `--bg` when checked
 * (DESIGN-NOTES §4.4). shadcn ships a 16px box with radius 4px, a
 * `--border-control` edge and a lucide CheckIcon.
 *
 * The check is inline SVG rather than a lucide icon because the design's
 * stroke is 8px wide inside a 12px box, which no icon size lands on, and
 * because a header table draws one of these per row.
 */
function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer flex size-3 shrink-0 items-center justify-center rounded-sm border border-border-strong outline-none transition-colors",
        "focus-visible:border-primary disabled:cursor-not-allowed disabled:opacity-50",
        "data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground",
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator data-slot="checkbox-indicator" className="grid place-content-center">
        <svg viewBox="0 0 8 8" className="size-2" aria-hidden>
          <path
            d="M1 4.2 3 6.2 7 1.8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

export { Checkbox };
