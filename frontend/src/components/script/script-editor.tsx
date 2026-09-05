import { useEffect } from "react";
import { Link } from "@tanstack/react-router";

import { CodeEditor } from "@/components/editor/code-editor";
import { ConflictBanner } from "@/components/request/conflict-banner";
import { hint } from "@/lib/platform";
import { nodeLink } from "@/lib/paths";
import { cn } from "@/lib/utils";
import { useScripts } from "@/state/scripts-context";
import type { ScriptDocument } from "@bindings/internal/services";

/**
 * The centre pane for `/s/$path`: a `.js` file in the collection.
 *
 * A script has been a row in the tree since the walker learned about them
 * (docs/FORMAT.md §2.4) and was the one row that could not be opened —
 * clicking it showed the folder that held it, with a comment saying the
 * editor arrived with the script engine. The engine has been running these
 * files for some time; this is the half that lets you read one.
 *
 * **The text is the document.** There is no parsed model, no serializer and
 * no canonical form: Go writes what the editor holds, byte for byte, so a
 * script cannot be reformatted by saving it and nobody's prettier config ends
 * up in a diff. That is also why scripts have their own provider rather than
 * joining `documents-context`, whose draft is a `httpfile.File`.
 *
 * The header says what the file *is*, because a script's whole identity is
 * its name and that convention is easy to get wrong: `_post.js` runs after
 * every request in its folder, `create-order.post.js` runs after that one
 * request, and anything else is a module that runs only when a hook imports
 * it. Saying it here is cheaper than remembering §2.4.
 */
export function ScriptEditor({ path }: { path: string }) {
  const { get, open, edit, save, reload, keepMine } = useScripts();
  const state = get(path);

  useEffect(() => {
    open(path);
  }, [open, path]);

  const doc = state?.loaded;

  if (!state || (state.busy && !doc)) return <Centered>Loading…</Centered>;
  if (state.error && !doc) return <Centered tone="warning">{state.error}</Centered>;
  if (!doc || state.draft === null) return <Centered>Loading…</Centered>;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {state.conflict ? (
        <ConflictBanner
          path={path}
          onReload={() => void reload(path)}
          onKeepMine={() => keepMine(path)}
        />
      ) : null}
      {state.error ? (
        <p className="shrink-0 border-b border-border-danger bg-destructive/5 px-4 py-1.5 text-meta text-destructive">
          {state.error}
        </p>
      ) : null}

      <Header doc={doc} dirty={state.dirty} busy={state.busy} onSave={() => void save(path)} />

      <div className="min-h-0 flex-1">
        <CodeEditor
          value={state.draft}
          mode="javascript"
          onChange={(text) => edit(path, text)}
          // CodeMirror sees keys before the window does, so ⌘S is handed to it
          // as a keymap calling the same function the shell's map calls. The
          // request editor does the same, and for the same reason.
          keys={[
            {
              key: "Mod-s",
              preventDefault: true,
              run: () => {
                void save(path);
                return true;
              },
            },
          ]}
          className="h-full"
        />
      </div>
    </div>
  );
}

/**
 * The file, what runs it, and Save.
 *
 * `WHAT IT IS` in words rather than a badge: "runs after every request in
 * orders/" is the sentence somebody opening a hook for the first time needs,
 * and there is room for it here in a way there is not on a 24px tree row.
 */
function Header({
  doc,
  dirty,
  busy,
  onSave,
}: {
  doc: ScriptDocument;
  dirty: boolean;
  busy: boolean;
  onSave: () => void;
}) {
  return (
    <div className="shrink-0 border-b border-border px-4 py-3">
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-title text-fg-emphasis">{doc.name}</span>
        <span
          className={cn(
            "shrink-0 rounded-sm border px-1 text-micro tracking-[.06em] uppercase",
            doc.hook ? "border-border-control text-fg-dim" : "border-border-hairline text-fg-faint",
          )}
        >
          {doc.hook ? "hook" : "lib"}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-meta text-fg-faint">{doc.path}</span>
        <button
          type="button"
          disabled={!dirty || busy}
          onClick={onSave}
          className="h-[26px] shrink-0 gap-1.5 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
        >
          Save <span className="font-mono text-label opacity-70">{hint("S")}</span>
        </button>
      </div>

      <p className="mt-1 text-meta text-fg-dim">
        <Runs doc={doc} />
      </p>
    </div>
  );
}

/** What runs this file, in a sentence, with a link to whatever owns it. */
function Runs({ doc }: { doc: ScriptDocument }) {
  if (!doc.hook) {
    return (
      <>
        A plain ES module. <span className="font-mono text-fg-faint">Nothing runs it</span> unless a
        hook imports it (docs/FORMAT.md §2.4). It gets a JavaScript realm and nothing else — no
        filesystem, no process, no network, no timers.
      </>
    );
  }
  const when = doc.phase === "pre" ? "before" : "after";
  if (doc.hookOf) {
    return (
      <>
        Runs <span className="text-fg-secondary">{when}</span>{" "}
        <Link {...nodeLink("request", doc.hookOf)} className="font-mono text-fg-faint hover:text-fg">
          {doc.hookOf}
        </Link>
        , and only that request.
      </>
    );
  }
  return (
    <>
      Runs <span className="text-fg-secondary">{when} every request</span> in{" "}
      <Link {...nodeLink("folder", doc.scope ?? "")} className="font-mono text-fg-faint hover:text-fg">
        {doc.scope ? `${doc.scope}/` : "the collection root"}
      </Link>{" "}
      and below.
    </>
  );
}

function Centered({ children, tone }: { children: React.ReactNode; tone?: "warning" }) {
  return (
    <div className="flex h-full items-center justify-center px-8">
      <p
        className={cn(
          "max-w-[520px] text-center text-meta",
          tone === "warning" ? "text-destructive" : "text-fg-dim",
        )}
      >
        {children}
      </p>
    </div>
  );
}
