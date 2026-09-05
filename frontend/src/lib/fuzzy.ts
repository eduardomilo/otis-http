/**
 * Fuzzy matching for the command palette (screen 2c).
 *
 * The design's own example is the specification: the query `ord cre` matches
 * the request `create-order` at `/v2/orders`, highlighting "cre" at the start
 * of the name and "ord" inside both the name and the URL. So:
 *
 *   - **Terms are independent.** The query is split on whitespace and every
 *     term must match something. Order does not matter, which is why "ord cre"
 *     finds "create-order" — nobody types the parts of a name in the order the
 *     name happens to use.
 *   - **A term may match any field.** "cre" is only in the name and "ord" is in
 *     both; the row matches because each term found a home.
 *   - **Matched characters come back as positions**, not as a rewritten string,
 *     because the highlighting is character-level (DESIGN-NOTES §7.4: matched
 *     characters take the accent at weight 500, with no background) and a
 *     component should style them rather than parse markup out of a string.
 *
 * Contiguity is preferred over cleverness. A contiguous run is what people
 * mean almost every time, and a subsequence match is the fallback — that order
 * is what stops "cre" from scoring `c`…`r`…`e` scattered across a long URL
 * above `create-order`.
 */

/** How much a field's score counts. A name matters more than a URL. */
export interface Field {
  text: string;
  weight: number;
}

/** What a query matched, and where. */
export interface Match {
  /** Higher is better. Meaningless in absolute terms; only the order matters. */
  score: number;
  /** Matched character indexes per field, in the order the fields were given. */
  positions: number[][];
}

/** Characters after which a match counts as starting a word. */
const SEPARATORS = new Set(["-", "_", "/", ".", " ", "?", "&", "=", ":", "{", "}"]);

const SCORE_CONTIGUOUS = 1000;
const SCORE_WORD_START = 400;
const SCORE_PREFIX = 300;
const SCORE_PER_CHARACTER = 10;
/** Subtracted per character between the field's start and the match. */
const PENALTY_DISTANCE = 1;

/**
 * Scores a query against a row's fields, or returns null when a term matched
 * nothing.
 *
 * An empty query matches everything with score 0 and no highlights, which is
 * what the palette wants when it has not been typed into: every row, in its
 * own order.
 */
export function match(query: string, fields: readonly Field[]): Match | null {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const positions: number[][] = fields.map(() => []);
  if (terms.length === 0) {
    return { score: 0, positions };
  }

  const lowered = fields.map((field) => field.text.toLowerCase());
  let total = 0;

  for (const term of terms) {
    let best: { field: number; score: number; at: number[] } | null = null;
    for (let i = 0; i < fields.length; i++) {
      const found = matchTerm(term, lowered[i]);
      if (!found) continue;
      const score = found.score * fields[i].weight;
      if (!best || score > best.score) best = { field: i, score, at: found.positions };
    }
    // Every term has to find a home. A row that matches "ord" but not "cre"
    // is not what somebody typing "ord cre" is looking for.
    if (!best) return null;
    total += best.score;
    positions[best.field].push(...best.at);
  }

  for (const list of positions) {
    list.sort((a, b) => a - b);
  }
  return { score: total, positions: positions.map(dedupe) };
}

/**
 * Matches one term in one field: a contiguous run if there is one, otherwise a
 * subsequence.
 */
function matchTerm(term: string, text: string): { score: number; positions: number[] } | null {
  const at = text.indexOf(term);
  if (at >= 0) {
    const positions: number[] = [];
    for (let i = 0; i < term.length; i++) positions.push(at + i);
    let score = SCORE_CONTIGUOUS + term.length * SCORE_PER_CHARACTER - at * PENALTY_DISTANCE;
    if (at === 0) score += SCORE_PREFIX;
    else if (SEPARATORS.has(text[at - 1])) score += SCORE_WORD_START;
    return { score, positions };
  }
  return subsequence(term, text);
}

/**
 * The fallback: the term's characters in order but not adjacent.
 *
 * Greedy from the left, preferring a character that starts a word. It is not
 * an optimal alignment and does not need to be — a subsequence match is
 * already the weaker answer, and its job is to keep a row findable rather than
 * to rank it finely.
 */
function subsequence(term: string, text: string): { score: number; positions: number[] } | null {
  const positions: number[] = [];
  let score = 0;
  let from = 0;

  for (const character of term) {
    let found = -1;
    // Prefer a word start over the first plain occurrence: typing "co" should
    // find "create-order" by its two word starts rather than by the "co" that
    // is not there.
    for (let i = from; i < text.length; i++) {
      if (text[i] !== character) continue;
      if (found < 0) found = i;
      if (i === 0 || SEPARATORS.has(text[i - 1])) {
        found = i;
        break;
      }
    }
    if (found < 0) return null;
    if (found === 0) score += SCORE_PREFIX;
    else if (SEPARATORS.has(text[found - 1])) score += SCORE_WORD_START;
    // Adjacent to the previous match is nearly as good as contiguous.
    if (positions.length > 0 && found === positions[positions.length - 1] + 1) {
      score += SCORE_PER_CHARACTER * 8;
    }
    score += SCORE_PER_CHARACTER - found * PENALTY_DISTANCE;
    positions.push(found);
    from = found + 1;
  }
  return { score, positions };
}

function dedupe(list: number[]): number[] {
  const out: number[] = [];
  for (const value of list) {
    if (out[out.length - 1] !== value) out.push(value);
  }
  return out;
}

/** A run of matched characters, for rendering. */
export interface Segment {
  text: string;
  matched: boolean;
}

/**
 * Splits text into matched and unmatched runs.
 *
 * Runs rather than one span per character: a name of twenty characters would
 * otherwise be twenty DOM nodes, and the palette draws two dozen rows on every
 * keystroke.
 */
export function segments(text: string, positions: readonly number[]): Segment[] {
  if (positions.length === 0) return text ? [{ text, matched: false }] : [];
  const marked = new Set(positions);
  const out: Segment[] = [];
  let start = 0;
  let matched = marked.has(0);
  for (let i = 1; i <= text.length; i++) {
    const here = i < text.length && marked.has(i);
    if (i === text.length || here !== matched) {
      out.push({ text: text.slice(start, i), matched });
      start = i;
      matched = here;
    }
  }
  return out;
}

/**
 * A subsequence match, the same shape of matching every fuzzy finder uses:
 * "ordcre" matches "orders/create-order". Case-insensitive.
 *
 * The plain yes/no next to `match`'s scoring above, for the two filters that
 * only hide rows and never reorder them — the sidebar tree and the environment
 * editor's variable list. Neither ranks, so neither needs a score, and both
 * need to answer the same way: a filter that matched differently in two places
 * would be two filters.
 */
export function fuzzyMatches(query: string, text: string): boolean {
  if (query === "") return true;
  const haystack = text.toLowerCase();
  let at = 0;
  for (const char of query.toLowerCase()) {
    at = haystack.indexOf(char, at);
    if (at === -1) return false;
    at++;
  }
  return true;
}
