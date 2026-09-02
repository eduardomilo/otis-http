import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { hint } from "@/lib/platform";

/**
 * The command palette (screen 2c).
 *
 * Increment 8 gives it its container and nothing else: ⌘K opens it, Escape
 * closes it, and it has no results. Modes (`@` environments, `>` commands,
 * `:` recents), fuzzy matching with character-level highlighting and the
 * footer are Phase D — none of them exist in a primitive
 * (DESIGN-NOTES §7.4), and the shell is not the place to invent them.
 *
 * Rendered here rather than through shadcn's CommandDialog so the palette
 * takes the design's own geometry: 640px wide at top: 120px, a 44px input,
 * and the design's one permitted drop shadow (DESIGN-NOTES §4.7, §5).
 */
export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        showCloseButton={false}
        // sm:max-w-[640px] is not redundant: the shadcn default carries
        // sm:max-w-sm, and a responsive variant survives merging with an
        // unprefixed max-w, which would cap the palette at 384px.
        className="top-[120px] w-[640px] max-w-[640px] translate-y-0 gap-0 overflow-hidden rounded-lg! border-border-strong bg-raised p-0 shadow-[0_16px_48px_rgba(0,0,0,.6)] sm:max-w-[640px]"
      >
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <DialogDescription className="sr-only">
          Search requests, environments and commands.
        </DialogDescription>

        <Command className="bg-raised">
          <div className="flex h-11 items-center gap-2 border-b border-border px-4">
            <span className="font-mono text-field text-fg-faint">›</span>
            <CommandInput
              placeholder="Search"
              className="h-full flex-1 border-0 bg-transparent p-0 font-mono text-field text-fg-emphasis placeholder:text-fg-dim"
            />
            <span className="font-mono text-label text-fg-faint">esc</span>
          </div>

          <CommandList className="max-h-[420px] px-0 py-2">
            <CommandEmpty className="px-4 py-6 text-center text-ui text-fg-dim">
              Nothing to search yet — the palette fills in later.
            </CommandEmpty>
          </CommandList>

          <div className="flex h-[30px] items-center justify-between border-t border-border px-4 font-mono text-label text-fg-faint">
            <span>↑↓ move · ↵ open</span>
            <span>{hint("K")}</span>
          </div>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
