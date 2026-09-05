/**
 * HTTP method labels.
 *
 * DESIGN-NOTES §2.5 defines colours for five methods. §9.2 left the rest open,
 * and §9.37 closes it: `HEAD`, `OPTIONS`, `TRACE` and `CONNECT` share one
 * colour, `--color-method-other`. One and not four — the palette is already
 * carrying more meanings than it comfortably can (§9.3), and four more hues
 * nobody could tell apart at this size would cost the five that work. What the
 * shared colour says is "not one of the five you are scanning for", and the
 * word itself says which.
 *
 * A **custom** method still has none. The parser accepts any uppercase token,
 * so Otis cannot know what one means, and grey is the honest answer.
 */

const METHOD_COLOR: Record<string, string> = {
  GET: "text-method-get",
  POST: "text-method-post",
  PUT: "text-method-put",
  PATCH: "text-method-patch",
  DELETE: "text-method-delete",
  HEAD: "text-method-other",
  OPTIONS: "text-method-other",
  TRACE: "text-method-other",
  CONNECT: "text-method-other",
};

/** The text colour class for a method label. */
export function methodColor(method: string | undefined): string {
  if (!method) return "text-fg-muted";
  return METHOD_COLOR[method.toUpperCase()] ?? "text-fg-muted";
}

/**
 * The classes for the method gutter: a fixed 48px right-aligned column so
 * every name in a list starts at the same x (DESIGN-NOTES §4.2).
 *
 * The label is 9px, not the 10px §4.2 specifies — §9.37 records why. At 9px
 * IBM Plex Mono the seven characters of `OPTIONS` are ~38px and fit inside the
 * gutter with its 8px padding, which is the other half of §9.2: the column no
 * longer clips a standard method. `overflow-hidden` stays for a custom method
 * longer than that, and callers still put the full method in a title.
 */
export const methodGutter =
  "w-[var(--method-gutter-width)] shrink-0 overflow-hidden pr-2 text-right font-mono text-micro font-medium tracking-[.02em]";
