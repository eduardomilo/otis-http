/**
 * `{{variable}}` tokens inside a CodeMirror editor.
 *
 * DESIGN-NOTES §2.7 gives a variable reference its own treatment in both the
 * body editor and the URL field: the accent on `--accent-wash`, whatever the
 * surrounding syntax. Increment 10 adds one more state the design does not
 * draw — a name nothing defines, which takes the warning amber (§2.6) — so an
 * unresolved variable is visible before the request is sent rather than after
 * it fails.
 *
 * The decoration is a `MatchDecorator` over the visible ranges only, so a body
 * of any size costs the same as a screenful.
 */

import { StateEffect, StateField, type Extension } from "@codemirror/state";
import {
  Decoration,
  type DecorationSet,
  EditorView,
  MatchDecorator,
  ViewPlugin,
  type ViewUpdate,
} from "@codemirror/view";

import { indexVariables, variableState, variableTitle, type VariableIndex } from "@/lib/variables";
import type { VariableRef } from "@bindings/internal/services";

/** Replaces the index the decoration consults. */
export const setVariableIndex = StateEffect.define<VariableIndex>();

/**
 * The variable index, as editor state.
 *
 * It lives in the editor rather than in a closure because it changes on every
 * environment switch and every reload, and a decorator rebuilt from scratch
 * each time would lose the plugin's cached ranges.
 */
export const variableIndexField = StateField.define<VariableIndex>({
  create: () => new Map(),
  update(value, transaction) {
    for (const effect of transaction.effects) {
      if (effect.is(setVariableIndex)) return effect.value;
    }
    return value;
  },
});

/** Pushes a new index into a live editor. */
export function updateVariableIndex(view: EditorView, refs: readonly VariableRef[] | undefined) {
  view.dispatch({ effects: setVariableIndex.of(indexVariables(refs)) });
}

const marks = {
  resolved: Decoration.mark({ class: "cm-otis-var" }),
  secret: Decoration.mark({ class: "cm-otis-var cm-otis-var-secret" }),
  unresolved: Decoration.mark({ class: "cm-otis-var cm-otis-var-unresolved" }),
};

/** The same pattern as lib/variables.ts and internal/resolve/variables.go. */
const VARIABLE = /\{\{\s*([A-Za-z_$][\w.-]*)\s*\}\}/g;

/** The plugin: one decorator, re-matched as the document and the index change. */
export const variableDecoration = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    private decorator: MatchDecorator;

    constructor(view: EditorView) {
      this.decorator = new MatchDecorator({
        regexp: VARIABLE,
        decoration: (match, editor) => {
          const index = editor.state.field(variableIndexField, false) ?? new Map();
          return marks[variableState(index, match[1])];
        },
      });
      this.decorations = this.decorator.createDeco(view);
    }

    update(update: ViewUpdate) {
      const indexChanged = update.transactions.some((tr) =>
        tr.effects.some((effect) => effect.is(setVariableIndex)),
      );
      if (indexChanged) {
        // A new index restyles every token, so the cached ranges go.
        this.decorations = this.decorator.createDeco(update.view);
        return;
      }
      this.decorations = this.decorator.updateDeco(update, this.decorations);
    }
  },
  { decorations: (plugin) => plugin.decorations },
);

/** The token styling, kept beside the plugin that applies it. */
export const variableTheme = EditorView.baseTheme({
  ".cm-otis-var": {
    // §2.4: --accent on --accent-wash, radius 3px (§5).
    color: "var(--accent)",
    backgroundColor: "var(--accent-wash)",
    borderRadius: "3px",
  },
  // §2.6: a secret is amber wherever it appears.
  ".cm-otis-var-secret": { color: "var(--color-secret)" },
  // The warning state: amber text on an amber wash, so it reads as attention
  // rather than as a second accent.
  ".cm-otis-var-unresolved": {
    color: "var(--color-warning)",
    backgroundColor: "rgba(251, 191, 36, .10)",
  },
});

/**
 * A tooltip naming a hovered token's origin.
 *
 * A plain `title`, set on hover, rather than a Radix tooltip or CodeMirror's
 * own hover tooltip: the same reason the tree's git dots use one
 * (DESIGN-NOTES §6, and the note in CLAUDE.md) — a decoration per token is
 * cheap only while it stays a span.
 */
const variableTooltip: Extension = EditorView.domEventHandlers({
  mouseover(event, view) {
    const target = event.target;
    if (!(target instanceof HTMLElement) || !target.classList.contains("cm-otis-var")) return;
    const name = target.textContent?.replace(/[{}\s]/g, "") ?? "";
    const index = view.state.field(variableIndexField, false) ?? new Map();
    target.title = variableTitle(index, name);
  },
});

/** Everything a variable-aware editor needs, in one extension. */
export const variableExtensions: Extension[] = [
  variableIndexField,
  variableDecoration,
  variableTheme,
  variableTooltip,
];
