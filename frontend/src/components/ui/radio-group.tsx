import * as React from "react";
import { RadioGroup as RadioGroupPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * Deviation from the shadcn default: the design's radio is a 12px circle with
 * a `--border-strong` edge when unselected and a `--accent` edge plus a 6px
 * *accent* dot when selected (DESIGN-NOTES §4.4). shadcn fills the whole
 * circle with the accent and puts a `--accent-on` dot inside it, which at
 * 12px reads as a filled disc rather than a radio.
 *
 * The group is also not a grid: screen 4b stacks three cards with their own
 * spacing, and the selected one expands (DESIGN-NOTES §7.5).
 */
function RadioGroup({ className, ...props }: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return (
    <RadioGroupPrimitive.Root
      data-slot="radio-group"
      className={cn("flex w-full flex-col gap-2", className)}
      {...props}
    />
  );
}

function RadioGroupItem({
  className,
  ...props
}: React.ComponentProps<typeof RadioGroupPrimitive.Item>) {
  return (
    <RadioGroupPrimitive.Item
      data-slot="radio-group-item"
      className={cn(
        "group/radio relative flex aspect-square size-3 shrink-0 items-center justify-center rounded-full border border-border-strong outline-none transition-colors",
        "focus-visible:border-primary disabled:cursor-not-allowed disabled:opacity-50",
        "data-checked:border-primary",
        className,
      )}
      {...props}
    >
      <RadioGroupPrimitive.Indicator data-slot="radio-group-indicator" className="flex">
        <span className="size-1.5 rounded-full bg-primary" />
      </RadioGroupPrimitive.Indicator>
    </RadioGroupPrimitive.Item>
  );
}

export { RadioGroup, RadioGroupItem };
