/**
 * The attributes every field in Otis that holds a *value* has to carry.
 *
 * Otis' window is a WKWebView, and macOS applies its own text substitutions
 * inside one: typing `qa3` into "New environment" produced `Qa3`, which is a
 * different file name from the one the dialog had just previewed and a
 * different name from the one every `{{...}}` in the collection was written
 * against. The same substitution reaches a header name, a variable name, a
 * query parameter and a search term.
 *
 * Nothing typed into this app is prose. It is names, values, URLs and
 * filter terms, and every one of them means exactly what was typed — so a
 * field takes these unless there is a reason it should not. The one field
 * that should not is the commit message, which is prose and wants its
 * spellchecker.
 *
 * Spread rather than defaulted inside `components/ui/input`, because half of
 * these are plain `<input>` elements inside table rows: a default on the
 * shadcn component would cover the dialogs and quietly miss the tables.
 */
export const verbatimText = {
  autoCapitalize: "none",
  autoCorrect: "off",
  autoComplete: "off",
  spellCheck: false,
} as const;
