/**
 * The file name a typed name will become — a *preview* of Go's answer.
 *
 * `internal/collection.Slug` is the real one, and it is the one that decides
 * what lands on disk: it also resolves collisions against what is actually in
 * the directory, which the window cannot see. So this exists only so the
 * create dialog can show the path while you type, and the rules are kept
 * deliberately identical to Go's (docs/FORMAT.md §7): lower-cased, runs of
 * anything other than an ASCII letter or digit become one hyphen, and trailing
 * hyphens come off.
 *
 * Where the two can differ is the collision suffix. Go may answer
 * `create-order-2.http` where this previewed `create-order.http`, which is why
 * the caller navigates to the path Go returns and never to the preview.
 */
export function slugFor(name: string): string {
  let out = "";
  let dash = false;
  for (const character of name.toLowerCase()) {
    const code = character.codePointAt(0) ?? 0;
    const isAsciiLetterOrDigit =
      code < 128 && /[a-z0-9]/.test(character);
    if (isAsciiLetterOrDigit) {
      out += character;
      dash = false;
    } else if (!dash && out.length > 0) {
      out += "-";
      dash = true;
    }
  }
  return out.replace(/-+$/, "");
}
