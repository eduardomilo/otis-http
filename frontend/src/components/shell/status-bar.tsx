import { cn } from "@/lib/utils";

/**
 * The status bar: 26px, full width, mono 11px in --fg-dim
 * (DESIGN-NOTES §4.1, §3).
 *
 * Deviation from the design: screen 1a puts the branch in a footer under the
 * sidebar tree and runs the status bar across the centre pane only. Phase B
 * runs one bar the full width of the window and gives the branch its left
 * slot, so the three slots below (branch, file, context) have one home. The
 * type, height and colours are unchanged.
 *
 * Every slot is filled in later: the branch and the git letter in increment 9,
 * the context line in Phase C.
 */
export function StatusBar({
  branch,
  file,
  gitStatus,
  context,
}: {
  branch?: string | null;
  file?: string | null;
  gitStatus?: string | null;
  context?: string | null;
}) {
  return (
    <footer className="flex h-[var(--status-bar-height)] shrink-0 items-center gap-4 border-t border-border bg-background px-3 font-mono text-meta text-fg-dim">
      <Slot className="w-[220px] shrink-0">{branch ?? <Empty />}</Slot>

      <div className="flex min-w-0 flex-1 items-center justify-center gap-2">
        {file ? (
          <>
            <span className="truncate">{file}</span>
            {gitStatus ? <span className="text-modified">{gitStatus}</span> : null}
          </>
        ) : (
          <Empty />
        )}
      </div>

      <Slot className="w-[220px] shrink-0 justify-end">{context ?? <Empty />}</Slot>
    </footer>
  );
}

function Slot({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn("flex items-center gap-2 truncate", className)}>{children}</div>;
}

/** An unfilled slot. Dim enough to read as "not known yet", not as content. */
function Empty() {
  return <span className="text-fg-ghost">—</span>;
}
