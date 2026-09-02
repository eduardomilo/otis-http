/**
 * The CodeMirror 6 theme, syntax colours and language modes.
 *
 * Every value comes from docs/design/DESIGN-NOTES.md — the JSON palette from
 * §2.7, the gutters and line heights from §4.3 and §4.6, the surfaces from
 * §2.1. Values are read from the CSS custom properties defined in index.css
 * rather than repeated as hex, so there is still one place a colour is written
 * down.
 *
 * CodeMirror 6 was chosen over CodeMirror 5 (Bruno's editor) and Monaco for
 * the reason DESIGN-NOTES §7.8 gives: the window is served from a custom
 * scheme, so an editor that loads web workers from absolute paths cannot work.
 * CodeMirror 6 has no workers.
 */

import { HighlightStyle, StreamLanguage, syntaxHighlighting } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { json } from "@codemirror/lang-json";
import { xml } from "@codemirror/lang-xml";
import { javascript } from "@codemirror/lang-javascript";
import type { Extension } from "@codemirror/state";

/** The editor chrome: surfaces, gutters, the current line, the cursor. */
export const otisTheme = EditorView.theme(
  {
    "&": {
      color: "var(--fg)",
      backgroundColor: "transparent",
      fontFamily: "var(--font-mono)",
      // §3: 12px is the default, with a 20px line height for all code.
      fontSize: "12px",
      height: "100%",
    },
    ".cm-scroller": {
      fontFamily: "var(--font-mono)",
      lineHeight: "20px",
      overflow: "auto",
    },
    ".cm-content": {
      padding: "6px 0",
      caretColor: "var(--accent)",
    },
    "&.cm-focused": { outline: "none" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--accent)" },
    // The selection layer needs both selectors: the second is what a focused
    // editor uses, and without it the selection is invisible while typing.
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
      backgroundColor: "var(--bg-selected)",
    },
    // §4.6: a 44px line-number gutter with 14px of right padding in the body
    // editor, numbers in --fg-ghost and unselectable.
    ".cm-gutters": {
      backgroundColor: "transparent",
      border: "none",
      color: "var(--fg-ghost)",
      userSelect: "none",
    },
    ".cm-lineNumbers .cm-gutterElement": {
      minWidth: "44px",
      padding: "0 14px 0 0",
      textAlign: "right",
    },
    ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--fg-faint)" },
    // §2.1: the current line takes --bg-inset (screen 1a, line 9).
    ".cm-activeLine": { backgroundColor: "var(--bg-inset)" },
    ".cm-foldGutter .cm-gutterElement": { padding: "0 2px", color: "var(--fg-ghost)" },
    ".cm-foldPlaceholder": {
      backgroundColor: "var(--bg-control)",
      border: "1px solid var(--border-control)",
      borderRadius: "3px",
      color: "var(--fg-dim)",
      padding: "0 4px",
      margin: "0 4px",
      fontSize: "10px",
    },
    ".cm-placeholder": { color: "var(--fg-dim)" },
    // §5: 8px scrollbars with a --border-control thumb and no track. The
    // editor's own scroller needs them declared again; the rule in index.css
    // does not reach inside a shadow-free but separately styled subtree
    // consistently across webviews.
    ".cm-scroller::-webkit-scrollbar": { width: "8px", height: "8px" },
    ".cm-scroller::-webkit-scrollbar-track": { background: "transparent" },
    ".cm-scroller::-webkit-scrollbar-thumb": {
      background: "var(--border-control)",
      borderRadius: "2px",
    },
  },
  { dark: true },
);

/**
 * Syntax colours, DESIGN-NOTES §2.7. The design names five roles for JSON;
 * the other languages reuse them so a body and a script read as one system:
 * a key colour for property names and tags, a string colour for strings, amber
 * for numbers, violet for booleans, keywords and null, and --fg-dim for
 * everything structural.
 */
export const otisHighlight = HighlightStyle.define(
  [
    { tag: [t.propertyName, t.definition(t.propertyName)], color: "#93c5fd" },
    { tag: [t.string, t.special(t.string)], color: "var(--fg-secondary)" },
    { tag: [t.number], color: "var(--color-modified)" },
    { tag: [t.bool, t.null, t.keyword, t.atom], color: "#c084fc" },
    { tag: [t.punctuation, t.separator, t.bracket, t.brace], color: "var(--fg-dim)" },
    { tag: [t.comment], color: "var(--fg-faint)", fontStyle: "italic" },
    { tag: [t.tagName], color: "#93c5fd" },
    { tag: [t.attributeName], color: "var(--fg-muted)" },
    { tag: [t.attributeValue], color: "var(--fg-secondary)" },
    { tag: [t.variableName, t.name], color: "var(--fg)" },
    { tag: [t.function(t.variableName)], color: "#93c5fd" },
    { tag: [t.typeName, t.className], color: "#c084fc" },
    { tag: [t.operator], color: "var(--fg-dim)" },
    { tag: [t.invalid], color: "var(--danger)" },
  ],
  { themeType: "dark" },
);

/**
 * GraphQL, as a stream mode.
 *
 * A hand-written mode rather than `cm6-graphql`, which needs the whole
 * `graphql` package for a schema-aware parser Otis has no schema for. The
 * token set GraphQL needs for colour is small — names, keywords, strings,
 * numbers, variables, directives, comments, punctuation — and this maps them
 * onto the same tags §2.7 already colours.
 */
const graphqlKeywords = new Set([
  "query",
  "mutation",
  "subscription",
  "fragment",
  "on",
  "type",
  "input",
  "enum",
  "interface",
  "union",
  "scalar",
  "schema",
  "extend",
  "implements",
  "directive",
  "repeatable",
  "true",
  "false",
  "null",
]);

const graphqlMode = StreamLanguage.define<{ inBlockString: boolean }>({
  name: "graphql",
  startState: () => ({ inBlockString: false }),
  token(stream, state) {
    if (state.inBlockString) {
      while (!stream.eol()) {
        if (stream.match('"""')) {
          state.inBlockString = false;
          break;
        }
        stream.next();
      }
      return "string";
    }
    if (stream.eatSpace()) return null;
    if (stream.match("#")) {
      stream.skipToEnd();
      return "comment";
    }
    if (stream.match('"""')) {
      state.inBlockString = true;
      return "string";
    }
    if (stream.match(/^"(?:[^"\\]|\\.)*"?/)) return "string";
    if (stream.match(/^-?\d+(\.\d+)?([eE][+-]?\d+)?/)) return "number";
    if (stream.match(/^\$[_A-Za-z][_0-9A-Za-z]*/)) return "variableName";
    if (stream.match(/^@[_A-Za-z][_0-9A-Za-z]*/)) return "meta";
    if (stream.match(/^[_A-Za-z][_0-9A-Za-z]*/)) {
      const word = stream.current();
      if (graphqlKeywords.has(word)) return "keyword";
      // A name followed by a colon is a field alias or an argument name; both
      // are the "key" role in §2.7.
      const rest = stream.string.slice(stream.pos);
      return /^\s*:/.test(rest) ? "propertyName" : "name";
    }
    stream.next();
    return "punctuation";
  },
  languageData: { commentTokens: { line: "#" } },
});

/** The body-editor modes increment 10 supports. */
export type EditorMode = "json" | "xml" | "graphql" | "javascript" | "text";

/** The language extension for a mode, or none for plain text. */
export function languageFor(mode: EditorMode): Extension[] {
  switch (mode) {
    case "json":
      return [json()];
    case "xml":
      return [xml()];
    case "graphql":
      return [graphqlMode];
    case "javascript":
      return [javascript()];
    case "text":
      return [];
  }
}

/** The theme and syntax colours, shared by every editor in the window. */
export const otisEditorTheme: Extension[] = [otisTheme, syntaxHighlighting(otisHighlight)];

/**
 * The body editor's mode, from the effective Content-Type.
 *
 * The header is what the server will be told the body is, so it is what the
 * editor should believe. GraphQL is the exception: it travels as JSON
 * (docs/FORMAT.md §7 has the importer build the JSON envelope Postman sends),
 * so it is chosen by an explicit `application/graphql`, and a JSON body that
 * happens to hold a query still edits as JSON.
 */
export function modeForContentType(contentType: string | undefined): EditorMode {
  const type = (contentType ?? "").split(";")[0].trim().toLowerCase();
  if (type === "") return "text";
  if (type === "application/graphql" || type.endsWith("+graphql")) return "graphql";
  if (type === "application/json" || type.endsWith("+json")) return "json";
  if (type === "text/xml" || type === "application/xml" || type.endsWith("+xml")) return "xml";
  if (type === "application/javascript" || type === "text/javascript") return "javascript";
  return "text";
}

/** The label the sub-tab strip shows for a mode (screen 1a: `application/json`). */
export const MODE_LABEL: Record<EditorMode, string> = {
  json: "JSON",
  xml: "XML",
  graphql: "GraphQL",
  javascript: "JavaScript",
  text: "Text",
};
