import { useEffect, useMemo, useState } from "react";

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { errorText } from "@/lib/errors";
import { verbatimText } from "@/lib/text-input";
import { ScriptService } from "@bindings/internal/services";
import type { Node, ScriptPlan } from "@bindings/internal/services";

/**
 * Making a new `.js` (DESIGN-NOTES §9.41).
 *
 * A script's kind is decided entirely by its **file name** (docs/FORMAT.md
 * §2.4): `_post.js` is a folder hook, `create-order.post.js` beside
 * `create-order.http` is that request's hook, and anything else is a module
 * that nothing runs. So the dialog asks the question the table answers —
 * *what should run this* — and Go names the file. Asking for a name instead
 * would be asking somebody to encode a convention they came here to be told
 * about, and `utils.pre.js` beside no `utils.http` is a module with an
 * unfortunate name rather than the hook they meant.
 *
 * `Writes` and the sentence under it come from `ScriptService.Plan`, not from
 * a rule mirrored here: §2.4 has one implementation and it is in Go. That is
 * also what makes "already exists" visible before the button is pressed —
 * a folder has at most one `_pre.js`, so unlike a request there is no second
 * name to fall back to.
 */

type Kind = "folder" | "request" | "module";

export function CreateScriptDialog({
  folder,
  requests,
  onClose,
  onCreated,
}: {
  /** The folder it goes in, as a node path; null when the dialog is closed. */
  folder: string | null;
  /** Every request in the collection, for the request-hook picker. */
  requests: Node[];
  onClose: () => void;
  onCreated: (nodePath: string) => void;
}) {
  const open = folder !== null;
  const [kind, setKind] = useState<Kind>("folder");
  const [phase, setPhase] = useState<"pre" | "post">("post");
  const [request, setRequest] = useState("");
  const [name, setName] = useState("");
  const [plan, setPlan] = useState<ScriptPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  /**
   * The requests this folder holds, first and separated from the rest.
   *
   * A request hook has to sit beside its request, so choosing one from
   * another folder writes into that folder instead of the one the dialog
   * says it is in. Both are legitimate — you may well have opened the menu
   * from the wrong row — so the list carries every request and puts the ones
   * that need no explanation at the top.
   */
  const nearby = useMemo(() => {
    const inFolder = requests.filter((node) => parentOf(node.path) === folder);
    const rest = requests.filter((node) => parentOf(node.path) !== folder);
    return [...inFolder, ...rest];
  }, [requests, folder]);

  useEffect(() => {
    if (!open) return;
    setKind("folder");
    setPhase("post");
    setRequest(nearby[0]?.path ?? "");
    setName("");
    setPlan(null);
    setError(null);
    setBusy(false);
    // `nearby` is recomputed whenever the tree changes; resetting on that
    // would clear the dialog under someone mid-choice, so this depends on
    // the dialog opening and nothing else.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, folder]);

  // Go answers what will be written and what will run it, on every change.
  // It is a local call, and the alternative is a second implementation of
  // §2.4 living in the window.
  useEffect(() => {
    if (!open) return;
    let live = true;
    ScriptService.Plan({ kind, folder: folder ?? "", phase, request, name })
      .then((next) => {
        if (live) setPlan(next ?? null);
      })
      .catch((cause: unknown) => {
        if (live) setError(errorText(cause));
      });
    return () => {
      live = false;
    };
  }, [open, kind, folder, phase, request, name]);

  const create = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const nodePath = await ScriptService.Create({
        kind,
        folder: folder ?? "",
        phase,
        request,
        name,
      });
      onCreated(nodePath ?? "");
      onClose();
    } catch (cause) {
      setError(errorText(cause));
      setBusy(false);
    }
  };

  const blocked = Boolean(plan?.problem) || busy || (kind === "request" && request === "");

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="w-[560px] max-w-[92vw] grid grid-cols-[minmax(0,1fr)] rounded-md border border-border-strong bg-raised p-4 sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle className="text-ui text-fg-emphasis">New script</DialogTitle>
          <DialogDescription className="text-meta text-fg-dim">
            {folder === "" ? "In the collection root." : `In ${folder}/.`}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <Field label="What runs it">
            <Picker
              value={kind}
              onChange={(next) => setKind(next as Kind)}
              options={[
                { value: "folder", label: "Every request in this folder and below" },
                { value: "request", label: "One request" },
                { value: "module", label: "Nothing — a module a hook imports" },
              ]}
            />
          </Field>

          {kind !== "module" ? (
            <Field label="When">
              <Picker
                value={phase}
                onChange={(next) => setPhase(next as "pre" | "post")}
                options={[
                  { value: "pre", label: "Before the request goes out" },
                  { value: "post", label: "After the response comes back" },
                ]}
              />
            </Field>
          ) : null}

          {kind === "request" ? (
            <Field label="Request">
              {nearby.length === 0 ? (
                <p className="text-meta text-fg-dim">
                  There are no requests in this collection yet.
                </p>
              ) : (
                <Picker
                  value={request}
                  onChange={setRequest}
                  mono
                  options={nearby.map((node) => ({ value: node.path, label: node.path }))}
                />
              )}
            </Field>
          ) : null}

          {kind === "module" ? (
            <Field label="File name">
              <Input
                {...verbatimText}
                autoFocus
                value={name}
                placeholder="idempotency"
                aria-label="File name"
                onChange={(event) => {
                  setName(event.target.value);
                  setError(null);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !blocked) {
                    event.preventDefault();
                    void create();
                  }
                }}
                className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
              />
            </Field>
          ) : null}

          <div className="rounded-md border border-border-control px-3 py-2">
            <p className="text-meta text-fg-dim">
              Writes{" "}
              <span className="font-mono break-all text-fg-secondary">
                {plan?.path || "…"}
              </span>
            </p>
            {plan?.runs ? <p className="mt-1 text-meta text-fg-faint">{plan.runs}</p> : null}
            {plan?.problem ? (
              <p className="mt-1 text-meta text-warning">{plan.problem}</p>
            ) : null}
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
            disabled={blocked}
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

/** The auth form's select, at full width. */
function Picker({
  value,
  onChange,
  options,
  mono,
}: {
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  mono?: boolean;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="h-[26px] w-full rounded-sm border-border-control bg-inset px-2 text-ui">
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="rounded-md border-border-control bg-raised">
        {options.map((option) => (
          <SelectItem
            key={option.value}
            value={option.value}
            className={mono ? "font-mono text-ui" : "text-ui"}
          >
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/** A node path's parent, "" for the collection root. */
function parentOf(nodePath: string): string {
  const cut = nodePath.lastIndexOf("/");
  return cut === -1 ? "" : nodePath.slice(0, cut);
}
