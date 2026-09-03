import { Plus, Trash2 } from "lucide-react";

import { CodeEditor } from "@/components/editor/code-editor";
import { Button } from "@/components/ui/button";
import {
  addScript,
  removeScriptAt,
  setScriptAt,
  scriptsOf,
  type ScriptPhase,
} from "@/lib/http-file";
import { cn } from "@/lib/utils";
import type { Request, Script } from "@bindings/internal/httpfile";

/**
 * The Scripts tab: the `{% %}` blocks, editable.
 *
 * Editable from Increment 15, because the app now runs them — an editable
 * script the app could not run would have been an offer it did not keep,
 * which is why this was read-only until the engine existed.
 *
 * The blocks are shown verbatim with JavaScript highlighting and the line the
 * `{%` sits on, so a script is findable in the file (docs/FORMAT.md §1.10
 * records exactly that line). Go's serializer still writes the file: this
 * edits the parsed model and hands it back, so there is one answer to what
 * canonical form is.
 *
 * The external `> ./handler.js` form names its file rather than showing it:
 * the parser records the path and does not read it, and that file is a tree
 * row of its own with a HOOK badge (docs/FORMAT.md §2.4).
 */
export function ScriptsTab({
  entry,
  onEdit,
}: {
  entry: Request;
  onEdit: (fn: (entry: Request) => Request) => void;
}) {
  const pre = scriptsOf(entry, "pre");
  const post = scriptsOf(entry, "post");

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto py-3">
      <Group
        label="Pre-request"
        marker="< {% … %}"
        phase="pre"
        scripts={pre}
        onEdit={onEdit}
        note="Runs before the request's variables are resolved, after every folder's _pre.js above it — so a value set here can be referenced by a folder header."
      />
      <Group
        label="Post-response"
        marker="> {% … %}"
        phase="post"
        scripts={post}
        onEdit={onEdit}
        note="Runs first of the post-response hooks, before the folders' _post.js. This is where test() and expect() live."
      />

      <p className="max-w-[640px] text-meta text-fg-faint">
        A script gets no filesystem, no process and no network — it shapes the request Otis is about
        to send, and reads the response that came back. The whole API is docs/FORMAT.md §9.
      </p>
    </div>
  );
}

function Group({
  label,
  marker,
  phase,
  scripts,
  note,
  onEdit,
}: {
  label: string;
  marker: string;
  phase: ScriptPhase;
  scripts: readonly Script[];
  note: string;
  onEdit: (fn: (entry: Request) => Request) => void;
}) {
  const example =
    phase === "pre"
      ? `vars.request.set("idemKey", crypto.randomUUID());\n`
      : `test("ok", () => expect(response.ok).toBeTruthy());\n`;

  return (
    <section className="flex flex-col gap-1.5">
      <header className="flex items-center gap-2">
        {/* §8.6: a 10px uppercase label, with the syntax beside it in mono. */}
        <span className="text-label tracking-[.06em] text-fg-dim uppercase">{label}</span>
        <span className="font-mono text-label text-fg-faint">{marker}</span>
        <div className="flex-1" />
        <button
          type="button"
          onClick={() => onEdit((e) => addScript(e, phase, example))}
          className="flex items-center gap-1 text-meta text-fg-muted hover:text-fg-emphasis"
        >
          <Plus className="size-3" />
          Add a block
        </button>
      </header>

      {scripts.length === 0 ? (
        <p className="max-w-[640px] text-meta text-fg-dim">{note}</p>
      ) : (
        scripts.map((script, at) => (
          <ScriptBlock
            key={script.line ? `line-${script.line}` : `new-${at}`}
            script={script}
            onChange={(text) => onEdit((e) => setScriptAt(e, phase, at, text))}
            onRemove={() => onEdit((e) => removeScriptAt(e, phase, at))}
          />
        ))
      )}
    </section>
  );
}

function ScriptBlock({
  script,
  onChange,
  onRemove,
}: {
  script: Script;
  onChange: (text: string) => void;
  onRemove: () => void;
}) {
  if (script.filePath) {
    return (
      <div className="flex items-center gap-2 rounded-sm border border-border-control bg-inset px-2.5 py-1.5">
        {/* §3: the `js` marker is mono 10px, weight 500. */}
        <span className="font-mono text-label font-medium text-fg-dim">js</span>
        <span className="font-mono text-ui text-fg-secondary">{script.filePath}</span>
        <span className="text-meta text-fg-faint">
          an external handler — edit the file itself
        </span>
      </div>
    );
  }
  return (
    <div className="group overflow-hidden rounded-sm border border-border-control bg-inset">
      <div className="flex items-center justify-between border-b border-border-hairline px-2.5 py-1">
        <span className="font-mono text-label font-medium text-fg-dim">js</span>
        <div className="flex items-center gap-2">
          {script.line ? (
            <span className="font-mono text-label text-fg-faint">line {script.line}</span>
          ) : (
            <span className="font-mono text-label text-fg-faint">new</span>
          )}
          <Button
            type="button"
            onClick={onRemove}
            title="Remove this block"
            aria-label="Remove this block"
            className="size-5 rounded-sm bg-transparent p-0 text-fg-ghost hover:bg-control hover:text-destructive"
          >
            <Trash2 className="size-3" />
          </Button>
        </div>
      </div>
      <CodeEditor
        value={(script.text ?? "").replace(/^\n/, "")}
        onChange={onChange}
        mode="javascript"
        gutters={false}
        className={cn("max-h-[320px] px-2.5")}
      />
    </div>
  );
}
