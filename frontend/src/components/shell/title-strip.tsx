import { Link } from "@tanstack/react-router";

import { AgentChip } from "@/components/shell/agent-chip";
import { EnvironmentChip } from "@/components/shell/environment-chip";
import { nodeLink } from "@/lib/paths";
import { cn } from "@/lib/utils";

/**
 * The title strip: 38px, at the top of the content area on every platform
 * (DESIGN-NOTES §4.1, SCREENS.md "Chrome shared by every screen").
 *
 * On macOS the native title bar is hidden and the traffic lights are inset
 * into this strip, so the left of it is reserved for them. On Windows and
 * Linux the window has its ordinary frame and this strip sits below it, with
 * nothing to reserve.
 *
 * **The strip is 52px on macOS and 38px everywhere else**, which is not a
 * design choice — AppKit puts the lights at the vertical centre of a standard
 * toolbar band and Wails offers no way to move them, so at 38px they sat 8px
 * below this strip's own text and ~4px above the row beneath. The slot is
 * 80px wide for the same reason: that is what three 12pt buttons and their
 * insets actually occupy, and 52px was a guess that happened not to collide.
 * DESIGN-NOTES §9.44.
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
      {/* The traffic lights' own width, measured rather than assumed. */}
      <div className={cn("shrink-0", reserveTrafficLights ? "w-[80px]" : "w-2")} />

      <div className="flex min-w-0 flex-1 items-center justify-center gap-2 px-2">
        {name ? (
          <>
            {/* The name opens the collection root's folder view — its shared
                auth, headers and variables. The design puts the collection's
                identity here and the root is not a tree row, so the name is
                the obvious thing to click and did nothing (§9.40). The strip
                is the window's drag handle, hence `no-drag`. */}
            <Link
              {...nodeLink("folder", "")}
              title="Shared settings for the whole collection"
              style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
              className="truncate rounded-sm px-1 text-ui text-fg-muted hover:bg-control hover:text-fg-emphasis"
            >
              {name}
            </Link>
            <span className="text-fg-faint">·</span>
            <span className="truncate font-mono text-ui text-fg-dim">{path}</span>
          </>
        ) : (
          <span className="text-ui text-fg-dim">No collection open</span>
        )}
      </div>

      {/* Right of the collection name and left of the environment selector:
          the two together read as the sentence that matters — which
          collection, and who else can reach it (DESIGN-NOTES §9.22). */}
      <AgentChip />
      <EnvironmentChip enabled={name !== null} />

      {/* The window's edge. On macOS the top-right corner is rounded and the
          environment chip was sitting a few pixels from the curve. */}
      <div className="w-[calc(var(--edge-inset)-0.75rem)] shrink-0" />
    </header>
  );
}
