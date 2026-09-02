import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useEnvironments } from "@/state/environment-context";

/**
 * The environment selector in the title strip (SCREENS.md, chrome).
 *
 * The chip itself is drawn to the design: 24px tall, `padding: 0 8px 0 10px`,
 * `--bg-control`, a status dot, the name in mono, a chevron (DESIGN-NOTES
 * §4.4).
 *
 * The menu is not. DESIGN-NOTES §9.11 records that screen 1c draws the chip in
 * its *open* state — accent border, up-chevron — with no menu rendered
 * anywhere, so the popover has no design at all. §6 maps the selector to a
 * shadcn `Select`; this is a `DropdownMenu` instead, because the surface has
 * to carry an action ("Edit environments…") as well as a choice, and because
 * "no environment" is a real option that Radix `Select` cannot represent (its
 * item values may not be empty). The radio items keep the one-of-N semantics
 * `Select` would have given.
 *
 * The dot follows §2.6, same as the sidebar list: accent for the active one,
 * red for an environment that confirms before send, `--fg-faint` otherwise.
 */
export function EnvironmentChip({ enabled }: { enabled: boolean }) {
  const navigate = useNavigate();
  const { environments, active, activeEnvironment, activate } = useEnvironments();

  return (
    <div
      className="flex shrink-0 items-center gap-2 pr-3 pl-2"
      style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
    >
      <span className="text-meta text-fg-faint">env</span>

      {!enabled ? (
        <Button
          type="button"
          disabled
          title="Open a collection to choose an environment"
          className="h-6 rounded-md border border-border-control bg-control px-2 font-mono text-ui text-fg-dim hover:bg-control disabled:opacity-100"
        >
          —
          <ChevronDown className="size-3 text-fg-faint" />
        </Button>
      ) : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              title={
                activeEnvironment?.confirmBeforeSend
                  ? `${active} confirms before every send`
                  : "Choose the environment requests resolve against"
              }
              className="h-6 rounded-md border border-border-control bg-control pr-2 pl-2.5 font-mono text-ui text-fg-secondary hover:bg-selected"
            >
              <span
                className={cn(
                  "size-1.5 rounded-full",
                  !active
                    ? "bg-fg-faint"
                    : activeEnvironment?.confirmBeforeSend
                      ? "bg-destructive"
                      : "bg-primary",
                )}
              />
              {active || "none"}
              <ChevronDown className="size-3 text-fg-faint" />
            </Button>
          </DropdownMenuTrigger>

          <DropdownMenuContent align="end" className="min-w-[220px]">
            <DropdownMenuRadioGroup
              value={active}
              onValueChange={(name) => void activate(name)}
            >
              {/* "" is a real state, not the absence of one: with no
                  environment active a request resolves against its file and
                  folder scopes only (docs/FORMAT.md §4.2). */}
              <DropdownMenuRadioItem value="">
                <span className="font-mono">none</span>
                <span className="ml-2 text-meta text-fg-faint">file and folder scopes only</span>
              </DropdownMenuRadioItem>
              {environments.map((env) => (
                <DropdownMenuRadioItem key={env.name} value={env.name} disabled={!!env.error}>
                  <span className="font-mono">{env.name}</span>
                  <span className="ml-2 text-meta text-fg-faint">
                    {env.error
                      ? "does not parse"
                      : env.confirmBeforeSend
                        ? "confirms before send"
                        : `${env.variables} ${env.variables === 1 ? "variable" : "variables"}`}
                  </span>
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() =>
                void navigate(
                  environments.length > 0
                    ? { to: "/env/$name", params: { name: environments[0].name } }
                    : { to: "/" },
                )
              }
            >
              Edit environments…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
