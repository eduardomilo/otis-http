import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";

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
import { EnvironmentService } from "@bindings/internal/services";
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
            <Row key={env.name} env={env} active={env.name === active} open={env.name === activeName} />
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
    </div>
  );
}

function Row({
  env,
  active,
  open,
}: {
  env: EnvironmentSummary;
  active: boolean;
  open: boolean;
}) {
  return (
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
