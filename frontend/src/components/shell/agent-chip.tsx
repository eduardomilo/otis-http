import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useMCP } from "@/state/mcp-context";

/**
 * The agent indicator in the title strip (DESIGN-NOTES §9.22, docs/MCP.md §11).
 *
 * The design draws no such element, so §9.22 decides it: the title strip, in
 * amber. Amber because the accent means "good" in this design and an agent
 * holding your credentials is not an achievement, and because red means
 * destruction and an enabled server is not a fault. §9.3 flags amber as
 * already carrying four meanings; the fifth is defensible here because none of
 * the other four is a colour in the title strip, so it never has to be told
 * apart from them in one glance.
 *
 * Off, the chip is **nothing at all**: a feature that is off should not occupy
 * the chrome. That is also why there is no "turn agents on" affordance here —
 * enabling is a deliberate act, and the place for it is the popover you reach
 * once something is already on. Until then it is in the command palette.
 *
 * The count is exact, like every count in Otis (§8.5). A chip reading
 * "2 waiting" over a popover listing three is worse than no chip, because the
 * number is the only reason to look.
 */
export function AgentChip() {
  const {
    status,
    setEnabled,
    setCapability,
    setAlwaysConfirmSends,
    setPersistAuditLog,
    disconnect,
    copyClientBlock,
    error,
  } = useMCP();
  const [copied, setCopied] = useState(false);

  // Nothing at all when the server is off.
  if (!status?.enabled) return null;

  const waiting = status.waiting ?? 0;
  const label = waiting > 0
    ? `${waiting} waiting`
    : status.client || "idle";

  return (
    <div
      className="flex shrink-0 items-center pr-1"
      style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
    >
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            title="An MCP client can drive this collection. Click for controls."
            className={cn(
              "flex h-6 items-center gap-1.5 rounded-md border border-border-control bg-control px-2 text-ui hover:bg-selected",
              waiting > 0 ? "text-modified" : "text-fg-secondary",
            )}
          >
            <span
              aria-hidden
              className={cn(
                "size-1.5 rounded-full",
                // A live dot only when something has actually connected:
                // "enabled" and "an agent is here" are different facts and
                // the chip must not conflate them.
                status.client ? "bg-modified" : "bg-fg-faint",
              )}
            />
            <span className="text-fg-faint">agent</span>
            <span className="text-fg-faint">·</span>
            <span className="max-w-[140px] truncate font-mono">{label}</span>
          </Button>
        </DropdownMenuTrigger>

        <DropdownMenuContent
          align="end"
          className="w-[320px] rounded-md border border-border-strong bg-raised p-3"
        >
          <p className="text-meta text-fg-dim">
            {status.running
              ? `Listening on 127.0.0.1:${status.port}.`
              : "The listener is not running."}
          </p>
          {status.client ? (
            <p className="mt-0.5 truncate text-meta text-fg-faint">
              Connected: <span className="font-mono text-fg-secondary">{status.client}</span>
            </p>
          ) : (
            <p className="mt-0.5 text-meta text-fg-faint">Nothing has connected yet.</p>
          )}

          <DropdownMenuSeparator className="my-2 bg-border" />

          {/* The three capabilities. All off by default; each is its own
              deliberate act. */}
          <p className="text-meta text-fg-faint">What agents may do</p>
          <div className="mt-1.5 flex flex-col gap-1.5">
            <Switch
              label="Read the collection"
              hint="Requests, environments' shapes, responses. Never a secret value."
              checked={status.read}
              onChange={(on) => void setCapability("read", on)}
            />
            <Switch
              label="Send requests"
              hint="Two calls per send, and a person is asked before anything leaves."
              checked={status.run}
              onChange={(on) => void setCapability("run", on)}
            />
            <Switch
              label="Create and edit requests"
              hint={
                status.writeBlocked ||
                "New files are uncommitted, so sending one needs your confirmation."
              }
              checked={status.write}
              disabled={Boolean(status.writeBlocked)}
              onChange={(on) => void setCapability("write", on)}
            />
          </div>

          <DropdownMenuSeparator className="my-2 bg-border" />

          <div className="flex flex-col gap-1.5">
            <Switch
              label="Confirm every send"
              hint="Even where an environment says agents may send unasked."
              checked={status.alwaysConfirmSends}
              onChange={(on) => void setAlwaysConfirmSends(on)}
            />
            <Switch
              label="Keep an audit log"
              hint="A durable record of which endpoints you asked an agent to call."
              checked={status.persistAuditLog}
              onChange={(on) => void setPersistAuditLog(on)}
            />
          </div>

          <DropdownMenuSeparator className="my-2 bg-border" />

          <div className="flex items-center gap-2">
            <Button
              type="button"
              onClick={async () => {
                // Go writes the clipboard and reports whether it took. The
                // window used to do it with navigator.clipboard, which
                // rejects from a custom scheme — so the button did nothing
                // at all, silently. A refusal now lands in the popover's
                // error line like every other one.
                if (!(await copyClientBlock())) return;
                setCopied(true);
                window.setTimeout(() => setCopied(false), 1500);
              }}
              className="h-6 rounded-md border border-border-control bg-control px-2 text-ui text-fg-secondary hover:bg-selected"
            >
              {copied ? "Copied" : "Copy client config"}
            </Button>
            <span className="text-meta text-fg-faint">
              The port and token change every launch.
            </span>
          </div>

          {status.auditError ? (
            <p className="mt-2 text-meta text-modified">
              The audit log could not be written: {status.auditError}
            </p>
          ) : null}
          {error ? <p className="mt-2 text-meta text-destructive">{error}</p> : null}

          <DropdownMenuSeparator className="my-2 bg-border" />

          {/* The kill switch (§10). Destructive treatment because there is no
              undo: the token is revoked, not rotated, and there is no way
              back to the old one. */}
          <Button
            type="button"
            onClick={() => void disconnect()}
            className="h-6 w-full rounded-md border border-border-danger bg-transparent px-2 text-ui text-destructive hover:bg-destructive/10"
          >
            Disconnect agents
          </Button>
          <p className="mt-1 text-meta text-fg-faint">
            Revokes the token, cancels anything in flight, and turns all three off.
            Reconnecting is not enough.
          </p>

          {status.recent?.length ? (
            <>
              <DropdownMenuSeparator className="my-2 bg-border" />
              <p className="text-meta text-fg-faint">Recent calls</p>
              <ul className="mt-1 max-h-[180px] overflow-y-auto">
                {status.recent.slice(0, 20).map((entry, index) => (
                  <li
                    key={`${entry.at}-${index}`}
                    className="flex items-baseline gap-2 py-0.5 text-meta"
                  >
                    <span className={cn("shrink-0 font-mono", decisionColor(entry.decision))}>
                      {entry.decision}
                    </span>
                    <span className="shrink-0 font-mono text-fg-dim">{entry.tool}</span>
                    <span className="min-w-0 truncate font-mono text-fg-faint">
                      {entry.target}
                    </span>
                  </li>
                ))}
              </ul>
            </>
          ) : null}

          <DropdownMenuSeparator className="my-2 bg-border" />
          <Button
            type="button"
            variant="ghost"
            onClick={() => void setEnabled(false)}
            className="h-6 w-full px-2 text-ui text-fg-faint hover:text-fg-emphasis"
          >
            Turn the agent server off
          </Button>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/** One labelled switch with its reason underneath. */
function Switch({
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    <label
      className={cn(
        "flex items-start gap-2",
        disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
      )}
      title={disabled ? hint : undefined}
    >
      <Checkbox
        checked={checked}
        disabled={disabled}
        onCheckedChange={(next) => onChange(next === true)}
        className="mt-[2px]"
      />
      <span className="min-w-0">
        <span className="block text-ui text-fg-secondary">{label}</span>
        <span className="block text-meta text-fg-faint">{hint}</span>
      </span>
    </label>
  );
}

/**
 * The audit list's colours. A refusal is not a failure — it is the system
 * working — so it is not red; only a policy denial and a rate limit are worth
 * a warning colour, and `allowed` is deliberately quiet.
 */
function decisionColor(decision: string): string {
  switch (decision) {
    case "confirmed":
      return "text-primary";
    case "refused":
    case "timed-out":
      return "text-fg-dim";
    case "denied-by-policy":
    case "rate-limited":
      return "text-modified";
    case "asked":
      return "text-secret";
    default:
      return "text-fg-faint";
  }
}
