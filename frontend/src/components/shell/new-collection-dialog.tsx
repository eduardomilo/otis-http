import { useEffect, useState } from "react";

import { LocationField } from "@/components/shell/location-field";
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
import { errorText } from "@/lib/errors";
import { verbatimText } from "@/lib/text-input";
import { StartService } from "@bindings/internal/services";
import type { StartDefaults, StartResult } from "@bindings/internal/services";

/**
 * Screen 2b's "Start fresh" card (DESIGN-NOTES §9.39).
 *
 * A location, a name, and a list of the three files it will write — because
 * §8.2 asks every write to announce itself first, and this one creates a
 * directory in somebody's home folder. The list is not a summary: it is
 * exactly what `collection.Scaffold` writes, and if a fourth file is ever
 * added it has to be added here too, which is the point of naming them.
 *
 * The card's example line reads `mkdir .requests && git init` and only the
 * first half of that happens; the note under the files says so rather than
 * leaving it to be discovered when the changes view reports no repository.
 */
export function NewCollectionDialog({
  open,
  defaults,
  onClose,
  onCreated,
}: {
  open: boolean;
  defaults: StartDefaults | null;
  onClose: () => void;
  onCreated: (result: StartResult) => void;
}) {
  const [parent, setParent] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Fresh every time it opens, seeded from Go: beside the last collection, or
  // the home directory before there is one.
  useEffect(() => {
    if (!open) return;
    setParent(defaults?.parent ?? "");
    setName(defaults?.name ?? "");
    setError(null);
    setBusy(false);
  }, [open, defaults]);

  const create = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const result = await StartService.Create(parent, name);
      if (result) onCreated(result);
      onClose();
    } catch (cause) {
      setError(errorText(cause));
      setBusy(false);
    }
  };

  const path = parent && name.trim() ? `${parent}/${name.trim()}` : "";

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      {/* The primitive caps itself at `sm:max-w-sm`, so widening it takes an
          `sm:` of its own — a breakpoint on a dialog, which is the one place
          CLAUDE.md allows one, because a dialog is measured against the
          viewport and not against a resizable pane. Three labelled fields and
          an absolute path do not fit in 384px.

          `grid-cols-[minmax(0,1fr)]` is the other half: the primitive is a
          grid, an implicit track is `auto`, and an `auto` track grows to its
          content — so an absolute path made the row wider than the panel and
          painted outside it rather than wrapping. */}
      <DialogContent className="grid w-[560px] max-w-[92vw] grid-cols-[minmax(0,1fr)] sm:max-w-[560px] rounded-md border border-border-strong bg-raised p-4">
        <DialogHeader>
          <DialogTitle className="text-ui text-fg-emphasis">Start fresh</DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            A new collection with one example request and a local environment.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <Field label="Where">
            <LocationField value={parent} onChange={setParent} onError={setError} />
          </Field>

          <Field label="Folder name">
            <Input
              {...verbatimText}
              autoFocus
              value={name}
              placeholder=".requests"
              aria-label="Folder name"
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
              className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
            />
          </Field>

          <div className="rounded-md border border-border-control px-3 py-2">
            <p className="text-meta text-fg-dim">
              {path ? (
                <>
                  Creates{" "}
                  {/* An absolute path is long and has nothing to break on, so
                      it wraps by character rather than pushing the dialog
                      wider than the window. */}
                  <span className="font-mono break-all text-fg-secondary">{path}</span>{" "}
                  containing:
                </>
              ) : (
                "Creates a folder containing:"
              )}
            </p>
            <ul className="mt-1.5 flex flex-col gap-0.5 font-mono text-meta text-fg-faint">
              <li>_folder.http — settings shared by every request below</li>
              <li>env/local.json — one variable, baseUrl</li>
              <li>example.http — a GET at {"{{baseUrl}}"}/health</li>
            </ul>
            <p className="mt-2 text-meta text-fg-faint">
              Otis does not run <span className="font-mono">git init</span>. Inside a repository
              this is versioned already; outside one it is a folder of files until you make it a
              repository yourself.
            </p>
          </div>

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
            disabled={busy || !parent || name.trim() === ""}
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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-meta text-fg-muted">{label}</span>
      {children}
    </div>
  );
}
