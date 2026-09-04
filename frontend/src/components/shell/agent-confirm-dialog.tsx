import { useEffect, useRef, useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { methodColor } from "@/lib/method";
import { cn } from "@/lib/utils";
import { useMCP } from "@/state/mcp-context";
import { DiffService } from "@bindings/internal/services";
import type { FileDiff } from "@bindings/internal/diff";

/**
 * The in-app confirmation an agent's send waits on (docs/MCP.md §6.4, §5.1).
 *
 * This is the surface no client preference can auto-approve, and it is the one
 * place a person sees what an agent is actually about to do. A Go tool call is
 * blocked on the answer with a 60-second deadline running, so this dialog
 * cannot be dismissed into an ambiguous state: every way out of it is an
 * answer, and the deadline is a refusal.
 *
 * Two variants. The ordinary one confirms a send. The **danger** one is §5.1 —
 * an unreviewed request that would consume a secret, the single case where a
 * mistaken click cannot be taken back, because the credential is on the wire
 * and gone. It takes:
 *
 * - `--border-danger`, which DESIGN-NOTES §2.2 reserves for "Discard
 *   changes…" *only* — the one other action in Otis that destroys something
 *   git cannot get back. This is the second, and the shared border is the
 *   design's existing way of saying so.
 * - **The destination in the button.** "Send to `evil.test`", never "Send".
 *   Muscle memory cannot approve a host it has to read, and the host is the
 *   one fact that distinguishes an exfiltration attempt from ordinary work.
 * - **The diff, in the dialog.** The request is unreviewed, so there *is* a
 *   diff, and what the agent wrote is the thing being approved. It is in the
 *   dialog and not behind it: the first version of this said "press ⌘G to read
 *   the diff", which was untrue twice over — `useKeymap` fires no binding
 *   inside a `[role="dialog"]`, and a modal would have covered the diff even
 *   if it had. §5.1 asks for it here, and here is also the only place it can
 *   be read without answering first.
 * - **The secret named**, and the fact that nobody reviewed the request.
 * - **Refuse focused.** Nothing here proceeds by inattention, and the return
 *   key must not be the dangerous one.
 *
 * §13 is honest that a dialog is only as good as its reading, and §14.7 records
 * that turning a flat refusal into an informed decision was a real reduction
 * in safety bought for a real gain. Everything above is spending that
 * difference on making the dialog hard to click through.
 */
export function AgentConfirmDialog() {
  const { confirmation, resolvedReason, answer, clearError } = useMCP();
  const refuseRef = useRef<HTMLButtonElement>(null);
  const [diff, setDiff] = useState<FileDiff | null>(null);

  // Only for the danger variant, and only for the file being asked about. An
  // ordinary confirmation is about a committed request, so there is no diff to
  // show and fetching one would say "no changes" where nothing is wrong.
  const dangerPath = confirmation?.danger ? confirmation.path : null;
  useEffect(() => {
    if (!dangerPath) {
      setDiff(null);
      return;
    }
    let live = true;
    void DiffService.File(dangerPath)
      .then((file) => {
        if (live) setDiff(file ?? null);
      })
      // A diff that cannot be read must not stop the dialog: the decision is
      // still the person's to make, and the rest of the dialog is what it
      // rests on.
      .catch(() => {
        if (live) setDiff(null);
      });
    return () => {
      live = false;
    };
  }, [dangerPath]);

  // Refuse takes focus, in both variants: the send is what needs a decision,
  // not the refusal.
  useEffect(() => {
    if (confirmation) refuseRef.current?.focus();
  }, [confirmation?.id, confirmation]);

  if (!confirmation) {
    if (!resolvedReason) return null;
    return (
      <Dialog open onOpenChange={() => clearError()}>
        <DialogContent className="rounded-md border border-border-strong bg-raised p-4">
          <DialogTitle className="text-ui text-fg-emphasis">Confirmation expired</DialogTitle>
          <p className="text-meta text-fg-dim">{resolvedReason}</p>
        </DialogContent>
      </Dialog>
    );
  }

  const danger = confirmation.danger;
  const sendLabel = confirmation.host ? `Send to ${confirmation.host}` : "Send";

  return (
    <Dialog
      open
      // There is no way to close this without answering. A Go call is blocked
      // on it, so an escape that just hid the dialog would leave the agent
      // waiting on a person who can no longer see the question — and the
      // deadline would refuse it a minute later with nobody told. Escape and
      // the overlay both refuse, which is the same answer the deadline gives.
      onOpenChange={(open) => {
        if (!open) void answer(confirmation.id, false);
      }}
    >
      <DialogContent
        className={cn(
          "max-w-[520px] rounded-md bg-raised p-4",
          danger ? "border border-border-danger" : "border border-border-strong",
        )}
      >
        <div className="flex items-start gap-2">
          {danger ? (
            <AlertTriangle className="mt-[2px] size-4 shrink-0 text-destructive" aria-hidden />
          ) : null}
          <div className="min-w-0 flex-1">
            <DialogTitle
              className={cn("text-ui", danger ? "text-destructive" : "text-fg-emphasis")}
            >
              {danger ? "An agent wants to send a credential" : "An agent wants to send a request"}
            </DialogTitle>
            <p className="mt-1 text-meta text-fg-dim">
              {confirmation.client || "An MCP client"} asked, via{" "}
              <span className="font-mono">{confirmation.tool}</span>.
            </p>
          </div>
        </div>

        {/* What will actually happen. The URL is the whole point of the
            dialog, so it gets the most room and is not truncated away. */}
        <div className="mt-3 rounded-sm border border-border bg-inset p-3">
          <div className="flex items-baseline gap-2">
            <span
              className={cn("shrink-0 font-mono text-meta", methodColor(confirmation.method))}
            >
              {confirmation.method}
            </span>
            <span className="min-w-0 break-all font-mono text-ui text-fg">{confirmation.url}</span>
          </div>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-meta">
            <dt className="text-fg-faint">request</dt>
            <dd className="truncate font-mono text-fg-secondary">{confirmation.path}</dd>
            <dt className="text-fg-faint">environment</dt>
            <dd className="font-mono text-fg-secondary">
              {confirmation.environment || <span className="text-fg-faint">none</span>}
            </dd>
            {confirmation.usesSecret ? (
              <>
                <dt className="text-fg-faint">secret</dt>
                <dd className="font-mono text-secret">
                  {confirmation.secrets?.length
                    ? `${confirmation.secrets.join(", ")} — from the keychain`
                    : "one, from the keychain"}
                </dd>
              </>
            ) : null}
            <dt className="text-fg-faint">reviewed</dt>
            <dd className={confirmation.reviewed ? "text-fg-secondary" : "text-modified"}>
              {confirmation.reviewed ? "yes, committed" : "no — this request is not committed"}
            </dd>
          </dl>
        </div>

        <p className={cn("mt-3 text-meta", danger ? "text-destructive" : "text-fg-dim")}>
          {capitalize(confirmation.reason)}.
        </p>

        {danger ? <DiffInDialog path={confirmation.path} file={diff} /> : null}

        <div className="mt-4 flex items-center justify-end gap-2">
          <Button
            ref={refuseRef}
            type="button"
            onClick={() => void answer(confirmation.id, false)}
            className="h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
          >
            Refuse
          </Button>
          <Button
            type="button"
            onClick={() => void answer(confirmation.id, true)}
            className={cn(
              "h-6 rounded-md px-2.5 text-ui",
              danger
                ? "border border-border-danger bg-transparent text-destructive hover:bg-destructive/10"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {sendLabel}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * What the agent wrote, as a diff, inside the dialog (§5.1).
 *
 * Deliberately *not* `components/diff/hunk-view`: that one carries the
 * staging and discarding controls a review needs, and a security dialog is
 * not a place to stage a hunk — the only two things to do here are approve
 * the send and refuse it. So this is a read-only unified rendering, scrolled
 * rather than paged, with the same colours the diff view uses.
 */
function DiffInDialog({ path, file }: { path: string; file: FileDiff | null }) {
  if (!file) {
    return (
      <p className="mt-2 text-meta text-fg-faint">
        Nobody has reviewed where this credential goes.
      </p>
    );
  }
  if (file.binary || !file.hunks?.length) {
    return (
      <p className="mt-2 text-meta text-fg-faint">
        {file.note || "Nobody has reviewed where this credential goes."}
      </p>
    );
  }

  return (
    <div className="mt-3">
      <p className="text-meta text-fg-faint">
        What is uncommitted in <span className="font-mono text-fg-dim">{path}</span> —{" "}
        <span className="text-primary">+{file.adds}</span>{" "}
        <span className="text-destructive">−{file.dels}</span>
      </p>
      {/* Its own scroll container, so a long diff cannot push the buttons off
          the dialog — the decision has to stay reachable. */}
      <div className="mt-1 max-h-[220px] overflow-auto rounded-sm border border-border bg-inset">
        {file.hunks.map((hunk, index) => (
          <div key={index}>
            <div className="border-b border-border bg-control px-2 py-0.5 font-mono text-micro text-fg-faint">
              {hunk.label ||
                `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`}
            </div>
            {hunk.lines?.map((line, lineIndex) => (
              <div
                key={lineIndex}
                className={cn(
                  "whitespace-pre px-2 font-mono text-micro",
                  // The same two washes the diff view uses (§2.3), so an
                  // added line looks the same here as it does in ⌘G.
                  line.kind === "+" && "bg-[rgba(52,211,153,.08)] text-primary",
                  line.kind === "-" && "bg-[rgba(248,113,113,.08)] text-destructive",
                  line.kind === " " && "text-fg-dim",
                )}
              >
                {/* The kind *is* the prefix character, so there is nothing to
                    map: a space for context, + or - otherwise. */}
                {line.kind}
                {line.text}
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

function capitalize(text: string): string {
  return text ? text[0].toUpperCase() + text.slice(1) : text;
}
