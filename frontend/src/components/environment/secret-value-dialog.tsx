import { useEffect, useState } from "react";

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
import { verbatimText } from "@/lib/text-input";
import { EnvironmentService } from "@bindings/internal/services";
import type { EnvironmentDocument } from "@bindings/internal/services";

/**
 * The one direction a secret value is allowed to travel: in.
 *
 * The user types the value here and it goes window → Go → keychain. That is
 * how a value gets into the store at all, and it is not what the secrets rule
 * forbids — the rule is that a *resolved* value never comes back out
 * (CLAUDE.md; docs/FORMAT.md §5). The field is a password input so the
 * webview does not offer to remember it, and nothing keeps the string after
 * the call returns.
 *
 * `existing` chooses between replacing the value of a reference that is
 * already in the file and turning a plain variable into a secret. The second
 * is the destructive-looking one, so it says what it will do to the committed
 * file before it does it (DESIGN-NOTES §8.2).
 */
export function SecretValueDialog({
  open,
  onOpenChange,
  env,
  name,
  existing,
  apply,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  env: string;
  name: string;
  /** True when the file already holds a `{"$secret": "keychain"}` reference. */
  existing: boolean;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
}) {
  const [value, setValue] = useState("");

  // Never keep a typed value around after the dialog closes.
  useEffect(() => {
    if (!open) setValue("");
  }, [open]);

  async function save() {
    const ok = await apply(
      existing
        ? EnvironmentService.SetSecretValue(env, name, value)
        : EnvironmentService.MakeSecret(env, name, value),
    );
    if (ok) {
      setValue("");
      onOpenChange(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {existing ? `Set the value of ${name}` : `Make ${name} a secret`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {existing ? (
              <>
                The value goes to this machine's keychain under{" "}
                <span className="font-mono text-fg-secondary">
                  {env}/{name}
                </span>
                . <span className="font-mono text-fg-secondary">{env}.json</span> is not touched —
                it already holds the reference.
              </>
            ) : (
              <>
                Replaces the committed value in{" "}
                <span className="font-mono text-fg-secondary">env/{env}.json</span> with{" "}
                <span className="font-mono text-fg-secondary">{"{ \"$secret\": \"keychain\" }"}</span>{" "}
                and puts the value in this machine's keychain. The old committed value is removed
                from the file — but it is still in git history, so rotate it if it was real.
              </>
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <input
          {...verbatimText}
          autoFocus
          type="password"
          value={value}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && value) {
              event.preventDefault();
              void save();
            }
          }}
          placeholder="value"
          aria-label={`Value for ${name}`}
          autoComplete="off"
          className="h-[26px] rounded-md border border-border-control bg-inset px-2 font-mono text-ui text-fg outline-none placeholder:text-fg-dim"
        />
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!value}
            onClick={(event) => {
              event.preventDefault();
              void save();
            }}
          >
            {existing ? "Set value" : "Make it a secret"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
