import { GitBranch } from "lucide-react";

import { ActivityLog } from "@/components/shell/activity-log";

import { cn } from "@/lib/utils";
import type { State as GitState } from "@bindings/internal/git";

/**
 * The status bar: 26px, full width, mono 11px in --fg-dim
 * (DESIGN-NOTES §4.1, §3).
 *
 * Deviation from the design: screen 1a puts the branch in a footer under the
 * sidebar tree and runs the status bar across the centre pane only. Phase B
 * runs one bar the full width of the window and gives the branch its left
 * slot, so the three slots below (branch, file, context) have one home. The
 * type, height and colours are unchanged.
 */
export function StatusBar({
  git,
  file,
  gitStatus,
  context,
  totals,
  onShowChanges,
}: {
  git: GitState | null;
  file?: string | null;
  gitStatus?: string | null;
  context?: string | null;
  /** The diff view's "+4 −2 · 2 hunks" (screen 1b), when it is on screen. */
  totals?: { adds: number; dels: number; hunks: number } | null;
  /** Pressing the branch shows what changed. SCREENS.md notes the design
   *  never says how you get into the diff view; this is one of the two ways. */
  onShowChanges?: () => void;
}) {
  return (
    <footer className="flex h-[var(--status-bar-height)] shrink-0 items-center gap-4 border-t border-border bg-background px-[var(--edge-inset)] font-mono text-meta text-fg-dim">
      <div className="flex w-[260px] shrink-0 items-center gap-2 truncate">
        {onShowChanges && git?.repository ? (
          <button
            type="button"
            onClick={onShowChanges}
            title="Show what changed (⌘G)"
            className="flex min-w-0 items-center gap-2 truncate rounded-sm hover:text-fg"
          >
            <Branch git={git} />
          </button>
        ) : (
          <Branch git={git} />
        )}
      </div>

      <div className="flex min-w-0 flex-1 items-center justify-center gap-2">
        {totals ? (
          <>
            <span className="text-primary">+{totals.adds}</span>
            <span className="text-destructive">−{totals.dels}</span>
            <span className="text-fg-faint">·</span>
            <span>
              {totals.hunks} {totals.hunks === 1 ? "hunk" : "hunks"}
            </span>
            {file ? <span className="truncate text-fg-faint">{file}</span> : null}
          </>
        ) : file ? (
          <>
            <span className="truncate">{file}</span>
            <StatusLetter status={gitStatus} />
          </>
        ) : (
          <Empty />
        )}
      </div>

      {/* The context slot has a floor rather than a fixed width: screen 1b's
          summary ("Last commit: … · 2h ago · you") is much longer than screen
          1c's, and truncating it to 260px loses the part that identifies the
          commit. */}
      <div className="flex min-w-[260px] max-w-[46%] shrink items-center justify-end gap-2 truncate">
        <span className="truncate">{context ?? <Empty />}</span>
        {/* Last in the bar, after the context: it is the only thing here that
            is a control rather than a statement, and it stays quiet until
            something has gone wrong. */}
        <ActivityLog />
      </div>
    </footer>
  );
}

/**
 * Branch, then the counts: modified files in amber, untracked in the accent,
 * and ahead/behind in --fg-faint (SCREENS.md, "Chrome shared by every screen").
 */
function Branch({ git }: { git: GitState | null }) {
  if (!git?.repository) {
    // Not a repository is a normal state, not an error: a collection is a
    // directory of files and works perfectly well outside version control.
    return <Empty />;
  }

  const statuses = Object.values(git.statuses ?? {});
  const modified = statuses.filter((s) => s === "M" || s === "D").length;
  const untracked = statuses.filter((s) => s === "U" || s === "A").length;

  return (
    <>
      <GitBranch className="size-3 shrink-0 text-fg-faint" />
      <span className="truncate text-fg">{git.detached ? git.head : git.branch}</span>
      {modified > 0 ? <span className="text-modified">{modified}</span> : null}
      {untracked > 0 ? <span className="text-primary">{untracked}</span> : null}
      {git.hasUpstream && git.ahead > 0 ? (
        <span className="text-fg-faint">↑{git.ahead}</span>
      ) : null}
      {git.hasUpstream && git.behind > 0 ? (
        <span className="text-fg-faint">↓{git.behind}</span>
      ) : null}
      {statuses.length === 0 ? <span className="text-primary">clean</span> : null}
    </>
  );
}

function StatusLetter({ status }: { status?: string | null }) {
  if (!status) return null;
  return (
    <span className={cn(status === "U" || status === "A" ? "text-primary" : "text-modified")}>
      {status}
    </span>
  );
}

/** An unfilled slot. Dim enough to read as "not known yet", not as content. */
function Empty() {
  return <span className="text-fg-ghost">—</span>;
}
