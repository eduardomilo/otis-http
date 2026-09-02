import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useDiff } from "@/state/diff-context";
import type { Change } from "@bindings/internal/diff";

/**
 * The sidebar of screen 1b: the changes list in place of the request tree,
 * with the commit box under it.
 *
 * Rows are 24px like every other list in the app (DESIGN-NOTES §4.3) and carry
 * the same selection treatment: `--bg-selected` and a 2px accent left edge
 * (§2.4). The status letter takes the colour of its meaning from §2.6 — amber
 * for modified, the accent for a new file, red for a deletion.
 */
export function ChangesList({ activePath }: { activePath: string }) {
  const { overview, changes, error, clearError } = useDiff();

  return (
    <div className="flex h-full flex-col bg-background px-2.5">
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-border">
        <span className="pl-1 text-ui text-fg-muted">Changes</span>
        <span className="pr-1 font-mono text-meta text-fg-dim">{changes.length}</span>
      </div>

      <div className="min-h-0 flex-1 overflow-auto py-1">
        {!overview ? null : !overview.repository ? (
          <Empty>
            This collection is not in a git repository. It still works perfectly well — a collection
            is a directory of files.
          </Empty>
        ) : changes.length === 0 ? (
          <Empty>Nothing has changed since the last commit.</Empty>
        ) : (
          changes.map((change) => (
            <Row key={change.path} change={change} selected={change.path === activePath} />
          ))
        )}
      </div>

      {error ? (
        <button
          type="button"
          onClick={clearError}
          title="Dismiss"
          className="mb-2 max-h-24 overflow-auto rounded-sm border border-border-danger px-2 py-1.5 text-left text-meta text-destructive"
        >
          {error}
        </button>
      ) : null}

      {overview?.repository ? <CommitBox /> : null}
    </div>
  );
}

function Row({ change, selected }: { change: Change; selected: boolean }) {
  const renamed = change.status === "R" && change.oldPath;
  return (
    <Link
      to="/diff/$path"
      params={{ path: change.path }}
      title={
        renamed
          ? `moved from ${change.oldPath}${describeStaging(change)}`
          : `${change.path}${describeStaging(change)}`
      }
      className={cn(
        "flex h-[var(--row-height)] items-center gap-2 rounded-sm pr-1",
        selected
          ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--accent)]"
          : "text-fg-secondary hover:bg-control",
      )}
    >
      <span
        className={cn(
          "w-4 shrink-0 pl-1 text-center font-mono text-label",
          statusColor(change.status),
        )}
      >
        {change.status}
      </span>

      {/* dir=rtl truncates from the left, so a long path keeps the file name —
          the part that identifies the row. text-left puts the result back
          where it belongs: rtl would otherwise right-align it too.
          A rename shows only its new path for the same reason: appending
          "← old-name" would make the old name the part truncation keeps, and
          where the file *is* now is what the row is for. The move is in the
          R, in the row's title, and in the diff header. */}
      <span className="min-w-0 flex-1 truncate text-left font-mono text-ui" dir="rtl">
        <span dir="ltr">{change.path}</span>
      </span>

      {/* A partly staged file says so: without it a row that is half staged
          looks exactly like one that is not staged at all. */}
      {change.staged && change.unstaged ? (
        <span className="shrink-0 text-label text-fg-faint" title="Partly staged">
          part
        </span>
      ) : change.staged ? (
        <span className="shrink-0 text-label text-primary" title="Staged">
          ✓
        </span>
      ) : null}

      {/* A move says so on the row, so the R is not the only signal. */}
      {renamed ? (
        <span className="shrink-0 text-label text-fg-faint" title={`moved from ${change.oldPath}`}>
          moved
        </span>
      ) : null}

      {change.binary ? (
        <span className="shrink-0 font-mono text-meta text-fg-faint">bin</span>
      ) : (
        <>
          <span className="shrink-0 font-mono text-meta text-primary">+{change.adds}</span>
          <span className="shrink-0 font-mono text-meta text-destructive">−{change.dels}</span>
        </>
      )}
    </Link>
  );
}

function describeStaging(change: Change): string {
  if (change.staged && change.unstaged) return " · partly staged";
  if (change.staged) return " · staged";
  return "";
}

function statusColor(status: string): string {
  switch (status) {
    case "U":
    case "A":
      return "text-primary";
    case "D":
      return "text-destructive";
    default:
      return "text-modified";
  }
}

/**
 * The commit box: a 56px message field and the two buttons (DESIGN-NOTES §6
 * maps the field to a Textarea, §4.1 gives it 56px).
 *
 * "Stage all" is scoped to the collection; the commit is not, because a commit
 * is the repository's. The note under the buttons says so rather than letting
 * somebody discover it.
 */
function CommitBox() {
  const { overview, changes, stageAll, commit } = useDiff();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  const anythingStaged = changes.some((c) => c.staged);
  const anythingUnstaged = changes.some((c) => c.unstaged);
  const canCommit = Boolean(overview?.canCommit) && anythingStaged && message.trim().length > 0;

  async function send() {
    setBusy(true);
    try {
      await commit(message.trim());
      setMessage("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="shrink-0 border-t border-border py-3">
      <textarea
        value={message}
        onChange={(event) => setMessage(event.target.value)}
        onKeyDown={(event) => {
          // ⌘↵ commits, matching Send in the request editor. The shell's key
          // map deliberately never fires inside a text field for bare keys,
          // and this is a field's own behaviour rather than a shortcut.
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && canCommit) {
            event.preventDefault();
            void send();
          }
        }}
        placeholder="Commit message"
        aria-label="Commit message"
        spellCheck={false}
        className="h-14 w-full resize-none rounded-md border border-border-control bg-inset px-2 py-1.5 text-ui text-fg outline-none placeholder:text-fg-dim"
      />

      <div className="mt-2 flex gap-2">
        <Button
          type="button"
          disabled={!anythingUnstaged || busy}
          onClick={() => void stageAll()}
          title="Stages every change in this collection. Changes elsewhere in the repository are left alone."
          className="h-[26px] flex-1 rounded-md border border-border-control bg-control text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
        >
          Stage all
        </Button>
        <Button
          type="button"
          disabled={!canCommit || busy}
          onClick={() => void send()}
          title={
            overview?.canCommit
              ? "Commits everything staged in the repository"
              : (overview?.reason ?? "")
          }
          className="h-[26px] flex-1 rounded-md border border-border-control bg-control text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
        >
          Commit
        </Button>
      </div>

      {!overview?.canCommit && overview?.reason ? (
        <p className="mt-2 text-meta text-modified">{overview.reason}</p>
      ) : (
        <p className="mt-2 text-meta text-fg-faint">
          A commit records everything staged in the repository, not only this collection.
        </p>
      )}

      {overview?.lastCommit ? (
        <p className="mt-2 flex items-center gap-1 truncate text-meta text-fg-faint">
          <ArrowRight className="size-3 shrink-0" />
          <span className="truncate">
            Last: “{overview.lastCommit.subject}” · {overview.lastCommit.author}
          </span>
        </p>
      ) : null}
    </div>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="px-1 py-2 text-meta text-fg-dim">{children}</p>;
}
