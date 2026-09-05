import { useEffect, useState } from "react";

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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { slugFor } from "@/lib/slug";
import { verbatimText } from "@/lib/text-input";
import { FolderService, RequestService } from "@bindings/internal/services";
import type { Node } from "@bindings/internal/services";

/**
 * Renaming, duplicating and deleting a row in the tree — the three items the
 * design's context menu draws and that were disabled until Otis could write.
 *
 * Duplicate has no dialog: it makes a new file whose name is derived, beside
 * the one it copied, and nothing about that is worth stopping for. The other
 * two do, for opposite reasons — a rename needs a name typed, and a delete
 * needs a moment.
 *
 * Both dialogs say what will happen to the files before it happens
 * (DESIGN-NOTES §8.2). The delete says whether git still has a copy, because
 * that is the whole difference between an inconvenience and a loss, and Otis
 * already knows: the tree carries each node's git status for its dot.
 */

/** What the menu asked for. */
export type NodeAction = "rename" | "duplicate" | "delete";

/** The row an action is aimed at, and which action. */
export interface NodeTarget {
  action: NodeAction;
  node: Node;
}

/** True for the rows these three operations understand. */
export function canManage(node: Node | null): node is Node {
  return Boolean(node) && (node!.kind === "request" || node!.kind === "folder") && node!.path !== "";
}

export function NodeActionDialogs({
  target,
  onClose,
  onRenamed,
  onDeleted,
  onError,
}: {
  target: NodeTarget | null;
  onClose: () => void;
  /** The node path Go used, which may differ from the preview. */
  onRenamed: (from: string, to: string, node: Node) => void;
  onDeleted: (path: string) => void;
  onError: (message: string, detail: string) => void;
}) {
  return (
    <>
      <RenameDialog
        node={target?.action === "rename" ? target.node : null}
        onClose={onClose}
        onRenamed={onRenamed}
        onError={onError}
      />
      <DeleteDialog
        node={target?.action === "delete" ? target.node : null}
        onClose={onClose}
        onDeleted={onDeleted}
        onError={onError}
      />
    </>
  );
}

/**
 * Naming it again.
 *
 * A request's name lives in two places — the `# @name` directive and the file
 * name — and this changes both, because they are two views of one thing and a
 * rename that moved only one would leave a `place-order.http` called "Create
 * order" that nobody meant. That is also what Create does with a typed name,
 * so the two are symmetric. The dialog shows both lines rather than
 * explaining the rule.
 *
 * A folder has no `# @name`: its name is the directory's (docs/FORMAT.md
 * §2.1), so there is one line to show.
 */
function RenameDialog({
  node,
  onClose,
  onRenamed,
  onError,
}: {
  node: Node | null;
  onClose: () => void;
  onRenamed: (from: string, to: string, node: Node) => void;
  onError: (message: string, detail: string) => void;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (node) {
      setName(node.name);
      setError(null);
      setBusy(false);
    }
  }, [node]);

  if (!node) return <Dialog open={false} onOpenChange={() => {}} />;

  const folder = node.kind === "folder";
  const trimmed = name.trim();
  const parent = node.path.includes("/") ? node.path.slice(0, node.path.lastIndexOf("/") + 1) : "";
  const slug = slugFor(trimmed) || (folder ? "folder" : "request");
  const to = folder ? `${parent}${slug}/` : `${parent}${slug}.http`;
  const from = folder ? `${node.path}/` : node.path;
  const unchanged = trimmed === "" || trimmed === node.name;

  async function rename() {
    if (busy || !node || unchanged) return;
    setBusy(true);
    try {
      const path = folder
        ? await FolderService.Rename(node.path, trimmed)
        : await RequestService.Rename(node.path, trimmed);
      onRenamed(node.path, path ?? node.path, node);
      onClose();
    } catch (cause) {
      setError(String(cause));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="rounded-md border border-border-strong bg-raised p-4">
        <DialogHeader>
          <DialogTitle className="text-ui text-fg-emphasis">
            {folder ? "Rename folder" : "Rename request"}
          </DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            Currently <span className="font-mono text-fg-secondary">{from}</span>
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Input
            {...verbatimText}
            autoFocus
            value={name}
            aria-label="Name"
            onFocus={(event) => event.currentTarget.select()}
            onChange={(event) => {
              setName(event.target.value);
              setError(null);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void rename();
              }
            }}
            className="h-[30px] rounded-sm border-border-control bg-inset px-2 text-ui text-fg"
          />

          <p className="font-mono text-meta text-fg-faint">
            {unchanged ? (
              "Type a new name."
            ) : (
              <>
                Renames <span className="text-fg-secondary">{from}</span> →{" "}
                <span className="text-fg-secondary">{to}</span>
                {folder ? null : (
                  <>
                    <br />
                    Sets <span className="text-fg-secondary">{`# @name ${trimmed}`}</span>
                  </>
                )}
              </>
            )}
          </p>

          {error ? <p className="text-meta text-destructive">{error}</p> : null}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            className="h-6 px-2 text-ui text-fg-faint hover:text-fg-emphasis"
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={busy || unchanged}
            onClick={() => void rename()}
            className="h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            Rename
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The one confirmation in the tree.
 *
 * `internal/diff`'s discard takes its confirmation as a *parameter* because it
 * is one of four things a row of buttons does and had to be unreachable by
 * accident. `Delete` is not that: the method's name is the whole of what it
 * does, so the dialog is the safety rather than a courtesy on top of one.
 *
 * It says whether git can bring the file back, which is the difference
 * between an inconvenience and a loss. The tree already carries each node's
 * status for its dot, so this costs nothing to say and would be conspicuous
 * to leave out.
 */
function DeleteDialog({
  node,
  onClose,
  onDeleted,
  onError,
}: {
  node: Node | null;
  onClose: () => void;
  onDeleted: (path: string) => void;
  onError: (message: string, detail: string) => void;
}) {
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (node) setBusy(false);
  }, [node]);

  if (!node) return null;

  const folder = node.kind === "folder";
  const below = folder ? countRequests(node) : 0;
  const untracked = node.gitStatus === "U" || node.gitStatus === "A";

  async function remove() {
    if (busy || !node) return;
    setBusy(true);
    try {
      if (folder) await FolderService.Delete(node.path);
      else await RequestService.Delete(node.path);
      onDeleted(node.path);
      onClose();
    } catch (cause) {
      // The dialog is closing, so the message has nowhere to go but the log —
      // which is what the log is for.
      onError(`Could not delete ${node.path}`, String(cause));
      onClose();
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent className="max-w-[440px]">
        <AlertDialogHeader>
          <AlertDialogTitle className="text-ui font-medium text-fg-emphasis">
            Delete {folder ? "folder" : "request"} {node.name}?
          </AlertDialogTitle>
          <AlertDialogDescription className="text-meta text-fg-muted">
            <span className="font-mono text-fg-dim">
              {node.path}
              {folder ? "/" : ""}
            </span>{" "}
            {folder ? (
              <>
                and everything in it
                {below > 0 ? ` — ${below} ${below === 1 ? "request" : "requests"}` : ""}.
              </>
            ) : (
              "is removed from disk."
            )}
            <br />
            {folder ? (
              <>Anything in it that git has not seen is gone for good.</>
            ) : untracked ? (
              <>This file is not in git yet, so this cannot be undone.</>
            ) : (
              <>
                git still has the committed version:{" "}
                <span className="font-mono text-fg-dim">git checkout -- {node.path}</span> brings it
                back.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="gap-2">
          <AlertDialogCancel className="h-6 rounded-sm border-border-control bg-control px-2.5 text-ui text-fg-secondary">
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            variant="outline"
            disabled={busy}
            onClick={(event) => {
              event.preventDefault();
              void remove();
            }}
            className="h-6 rounded-sm border-border-danger bg-transparent px-2.5 text-ui text-destructive hover:bg-destructive/10"
          >
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/** Every request in a subtree, which is what a folder's deletion costs. */
function countRequests(node: Node): number {
  let total = node.kind === "request" ? 1 : 0;
  for (const child of node.children ?? []) total += countRequests(child);
  return total;
}
