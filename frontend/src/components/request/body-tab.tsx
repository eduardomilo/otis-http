import { useMemo } from "react";
import type { KeyBinding } from "@codemirror/view";
import { FileCode2, Wand2 } from "lucide-react";

import { CodeEditor } from "@/components/editor/code-editor";
import { MODE_LABEL, type EditorMode } from "@/components/editor/otis-theme";
import { setBody } from "@/lib/http-file";
import { formatJson } from "@/lib/json";
import { cn } from "@/lib/utils";
import type { Request } from "@bindings/internal/httpfile";
import type { VariableRef } from "@bindings/internal/services";

/**
 * The Body tab (screen 1a): a CodeMirror editor with a 44px line-number
 * gutter, a `--bg-inset` current line, and `{{variable}}` tokens on a wash.
 *
 * The body is preserved byte for byte unless the user edits it. That falls out
 * of the model: `Body.raw` is carried through the parse, the draft and the save
 * untouched, and the editor writes it only from an actual change event.
 *
 * A `< ./file` body (docs/FORMAT.md §1.8) is not text this editor owns — the
 * bytes are in another file, and Otis does not read it — so the tab names the
 * file instead of pretending to hold it.
 */
export function BodyTab({
  entry,
  mode,
  variables,
  onEdit,
  onSend,
  onSave,
}: {
  entry: Request;
  mode: EditorMode;
  variables?: readonly VariableRef[];
  onEdit: (fn: (entry: Request) => Request) => void;
  onSend?: () => void;
  onSave?: () => void;
}) {
  const keys = useMemo<KeyBinding[]>(
    () => [
      { key: "Mod-Enter", preventDefault: true, run: () => (onSend?.(), true) },
      { key: "Mod-s", preventDefault: true, run: () => (onSave?.(), true) },
    ],
    [onSend, onSave],
  );

  if (entry.body?.filePath) {
    return (
      <div className="flex min-h-0 flex-1 flex-col items-start gap-2 py-4">
        <div className="flex items-center gap-2 rounded-md border border-border-control bg-inset px-3 py-2">
          <FileCode2 className="size-3.5 text-fg-muted" />
          <span className="font-mono text-ui text-fg-secondary">{entry.body.filePath}</span>
        </div>
        <p className="max-w-[560px] text-meta text-fg-dim">
          The body is read from this file at send time, relative to the request's own directory.
          {entry.body.substituteVariables
            ? " Variables inside it are resolved with this request's scope."
            : " Its bytes are sent verbatim; variables inside it are not resolved."}
        </p>
      </div>
    );
  }

  const raw = entry.body?.raw ?? "";
  const formatted = mode === "json" ? formatJson(raw) : null;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {formatted !== null && formatted !== raw ? (
        <div className="flex shrink-0 items-center justify-end py-1.5">
          <button
            type="button"
            onClick={() => onEdit((e) => setBody(e, formatted))}
            className={cn(
              "flex h-6 items-center gap-1.5 rounded-sm border border-border-control bg-control px-2.5",
              "text-ui text-fg-secondary hover:text-fg-emphasis",
            )}
          >
            <Wand2 className="size-3" />
            Format {MODE_LABEL[mode]}
          </button>
        </div>
      ) : null}
      <CodeEditor
        value={raw}
        mode={mode}
        variables={variables}
        keys={keys}
        placeholder="No body."
        onChange={(next) => onEdit((e) => setBody(e, next))}
        className="-mx-4"
      />
    </div>
  );
}
