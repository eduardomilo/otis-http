import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

/**
 * The type scale, named for tailwind-merge.
 *
 * tailwind-merge resolves conflicts by knowing which utilities belong to the
 * same group, and its table is Tailwind's default one: `text-xs`, `text-sm`,
 * `text-base`. This design ships its own nine sizes (DESIGN-NOTES §3), so
 * `text-micro` is a name it has never seen — and its fallback for an unknown
 * `text-*` is *text colour*. Which makes `cn("text-label", "text-method-get")`
 * two colours in conflict, and the last one wins:
 *
 *     twMerge("text-label", "text-method-get")  // -> "text-method-get"
 *
 * Every method label in the tree, the palette and the drag ghost was drawn
 * that way, so every one of them inherited 12px from the row instead of the
 * 10px §2.5 specifies — the size class was there in the source, in the class
 * string, and gone from the element. The same shape of bug was waiting for any
 * `cn(<size>, <colour>)` anywhere else in the app.
 *
 * Naming the scale here is the fix for all of them at once, and it has to stay
 * in step with the `--text-*` tokens in index.css: a size added there and not
 * here is a size that silently stops applying the moment it meets a colour.
 */
const FONT_SIZES = [
  "text-micro",
  "text-label",
  "text-meta",
  "text-ui",
  "text-code",
  "text-result",
  "text-field",
  "text-title",
  "text-heading",
  "text-display",
]

const twMerge = extendTailwindMerge({
  extend: { classGroups: { "font-size": FONT_SIZES } },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
