import { GitBranch } from "lucide-react";

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
}: {
  git: GitState | null;
  file?: string | null;
  gitStatus?: string | null;
  context?: string | null;
}) {
  return (
    <footer className="flex h-[var(--status-bar-height)] shrink-0 items-center gap-4 border-t border-border bg-background px-3 font-mono text-meta text-fg-dim">
      <div className="flex w-[260px] shrink-0 items-center gap-2 truncate">
        <Branch git={git} />
      </div>

      <div className="flex min-w-0 flex-1 items-center justify-center gap-2">
        {file ? (
          <>
            <span className="truncate">{file}</span>
            <StatusLetter status={gitStatus} />
          </>
        ) : (
          <Empty />
        )}
      </div>

      <div className="flex w-[260px] shrink-0 items-center justify-end gap-2 truncate">
        {context ?? <Empty />}
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
