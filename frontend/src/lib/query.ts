/**
 * Query parameters, as the Params tab edits them.
 *
 * The tab is a view of the URL, not a second place params live: parsing splits
 * the URL's query string into rows, and writing rebuilds it. The requirement
 * from increment 10 is that the round trip is non-destructive to ordering, so
 * the split keeps the parameters in the order the URL had them and the rebuild
 * writes them back in the order the table shows.
 *
 * Percent-encoding is deliberately *not* normalised. A request file is text a
 * person wrote, and `{{baseUrl}}/v2/orders?expand=customer` must not come back
 * as `%7B%7BbaseUrl%7D%7D/v2/orders?expand=customer`. So each name and value
 * keeps the bytes it had, decoded only for display, and a row the user edits is
 * encoded with the minimum that keeps the URL parseable.
 */

/** One row of the Params table. */
export interface QueryParam {
  /** The name as typed, decoded for display. */
  name: string;
  /** The value as typed, decoded for display. */
  value: string;
  /**
   * The parameter's source text, `name=value` exactly as it appeared. It is
   * kept so an untouched row is written back byte for byte.
   */
  raw?: string;
  /** False for a row the user unchecked; it is dropped from the URL. */
  enabled: boolean;
}

/** A URL split at its query string. */
export interface SplitUrl {
  /** Everything before the "?". */
  base: string;
  params: QueryParam[];
  /** The "#fragment", including the hash, or "". */
  hash: string;
}

/**
 * Splits a URL into its base, its parameters and its fragment.
 *
 * A URL with no "?" yields no parameters, and an empty parameter (`?&a=1`) is
 * dropped: it is not a row the table can show, and rebuilding without it
 * produces the same request.
 */
export function splitUrl(url: string): SplitUrl {
  // The fragment comes off first: a "?" after a "#" is part of the fragment.
  const hashAt = url.indexOf("#");
  const hash = hashAt === -1 ? "" : url.slice(hashAt);
  const withoutHash = hashAt === -1 ? url : url.slice(0, hashAt);

  const queryAt = withoutHash.indexOf("?");
  if (queryAt === -1) return { base: withoutHash, params: [], hash };

  const base = withoutHash.slice(0, queryAt);
  const query = withoutHash.slice(queryAt + 1);
  const params: QueryParam[] = [];
  for (const piece of query.split("&")) {
    if (piece === "") continue;
    const equals = piece.indexOf("=");
    const rawName = equals === -1 ? piece : piece.slice(0, equals);
    const rawValue = equals === -1 ? "" : piece.slice(equals + 1);
    params.push({
      name: decode(rawName),
      value: decode(rawValue),
      raw: piece,
      enabled: true,
    });
  }
  return { base, params, hash };
}

/**
 * Rebuilds a URL from its parts. A row that still carries its `raw` and whose
 * decoded name and value match it is written back unchanged; an edited or new
 * row is encoded.
 */
export function buildUrl({ base, params, hash }: SplitUrl): string {
  const pieces: string[] = [];
  for (const param of params) {
    if (!param.enabled) continue;
    if (param.name === "" && param.value === "") continue;
    pieces.push(param.raw !== undefined && isUnchanged(param) ? param.raw : encodeParam(param));
  }
  const query = pieces.length > 0 ? "?" + pieces.join("&") : "";
  return base + query + hash;
}

/** Replaces a URL's query string with params, keeping base and fragment. */
export function withParams(url: string, params: QueryParam[]): string {
  const { base, hash } = splitUrl(url);
  return buildUrl({ base, params, hash });
}

/** True when a row still matches the source text it was parsed from. */
function isUnchanged(param: QueryParam): boolean {
  if (param.raw === undefined) return false;
  const parsed = splitUrl("?" + param.raw).params[0];
  return parsed !== undefined && parsed.name === param.name && parsed.value === param.value;
}

/**
 * Encodes one parameter.
 *
 * `encodeURIComponent` escapes the braces of a `{{variable}}`, which would
 * turn a reference into literal text, so they are restored afterwards. The
 * same goes for a plain `/`, `:` and `,`, which are legal in a query value and
 * which every API in the design's example collection uses unescaped.
 */
function encodeParam(param: QueryParam): string {
  const name = encodePiece(param.name);
  return param.value === "" ? name : `${name}=${encodePiece(param.value)}`;
}

function encodePiece(text: string): string {
  return encodeURIComponent(text)
    .replace(/%7B/g, "{")
    .replace(/%7D/g, "}")
    .replace(/%2F/g, "/")
    .replace(/%3A/g, ":")
    .replace(/%2C/g, ",");
}

/** Decodes a piece for display, leaving a malformed escape as written. */
function decode(text: string): string {
  try {
    return decodeURIComponent(text.replace(/\+/g, " "));
  } catch {
    return text;
  }
}
