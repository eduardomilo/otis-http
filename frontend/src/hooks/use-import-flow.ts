import { useCallback, useState } from "react";

import { ImportService } from "@bindings/internal/services";
import type { ImportPlan } from "@bindings/internal/services";

/**
 * Choosing a Postman export and holding the plan until it is confirmed.
 *
 * It is a hook rather than state in one component because the two places an
 * import starts from are on opposite sides of the tree: the start screen's
 * card, which `__root` renders when nothing is open, and the command palette,
 * which lives inside `AppShell` and only exists when something is. One flow,
 * two mount points, and no context for a piece of state that never has to be
 * read anywhere else.
 */
export function useImportFlow() {
  const [plan, setPlan] = useState<ImportPlan | null>(null);

  const start = useCallback(async () => {
    try {
      const chosen = await ImportService.Choose();
      // Cancelling the picker returns a zero plan, which is not an error and
      // not a dialog either.
      if (chosen?.id) setPlan(chosen);
    } catch {
      // A file that is not a Postman export fails in Go with a message about
      // the file. There is nowhere useful to put it before the dialog exists,
      // and the picker is one click away.
    }
  }, []);

  return {
    start,
    /** Spread onto ImportDialog. */
    dialog: {
      plan,
      onPlan: setPlan,
      onClose: () => setPlan(null),
    },
  };
}
