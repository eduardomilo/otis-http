import { useState } from "react";

import { Button } from "@/components/ui/button";
import { errorText } from "@/lib/errors";
import { StartService } from "@bindings/internal/services";

/**
 * "Where it goes": a read-only path and the button that changes it.
 *
 * Shared by the two start-screen dialogs because they ask the same question
 * and must answer it the same way — the path is never typed, it comes from the
 * native picker, because the frontend never touches disk (CLAUDE.md) and a
 * typed path is a path nobody has checked exists.
 */
export function LocationField({
  value,
  onChange,
  onError,
}: {
  value: string;
  onChange: (path: string) => void;
  onError: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);

  const choose = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const chosen = await StartService.ChooseLocation();
      // Cancelling the picker returns "" and is not a failure.
      if (chosen) onChange(chosen);
    } catch (cause) {
      onError(errorText(cause));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex items-center gap-2">
      <span className="min-w-0 flex-1 truncate rounded-sm border border-border-control bg-inset px-2 py-1 font-mono text-ui text-fg-secondary">
        {value || <span className="text-fg-faint">Choose a folder…</span>}
      </span>
      <Button
        type="button"
        disabled={busy}
        onClick={() => void choose()}
        className="h-[26px] shrink-0 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
      >
        Choose…
      </Button>
    </div>
  );
}
