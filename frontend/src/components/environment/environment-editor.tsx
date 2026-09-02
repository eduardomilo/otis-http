import { useCallback, useEffect, useState } from "react";
import { FileText, Lock } from "lucide-react";

import { SecretDetail } from "@/components/environment/secret-detail";
import { SecretValueDialog } from "@/components/environment/secret-value-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { EnvironmentService } from "@bindings/internal/services";
import type { EnvironmentDocument, EnvironmentRow } from "@bindings/internal/services";

/**
 * The environment editor (screen 1c).
 *
 * Table geometry is DESIGN-NOTES §4.5: `28px 220px 1fr 120px 60px`, 36px rows,
 * a 28px heading row. Secret rows carry the three things §8.3 requires at
 * once — an amber lock, a masked value with `letter-spacing: 2px`, and a
 * storage label reading Keychain — on the faint amber row tint of §2.6.
 *
 * # Two deviations from the design, both about the secrets rule
 *
 * **Reveal is Copy.** The design's `Reveal` chip would put the value on
 * screen, which means handing it to the webview, where it would sit in a React
 * tree, a DOM node and any devtools session. CLAUDE.md forbids that outright:
 * a resolved secret never crosses the binding. So the chip copies instead —
 * Go reads the keychain and writes the system clipboard, and the value never
 * enters this process. The user still gets at their own credential; it simply
 * never becomes pixels.
 *
 * **No "Set on this machine · Aug 28".** The design dates a stored secret. The
 * keychain does not report one, and the key index deliberately holds nothing
 * but keys, so there is nowhere honest to read a date from. The row says
 * whether a value is stored here, which is the part that changes what you do
 * next.
 */

const GRID = "grid grid-cols-[28px_220px_1fr_120px_60px] items-center gap-3 px-4";

export function EnvironmentEditor({ name }: { name: string }) {
  const [doc, setDoc] = useState<EnvironmentDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setDoc((await EnvironmentService.Load(name)) ?? null);
      setError(null);
    } catch (cause) {
      setDoc(null);
      setError(String(cause));
    }
  }, [name]);

  useEffect(() => {
    setSelected(null);
    void load();
  }, [load]);

  // Every mutating call answers with the document as it now is, so the table
  // never has to guess what the write did.
  const apply = useCallback(async (call: Promise<EnvironmentDocument | null>) => {
    try {
      const next = await call;
      if (next) setDoc(next);
      setError(null);
      return true;
    } catch (cause) {
      setError(String(cause));
      return false;
    }
  }, []);

  if (error && !doc) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-4">
        <p className="font-mono text-ui text-fg-secondary">env/{name}.json</p>
        <p className="max-w-[520px] text-center text-meta text-destructive">{error}</p>
      </div>
    );
  }
  if (!doc) return <div className="h-full" />;

  const secret = doc.rows?.find((row) => row.name === selected && row.secret) ?? null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header doc={doc} onAdd={() => setSelected(null)} apply={apply} />

      {error ? <p className="px-4 pb-2 text-meta text-destructive">{error}</p> : null}

      <div className="min-h-0 flex-1 overflow-auto">
        <div
          className={cn(
            GRID,
            "sticky top-0 z-10 h-[28px] border-b border-border bg-background text-label text-fg-dim",
          )}
        >
          <span />
          <span>Name</span>
          <span>Value</span>
          <span>Storage</span>
          <span />
        </div>

        {(doc.rows ?? []).length === 0 ? (
          <p className="px-4 py-4 text-meta text-fg-dim">
            No variables yet. Add one, or add a secret to keep its value in the keychain.
          </p>
        ) : (
          (doc.rows ?? []).map((row) => (
            <Row
              key={row.name}
              row={row}
              env={doc.name}
              selected={row.name === selected}
              keychainAvailable={doc.keychain.available}
              onSelect={() => setSelected(row.secret ? row.name : null)}
              apply={apply}
            />
          ))
        )}

        {secret ? (
          <SecretDetail
            row={secret}
            doc={doc}
            apply={apply}
            onForgotten={() => setSelected(null)}
          />
        ) : null}
      </div>
    </div>
  );
}

/** `staging · env/staging.json`, the counts, and Add variable. */
function Header({
  doc,
  onAdd,
  apply,
}: {
  doc: EnvironmentDocument;
  onAdd: () => void;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  const [adding, setAdding] = useState<"plain" | "secret" | null>(null);
  return (
    <div className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
      <span className="font-mono text-title text-fg-emphasis">{doc.name}</span>
      <span className="text-fg-faint">·</span>
      <span className="font-mono text-ui text-fg-dim">{doc.path}</span>

      <div className="flex-1" />

      <span className="font-mono text-meta text-fg-dim">
        {doc.variables} {doc.variables === 1 ? "variable" : "variables"}
        {doc.secrets > 0 ? ` · ${doc.secrets} ${doc.secrets === 1 ? "secret" : "secrets"}` : null}
      </span>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            className="h-[26px] rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
          >
            Add variable
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            onSelect={() => {
              onAdd();
              setAdding("plain");
            }}
          >
            Variable — value committed to {doc.path}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            disabled={!doc.keychain.available}
            onSelect={() => {
              onAdd();
              setAdding("secret");
            }}
          >
            Secret — value in the OS keychain
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <AddVariableDialog
        mode={adding}
        doc={doc}
        onClose={() => setAdding(null)}
        apply={apply}
      />
    </div>
  );
}

function Row({
  row,
  env,
  selected,
  keychainAvailable,
  onSelect,
  apply,
}: {
  row: EnvironmentRow;
  env: string;
  selected: boolean;
  keychainAvailable: boolean;
  onSelect: () => void;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  const [value, setValue] = useState(row.value);
  const [replacing, setReplacing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [renaming, setRenaming] = useState(false);

  // The row is re-created from Go after every write, so the field follows the
  // document rather than keeping a stale local edit.
  useEffect(() => setValue(row.value), [row.value]);

  return (
    <>
      <div
        onClick={onSelect}
        className={cn(
          "group h-9 border-b border-border-hairline",
          GRID,
          row.secret && "bg-[rgba(251,191,36,.03)]",
          selected ? "bg-selected" : "hover:bg-inset",
        )}
      >
        {/*
          Checked and disabled, with the reason on hover — the same treatment
          the Headers tab gives its own checkbox, and for the same reason.
          DESIGN-NOTES §9.5 is explicit that a disabled row has no on-disk
          representation: the design puts a checkbox on every environment row,
          and docs/FORMAT.md §4.3 defines no way to write a disabled key into
          an environment file. Removing the variable is in the row menu, which
          is an operation the format does have.
        */}
        <Checkbox
          checked
          disabled
          aria-label={`${row.name} enabled`}
          title="An environment file has no way to write a disabled key (docs/FORMAT.md §4.3, DESIGN-NOTES §9.5). Remove the variable from the row menu instead."
          className="justify-self-end disabled:opacity-100"
        />

        <span className="truncate font-mono text-ui text-fg">{row.name}</span>

        {row.secret ? (
          <div className="flex min-w-0 items-center gap-2">
            {/* §8.3 and §3: a masked value with letter-spacing 2px, so the
                dots read as a field rather than as a word. */}
            <span
              className="truncate font-mono text-ui tracking-[2px] text-fg-dim"
              aria-label="masked secret value"
            >
              {row.present ? "••••••••••••••••" : ""}
            </span>
            {row.present ? (
              <>
                <Chip
                  onClick={() => void EnvironmentService.CopySecretValue(env, row.name)}
                  title={`Copies the value of ${row.key} to the clipboard. Otis never puts a secret value on screen — see the note in the panel below.`}
                >
                  Copy
                </Chip>
                <Chip disabled={!keychainAvailable} onClick={() => setReplacing(true)}>
                  Replace
                </Chip>
              </>
            ) : (
              <>
                <span className="shrink-0 text-meta text-modified">not set on this machine</span>
                <Chip disabled={!keychainAvailable} onClick={() => setReplacing(true)}>
                  Set value
                </Chip>
              </>
            )}
          </div>
        ) : (
          <input
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onBlur={() => {
              if (value !== row.value) void apply(EnvironmentService.SetVariable(env, row.name, value));
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.currentTarget.blur();
              if (event.key === "Escape") {
                setValue(row.value);
                event.currentTarget.blur();
              }
            }}
            aria-label={`${row.name} value`}
            className="w-full bg-transparent font-mono text-ui text-fg-secondary outline-none"
          />
        )}

        {row.secret ? (
          <span className="flex items-center gap-1.5 text-meta text-secret">
            <Lock className="size-3 shrink-0" />
            Keychain
          </span>
        ) : (
          <span className="flex items-center gap-1.5 truncate font-mono text-meta text-fg-dim">
            <FileText className="size-3 shrink-0 text-fg-faint" />
            <span className="truncate">{env}.json</span>
          </span>
        )}

        <RowMenu
          row={row}
          keychainAvailable={keychainAvailable}
          onRename={() => setRenaming(true)}
          onRemove={() => setRemoving(true)}
          onMakeSecret={() => setReplacing(true)}
          onMakePlain={() => void apply(EnvironmentService.SetVariable(env, row.name, ""))}
        />
      </div>

      <SecretValueDialog
        open={replacing}
        onOpenChange={setReplacing}
        env={env}
        name={row.name}
        existing={row.secret}
        apply={apply}
      />
      <RenameDialog
        open={renaming}
        onOpenChange={setRenaming}
        env={env}
        row={row}
        apply={apply}
      />
      <RemoveDialog
        open={removing}
        onOpenChange={setRemoving}
        env={env}
        row={row}
        apply={apply}
      />
    </>
  );
}

/** §4.4's inline chip: 10–11px, radius 3px, on --border-control. */
function Chip({
  children,
  onClick,
  disabled,
  title,
}: {
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      title={title}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
      className="shrink-0 rounded-sm border border-border-control px-1.5 text-meta text-fg-muted hover:bg-control hover:text-fg-emphasis disabled:opacity-40"
    >
      {children}
    </button>
  );
}

/** The `···` row menu (§6: a DropdownMenu whose trigger is text, not a button). */
function RowMenu({
  row,
  keychainAvailable,
  onRename,
  onRemove,
  onMakeSecret,
  onMakePlain,
}: {
  row: EnvironmentRow;
  keychainAvailable: boolean;
  onRename: () => void;
  onRemove: () => void;
  onMakeSecret: () => void;
  onMakePlain: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        onClick={(event) => event.stopPropagation()}
        className="justify-self-end px-2 text-fg-ghost group-hover:text-fg-muted"
        aria-label={`${row.name} menu`}
      >
        ···
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onRename}>Rename…</DropdownMenuItem>
        {row.secret ? (
          <DropdownMenuItem onSelect={onMakePlain}>
            Make it a plain variable — the value is forgotten
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem disabled={!keychainAvailable} onSelect={onMakeSecret}>
            Make it a secret — the value moves to the keychain
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={onRemove}>
          Remove…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AddVariableDialog({
  mode,
  doc,
  onClose,
  apply,
}: {
  mode: "plain" | "secret" | null;
  doc: EnvironmentDocument;
  onClose: () => void;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const secret = mode === "secret";

  function reset() {
    setName("");
    setValue("");
    onClose();
  }

  async function add() {
    const ok = await apply(
      secret
        ? EnvironmentService.MakeSecret(doc.name, name.trim(), value)
        : EnvironmentService.SetVariable(doc.name, name.trim(), value),
    );
    if (ok) reset();
  }

  return (
    <AlertDialog open={mode !== null} onOpenChange={(open) => !open && reset()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{secret ? "Add a secret" : "Add a variable"}</AlertDialogTitle>
          <AlertDialogDescription>
            {secret ? (
              <>
                Writes{" "}
                <span className="font-mono text-fg-secondary">
                  "{name.trim() || "name"}": {"{"} "$secret": "keychain" {"}"}
                </span>{" "}
                to <span className="font-mono text-fg-secondary">{doc.path}</span>. The value goes
                to this machine's keychain and is never written to disk.
              </>
            ) : (
              <>
                Writes the name and value to{" "}
                <span className="font-mono text-fg-secondary">{doc.path}</span>, which is committed.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-2">
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="name"
            aria-label="Variable name"
            className="h-[26px] rounded-md border border-border-control bg-inset px-2 font-mono text-ui text-fg outline-none placeholder:text-fg-dim"
          />
          <input
            type={secret ? "password" : "text"}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && name.trim()) {
                event.preventDefault();
                void add();
              }
            }}
            placeholder="value"
            aria-label="Variable value"
            className="h-[26px] rounded-md border border-border-control bg-inset px-2 font-mono text-ui text-fg outline-none placeholder:text-fg-dim"
          />
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!name.trim() || (secret && !value)}
            onClick={(event) => {
              event.preventDefault();
              void add();
            }}
          >
            Add
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function RenameDialog({
  open,
  onOpenChange,
  env,
  row,
  apply,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  env: string;
  row: EnvironmentRow;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  const [to, setTo] = useState(row.name);
  useEffect(() => setTo(row.name), [row.name, open]);

  async function rename() {
    if (await apply(EnvironmentService.RenameVariable(env, row.name, to.trim()))) {
      onOpenChange(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Rename {row.name}</AlertDialogTitle>
          <AlertDialogDescription>
            {row.secret
              ? "The keychain entry moves with the reference, so the value is not orphaned."
              : "Every {{reference}} to the old name stops resolving; Otis does not rewrite requests."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <input
          autoFocus
          value={to}
          onChange={(event) => setTo(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && to.trim()) {
              event.preventDefault();
              void rename();
            }
          }}
          aria-label="New name"
          className="h-[26px] rounded-md border border-border-control bg-inset px-2 font-mono text-ui text-fg outline-none"
        />
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!to.trim() || to.trim() === row.name}
            onClick={(event) => {
              event.preventDefault();
              void rename();
            }}
          >
            Rename
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/** Removing is destructive, so the dialog names exactly what is lost. */
function RemoveDialog({
  open,
  onOpenChange,
  env,
  row,
  apply,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  env: string;
  row: EnvironmentRow;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {row.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {row.secret ? (
              <>
                The reference leaves <span className="font-mono text-fg-secondary">{env}.json</span>{" "}
                and the value is removed from this machine's keychain. A value nothing references
                and nobody can see is worse than no value, so the two go together.
              </>
            ) : (
              <>
                The variable leaves{" "}
                <span className="font-mono text-fg-secondary">{env}.json</span>. It is one line in
                the diff, so git can put it back.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={async (event) => {
              event.preventDefault();
              if (await apply(EnvironmentService.RemoveVariable(env, row.name))) {
                onOpenChange(false);
              }
            }}
          >
            Remove
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
