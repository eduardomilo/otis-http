import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { CloneDialog } from "@/components/shell/clone-dialog";
import { NewCollectionDialog } from "@/components/shell/new-collection-dialog";
import { buildLabel, useBuildInfo } from "@/hooks/use-build-info";
import { hint } from "@/lib/platform";
import { cn } from "@/lib/utils";
import type { Recent } from "@bindings/internal/settings";
import { ImportDialog } from "@/components/shell/import-dialog";
import { useImportFlow } from "@/hooks/use-import-flow";
import { useCollection } from "@/state/collection-context";
import { useLog } from "@/state/log-context";
import { useRecents } from "@/state/use-recents";
import { StartService } from "@bindings/internal/services";
import type { StartDefaults } from "@bindings/internal/services";

/**
 * First launch, no collection open (screen 2b).
 *
 * One 720px column: a headline, four starter cards in a 2x2 grid, the recent
 * list, and a footer. All four are wired — Clone and Start fresh were marked
 * `soon` because neither was in the A–E plan (DESIGN-NOTES §9.9); §9.39 is
 * what they turned out to be.
 */
export function EmptyState() {
  const { openViaDialog, open, error } = useCollection();
  const build = useBuildInfo();
  const { log } = useLog();
  // What the two dialogs need before they can be drawn: where to start
  // looking, the conventional folder name, and whether this machine has a git
  // to clone with. Read once — none of it changes while this screen is up,
  // because a collection opening is what replaces the screen.
  const [defaults, setDefaults] = useState<StartDefaults | null>(null);
  const [starting, setStarting] = useState<"new" | "clone" | null>(null);
  useEffect(() => {
    StartService.Defaults()
      .then((value) => setDefaults(value ?? null))
      .catch((cause: unknown) => log("Could not read the start defaults", String(cause)));
  }, [log]);
  // The start screen is the one place an import becomes a *new* collection,
  // so it starts the flow itself: AppShell does not exist while this is on
  // screen.
  const { start: startImport, dialog: importDialog } = useImportFlow();

  return (
    <div className="flex min-h-0 flex-1 justify-center overflow-y-auto">
      <div className="flex w-[720px] flex-col gap-7 px-4 py-[120px]">
        <header className="flex flex-col gap-2">
          <h1 className="text-display font-medium tracking-[-.01em] text-fg-emphasis">
            Open a collection
          </h1>
          <p className="text-ui text-fg-muted">
            A collection is a folder of <code className="font-mono">.http</code> files. It
            lives next to your code, versions with git, and works without an account.
          </p>
        </header>

        {error ? (
          <p className="rounded-md border border-border-danger px-3 py-2 font-mono text-meta text-destructive">
            {error}
          </p>
        ) : null}

        <div className="grid grid-cols-2 gap-2.5">
          <StarterCard
            title="Open folder"
            shortcut={hint("O")}
            description="Point at a directory that contains .http files. Nested folders become the tree."
            example="~/code/…/.requests"
            highlighted
            onClick={() => void openViaDialog()}
          />
          <StarterCard
            title="Clone repository"
            shortcut={hint("O", true)}
            description={
              defaults?.cloneBlocked ||
              "Clone a git repo and open its collection. Your own git does the authenticating; Otis never asks for a password."
            }
            example="git@github.com:org/api-requests.git"
            disabled={Boolean(defaults?.cloneBlocked)}
            note={defaults?.cloneBlocked ? "no git" : undefined}
            onClick={() => setStarting("clone")}
          />
          <StarterCard
            title="Import from Postman"
            shortcut={hint("I")}
            description="Convert a Postman collection or environment export into .http files. Nothing is uploaded."
            example="collection.json → *.http"
            onClick={() => void startImport()}
          />
          <StarterCard
            title="Start fresh"
            shortcut={hint("N")}
            description="Create an empty collection folder with one example request and a local environment."
            example="mkdir .requests"
            onClick={() => setStarting("new")}
          />
        </div>

        <RecentList onOpen={(path) => void open(path).catch(() => {})} />

        {/* The version lives here because this is the screen every launch
            without a collection lands on, and because there is no updater to
            ask (DESIGN-NOTES §9.18). ⌘P › "Copy version" is the other half:
            this one is for reading, that one for pasting into a bug report. */}
        <footer className="flex items-center justify-center gap-3 border-t border-border pt-5 text-meta text-fg-faint">
          <span>Everything stays on this machine and in your repo.</span>
          {build ? (
            <>
              <span>·</span>
              <span className="font-mono" title={`Otis ${buildLabel(build)} — ${build.platform}`}>
                {buildLabel(build)}
              </span>
            </>
          ) : null}
        </footer>
      </div>

      <ImportDialog
        {...importDialog}
        onImported={(root) => {
          // Go opened it already; the window has nothing to navigate to but
          // the tree, which the collection change puts on screen anyway.
          void root;
        }}
      />

      {/* Both of these open the collection in Go before they return, which is
          what replaces this screen with the shell — so neither has anything
          to navigate to. A note is the one thing worth carrying out: a clone
          that worked but held no .http files has an outcome the tree cannot
          explain by itself. */}
      <NewCollectionDialog
        open={starting === "new"}
        defaults={defaults}
        onClose={() => setStarting(null)}
        onCreated={(result) => {
          if (result.note) log(result.note, "");
        }}
      />
      <CloneDialog
        open={starting === "clone"}
        defaults={defaults}
        onClose={() => setStarting(null)}
        onCloned={(result) => {
          if (result.note) log(result.note, "");
        }}
      />
    </div>
  );
}

/**
 * One starter card.
 *
 * `note` is the small label beside the shortcut. It used to be the word
 * "soon", hard-coded, for the two cards that were not built; now that they
 * are, the only thing that can disable a card is the machine — no git on
 * PATH — and the label says which, because "soon" for something that is
 * finished would be a lie about the app rather than a fact about the machine.
 */
function StarterCard({
  title,
  shortcut,
  description,
  example,
  highlighted,
  disabled,
  note,
  onClick,
}: {
  title: string;
  shortcut: string;
  description: string;
  example: string;
  highlighted?: boolean;
  disabled?: boolean;
  note?: string;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex flex-col gap-2 rounded-md border px-4 py-3.5 text-left",
        highlighted ? "border-primary" : "border-border-control",
        disabled ? "cursor-default opacity-60" : "hover:bg-control",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-result text-fg-emphasis">{title}</span>
        <span className="flex items-center gap-2">
          {note ? <span className="text-meta text-fg-faint">{note}</span> : null}
          <span className="rounded-sm border border-border-control px-1 font-mono text-label text-fg-faint">
            {shortcut}
          </span>
        </span>
      </div>
      <p className="text-ui text-fg-muted">{description}</p>
      <p className="font-mono text-ui text-fg-faint">{example}</p>
    </button>
  );
}

function RecentList({ onOpen }: { onOpen: (path: string) => void }) {
  const { recents, remove, format } = useRecents();

  if (recents.length === 0) return null;

  return (
    <section className="flex flex-col gap-1">
      <div className="flex items-center justify-between px-1 pb-1">
        <h2 className="text-ui text-fg-muted">Recent</h2>
        <span className="text-meta text-fg-faint">Stored locally</span>
      </div>
      {recents.map((recent) => (
        <RecentRow
          key={recent.path}
          recent={recent}
          when={format(recent.lastOpened)}
          onOpen={() => onOpen(recent.path)}
          onRemove={() => void remove(recent.path)}
        />
      ))}
    </section>
  );
}

function RecentRow({
  recent,
  when,
  onOpen,
  onRemove,
}: {
  recent: Recent & { display: string };
  when: string;
  onOpen: () => void;
  onRemove: () => void;
}) {
  return (
    <div
      className={cn(
        "group flex h-[30px] items-center gap-3 rounded-sm px-1",
        recent.missing ? "text-fg-faint" : "hover:bg-selected",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        disabled={recent.missing}
        className="flex min-w-0 flex-1 items-center gap-3 text-left disabled:cursor-default"
      >
        <span className={cn("shrink-0 text-ui", recent.missing ? "text-fg-faint" : "text-fg-emphasis")}>
          {recent.name}
        </span>
        <span className="truncate font-mono text-ui text-fg-dim">{recent.display}</span>
      </button>

      {recent.missing ? (
        <>
          <span className="shrink-0 rounded-sm border border-border-control px-1 text-meta text-fg-faint">
            missing
          </span>
          <Button
            type="button"
            variant="ghost"
            onClick={onRemove}
            className="h-5 shrink-0 px-1.5 text-meta text-fg-faint hover:text-destructive"
          >
            Remove
          </Button>
        </>
      ) : (
        <span className="shrink-0 text-meta text-fg-faint">{when}</span>
      )}
    </div>
  );
}
