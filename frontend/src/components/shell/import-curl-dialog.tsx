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
import { errorText } from "@/lib/errors";
import { slugFor } from "@/lib/slug";
import { verbatimText } from "@/lib/text-input";
import { RequestService } from "@bindings/internal/services";
import type { CurlPlan } from "@bindings/internal/services";

/**
 * Importing a `curl` command (DESIGN-NOTES §9.43).
 *
 * The preview is **the file**, not a summary of it: what a person is agreeing
 * to is what lands on disk, and it comes from `RequestService.PlanCurl` —
 * Go's own parser and Go's own serializer, the two things that will actually
 * run — rather than from a rendering of a model one step from the truth.
 *
 * A command that will not parse is a `problem` and not a thrown error,
 * because this runs on every keystroke in a paste box: half a command is the
 * normal state of this field, and a red line per character is noise rather
 * than a report.
 *
 * What Otis could not translate is listed rather than dropped, and it is in
 * the file as comments too — the same thing the Postman importer does with an
 * auth type it cannot express. A pasted command is somebody's working
 * example, and refusing the whole of it for the sake of one flag would throw
 * away the part that does translate.
 */
export function ImportCurlDialog({
  folder,
  onClose,
  onCreated,
}: {
  /** The folder it goes in, as a node path; null when the dialog is closed. */
  folder: string | null;
  onClose: () => void;
  onCreated: (nodePath: string) => void;
}) {
  const open = folder !== null;
  const [command, setCommand] = useState("");
  const [name, setName] = useState("");
  const [touchedName, setTouchedName] = useState(false);
  const [plan, setPlan] = useState<CurlPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setCommand("");
    setName("");
    setTouchedName(false);
    setPlan(null);
    setError(null);
    setBusy(false);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    let live = true;
    // Debounced: a command is pasted more often than typed, but a person
    // editing one should not get a call per character.
    const timer = window.setTimeout(() => {
      RequestService.PlanCurl(command)
        .then((next) => {
          if (!live) return;
          setPlan(next ?? null);
          // The derived name fills the field until somebody edits it, so the
          // common case is paste-and-create and the uncommon one still works.
          if (!touchedName) setName(next?.name ?? "");
        })
        .catch((cause: unknown) => live && setError(errorText(cause)));
    }, 150);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [open, command, touchedName]);

  const create = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const nodePath = await RequestService.CreateFromCurl(folder ?? "", name, command);
      onCreated(nodePath ?? "");
      onClose();
    } catch (cause) {
      setError(errorText(cause));
      setBusy(false);
    }
  };

  const ready = Boolean(plan?.text) && !plan?.problem && !busy;
  const where = folder === "" ? "" : `${folder}/`;
  const slug = slugFor(name) || "request";

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="grid w-[620px] max-w-[92vw] grid-cols-[minmax(0,1fr)] rounded-md border border-border-strong bg-raised p-4 sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle className="text-ui text-fg-emphasis">Import from cURL</DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            {folder === "" ? "Into the collection root." : `Into ${folder}/.`} Nothing is sent
            anywhere — the command is parsed here.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <span className="text-meta text-fg-muted">Command</span>
            <textarea
              {...verbatimText}
              autoFocus
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              placeholder={"curl 'https://api.example.com/v2/orders' \\\n  -H 'accept: application/json'"}
              aria-label="Command"
              rows={5}
              className="w-full resize-y rounded-sm border border-border-control bg-inset px-2 py-1.5 font-mono text-code leading-5 text-fg outline-none placeholder:text-fg-faint focus:border-border-strong"
            />
          </div>

          <div className="flex flex-col gap-1">
            <span className="text-meta text-fg-muted">Name</span>
            <Input
              {...verbatimText}
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                setTouchedName(true);
              }}
              placeholder="from the URL"
              aria-label="Name"
              className="h-[26px] rounded-sm border-border-control bg-inset px-2 text-ui text-fg"
            />
            {plan?.text ? (
              <p className="font-mono text-meta text-fg-faint">
                Writes <span className="text-fg-secondary">{`${where}${slug}.http`}</span>
              </p>
            ) : null}
          </div>

          {plan?.problem ? (
            <p className="text-meta text-warning">{plan.problem}</p>
          ) : plan?.text ? (
            <div className="flex flex-col gap-1">
              <span className="text-meta text-fg-muted">The file it will write</span>
              <pre className="max-h-[200px] overflow-auto rounded-sm border border-border-control bg-inset p-2.5 font-mono text-code leading-5 text-fg-secondary">
                {plan.text.replace(/\n+$/, "")}
              </pre>
              {plan.notes?.length ? (
                <p className="text-meta text-warning">
                  {plan.notes.length === 1 ? "One part" : `${plan.notes.length} parts`} of the
                  command could not be translated; the file says which.
                </p>
              ) : null}
            </div>
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
            disabled={!ready}
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
