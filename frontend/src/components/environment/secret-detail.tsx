import { useState } from "react";
import { Check, Lock } from "lucide-react";

import { SecretValueDialog } from "@/components/environment/secret-value-dialog";
import { Button } from "@/components/ui/button";
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
import { EnvironmentService } from "@bindings/internal/services";
import type { EnvironmentDocument, EnvironmentRow } from "@bindings/internal/services";

/**
 * The secret detail panel of screen 1c: what gets committed on the left, where
 * the value lives on the right, split by a vertical rule.
 *
 * It is the clearest statement in the design of the promise the product makes
 * about secrets, so the four claims on the right are rendered as claims — and
 * every one of them is true of the implementation, not aspirational. The
 * keychain service string is `secrets.Key(collection, env, name)`, which is
 * exactly what the design shows.
 */
export function SecretDetail({
  row,
  doc,
  apply,
  onForgotten,
}: {
  row: EnvironmentRow;
  doc: EnvironmentDocument;
  apply: (call: Promise<EnvironmentDocument | null>) => Promise<boolean>;
  onForgotten: () => void;
}) {
  const [replacing, setReplacing] = useState(false);
  const [forgetting, setForgetting] = useState(false);

  return (
    <div className="m-4 rounded-md border border-border-control">
      <div className="flex items-center gap-2 border-b border-border px-3 py-2.5">
        <Lock className="size-3.5 text-secret" />
        <span className="font-mono text-ui text-fg-emphasis">{row.name}</span>
        <span className="rounded-sm border border-border-secret bg-[#1a1506] px-1.5 py-px text-micro text-secret">
          Secret · OS keychain
        </span>
        <div className="flex-1" />
        <span className="text-meta text-fg-faint">
          {row.present ? "Set on this machine" : "Not set on this machine"}
        </span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2">
        <div className="border-border p-3 lg:border-r">
          <p className="mb-2 text-meta text-fg-muted">What gets committed</p>
          <pre className="overflow-x-auto rounded-sm border border-border-control bg-inset p-3 font-mono text-ui leading-5 text-fg-dim">
            {`{\n  "${row.name}": { "$secret": "keychain" }\n}`}
          </pre>
          <p className="mt-2 text-meta text-fg-dim">
            Only the reference. Teammates set their own value once; it resolves from their keychain.
          </p>
        </div>

        <div className="p-3">
          <p className="mb-2 text-meta text-fg-muted">Where the value lives</p>
          <ul className="flex flex-col gap-1.5">
            <Claim>
              The OS keychain, service{" "}
              <span className="font-mono text-fg-secondary">{row.key}</span>
            </Claim>
            <Claim>
              Never written to <span className="font-mono text-fg-secondary">{doc.path}</span>
            </Claim>
            <Claim>Never in git history, exports, or the settings file</Claim>
            <Claim>
              Never leaves this machine except in the request you send — and never crosses into
              this window at all
            </Claim>
          </ul>

          <div className="mt-3 flex items-center gap-2">
            <Button
              type="button"
              disabled={!doc.keychain.available}
              onClick={() => setReplacing(true)}
              className="h-[26px] rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
            >
              {row.present ? "Replace value" : "Set value"}
            </Button>
            {row.present ? (
              <Button
                type="button"
                onClick={() => setForgetting(true)}
                className="h-[26px] rounded-md border border-border-danger bg-transparent px-2.5 text-ui text-destructive hover:bg-[rgba(248,113,113,.08)]"
              >
                Remove from keychain
              </Button>
            ) : null}
          </div>

          {!doc.keychain.available ? (
            <p className="mt-2 text-meta text-modified">{doc.keychain.reason}</p>
          ) : null}
        </div>
      </div>

      <SecretValueDialog
        open={replacing}
        onOpenChange={setReplacing}
        env={doc.name}
        name={row.name}
        existing
        apply={apply}
      />

      <AlertDialog open={forgetting} onOpenChange={setForgetting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {row.name} from the keychain?</AlertDialogTitle>
            <AlertDialogDescription>
              The reference stays in{" "}
              <span className="font-mono text-fg-secondary">{doc.path}</span> — it is the team's,
              and this only gives up your machine's value. Every request that resolves{" "}
              <span className="font-mono text-fg-secondary">{`{{${row.name}}}`}</span> will fail
              until you set it again. Otis cannot show you the value first, so copy it now if you
              do not have it elsewhere.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={async (event) => {
                event.preventDefault();
                if (await apply(EnvironmentService.ForgetSecret(doc.name, row.name))) {
                  setForgetting(false);
                  onForgotten();
                }
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/** One accent checkmark and its claim (screen 1c's right column). */
function Claim({ children }: { children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-2 text-meta text-fg-secondary">
      <Check className="mt-0.5 size-3 shrink-0 text-primary" />
      <span>{children}</span>
    </li>
  );
}
