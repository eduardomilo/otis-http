/**
 * Colouring one line of formatted JSON.
 *
 * The response body is a virtualized list of lines fetched from Go, not a
 * CodeMirror document — a 40 MB body cannot be handed to an editor without
 * undoing the whole point of paging it. So the colouring is per line, done as
 * each line is drawn: sixty short strings per frame, which is nothing.
 *
 * A line can be tokenized on its own because Go has already formatted the
 * body: one value per line, and JSON strings cannot contain a raw newline, so
 * no token ever spans a line boundary. That is only true of the *formatted*
 * view; the raw view is drawn as plain text.
 *
 * Colours are DESIGN-NOTES §2.7.
 */

/** The roles §2.7 names, plus the whitespace that carries none. */
export type TokenRole =
  | "key"
  | "string"
  | "number"
  | "atom"
  | "punctuation"
  | "plain";

export interface JsonToken {
  text: string;
  role: TokenRole;
}

/** §2.7's five colours, plus a plain fallback. */
export const TOKEN_CLASS: Record<TokenRole, string> = {
  key: "text-[#93c5fd]",
  string: "text-fg-secondary",
  number: "text-modified",
  atom: "text-[#c084fc]",
  punctuation: "text-fg-dim",
  plain: "text-fg-secondary",
};

const STRING = /^"(?:[^"\\]|\\.)*"?/;
const NUMBER = /^-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/;
const ATOM = /^(?:true|false|null)/;
const PUNCTUATION = /^[{}[\],:]/;
const SPACE = /^\s+/;

/**
 * Splits one formatted JSON line into coloured runs. Concatenating every
 * `text` reproduces the line, so nothing can be lost to a tokenizer bug — a
 * character the scanner does not recognise is emitted as plain text rather
 * than dropped.
 */
export function tokenizeJsonLine(line: string): JsonToken[] {
  const tokens: JsonToken[] = [];
  let rest = line;
  const push = (text: string, role: TokenRole) => {
    // Runs of the same role are merged, so a line of punctuation is one span
    // rather than five.
    const last = tokens[tokens.length - 1];
    if (last && last.role === role) last.text += text;
    else tokens.push({ text, role });
  };

  while (rest.length > 0) {
    let match = SPACE.exec(rest);
    if (match) {
      push(match[0], "plain");
      rest = rest.slice(match[0].length);
      continue;
    }
    match = STRING.exec(rest);
    if (match) {
      // A string followed by a colon is a key; the design gives those their
      // own colour, and it is the one thing that makes formatted JSON
      // scannable.
      const after = rest.slice(match[0].length);
      push(match[0], /^\s*:/.test(after) ? "key" : "string");
      rest = after;
      continue;
    }
    match = NUMBER.exec(rest);
    if (match) {
      push(match[0], "number");
      rest = rest.slice(match[0].length);
      continue;
    }
    match = ATOM.exec(rest);
    if (match) {
      push(match[0], "atom");
      rest = rest.slice(match[0].length);
      continue;
    }
    match = PUNCTUATION.exec(rest);
    if (match) {
      push(match[0], "punctuation");
      rest = rest.slice(match[0].length);
      continue;
    }
    push(rest[0], "plain");
    rest = rest.slice(1);
  }
  return tokens;
}

/**
 * Splits one line of XML or HTML. Coarser than the JSON tokenizer on purpose:
 * a tag, its attributes and its text content are what a reader is scanning
 * for, and the design defines colours for exactly those.
 */
export function tokenizeXmlLine(line: string): JsonToken[] {
  const tokens: JsonToken[] = [];
  const tag = /^(\s*)(<\/?)([\w:.-]*)(.*?)(\/?>)(.*)$/.exec(line);
  if (!tag) return [{ text: line, role: "plain" }];
  const [, space, open, name, attributes, close, trailing] = tag;
  if (space) tokens.push({ text: space, role: "plain" });
  tokens.push({ text: open, role: "punctuation" });
  tokens.push({ text: name, role: "key" });
  if (attributes) {
    // name="value" pairs, with the name muted and the value read as a string.
    for (const part of attributes.split(/(=(?:"[^"]*"|'[^']*'))/)) {
      if (part.startsWith("=")) tokens.push({ text: part, role: "string" });
      else if (part) tokens.push({ text: part, role: "punctuation" });
    }
  }
  tokens.push({ text: close, role: "punctuation" });
  if (trailing) tokens.push({ text: trailing, role: "plain" });
  return tokens;
}
