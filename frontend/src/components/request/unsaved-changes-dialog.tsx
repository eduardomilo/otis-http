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
import { nodeDisplayName } from "@/lib/paths";

/** What the user chose when asked about a tab with unsaved changes. */
export type DiscardChoice = "save" | "discard" | "cancel";

/**
 * The question closing a dirty tab asks.
 *
 * `AlertDialog`, as DESIGN-NOTES §6 maps a confirmation to — the design draws
 * one only behind "Discard changes…" in the diff view, so the wording here
 * follows that: it names the file and says what each button does to the file
 * on disk, in keeping with §8.2 ("every write to disk is announced before it
 * happens").
 *
 * Escape and the scrim both mean Cancel, which is why the promise resolves
 * from `onOpenChange` as well as from the buttons: a dialog dismissed without
 * an answer must not leave the close waiting forever.
 */
export function UnsavedChangesDialog({
  path,
  onAnswer,
}: {
  /** The tab being closed, or null when nothing is being asked. */
  path: string | null;
  onAnswer: (choice: DiscardChoice) => void;
}) {
  return (
    <AlertDialog
      open={path !== null}
      onOpenChange={(open) => {
        if (!open) onAnswer("cancel");
      }}
    >
      <AlertDialogContent className="max-w-[420px]">
        <AlertDialogHeader>
          <AlertDialogTitle className="text-ui font-medium text-fg-emphasis">
            Save changes to {path ? nodeDisplayName(path) : ""}?
          </AlertDialogTitle>
          <AlertDialogDescription className="text-meta text-fg-muted">
            <span className="font-mono text-fg-dim">{path}</span> has unsaved edits. Discarding
            leaves the file on disk exactly as it is.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter className="gap-2">
          {/* AlertDialogCancel and AlertDialogAction already render a Button
              (see components/ui/alert-dialog.tsx), so these carry classes
              rather than nesting another one. Sizes are DESIGN-NOTES §4.4's
              small button: 24px tall, radius 3px. */}
          <AlertDialogCancel className="h-6 rounded-sm border-border-control bg-control px-2.5 text-ui text-fg-secondary">
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            variant="outline"
            onClick={() => onAnswer("discard")}
            className="h-6 rounded-sm border-border-danger bg-transparent px-2.5 text-ui text-destructive hover:bg-destructive/10"
          >
            Discard
          </AlertDialogAction>
          <AlertDialogAction
            onClick={() => onAnswer("save")}
            className="h-6 rounded-sm bg-primary px-2.5 text-ui font-medium text-primary-foreground hover:bg-primary-hover"
          >
            Save
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
