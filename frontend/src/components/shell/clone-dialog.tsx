import { useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";

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
import { OtisEvent } from "@/lib/events.gen";
import { errorText } from "@/lib/errors";
import { verbatimText } from "@/lib/text-input";
import { StartService } from "@bindings/internal/services";
import type { StartDefaults, StartResult } from "@bindings/internal/services";

/**
 * Screen 2b's "Clone repository" card (DESIGN-NOTES §9.39).
 *
 * The clone runs the user's own `git` in a subprocess, so it authenticates
 * exactly as their terminal does and **Otis never sees a credential** — there
 * is no password field on this dialog and there must never be one. The
 * subprocess also cannot prompt: a GUI app has no terminal, so a `git` that
 * decided to ask would hang with nothing on screen. It fails instead, and the
 * failure says to clone it in a terminal and use Open folder.
 *
 * git's output is shown while it runs, because a clone of anything real takes
 * long enough that a dialog with nothing in it looks broken. Only the last
 * few lines are kept, and they are dropped with the dialog: this is a
 * commentary, not a log.
 */
const PROGRESS_LINES = 6;

export function CloneDialog({
  open,
  defaults,
  onClose,
  onCloned,
}: {
  open: boolean;
  defaults: StartDefaults | null;
  onClose: () => void;
  onCloned: (result: StartResult) => void;
}) {
  const [url, setUrl] = useState("");
  const [parent, setParent] = useState("");
  const [name, setName] = useState("");
  const [suggested, setSuggested] = useState("");
  const [progress, setProgress] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const cancelled = useRef(false);

  useEffect(() => {
    if (!open) return;
    setUrl("");
    setParent(defaults?.parent ?? "");
    setName("");
    setSuggested("");
    setProgress([]);
    setError(null);
    setBusy(false);
    cancelled.current = false;
  }, [open, defaults]);

  // The folder name a URL implies is git's rule, so Go answers it rather than
  // the window guessing: leaving the name field empty means Go derives it,
  // and this is only what the preview line shows in the meantime. Debounced,
  // because it is a call per keystroke otherwise and the answer only matters
  // once you have stopped typing.
  useEffect(() => {
    if (!open) return;
    const trimmed = url.trim();
    if (trimmed === "") {
      setSuggested("");
      return;
    }
    const timer = window.setTimeout(() => {
      StartService.SuggestName(trimmed)
        .then((value) => setSuggested(value ?? ""))
        .catch(() => setSuggested(""));
    }, 200);
    return () => window.clearTimeout(timer);
  }, [open, url]);

  useEffect(() => {
    if (!open) return;
    const off = Events.On(OtisEvent.CloneProgress, (event) => {
      const line = event.data;
      if (typeof line !== "string") return;
      setProgress((lines) => [...lines, line].slice(-PROGRESS_LINES));
    });
    return () => off();
  }, [open]);

  const folder = name.trim() || suggested;
  const path = parent && folder ? `${parent}/${folder}` : "";

  const clone = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    setProgress([]);
    cancelled.current = false;
    try {
      const result = await StartService.Clone(url.trim(), parent, name.trim());
      if (result) onCloned(result);
      onClose();
    } catch (cause) {
      // A clone the user stopped is not a failure to report at them; the
      // dialog just goes back to how it was.
      setError(cancelled.current ? null : errorText(cause));
      setBusy(false);
      setProgress([]);
    }
  };

  const stop = () => {
    cancelled.current = true;
    void StartService.CancelClone();
  };

  const blocked = defaults?.cloneBlocked ?? "";

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) return;
        // Closing mid-clone stops it. A clone that carried on behind a closed
        // dialog would finish by switching the collection out from under
        // whatever the person did next.
        if (busy) stop();
        onClose();
      }}
    >
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
          <DialogTitle className="text-ui text-fg-emphasis">Clone repository</DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            Clones with your own git, then opens the collection inside it.
          </DialogDescription>
        </DialogHeader>

        {blocked ? (
          <p className="text-meta text-warning">{blocked}</p>
        ) : (
          <div className="flex flex-col gap-3">
            <Field label="Repository">
              <Input
                {...verbatimText}
                autoFocus
                value={url}
                disabled={busy}
                placeholder="git@github.com:org/api-requests.git"
                aria-label="Repository"
                onChange={(event) => {
                  setUrl(event.target.value);
                  setError(null);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    void clone();
                  }
                }}
                className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
              />
            </Field>

            <Field label="Where">
              <LocationField value={parent} onChange={setParent} onError={setError} />
            </Field>

            <Field label="Folder name">
              <Input
                {...verbatimText}
                value={name}
                disabled={busy}
                placeholder={suggested || "from the URL"}
                aria-label="Folder name"
                onChange={(event) => {
                  setName(event.target.value);
                  setError(null);
                }}
                className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
              />
            </Field>

            <p className="text-meta text-fg-faint">
              {path ? (
                <>
                  Clones into{" "}
                  <span className="font-mono break-all text-fg-secondary">{path}</span>
                </>
              ) : (
                "Otis never asks for a password: your git config, credential helper and SSH agent do the authenticating."
              )}
            </p>

            {progress.length > 0 ? (
              <div className="rounded-md border border-border-control bg-inset px-3 py-2">
                {progress.map((line, at) => (
                  <p key={at} className="truncate font-mono text-meta text-fg-dim">
                    {line}
                  </p>
                ))}
              </div>
            ) : null}

            {error ? <p className="text-meta text-destructive">{error}</p> : null}
          </div>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              if (busy) stop();
              onClose();
            }}
            className="h-6 px-2 text-ui text-fg-faint hover:text-fg-emphasis"
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={Boolean(blocked) || busy || url.trim() === "" || !parent}
            onClick={() => void clone()}
            className="h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            {busy ? "Cloning…" : "Clone"}
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
