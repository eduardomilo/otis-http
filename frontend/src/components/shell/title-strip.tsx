import { ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * The title strip: 38px, at the top of the content area on every platform
 * (DESIGN-NOTES §4.1, SCREENS.md "Chrome shared by every screen").
 *
 * On macOS the native title bar is hidden and the traffic lights are inset
 * into this strip, so the left 52px is reserved for them. On Windows and
 * Linux the window has its ordinary frame and this strip sits below it, with
 * nothing to reserve.
 *
 * The strip is the window's drag handle: `--wails-draggable: drag` inherits to
 * its children, so anything clickable inside opts back out with `no-drag`.
 */
export function TitleStrip({
  name,
  path,
  reserveTrafficLights,
}: {
  /** The collection's display name, or null when none is open. */
  name: string | null;
  /** The collection's path, home-relative, or null when none is open. */
  path: string | null;
  reserveTrafficLights: boolean;
}) {
  return (
    <header
      className="flex h-[var(--title-strip-height)] shrink-0 items-center border-b border-border bg-background"
      style={{ "--wails-draggable": "drag" } as React.CSSProperties}
    >
      {/* 52px traffic-light slot (DESIGN-NOTES §4.1). */}
      <div className={cn("shrink-0", reserveTrafficLights ? "w-[52px]" : "w-2")} />

      <div className="flex min-w-0 flex-1 items-center justify-center gap-2 px-2">
        {name ? (
          <>
            <span className="truncate text-ui text-fg-muted">{name}</span>
            <span className="text-fg-faint">·</span>
            <span className="truncate font-mono text-ui text-fg-dim">{path}</span>
          </>
        ) : (
          <span className="text-ui text-fg-dim">No collection open</span>
        )}
      </div>

      <EnvironmentChip />
    </header>
  );
}

/**
 * The environment selector. Environments are Phase C, so this is the disabled
 * chip screen 2b shows: an em dash and no menu. The design maps it to a
 * shadcn Select (DESIGN-NOTES §6) once there is something to select.
 */
function EnvironmentChip() {
  return (
    <div
      className="flex shrink-0 items-center gap-2 pr-3 pl-2"
      style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
    >
      <span className="text-meta text-fg-faint">env</span>
      <Button
        type="button"
        disabled
        title="Environments arrive in Phase C"
        className="h-6 rounded-md border border-border-control bg-control px-2 font-mono text-ui text-fg-dim hover:bg-control disabled:opacity-100"
      >
        —
        <ChevronDown className="size-3 text-fg-faint" />
      </Button>
    </div>
  );
}
