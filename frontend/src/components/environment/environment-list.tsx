import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";

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
import { BackToRequests } from "@/components/shell/back-to-requests";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { verbatimText } from "@/lib/text-input";
import { useEnvironments } from "@/state/environment-context";
import { EnvironmentService, LogService } from "@bindings/internal/services";
import type { EnvironmentSummary } from "@bindings/internal/services";

/**
 * The sidebar on screen 1c: the environment list in place of the request tree.
 *
 * Rows are the same 24px as a tree row (DESIGN-NOTES §4.3) and carry the same
 * selection treatment as every other list in the app — `--bg-selected` with a
 * 2px accent left edge (§2.4). The 48px method gutter becomes the status dot's
 * slot, right-aligned, so the dot lands on the same axis as a method label
 * (§4.2).
 *
 * The dot has three states, from §2.6: the accent for the active environment,
 * red for one that confirms before send (which is what marks production), and
 * `--fg-faint` for the rest.
 */
export function EnvironmentList({ activeName }: { activeName: string }) {
  const { environments, active, keychain } = useEnvironments();
  const [creating, setCreating] = useState(false);
  // The environment the row menu is deleting. Duplicating needs no dialog:
  // it writes a file whose name is derived, beside the one it copied.
  const [managing, setManaging] = useState<EnvironmentSummary | null>(null);

  return (
    <div className="flex h-full flex-col bg-background px-2.5">
      <div className="flex h-12 shrink-0 items-center justify-between">
        <div className="flex min-w-0 items-center gap-1">
          {/* This list replaced the request tree, so it carries the way back
              (DESIGN-NOTES §9.23). */}
          <BackToRequests />
          <span className="text-ui text-fg-muted">Environments</span>
        </div>
        <button
          type="button"
          onClick={() => setCreating(true)}
          title="New environment"
          aria-label="New environment"
          className="flex size-6 items-center justify-center rounded-sm text-fg-muted hover:bg-control hover:text-fg-emphasis"
        >
          <Plus className="size-4" />
        </button>
      </div>

      <div className="min-h-0 overflow-auto">
        {environments.length === 0 ? (
          <p className="px-1 py-2 text-meta text-fg-faint">
            No environments yet. A collection does not need one.
          </p>
        ) : (
          environments.map((env) => (
            <Row
              key={env.name}
              env={env}
              active={env.name === active}
              open={env.name === activeName}
              onManage={setManaging}
            />
          ))
        )}
      </div>

      {/* The note the design puts under the list. It is the product's claim
          about secrets, so it is worth the space. */}
      <p className="mt-4 border-t border-border px-1 pt-3 text-meta text-fg-dim">
        Environments are JSON files in{" "}
        <span className="font-mono text-fg-faint">env/</span>. Secrets are stored per-machine in the
        OS keychain and never written to disk.
      </p>

      {!keychain.available ? (
        <p className="mt-2 px-1 pb-3 text-meta text-modified">
          This machine has no keychain Otis can reach, so stored values cannot be read or written.
          The committed references are still shown.
        </p>
      ) : null}

      <div className="flex-1" />

      <NewEnvironmentDialog open={creating} onOpenChange={setCreating} />
      <DeleteEnvironmentDialog env={managing} onClose={() => setManaging(null)} />
    </div>
  );
}

/**
 * One environment, with the same three operations a request row offers.
 *
 * Duplicate is worth more here than anywhere else in the app: an environment
 * is mostly a shape — the same dozen names with different values — and
 * "staging, but pointed at my machine" is how the second one usually starts.
 * It copies the stored secret values across too, inside Go and inside the
 * keychain (EnvironmentService.Duplicate); a copy full of references to
 * nothing would not be a copy of anything useful.
 */
function Row({
  env,
  active,
  open,
  onManage,
}: {
  env: EnvironmentSummary;
  active: boolean;
  open: boolean;
  onManage: (env: EnvironmentSummary) => void;
}) {
  const navigate = useNavigate();
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
    <Link
      to="/env/$name"
      params={{ name: env.name }}
      title={env.error ? `${env.path}: ${env.error}` : env.description || env.path}
      className={cn(
        "flex h-[var(--row-height)] items-center rounded-sm",
        open
          ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--accent)]"
          : "text-fg-secondary hover:bg-control",
      )}
    >
      {/* The dot sits in the method gutter's 48px slot, right-aligned, so it
          shares an axis with the method labels of the request tree (§4.2). */}
      <span className="flex w-[var(--method-gutter-width)] shrink-0 justify-end pr-2">
        <span
          className={cn(
            "size-1.5 rounded-full",
            active
              ? "bg-primary"
              : env.confirmBeforeSend
                ? "bg-destructive"
                : "bg-fg-faint",
          )}
        />
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-ui">{env.name}</span>
      <span
        className={cn(
          "shrink-0 pr-1 pl-2 font-mono text-meta",
          env.error ? "text-destructive" : "text-fg-faint",
        )}
      >
        {env.error ? "broken" : `${env.name}.json`}
      </span>
    </Link>
      </ContextMenuTrigger>
      <ContextMenuContent className="w-52 text-ui *:data-[slot=context-menu-item]:text-ui">
        <ContextMenuItem
          onSelect={() => {
            void EnvironmentService.Duplicate(env.name)
              .then((doc) => doc && navigate({ to: "/env/$name", params: { name: doc.name } }))
              .catch((cause: unknown) =>
                LogService.Record("error", "environment", `Could not duplicate ${env.name}`, String(cause)),
              );
          }}
        >
          Duplicate
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem
          onSelect={() => onManage(env)}
          className="text-destructive data-[disabled]:text-destructive"
        >
          Delete…
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/**
 * Deleting an environment.
 *
 * It says what stays behind, because that is the part nobody expects: the
 * values its secret references pointed at are left in the keychain. They
 * belong to the machine rather than to the file (EnvironmentService.Delete),
 * and a colleague's branch may still reference them — so deleting the file is
 * not a way to forget a secret, and the dialog says which operation is.
 */
function DeleteEnvironmentDialog({
  env,
  onClose,
}: {
  env: EnvironmentSummary | null;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  if (!env) return null;
  return (
    <AlertDialog open onOpenChange={(next) => !next && onClose()}>
      <AlertDialogContent className="max-w-[440px]">
        <AlertDialogHeader>
          <AlertDialogTitle>Delete environment {env.name}?</AlertDialogTitle>
          <AlertDialogDescription className="text-meta text-fg-muted">
            <span className="font-mono text-fg-dim">{env.path}</span> is removed from disk. It is
            committed, so git still has it.
            <br />
            Any secret values it referenced stay in this machine's keychain — they belong to the
            machine, not to the file, and a colleague's branch may still reference them. Removing
            the row from the table is what forgets a value.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="gap-2">
          <AlertDialogCancel className="h-6 rounded-sm border-border-control bg-control px-2.5 text-ui text-fg-secondary">
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            variant="outline"
            onClick={(event) => {
              event.preventDefault();
              void EnvironmentService.Delete(env.name)
                .then(() => {
                  onClose();
                  void navigate({ to: "/env" });
                })
                .catch((cause: unknown) => {
                  onClose();
                  void LogService.Record("error", "environment", `Could not delete ${env.name}`, String(cause));
                });
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

/**
 * Creating an environment writes a file, so it says the file name before it
 * does (DESIGN-NOTES §8.2: every write to disk is announced before it happens).
 */
/**
 * Naming a new environment.
 *
 * Exported because the environment *index* route needs it too: with no
 * environments there is nothing for the list to list, and the centre pane has
 * to carry the same action rather than leaving a `+` in the sidebar as the
 * only way to make the first one.
 */
export function NewEnvironmentDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const trimmed = name.trim();

  async function create() {
    try {
      const doc = await EnvironmentService.Create(trimmed);
      onOpenChange(false);
      setName("");
      setError(null);
      if (doc) void navigate({ to: "/env/$name", params: { name: doc.name } });
    } catch (cause) {
      setError(String(cause));
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) {
          setName("");
          setError(null);
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>New environment</AlertDialogTitle>
          <AlertDialogDescription>
            {trimmed ? (
              <>
                Writes <span className="font-mono text-fg-secondary">env/{trimmed}.json</span> as an
                empty object.
              </>
            ) : (
              "The name becomes the file name under env/."
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Input
          {...verbatimText}
          autoFocus
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && trimmed) {
              event.preventDefault();
              void create();
            }
          }}
          placeholder="staging"
          aria-label="Environment name"
          className="h-[26px] rounded-md border-border-control bg-inset font-mono text-ui md:text-ui dark:bg-inset placeholder:text-fg-dim"
        />
        {error ? <p className="text-meta text-destructive">{error}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!trimmed}
            onClick={(event) => {
              event.preventDefault();
              void create();
            }}
          >
            Create
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
