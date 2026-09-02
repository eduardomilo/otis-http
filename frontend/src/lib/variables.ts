/**
 * `{{variable}}` references in request text.
 *
 * The grammar is docs/FORMAT.md §4.1: `{{name}}` with optional inner
 * whitespace, where name matches `[A-Za-z_$][A-Za-z0-9_.-]*`. Anything else
 * between double braces is literal text, not an error — so the pattern here
 * has to agree with the Go one in internal/resolve exactly, or the editor
 * would highlight a token Go treats as text.
 */

import type { VariableRef } from "@bindings/internal/services";

/** The same pattern as `varRe` in internal/resolve/variables.go. */
const VARIABLE = /\{\{\s*([A-Za-z_$][\w.-]*)\s*\}\}/g;

/** One run of text: either literal characters or a variable reference. */
export interface Token {
  /** The source text, including the braces for a variable. */
  text: string;
  /** The variable's name, or undefined for a literal run. */
  name?: string;
  /** Offset of `text` in the string it came from. */
  from: number;
}

/**
 * Splits text into literal and variable runs. Concatenating every `text` in
 * order reproduces the input.
 */
export function tokenize(text: string): Token[] {
  const tokens: Token[] = [];
  let at = 0;
  for (const match of text.matchAll(VARIABLE)) {
    const from = match.index;
    if (from > at) tokens.push({ text: text.slice(at, from), from: at });
    tokens.push({ text: match[0], name: match[1], from });
    at = from + match[0].length;
  }
  if (at < text.length) tokens.push({ text: text.slice(at), from: at });
  return tokens;
}

/** Every distinct variable name referenced in text, in first-use order. */
export function referencedNames(text: string): string[] {
  const names: string[] = [];
  for (const match of text.matchAll(VARIABLE)) {
    if (!names.includes(match[1])) names.push(match[1]);
  }
  return names;
}

/** How a `{{variable}}` token is drawn. */
export type VariableState =
  /** Resolves against the active environment and the file's own variables. */
  | "resolved"
  /** Resolves, and the value lives in the OS keychain. */
  | "secret"
  /** Nothing defines it — styled as a warning (DESIGN-NOTES §2.6). */
  | "unresolved";

/** A variable index keyed by name, as the editor consults it per token. */
export type VariableIndex = Map<string, VariableRef>;

/** Indexes the refs a Document carries. */
export function indexVariables(refs: readonly VariableRef[] | undefined): VariableIndex {
  const index: VariableIndex = new Map();
  for (const ref of refs ?? []) index.set(ref.name, ref);
  return index;
}

/**
 * The state of one token.
 *
 * An unknown name counts as unresolved: a request Otis has not resolved yet
 * has an empty index, and showing every token as resolved would be a claim
 * the editor cannot back.
 */
export function variableState(index: VariableIndex, name: string): VariableState {
  const ref = index.get(name);
  if (!ref?.resolved) return "unresolved";
  return ref.secret ? "secret" : "resolved";
}

/**
 * A token's tooltip: where the value came from, and its value when showing it
 * is safe. A secret names its storage and never its value.
 */
export function variableTitle(index: VariableIndex, name: string): string {
  const ref = index.get(name);
  if (!ref?.resolved) return `{{${name}}} — nothing defines this variable`;
  if (ref.secret) return `{{${name}}} — secret, stored in the OS keychain (${ref.source.path})`;
  const where = ref.origin === "builtin" ? "builtin" : ref.source.path;
  return `{{${name}}} = ${ref.value ?? ""} — ${where}`;
}

/** The Tailwind classes for a token, by state (DESIGN-NOTES §2.4, §2.6). */
export const VARIABLE_CLASS: Record<VariableState, string> = {
  resolved: "rounded-sm bg-primary-wash text-primary",
  secret: "rounded-sm bg-primary-wash text-secret",
  unresolved: "rounded-sm bg-warning/10 text-warning",
};
