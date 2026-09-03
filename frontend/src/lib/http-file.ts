/**
 * Editing a parsed `.http` file.
 *
 * The editor works on the model `internal/httpfile` produced and hands it back
 * to Go to serialize; Go's serializer is the only writer, so the canonical
 * layout of docs/FORMAT.md §1.13 has exactly one implementation. Everything
 * here is therefore a pure, immutable transform of that model — nothing in
 * this file renders a file back to text.
 *
 * Line numbers are carried through untouched. The serializer uses them to keep
 * preamble items in their source order, and an item added here has none, which
 * is what puts it after the existing ones (§1.13, step 2).
 */

import type { Directive, File, Header, Request, Script, Variable } from "@bindings/internal/httpfile";

/** The value that disables an inherited header (docs/FORMAT.md §3.2). */
export const INHERIT_MARKER = "!inherit";

/** The directive carrying authentication (§3.3). */
export const AUTH_DIRECTIVE = "auth";

/** The comment style Otis writes: "#", not "//". */
export const COMMENT_STYLE = "#";

/** The entry the editor shows, or undefined for a file with no entries. */
export function entryOf(file: File | null | undefined, index: number): Request | undefined {
  return file?.requests?.[index] ?? undefined;
}

/** Replaces one entry of a file, leaving the others alone. */
export function withEntry(file: File, index: number, entry: Request): File {
  const requests = [...(file.requests ?? [])];
  requests[index] = entry;
  return { ...file, requests };
}

/** Applies fn to one entry of a file. */
export function updateEntry(file: File, index: number, fn: (entry: Request) => Request): File {
  const entry = entryOf(file, index);
  if (!entry) return file;
  return withEntry(file, index, fn(entry));
}

/** Case-insensitive header-name comparison, as §1.7 specifies for lookups. */
export function sameHeaderName(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

/** The entry's own headers, never undefined. */
export function headersOf(entry: Request): Header[] {
  return entry.headers ?? [];
}

/** Appends a header. Duplicate names are legal and are all kept (§1.7). */
export function addHeader(entry: Request, name: string, value: string): Request {
  return { ...entry, headers: [...headersOf(entry), { name, value }] };
}

/** Replaces the header at index. */
export function setHeaderAt(entry: Request, index: number, header: Partial<Header>): Request {
  const headers = headersOf(entry).map((h, i) => (i === index ? { ...h, ...header } : h));
  return { ...entry, headers };
}

/** Removes the header at index. */
export function removeHeaderAt(entry: Request, index: number): Request {
  return { ...entry, headers: headersOf(entry).filter((_, i) => i !== index) };
}

/** Removes every header with the given name, case-insensitively. */
export function removeHeadersNamed(entry: Request, name: string): Request {
  return { ...entry, headers: headersOf(entry).filter((h) => !sameHeaderName(h.name, name)) };
}

/** True when the entry itself declares the named header. */
export function hasHeaderNamed(entry: Request, name: string): boolean {
  return headersOf(entry).some((h) => sameHeaderName(h.name, name));
}

/**
 * Copies an inherited header into the file with its current value, so the
 * folder entry stops applying (screen 4a's Override). Any local header of the
 * same name is replaced: two values of one header from one level would both be
 * sent, which is not what an override means.
 */
export function overrideHeader(entry: Request, name: string, value: string): Request {
  return addHeader(removeHeadersNamed(entry, name), name, value);
}

/**
 * Writes `Header: !inherit` for a name, so nothing of it is sent and the
 * change is visible in the diff (§3.2, screen 4a's Off).
 */
export function disableInheritedHeader(entry: Request, name: string): Request {
  return addHeader(removeHeadersNamed(entry, name), name, INHERIT_MARKER);
}

/** True when the entry carries an `!inherit` marker for the name. */
export function isInheritDisabled(entry: Request, name: string): boolean {
  return headersOf(entry).some(
    (h) => sameHeaderName(h.name, name) && h.value.trim() === INHERIT_MARKER,
  );
}

/** The entry's directives, never undefined. */
export function directivesOf(entry: Request): Directive[] {
  return entry.directives ?? [];
}

/** The value of the last directive with the given name (§1.4: last wins). */
export function directiveValue(entry: Request, name: string): string | undefined {
  const directives = directivesOf(entry);
  for (let i = directives.length - 1; i >= 0; i--) {
    if (directives[i].name === name) return directives[i].value ?? "";
  }
  return undefined;
}

/**
 * Sets a directive to a value, replacing every earlier one of that name.
 *
 * Replacing rather than appending keeps the file honest: §1.4 says the last
 * one wins, so leaving the old line behind would put a value in the file that
 * has no effect and would show up in the diff as a change nobody made.
 */
export function setDirective(entry: Request, name: string, value: string): Request {
  const kept = directivesOf(entry).filter((d) => d.name !== name);
  const previous = directivesOf(entry).find((d) => d.name === name);
  return {
    ...entry,
    // Keeping the replaced directive's line number holds its position in the
    // preamble, so switching an auth mode does not move the line.
    directives: [
      ...kept,
      { style: previous?.style ?? COMMENT_STYLE, name, value, line: previous?.line },
    ],
  };
}

/** Removes every directive with the given name. */
export function removeDirective(entry: Request, name: string): Request {
  return { ...entry, directives: directivesOf(entry).filter((d) => d.name !== name) };
}

/** The entry's `@name = value` declarations, never undefined. */
export function variablesOf(entry: Request): Variable[] {
  return entry.variables ?? [];
}

/** Replaces the entry's body with raw text, dropping any `< ./file` form. */
export function setBody(entry: Request, raw: string): Request {
  return { ...entry, body: { ...entry.body, raw, filePath: undefined } };
}

/**
 * Structural comparison of two parsed files.
 *
 * This is what "dirty" means. Comparing structures rather than serialized text
 * keeps the frontend out of the serializing business, and comparing the draft
 * against the file *as parsed* rather than against its bytes means a file
 * another tool wrote in a non-canonical layout does not open dirty: nothing
 * has been edited yet, and §1.13 already says the first save may reformat it.
 */
export function sameFile(a: File | null | undefined, b: File | null | undefined): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return stableStringify(a) === stableStringify(b);
}

/**
 * JSON with object keys in sorted order, so two structurally equal values
 * always produce the same string whatever order their keys were built in.
 * `undefined` members are dropped, which makes an absent field and one set to
 * undefined compare equal — exactly how Go's `omitempty` treats them.
 */
function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "null";
  if (Array.isArray(value)) return "[" + value.map(stableStringify).join(",") + "]";
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return "{" + entries.map(([k, v]) => JSON.stringify(k) + ":" + stableStringify(v)).join(",") + "}";
}

// --- Scripts (docs/FORMAT.md §1.10) ----------------------------------------

/** The kind of script block: which phase it belongs to. */
export type ScriptPhase = "pre" | "post";

/** The blocks of a phase, in file order. */
export function scriptsOf(entry: Request, phase: ScriptPhase): Script[] {
  return [...((phase === "pre" ? entry.preScripts : entry.postScripts) ?? [])];
}

/**
 * Replaces one block's text.
 *
 * The line number is carried through untouched, as everything here is: the
 * serializer writes the block where it was, and rewriting the line would move
 * it (§1.13).
 */
export function setScriptAt(entry: Request, phase: ScriptPhase, index: number, text: string): Request {
  const scripts = scriptsOf(entry, phase);
  if (!scripts[index]) return entry;
  scripts[index] = { ...scripts[index], text };
  return withScripts(entry, phase, scripts);
}

/** Appends a block. It has no line, which is what puts it after the rest. */
export function addScript(entry: Request, phase: ScriptPhase, text: string): Request {
  return withScripts(entry, phase, [...scriptsOf(entry, phase), { text }]);
}

/** Removes one block. */
export function removeScriptAt(entry: Request, phase: ScriptPhase, index: number): Request {
  const scripts = scriptsOf(entry, phase);
  if (!scripts[index]) return entry;
  scripts.splice(index, 1);
  return withScripts(entry, phase, scripts);
}

function withScripts(entry: Request, phase: ScriptPhase, scripts: Script[]): Request {
  return phase === "pre" ? { ...entry, preScripts: scripts } : { ...entry, postScripts: scripts };
}
