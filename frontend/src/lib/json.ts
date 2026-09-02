/**
 * Pretty-printing a JSON request body that contains `{{variables}}`.
 *
 * `JSON.parse` cannot see a body the way the user wrote it: `{"port": {{port}}}`
 * is not JSON, and neither is anything with a reference where a value belongs.
 * But those are the bodies people actually write — the design's own example has
 * `"customer_id": "{{customerId}}"` — so Format has to survive them.
 *
 * The trick is to stand a placeholder in for every reference before parsing and
 * put the reference back afterwards. Which placeholder depends on where the
 * reference sits: inside a string it becomes plain characters, and outside one
 * it becomes a quoted string, which is a legal JSON value. Restoring strips the
 * quotes again from the ones that were given them.
 *
 * A body that is still not JSON once the references are out of the way is left
 * alone: formatting is an offer, not a repair.
 */

/** The same pattern as lib/variables.ts and internal/resolve/variables.go. */
const VARIABLE = /\{\{\s*[A-Za-z_$][\w.-]*\s*\}\}/g;

/** How a reference was disguised, so restoring knows what to undo. */
interface Placeholder {
  /** The reference's source text, braces included. */
  reference: string;
  /** True when the placeholder was wrapped in quotes to stand as a value. */
  quoted: boolean;
}

/** The marker text, chosen to need no JSON escaping and never occur naturally. */
function marker(at: number): string {
  return `__otis_var_${at}__`;
}

/**
 * Formats JSON with two-space indentation, or returns null when the text is
 * not JSON (or is already formatted this way, which makes Format a no-op).
 */
export function formatJson(raw: string): string | null {
  if (raw.trim() === "") return null;

  const placeholders: Placeholder[] = [];
  const inString = stringMask(raw);
  const guarded = raw.replace(VARIABLE, (match, offset: number) => {
    const quoted = !inString[offset];
    placeholders.push({ reference: match, quoted });
    const at = placeholders.length - 1;
    return quoted ? `"${marker(at)}"` : marker(at);
  });

  let pretty: string;
  try {
    pretty = JSON.stringify(JSON.parse(guarded), null, 2);
  } catch {
    return null;
  }

  // Which quotes come off matters. A placeholder that stood in for a value was
  // given quotes here, so they go; one that stood inside a string is
  // surrounded by the string's *own* quotes, which have to stay — stripping
  // them turns `"createdAt": "{{$isoTimestamp}}"` into a syntax error.
  const restored = pretty.replace(
    /"__otis_var_(\d+)__"|__otis_var_(\d+)__/g,
    (match, quoted: string | undefined, bare: string | undefined) => {
      const placeholder = placeholders[Number(quoted ?? bare)];
      if (!placeholder) return match;
      if (quoted === undefined) return placeholder.reference;
      return placeholder.quoted ? placeholder.reference : `"${placeholder.reference}"`;
    },
  );
  return restored === raw ? null : restored;
}

/**
 * For each character of text, whether it is inside a double-quoted JSON string.
 *
 * A one-pass scan honouring backslash escapes. It does not have to be a JSON
 * parser: all it decides is whether a `{{` sits where a value goes or inside a
 * string, and a body malformed enough to fool it fails to parse anyway.
 */
function stringMask(text: string): boolean[] {
  const mask = new Array<boolean>(text.length).fill(false);
  let open = false;
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (open && char === "\\") {
      mask[i] = true;
      if (i + 1 < text.length) mask[++i] = true;
      continue;
    }
    if (char === '"') {
      // The quote itself is a delimiter, not content.
      open = !open;
      continue;
    }
    mask[i] = open;
  }
  return mask;
}
