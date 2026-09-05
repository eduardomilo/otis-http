import { useEffect, useLayoutEffect, useRef } from "react";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import {
  EditorView,
  drawSelection,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  placeholder as placeholderExt,
} from "@codemirror/view";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { bracketMatching, foldGutter, indentOnInput } from "@codemirror/language";

import { highlightCurrentLine, languageFor, otisEditorTheme, type EditorMode } from "@/components/editor/otis-theme";
import { updateVariableIndex, variableExtensions } from "@/components/editor/variable-decoration";
import { cn } from "@/lib/utils";
import type { VariableRef } from "@bindings/internal/services";

/**
 * A CodeMirror 6 editor.
 *
 * The extension set is assembled here rather than taken from `basicSetup`,
 * which pulls in autocompletion, a search panel and a lint gutter that the
 * design does not draw. What is here is what screen 1a shows: line numbers, a
 * fold gutter, a current-line highlight, bracket matching, history, and the
 * `{{variable}}` decoration.
 *
 * Plus two behaviours that draw nothing and are what make a body editor feel
 * like one: Enter indents to the language's level (`defaultKeymap`'s
 * `insertNewlineAndIndent`, which needs the language loaded to know what that
 * level is), and typing an opening brace, bracket or quote closes it
 * (`closeBrackets`). `closeBrackets` comes from `@codemirror/autocomplete`,
 * which is a package name rather than a statement about what it does: it adds
 * no completion UI, and the design's objection was to the popup.
 *
 * Two things change without recreating the editor, each behind its own
 * compartment: the language (the Content-Type can change while the body is
 * open) and the read-only flag (the Scripts tab). The variable index arrives as
 * a state effect for the same reason.
 */
export interface CodeEditorProps {
  value: string;
  onChange?: (value: string) => void;
  mode?: EditorMode;
  readOnly?: boolean;
  /** The variable index the `{{token}}` styling consults. */
  variables?: readonly VariableRef[];
  /** Extra keys, for the shortcuts the shell owns (⌘S, ⌘↵). */
  keys?: Parameters<typeof keymap.of>[0];
  placeholder?: string;
  className?: string;
  /** Line numbers and the fold gutter. On unless the editor is single-line. */
  gutters?: boolean;
  /**
   * One line, behaving like an `<input>`: no gutters, no wrapping, no vertical
   * scroll, and Tab moves focus instead of indenting. The URL field is this.
   */
  singleLine?: boolean;
}

export function CodeEditor({
  value,
  onChange,
  mode = "text",
  readOnly = false,
  variables,
  keys,
  placeholder,
  className,
  singleLine = false,
  gutters = !singleLine,
}: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const language = useRef(new Compartment());
  const editable = useRef(new Compartment());
  const extraKeys = useRef(new Compartment());

  // The change handler and the extra keys are read through refs so a new
  // closure on every render does not tear the editor down and rebuild it,
  // which would lose the cursor and the undo history.
  const latest = useRef({ onChange, keys });
  latest.current = { onChange, keys };

  useLayoutEffect(() => {
    const element = host.current;
    if (!element) return;

    const extensions: Extension[] = [
      history(),
      drawSelection(),
      indentOnInput(),
      bracketMatching(),
      // A single-line editor is a form field, and a field that turns a typed
      // quote into a pair is a field that fights you. The URL bar is that
      // field (it is an editor only for the {{variable}} decoration), and
      // `{{` there would close itself into `{{}}`.
      ...(singleLine || readOnly ? [] : [closeBrackets()]),
      // Ours, not CodeMirror's: it stands down while text is selected, so
      // the opaque current-line background cannot paint over the selection
      // layer beneath it (see otis-theme).
      highlightCurrentLine,
      ...(gutters ? [lineNumbers(), highlightActiveLineGutter(), foldGutter()] : []),
      // The extra keys come first so ⌘↵ and ⌘S reach the shell before
      // CodeMirror's own bindings claim them.
      extraKeys.current.of(keymap.of(latest.current.keys ?? [])),
      // closeBracketsKeymap ahead of the default one: it is what makes
      // Backspace delete both halves of a pair it just inserted.
      keymap.of([
        ...(singleLine || readOnly ? [] : closeBracketsKeymap),
        ...defaultKeymap,
        ...historyKeymap,
      ]),
      // Tab indents only in a multi-line editable body. Everywhere else Tab
      // has to move focus, which is CodeMirror's default and the only way out
      // of a one-line field for someone using the keyboard.
      ...(singleLine || readOnly ? [] : [keymap.of([indentWithTab])]),
      ...otisEditorTheme,
      ...variableExtensions,
      language.current.of(languageFor(mode)),
      editable.current.of([
        EditorState.readOnly.of(readOnly),
        EditorView.editable.of(!readOnly),
      ]),
      ...(placeholder ? [placeholderExt(placeholder)] : []),
      ...(singleLine ? [] : [EditorView.lineWrapping]),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged) return;
        latest.current.onChange?.(update.state.doc.toString());
      }),
    ];

    const created = new EditorView({
      state: EditorState.create({ doc: value, extensions }),
      parent: element,
    });
    view.current = created;
    return () => {
      created.destroy();
      view.current = null;
    };
    // Built once. Everything that can change afterwards is reconfigured below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gutters, singleLine]);

  // An external change to the text — a reload, a conflict resolved, a Format —
  // is applied as a transaction rather than a rebuild, so the scroll position
  // survives. A value that already matches is left alone: replacing the
  // document with itself would move the cursor to the end on every keystroke.
  useEffect(() => {
    const current = view.current;
    if (!current || current.state.doc.toString() === value) return;
    current.dispatch({
      changes: { from: 0, to: current.state.doc.length, insert: value },
    });
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({ effects: language.current.reconfigure(languageFor(mode)) });
  }, [mode]);

  useEffect(() => {
    view.current?.dispatch({
      effects: editable.current.reconfigure([
        EditorState.readOnly.of(readOnly),
        EditorView.editable.of(!readOnly),
      ]),
    });
  }, [readOnly]);

  useEffect(() => {
    view.current?.dispatch({
      effects: extraKeys.current.reconfigure(keymap.of(keys ?? [])),
    });
  }, [keys]);

  useEffect(() => {
    const current = view.current;
    if (current) updateVariableIndex(current, variables);
  }, [variables]);

  return (
    <div
      ref={host}
      className={cn(
        "min-h-0 min-w-0 flex-1 overflow-hidden",
        // The one-line rules live in index.css beside the rest of the theme.
        singleLine && "cm-single-line",
        className,
      )}
    />
  );
}
