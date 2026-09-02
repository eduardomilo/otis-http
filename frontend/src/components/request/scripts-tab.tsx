import { CodeEditor } from "@/components/editor/code-editor";
import { cn } from "@/lib/utils";
import type { Request, Script } from "@bindings/internal/httpfile";

/**
 * The Scripts tab: the `{% %}` blocks, read-only.
 *
 * Read-only is the increment's boundary, not a limitation of the editor —
 * execution is Phase D, and an editable script the app cannot run would be an
 * offer it does not keep. The blocks are shown verbatim with JavaScript
 * highlighting and the line the `{%` sits on, so a script is findable in the
 * file (docs/FORMAT.md §1.10 records exactly that line).
 *
 * The external `> ./handler.js` form names its file rather than showing it: the
 * parser records the path and does not read it, and that file is a tree row of
 * its own (screen 3a gives `_pre.js` and `_post.js` `HOOK` badges).
 */
export function ScriptsTab({ entry }: { entry: Request }) {
  const pre = entry.preScripts ?? [];
  const post = entry.postScripts ?? [];

  if (pre.length === 0 && post.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-2 py-4">
        <p className="max-w-[560px] text-meta text-fg-dim">
          No scripts in this request. A pre-request block is{" "}
          <Code>{"< {% … %}"}</Code> before the request line; a post-response block is{" "}
          <Code>{"> {% … %}"}</Code> after the body. Both run in Phase D.
        </p>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto py-3">
      <Group label="Pre-request" marker="< {% … %}" scripts={pre} />
      <Group label="Post-response" marker="> {% … %}" scripts={post} />
      <p className="max-w-[560px] text-meta text-fg-faint">
        Read-only: Otis does not run scripts yet. The blocks are preserved exactly as written when
        the file is saved.
      </p>
    </div>
  );
}

function Group({
  label,
  marker,
  scripts,
}: {
  label: string;
  marker: string;
  scripts: readonly Script[];
}) {
  if (scripts.length === 0) return null;
  return (
    <section className="flex flex-col gap-1.5">
      <header className="flex items-center gap-2">
        {/* §8.6: a 10px uppercase label, with the syntax beside it in mono. */}
        <span className="text-label tracking-[.06em] text-fg-dim uppercase">{label}</span>
        <span className="font-mono text-label text-fg-faint">{marker}</span>
      </header>
      {scripts.map((script, at) => (
        <ScriptBlock key={at} script={script} />
      ))}
    </section>
  );
}

function ScriptBlock({ script }: { script: Script }) {
  if (script.filePath) {
    return (
      <div className="flex items-center gap-2 rounded-sm border border-border-control bg-inset px-2.5 py-1.5">
        {/* §3: the `js` marker is mono 10px, weight 500. */}
        <span className="font-mono text-label font-medium text-fg-dim">js</span>
        <span className="font-mono text-ui text-fg-secondary">{script.filePath}</span>
      </div>
    );
  }
  return (
    <div className="overflow-hidden rounded-sm border border-border-control bg-inset">
      <div className="flex items-center justify-between border-b border-border-hairline px-2.5 py-1">
        <span className="font-mono text-label font-medium text-fg-dim">js</span>
        {script.line ? (
          <span className="font-mono text-label text-fg-faint">line {script.line}</span>
        ) : null}
      </div>
      <CodeEditor
        value={(script.text ?? "").replace(/^\n/, "").replace(/\n\s*$/, "")}
        mode="javascript"
        readOnly
        gutters={false}
        className={cn("max-h-[280px] px-2.5")}
      />
    </div>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return <code className="rounded-sm bg-control px-1 font-mono text-fg-secondary">{children}</code>;
}
