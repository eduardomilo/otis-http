import { useCallback, useEffect, useState } from "react";

import { HunkView } from "@/components/diff/hunk-view";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { cn } from "@/lib/utils";
import { DiffService } from "@bindings/internal/services";
import type { FileDiff, Hunk } from "@bindings/internal/diff";
import { useDiff } from "@/state/diff-context";

/**
 * The diff viewer (screen 1b's centre pane).
 *
 * # Where this leaves the design
 *
 * The design draws the file-level controls only: `Stage`, a vertical rule, and
 * `Discard changes…` in red at the far right. Per-hunk controls are required
 * but never drawn, so their placement is ours — and the brief is explicit that
 * Stage and Discard must not sit next to each other. So a hunk header carries
 * `Stage` (or `Unstage`) inline, and Discard lives in the `···` overflow menu
 * at the end of the row, marked destructive. Two controls a mis-click apart is
 * how you throw away work you meant to keep.
 *
 * The hunk header shows the semantic label the format allows rather than the
 * raw `@@ -1,9 +1,11 @@` offsets, which is the point of deriving them; the
 * offsets are still there, in the row's title, so nothing is lost.
 */
export function DiffView({ path }: { path: string }) {
  const { overview, revision, stage, unstage, stageHunks, unstageHunks } = useDiff();
  const [file, setFile] = useState<FileDiff | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [split, setSplit] = useState(false);
  const [confirming, setConfirming] = useState<{ hunks: number[] | null } | null>(null);

  const load = useCallback(async () => {
    try {
      setFile((await DiffService.File(path)) ?? null);
      setError(null);
    } catch (cause) {
      setFile(null);
      setError(String(cause));
    }
  }, [path]);

  // Re-read whenever the file changes or the view moved underneath — a stage,
  // a discard, a commit, or someone else's edit.
  useEffect(() => {
    void load();
  }, [load, revision]);

  if (!overview?.repository) {
    return (
      <Placeholder>
        This collection is not in a git repository, so there is nothing to diff. A collection is a
        directory of files and works perfectly well without one.
      </Placeholder>
    );
  }
  if (error) {
    return (
      <div className="flex h-full flex-col">
        <Header path={path} head={overview.head} split={split} setSplit={setSplit} file={null} />
        <Placeholder>{error}</Placeholder>
      </div>
    );
  }
  if (!file) return <div className="h-full" />;

  const fullyStaged = (file.hunks ?? []).length > 0 && (file.hunks ?? []).every((h) => h.staged);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header
        path={path}
        head={overview.head}
        split={split}
        setSplit={setSplit}
        file={file}
        fullyStaged={fullyStaged}
        onStage={() => void (fullyStaged ? unstage(path) : stage(path))}
        onDiscard={() => setConfirming({ hunks: null })}
      />

      <div className="min-h-0 flex-1 overflow-auto">
        {file.note ? (
          <Placeholder>{file.note}</Placeholder>
        ) : (file.hunks ?? []).length === 0 ? (
          <Placeholder>
            {file.status === "R"
              ? `Moved from ${file.oldPath}. The file's content did not change.`
              : "This file has no changes to show."}
          </Placeholder>
        ) : (
          (file.hunks ?? []).map((hunk, at) => (
            <HunkView
              key={at}
              hunk={hunk}
              split={split}
              header={
                <HunkHeader
                  hunk={hunk}
                  onStage={() =>
                    void (hunk.staged ? unstageHunks(path, [at]) : stageHunks(path, [at]))
                  }
                  onDiscard={() => setConfirming({ hunks: [at] })}
                />
              }
            />
          ))
        )}
      </div>

      <DiscardDialog
        request={confirming}
        file={file}
        path={path}
        onClose={() => setConfirming(null)}
      />
    </div>
  );
}

/**
 * `orders/create-order.http · Working tree vs HEAD a3f9c12`, the
 * Unified/Split control, Stage, a rule, and Discard changes… in red.
 *
 * The rule and the distance either side of it are the design's own separation
 * of Stage from Discard, and it is kept exactly.
 */
function Header({
  path,
  head,
  split,
  setSplit,
  file,
  fullyStaged,
  onStage,
  onDiscard,
}: {
  path: string;
  head: string;
  split: boolean;
  setSplit: (split: boolean) => void;
  file: FileDiff | null;
  fullyStaged?: boolean;
  onStage?: () => void;
  onDiscard?: () => void;
}) {
  return (
    <div className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <span className="truncate font-mono text-ui text-fg-emphasis">{path}</span>
      {file?.status === "R" && file.oldPath ? (
        <span className="shrink-0 font-mono text-meta text-fg-faint">← {file.oldPath}</span>
      ) : null}
      <span className="shrink-0 text-fg-faint">·</span>
      <span className="shrink-0 text-meta text-fg-muted">Working tree</span>
      <span className="shrink-0 text-meta text-fg-faint">vs</span>
      <span className="shrink-0 text-meta text-fg-muted">
        HEAD <span className="font-mono text-fg-dim">{head || "—"}</span>
      </span>

      <div className="flex-1" />

      <ToggleGroup
        type="single"
        value={split ? "split" : "unified"}
        onValueChange={(value) => value && setSplit(value === "split")}
        className="shrink-0"
      >
        <ToggleGroupItem value="unified" className="h-6 px-2 text-ui">
          Unified
        </ToggleGroupItem>
        <ToggleGroupItem value="split" className="h-6 px-2 text-ui">
          Split
        </ToggleGroupItem>
      </ToggleGroup>

      {onStage ? (
        <Button
          type="button"
          onClick={onStage}
          className="h-[26px] shrink-0 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
        >
          {fullyStaged ? "Unstage" : "Stage"}
        </Button>
      ) : null}

      {onDiscard ? (
        <>
          {/* The design's vertical rule: it is what keeps Discard away from
              Stage, and the distance is the safety. */}
          <div className="mx-1 h-5 w-px shrink-0 bg-border" />
          <Button
            type="button"
            onClick={onDiscard}
            className="h-[26px] shrink-0 rounded-md border border-border-danger bg-transparent px-2.5 text-ui text-destructive hover:bg-[rgba(248,113,113,.08)]"
          >
            Discard changes…
          </Button>
        </>
      ) : null}
    </div>
  );
}

/**
 * A hunk header: `@@ tests @@`, its counts, and its controls.
 *
 * `--fg-dim` on `--bg-inset` per §2.6. Stage is inline; Discard is behind the
 * `···` menu at the far end, which is the separation the brief requires.
 */
function HunkHeader({
  hunk,
  onStage,
  onDiscard,
}: {
  hunk: Hunk;
  onStage: () => void;
  onDiscard: () => void;
}) {
  const offsets = `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`;
  return (
    <div
      className="group sticky top-0 z-10 flex h-7 items-center gap-2 bg-inset px-2"
      title={offsets}
    >
      <span className="shrink-0 truncate font-mono text-code text-fg-dim">
        {hunk.label ? `@@ ${hunk.label} @@` : offsets}
      </span>
      <span className="shrink-0 font-mono text-meta text-primary">+{hunk.adds}</span>
      <span className="shrink-0 font-mono text-meta text-destructive">−{hunk.dels}</span>
      {hunk.staged ? (
        <span className="shrink-0 rounded-sm border border-border-control px-1 text-label text-primary">
          staged
        </span>
      ) : null}

      <div className="flex-1" />

      {/* Always visible, not hover-only: a control that appears only under
          the pointer is a control a keyboard user never finds, and the whole
          per-hunk workflow hangs off this one. It is dim until hovered, which
          is the same weight the design gives its own inline chips (§4.4). */}
      <button
        type="button"
        onClick={onStage}
        className="shrink-0 rounded-sm border border-border-control px-1.5 text-meta text-fg-faint hover:bg-control hover:text-fg-emphasis"
      >
        {hunk.staged ? "Unstage" : "Stage"}
      </button>

      <DropdownMenu>
        {/* Discard lives here, behind a menu and at the far end of the row,
            deliberately nowhere near Stage. Two controls a mis-click apart is
            how you throw away work you meant to keep. */}
        <DropdownMenuTrigger
          aria-label="Hunk actions"
          className="shrink-0 px-1.5 text-fg-ghost hover:text-fg-muted"
        >
          ···
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem variant="destructive" onSelect={onDiscard}>
            Discard this hunk…
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/**
 * The confirmation in front of a discard, naming what will be lost.
 *
 * Go refuses the operation without the confirm flag whatever this dialog does
 * (diff.ErrNoConfirm) — that is the safety. This is here so nobody throws away
 * work without reading what it was.
 */
function DiscardDialog({
  request,
  file,
  path,
  onClose,
}: {
  request: { hunks: number[] | null } | null;
  file: FileDiff;
  path: string;
  onClose: () => void;
}) {
  const { discardFile, discardHunks } = useDiff();
  const hunks = request?.hunks ?? null;
  const chosen = hunks?.map((at) => (file.hunks ?? [])[at]).filter(Boolean) ?? [];
  const adds = hunks ? chosen.reduce((n, h) => n + h.adds, 0) : file.adds;
  const dels = hunks ? chosen.reduce((n, h) => n + h.dels, 0) : file.dels;
  const untracked = file.status === "U";

  return (
    <AlertDialog open={request !== null} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {hunks
              ? `Discard ${chosen.length === 1 ? "this hunk" : `${chosen.length} hunks`}?`
              : untracked
                ? `Delete ${path}?`
                : `Discard changes to ${path}?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {hunks ? (
              <>
                {chosen.length === 1 && chosen[0].label ? (
                  <>
                    The <span className="font-mono text-fg-secondary">{chosen[0].label}</span> hunk
                    — {counts(adds, dels)} — goes back to what is committed.{" "}
                  </>
                ) : (
                  <>{counts(adds, dels)} go back to what is committed. </>
                )}
                The rest of the file is left as it is.
              </>
            ) : untracked ? (
              <>
                This file is not in git yet, so there is nothing to go back to:{" "}
                {counts(adds, dels)} and the file itself are deleted.
              </>
            ) : (
              <>
                {counts(adds, dels)} across{" "}
                {(file.hunks ?? []).length === 1
                  ? "one hunk"
                  : `${(file.hunks ?? []).length} hunks`}{" "}
                go back to what is committed.
              </>
            )}{" "}
            <span className="text-modified">
              git cannot get this back — it was never committed.
            </span>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            className="border-border-danger bg-transparent text-destructive hover:bg-[rgba(248,113,113,.08)]"
            onClick={(event) => {
              event.preventDefault();
              void (hunks ? discardHunks(path, hunks) : discardFile(path));
              onClose();
            }}
          >
            {untracked && !hunks ? "Delete the file" : "Discard"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function counts(adds: number, dels: number): string {
  const parts: string[] = [];
  if (adds > 0) parts.push(`${adds} added ${adds === 1 ? "line" : "lines"}`);
  if (dels > 0) parts.push(`${dels} removed ${dels === 1 ? "line" : "lines"}`);
  if (parts.length === 0) return "No line changes";
  return parts.join(" and ");
}

function Placeholder({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center px-8">
      <p className={cn("max-w-[520px] text-center text-meta text-fg-dim")}>{children}</p>
    </div>
  );
}
