/**
 * Turning a rejected binding call into a sentence a person can read.
 *
 * A Go service that returns an error rejects the generated binding's promise
 * with a `RuntimeError`, so `String(cause)` is `"RuntimeError: that folder
 * already exists"` — a class name from the bridge, in front of a message that
 * was written for the user. It means nothing to them and it is the first thing
 * they read.
 *
 * Older call sites still use `String(cause)` directly and show the prefix.
 * This is the one place to fix that, not each of them.
 */
export function errorText(cause: unknown): string {
  const text = cause instanceof Error ? cause.message : String(cause);
  // Only a leading `<Name>Error: ` comes off, and only once: a message may
  // legitimately contain "Error:" further in, quoted from git or from a
  // server, and cutting there would truncate the useful half.
  return text.replace(/^[A-Za-z]*Error:\s*/, "").trim() || String(cause);
}
