/**
 * HTTP method labels.
 *
 * DESIGN-NOTES §2.5 defines colours for five methods only. HEAD, OPTIONS,
 * TRACE, CONNECT and the custom methods the parser accepts have none (§9.2,
 * unresolved), so they fall back to text-fg-muted rather than being assigned
 * a colour here — that decision belongs in the design, not in this file.
 */

const METHOD_COLOR: Record<string, string> = {
  GET: "text-method-get",
  POST: "text-method-post",
  PUT: "text-method-put",
  PATCH: "text-method-patch",
  DELETE: "text-method-delete",
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
 * The width fits six characters at 10px mono. OPTIONS is seven and needs 52px,
 * which the design knows about and has not decided (§9.2). Until it does, the
 * column is clipped rather than allowed to grow: an overflowing label collides
 * with the name beside it, and keeping every name on the same x is the whole
 * reason the gutter exists. Callers put the full method in a title so the
 * clipped characters are still available.
 */
export const methodGutter =
  "w-[var(--method-gutter-width)] shrink-0 overflow-hidden pr-2 text-right font-mono text-label font-medium tracking-[.02em]";
