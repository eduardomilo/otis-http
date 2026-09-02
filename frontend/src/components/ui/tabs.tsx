import * as React from "react";
import { Tabs as TabsPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * The sub-tab strips: Params / Headers / Body / Auth / Scripts in the request
 * view, Body / Headers / Cookies / Tests in the response, Overview / Auth / …
 * in the folder view. Radix Tabs fits these exactly (DESIGN-NOTES §6); the
 * document tab bar does not and is hand-rolled (§7.3).
 *
 * Deviation from the shadcn default: shadcn draws a pill list on `--muted`
 * with a rounded active chip, and its "line" variant marks the active tab with
 * a 2px `--foreground` bar offset below the row. The design's strip is
 * transparent, sits on the pane's own bottom border, and marks the active tab
 * with a 1px accent border pulled onto that border with `margin-bottom: -1px`
 * (§2.4). Sizes come from the type scale, not Tailwind's `text-sm`.
 */
function Tabs({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      className={cn("flex min-h-0 flex-col", className)}
      {...props}
    />
  );
}

function TabsList({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn("flex shrink-0 items-stretch gap-4", className)}
      {...props}
    />
  );
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(
        "-mb-px flex cursor-default items-center gap-1.5 border-b border-transparent py-2 text-ui whitespace-nowrap text-fg-muted outline-none transition-colors",
        "hover:text-fg-secondary focus-visible:text-fg-emphasis",
        "data-active:border-primary data-active:font-medium data-active:text-fg-emphasis",
        className,
      )}
      {...props}
    />
  );
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("min-h-0 flex-1 outline-none", className)}
      {...props}
    />
  );
}

export { Tabs, TabsList, TabsTrigger, TabsContent };
