import { AlertTriangle } from "lucide-react";

/**
 * The file changed on disk while this tab had unsaved edits.
 *
 * Non-blocking, as increment 10 requires: it is a strip above the editor, not
 * a modal, so the edits stay reachable and nothing is decided for the user. A
 * document with *no* unsaved edits never gets here — it simply takes the new
 * file, the way the tree does (increment 9).
 *
 * Reload discards the draft and takes the file. Keep mine dismisses the strip
 * and makes the new file the baseline, so the next save is a deliberate
 * overwrite rather than a silent one.
 */
export function ConflictBanner({
  path,
  onReload,
  onKeepMine,
}: {
  path: string;
  onReload: () => void;
  onKeepMine: () => void;
}) {
  return (
    <div className="flex shrink-0 items-center gap-2.5 border-b border-border-secret bg-secret/5 px-4 py-1.5">
      <AlertTriangle className="size-3.5 shrink-0 text-warning" />
      <p className="min-w-0 flex-1 text-meta text-fg-secondary">
        <span className="font-mono text-fg">{path}</span> changed on disk while you were editing it.
      </p>
      <button
        type="button"
        onClick={onReload}
        className="h-6 shrink-0 rounded-sm border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:text-fg-emphasis"
      >
        Reload
      </button>
      <button
        type="button"
        onClick={onKeepMine}
        className="h-6 shrink-0 rounded-sm border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:text-fg-emphasis"
      >
        Keep mine
      </button>
    </div>
  );
}
