import { useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { ImportService } from "@bindings/internal/services";
import type { ImportPlan } from "@bindings/internal/services";

/**
 * What a Postman import would do, before it does any of it.
 *
 * The importer separates `Plan` from `Write`, and this is why: an import
 * creates a directory of files somebody has to review, and it routinely drops
 * things Postman can express and `.http` cannot. Writing first and reporting
 * afterwards would make that a discovery rather than a decision. DESIGN-NOTES
 * §8.2 asks every write to announce itself; this is the largest write Otis
 * makes.
 *
 * The destination is the interesting half. With nothing open the import
 * becomes a new collection beside the export; with a collection open it goes
 * *into* it as a new folder, so an export can be pulled into a collection you
 * already have. Both are shown as a path, and both can be changed before
 * anything is written.
 *
 * There is no "overwrite anyway". Go blocks a destination that has files in
 * it and says what is in the way; the fix is to choose somewhere else. The
 * CLI's `--force` is for somebody who typed a path and meant it, which is not
 * the same as a button beside a folder full of a colleague's work.
 */
export function ImportDialog({
  plan,
  onPlan,
  onClose,
  onImported,
}: {
  /** The plan being confirmed, or null when the dialog is closed. */
  plan: ImportPlan | null;
  /** A re-planned copy, after the destination changed. */
  onPlan: (plan: ImportPlan) => void;
  onClose: () => void;
  onImported: (root: string, nodePath: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!plan) return null;
  const blocked = Boolean(plan.blocked);

  async function change() {
    if (!plan) return;
    try {
      const next = await ImportService.ChooseDestination(plan.id);
      if (next) onPlan(next);
    } catch (cause) {
      setError(String(cause));
    }
  }

  async function commit() {
    if (!plan || busy || blocked) return;
    setBusy(true);
    setError(null);
    try {
      const result = await ImportService.Commit(plan.id);
      onImported(result?.root ?? "", result?.nodePath ?? "");
      onClose();
    } catch (cause) {
      setError(String(cause));
      setBusy(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (open) return;
        // A plan is the whole converted collection held in Go, so an
        // abandoned dialog says so rather than leaving it there.
        void ImportService.Discard(plan.id);
        onClose();
      }}
    >
      <DialogContent className="max-w-[560px] rounded-md border border-border-strong bg-raised p-4">
        <DialogTitle className="text-ui text-fg-emphasis">Import from Postman</DialogTitle>
        <p className="mt-1 truncate font-mono text-meta text-fg-faint" title={plan.source}>
          {plan.source}
        </p>

        <div className="mt-3 rounded-sm border border-border bg-inset p-3">
          <p className="text-ui text-fg">{plan.collectionName || "Untitled collection"}</p>
          <p className="mt-0.5 text-meta text-fg-dim">
            {count(plan.requests, "request")} · {count(plan.folders, "folder")}
            {plan.environments > 0 ? ` · ${count(plan.environments, "environment")}` : ""}
          </p>

          <div className="mt-3 flex items-baseline gap-2">
            <span className="shrink-0 text-meta text-fg-faint">
              {plan.inside ? "into this collection" : "writes to"}
            </span>
            <span className="min-w-0 flex-1 break-all font-mono text-meta text-fg-secondary">
              {plan.inside ? plan.nodePath : plan.destination}
            </span>
            <Button
              type="button"
              variant="ghost"
              onClick={() => void change()}
              className="h-5 shrink-0 px-1.5 text-meta text-fg-dim hover:text-fg-emphasis"
            >
              Change…
            </Button>
          </div>
          {plan.inside ? (
            <p className="mt-1 font-mono text-micro text-fg-faint">{plan.destination}</p>
          ) : null}
        </div>

        {blocked ? (
          <p className="mt-3 flex items-start gap-1.5 text-meta text-destructive">
            <AlertTriangle className="mt-[1px] size-3.5 shrink-0" aria-hidden />
            <span>{plan.blocked}</span>
          </p>
        ) : null}

        {/* What the conversion could not carry over. Shown before the write,
            because these are the reason to look at the result — and in a
            scroller, so a collection with fifty notes cannot push the buttons
            off the dialog. */}
        {plan.skipped?.length || plan.warnings?.length ? (
          <div className="mt-3 max-h-[180px] overflow-auto rounded-sm border border-border bg-inset">
            {plan.skipped?.map((note, index) => (
              <Note key={`s${index}`} kind="skipped" path={note.path} detail={note.detail} />
            ))}
            {plan.warnings?.map((note, index) => (
              <Note key={`w${index}`} kind="warning" path={note.path} detail={note.detail} />
            ))}
          </div>
        ) : null}

        {error ? <p className="mt-3 text-meta text-destructive">{error}</p> : null}

        <div className="mt-4 flex items-center justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              void ImportService.Discard(plan.id);
              onClose();
            }}
            className="h-6 px-2 text-ui text-fg-faint hover:text-fg-emphasis"
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={busy || blocked}
            onClick={() => void commit()}
            className="h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            {busy ? "Importing…" : `Import ${count(plan.requests, "request")}`}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** One report line. Skipped is amber, not red: nothing went wrong. */
function Note({
  kind,
  path,
  detail,
}: {
  kind: "skipped" | "warning";
  path: string;
  detail: string;
}) {
  return (
    <div className="flex items-baseline gap-2 px-2 py-0.5 text-micro">
      <span className={cn("shrink-0 font-mono", kind === "skipped" ? "text-fg-faint" : "text-modified")}>
        {kind === "skipped" ? "skipped" : "check"}
      </span>
      {path ? <span className="shrink-0 truncate font-mono text-fg-dim">{path}</span> : null}
      <span className="min-w-0 text-fg-dim">{detail}</span>
    </div>
  );
}

function count(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}
