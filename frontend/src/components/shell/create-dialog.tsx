import { useEffect, useState } from "react";

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
import { FolderService, RequestService } from "@bindings/internal/services";

/**
 * Naming a new request or folder.
 *
 * It shows the **path it will write** as you type, because that is the one
 * thing you cannot infer: the file is named for the slug of what you type
 * while the typed name is kept as the `# @name` directive, so "Create order"
 * becomes `create-order.http` and still reads as "Create order" in the tree.
 * DESIGN-NOTES §8.2 asks every write to announce itself first, and a name that
 * silently becomes a different file name is exactly the case that needs it.
 *
 * The slug shown here is a preview computed in the window; Go computes the
 * real one, resolves collisions against what is actually on disk, and returns
 * the node path it used. So the preview can be one character behind the truth
 * — `create-order.http` when `create-order-2.http` is what you will get — and
 * the caller navigates to whatever Go says rather than to what was previewed.
 */

export type CreateKind = "request" | "folder";

export function CreateDialog({
  kind,
  folder,
  onClose,
  onCreated,
}: {
  /** What to make, or null when the dialog is closed. */
  kind: CreateKind | null;
  /** The folder it goes in, as a node path; "" is the collection root. */
  folder: string;
  onClose: () => void;
  /** The node path Go actually used, which may differ from the preview. */
  onCreated: (nodePath: string, kind: CreateKind) => void;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A fresh name each time it opens: a dialog that remembers the last one is a
  // dialog you have to clear before you can use it.
  useEffect(() => {
    if (kind) {
      setName("");
      setError(null);
      setBusy(false);
    }
  }, [kind]);

  const slug = slugFor(name) || (kind === "folder" ? "folder" : "request");
  const where = folder === "" ? "" : `${folder}/`;
  const preview = kind === "folder" ? `${where}${slug}/` : `${where}${slug}.http`;

  async function create() {
    if (busy) return;
    setBusy(true);
    try {
      const nodePath =
        kind === "folder"
          ? await FolderService.Create(folder, name)
          : await RequestService.Create(folder, name);
      onCreated(nodePath ?? "", kind ?? "request");
      onClose();
    } catch (cause) {
      setError(String(cause));
      setBusy(false);
    }
  }

  return (
    <Dialog open={kind !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="rounded-md border border-border-strong bg-raised p-4">
        <DialogHeader>
          <DialogTitle className="text-ui text-fg-emphasis">
            {kind === "folder" ? "New folder" : "New request"}
          </DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            {folder === ""
              ? "In the collection root."
              : `In ${folder}/.`}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Input
            autoFocus
            value={name}
            placeholder={kind === "folder" ? "Payment methods" : "Create order"}
            aria-label="Name"
            onChange={(event) => {
              setName(event.target.value);
              setError(null);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void create();
              }
            }}
            className="h-[30px] rounded-sm border-border-control bg-inset px-2 text-ui text-fg"
          />

          <p className="font-mono text-meta text-fg-faint">
            {name.trim() === "" ? (
              "Writes a file named for what you type."
            ) : (
              <>
                Writes <span className="text-fg-secondary">{preview}</span>
                {kind === "request" ? (
                  <>
                    {" · "}
                    named <span className="text-fg-secondary">{name.trim()}</span> in the tree
                  </>
                ) : null}
              </>
            )}
          </p>

          {kind === "folder" ? (
            <p className="text-meta text-fg-faint">
              With a <span className="font-mono">_folder.http</span> inside it, because git does
              not track an empty directory.
            </p>
          ) : null}

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
            disabled={busy}
            onClick={() => void create()}
            className="h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
