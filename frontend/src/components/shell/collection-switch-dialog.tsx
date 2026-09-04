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

/** Which way the collection is being left. */
export type CollectionAction = "open" | "close";

/**
 * The question leaving a collection asks when a tab has unsaved changes.
 *
 * Opening another collection and closing this one both drop every open tab,
 * and a draft lives only in the window until it is saved — so both silently
 * threw away unsaved work before this existed. The tab bar's dirty dot is the
 * promise that a draft is still there; leaving without asking breaks it.
 *
 * `AlertDialog`, which DESIGN-NOTES §6 maps a confirmation to, and the wording
 * follows `unsaved-changes-dialog`: it says how many drafts, and what the
 * button does to them. Unlike that dialog there is no **Save** option, because
 * "save all" is not an operation Otis has — each document is saved against its
 * own file and a bulk write is not something to invent behind a confirmation.
 * Cancel and go back is the way to keep them.
 */
export function CollectionSwitchDialog({
  action,
  dirtyCount,
  onAnswer,
}: {
  /** What is being attempted, or null when nothing is being asked. */
  action: CollectionAction | null;
  dirtyCount: number;
  onAnswer: (proceed: boolean) => void;
}) {
  const leaving = action === "open" ? "Open another collection" : "Close this collection";
  return (
    <AlertDialog
      open={action !== null}
      // Escape and the scrim both mean cancel, so the promise is answered
      // from here as well as from the buttons.
      onOpenChange={(open) => {
        if (!open) onAnswer(false);
      }}
    >
      <AlertDialogContent className="max-w-[420px]">
        <AlertDialogHeader>
          <AlertDialogTitle className="text-ui font-medium text-fg-emphasis">
            {leaving} and discard {dirtyCount === 1 ? "1 unsaved change" : `${dirtyCount} unsaved changes`}?
          </AlertDialogTitle>
          <AlertDialogDescription className="text-meta text-fg-dim">
            {dirtyCount === 1
              ? "One open request has edits that are not written to its file."
              : `${dirtyCount} open requests have edits that are not written to their files.`}{" "}
            Leaving this collection closes every tab, and those edits are lost. Cancel and save
            them first if you want to keep them.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          {/* Styled exactly as unsaved-changes-dialog's pair, because this is
              the same question about the same kind of loss. Note the
              `variant="outline"`: without it AlertDialogAction takes the
              Button's default variant, whose `bg-primary text-primary-foreground`
              wins over these classes and paints the destructive button
              accent-green. */}
          <AlertDialogCancel
            className="h-6 rounded-sm border-border-control bg-control px-2.5 text-ui text-fg-secondary"
            onClick={() => onAnswer(false)}
          >
            Cancel
          </AlertDialogCancel>
          <AlertDialogAction
            variant="outline"
            className="h-6 rounded-sm border-border-danger bg-transparent px-2.5 text-ui text-destructive hover:bg-destructive/10"
            onClick={() => onAnswer(true)}
          >
            Discard and {action === "open" ? "open" : "close"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
