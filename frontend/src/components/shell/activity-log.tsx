import { AlertTriangle, Info, Trash2 } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { relativeTime } from "@/lib/time";
import { cn } from "@/lib/utils";
import { useLog } from "@/state/log-context";
import type { LogEntry } from "@bindings/internal/services";

/**
 * The activity log, in the status bar's right slot.
 *
 * The design draws no toast and no console (DESIGN-NOTES §6 lists every
 * overlay it uses, and neither is among them), so a failure Otis could not
 * act on had nowhere to be seen: it went to a `console.error` in a webview
 * with no console. This is the somewhere, and it is a popover for the same
 * reason it is not a toast — a failure is usually noticed *after* it
 * happened, when something did not work, and the thing you want then is a
 * list you can go and look at rather than a message that has already gone.
 *
 * Quiet by default: when nothing has failed the trigger is the word "log" in
 * `--fg-ghost`, the same weight as the status bar's own em dash. An error
 * puts a count beside it in `--destructive`, which is the only thing in the
 * bar that ever changes colour on its own.
 */
export function ActivityLog() {
  const { entries, unread, markRead, clear } = useLog();

  return (
    // A DropdownMenu rather than a Popover, which is what the agent chip
    // settled on for the same job (DESIGN-NOTES §9.22): the app has no
    // Popover primitive, and adding one to hold a list that a menu already
    // positions correctly would be a second overlay to keep in step.
    <DropdownMenu
      onOpenChange={(open) => {
        if (open) markRead();
      }}
    >
      <DropdownMenuTrigger
        title="What Otis tried and could not do"
        className={cn(
          "flex shrink-0 items-center gap-1 rounded-sm px-1 hover:text-fg",
          unread > 0 ? "text-destructive" : "text-fg-ghost",
        )}
      >
        log
        {unread > 0 ? <span>{unread}</span> : null}
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        side="top"
        className="w-[440px] rounded-md border-border-control bg-raised p-0"
      >
        <div className="flex h-8 shrink-0 items-center justify-between border-b border-border px-3">
          <span className="text-label tracking-[.06em] text-fg-dim uppercase">Activity</span>
          <div className="flex items-center gap-3">
            <span className="font-mono text-meta text-fg-faint">
              {entries.length === 0
                ? "nothing yet"
                : `${entries.length} ${entries.length === 1 ? "entry" : "entries"}`}
            </span>
            <button
              type="button"
              onClick={() => void clear()}
              disabled={entries.length === 0}
              title="Clear the log"
              aria-label="Clear the log"
              className="text-fg-faint hover:text-fg-emphasis disabled:opacity-40"
            >
              <Trash2 className="size-3" />
            </button>
          </div>
        </div>

        <div className="max-h-[320px] overflow-auto py-1">
          {entries.length === 0 ? (
            <p className="px-3 py-2 text-meta text-fg-dim">
              Nothing has failed this session. This is where a clipboard write that refused, a file
              Otis could not reveal or a watcher that stopped would appear — it is per-session and
              is never written to disk.
            </p>
          ) : (
            entries.map((entry) => <Row key={entry.id} entry={entry} />)
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Row({ entry }: { entry: LogEntry }) {
  const bad = entry.level === "error";
  return (
    <div className="flex gap-2 px-3 py-1.5 hover:bg-inset">
      <span className="mt-[3px] shrink-0">
        {bad ? (
          <AlertTriangle className="size-3 text-destructive" />
        ) : (
          <Info className="size-3 text-fg-faint" />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className={cn("min-w-0 flex-1 text-ui", bad ? "text-fg-emphasis" : "text-fg-muted")}>
            {entry.message}
          </span>
          <span className="shrink-0 font-mono text-label text-fg-faint" title={entry.at}>
            {relativeTime(entry.at)}
          </span>
        </div>
        {/* The source and the underlying error. Both are what a person would
            paste into a bug report, which is why they are selectable mono
            rather than a tooltip. */}
        <div className="mt-0.5 flex gap-2">
          <span className="shrink-0 font-mono text-label text-fg-faint">{entry.source}</span>
          {entry.detail ? (
            <span className="min-w-0 flex-1 font-mono text-label break-words text-fg-dim">
              {entry.detail}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}
