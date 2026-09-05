import { useCallback, useEffect, useMemo, useState } from "react";
import { CircleDashed, Play, X } from "lucide-react";

import { MarkdownView } from "@/components/folder/markdown";
import { FolderSettingsEditor } from "@/components/folder/settings-editor";
import {
  AuthPanel,
  HeadersPanel,
  Panel,
  ScriptsPanel,
  VariablesPanel,
} from "@/components/folder/panels";
import { CodeEditor } from "@/components/editor/code-editor";
import { CreateScriptDialog } from "@/components/shell/create-script-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useNavigate } from "@tanstack/react-router";

import { nodeLink } from "@/lib/paths";
import { hint } from "@/lib/platform";
import { allRequests } from "@/lib/tree";
import { indexVariables } from "@/lib/variables";
import { cn } from "@/lib/utils";
import { FolderService } from "@bindings/internal/services";
import type { FolderDocument } from "@bindings/internal/services";
import { useCollection } from "@/state/collection-context";
import { useEnvironments } from "@/state/environment-context";
import { useRuns, type Run } from "@/state/run-context";
import { useTabs } from "@/state/tabs-context";

/**
 * The folder view (screen 3a) — the screen that explains the product's model.
 *
 * Layout is DESIGN-NOTES §4.1's `1fr 440px`. The right column is the folder's
 * shared settings at a glance and is the same on every tab; the left column is
 * what the tab chooses. On Overview that is the README, which is why there is
 * no separate Docs tab — the documentation *is* the overview.
 *
 * On the other tabs the left column is the editor for that setting, at full
 * width, and the panels stay beside it. That duplication is deliberate: the
 * design draws all four panels beside the README, and dropping them from the
 * other tabs would mean losing the glance while you edit one of them.
 *
 * It only works while they are *beside* it. Below 800px the two columns stack,
 * and a glance underneath the thing it describes is not a glance — it is a
 * second, read-only copy of what you are editing, which is what "the Variables
 * tab shows the Overview in the background" turned out to be. So off the
 * two-column layout the panels belong to Overview alone.
 */

type Tab = "overview" | "auth" | "variables" | "scripts" | "headers";

export function FolderView({ path }: { path: string }) {
  const { active: env } = useEnvironments();
  const { runFor, start, cancel } = useRuns();
  const { tree } = useCollection();
  const { openTab } = useTabs();
  const navigate = useNavigate();
  // Its own instance of the dialog rather than the shell's. The shell's is
  // reached from the tree's menu and the palette; this one is reached from
  // the Scripts panel, which is a route away from AppShell and has no prop
  // to hand it. The dialog holds one piece of state — which folder — so a
  // second instance is cheaper than a context for it (DESIGN-NOTES §9.41).
  const [creatingScript, setCreatingScript] = useState<string | null>(null);
  const [doc, setDoc] = useState<FolderDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("overview");
  const [stopOnFailure, setStopOnFailure] = useState(false);

  const load = useCallback(async () => {
    try {
      setDoc((await FolderService.Load(path, env)) ?? null);
      setError(null);
    } catch (cause) {
      setDoc(null);
      setError(String(cause));
    }
  }, [path, env]);

  useEffect(() => {
    void load();
  }, [load]);

  const run = runFor(path);
  // A run sets session variables, so the panel has to re-read once it ends.
  useEffect(() => {
    if (run?.summary) void load();
  }, [run?.summary, load]);

  // Go resolved every reference the folder's own values make, against the
  // active environment, so a token reads as what it actually is: an
  // environment secret styles as resolved-and-secret rather than as a
  // warning, and a name nothing defines styles as the warning it is.
  const index = useMemo(() => indexVariables(doc?.references ?? undefined), [doc?.references]);

  if (error) {
    return (
      <div className="flex h-full items-center justify-center px-8">
        <p className="max-w-[520px] text-center text-meta text-destructive">{error}</p>
      </div>
    );
  }
  if (!doc) return <div className="h-full" />;

  const clearSession = () => {
    void FolderService.ClearSession(path)
      .then((next) => next && setDoc(next))
      .catch((cause) => setError(String(cause)));
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header
        doc={doc}
        running={Boolean(run && !run.summary && run.runId)}
        stopOnFailure={stopOnFailure}
        setStopOnFailure={setStopOnFailure}
        onRun={() => void start(path, stopOnFailure)}
        onCancel={() => void cancel(path)}
      />

      {/* A container, so the split below reacts to the *pane's* width. */}
      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value as Tab)}
        className="@container min-h-0 flex-1"
      >
        <TabsList className="h-8 shrink-0 gap-4 border-b border-border px-4">
          <FolderTab value="overview">Overview</FolderTab>
          <FolderTab value="auth">Auth</FolderTab>
          <FolderTab value="variables" count={(doc.variables?.length ?? 0) + (doc.session?.length ?? 0)}>
            Variables
          </FolderTab>
          <FolderTab value="scripts" count={doc.scripts?.length ?? 0}>
            Scripts
          </FolderTab>
          <FolderTab value="headers" count={doc.headers?.length ?? 0}>
            Headers
          </FolderTab>
        </TabsList>

        {/* DESIGN-NOTES §4.1's `1fr 440px`, but conditioned on a container
            query rather than a viewport one. This lives inside a resizable
            pane, so a `xl:` breakpoint asked the wrong question: on a 1512px
            window it split into two columns while the centre pane was 735px
            wide, which left the left column ~275px and clipped the auth
            arguments field and the variables table. 800px is the point at
            which 440 for the panels still leaves the editor a usable 360.
            Below it the two stack, panels under the editor. */}
        <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden @min-[800px]:grid-cols-[1fr_440px]">
          <div className="min-h-0 overflow-auto border-border px-4 py-3 @min-[800px]:border-r">
            {tab === "overview" ? (
              <ReadmePanel doc={doc} path={path} env={env} onSaved={setDoc} />
            ) : tab === "scripts" ? (
              // Scripts are files beside the folder, not lines in
              // _folder.http, so there is nothing here to edit — the panel
              // names them and the editor for one arrives with the script
              // engine's own screen.
              <ScriptsPanel doc={doc} onAdd={() => setCreatingScript(path)} />
            ) : (
              // §9.15 gives the left column on these tabs to "that one panel
              // again at full width". It is the editor instead: at full width
              // there is room to change the thing, and the panel opposite is
              // still there for the glance.
              <FolderSettingsEditor
                doc={doc}
                path={path}
                env={env}
                section={tab}
                index={index}
                onSaved={setDoc}
              />
            )}
          </div>

          <aside className="min-h-0 overflow-auto">
            {/* The run is not part of the glance and never duplicates the
                column beside it, so it shows whatever the width. */}
            {run ? <RunPanel run={run} /> : null}
            {/* The four panels are the glance *beside* the editor. Below the
                split there is nothing to be beside: they stack under it, and
                what had been a glance becomes a second, read-only copy of the
                thing being edited — two "Variables" headings, one of them
                inert. So off the two-column layout they belong to Overview,
                where the left column is the README and they are the only
                statement of what the folder does. */}
            <div className={cn(tab !== "overview" && "hidden @min-[800px]:block")}>
              <AuthPanel doc={doc} index={index} onEdit={() => setTab("auth")} />
              <VariablesPanel
                doc={doc}
                index={index}
                onClearSession={clearSession}
                onAdd={() => setTab("variables")}
              />
              <ScriptsPanel doc={doc} onAdd={() => setCreatingScript(path)} />
              <HeadersPanel doc={doc} index={index} />
            </div>
          </aside>
        </div>
      </Tabs>

      <CreateScriptDialog
        folder={creatingScript}
        requests={tree ? allRequests(tree.root) : []}
        onClose={() => setCreatingScript(null)}
        onCreated={(nodePath) => {
          if (!nodePath) return;
          openTab(nodePath, "script", { activate: true });
          void navigate(nodeLink("script", nodePath));
        }}
      />
    </div>
  );
}

/**
 * `orders/` at 15px mono, the counts, the subtitle that says what the settings
 * do, and Run folder.
 *
 * The counts are two different questions and say which they answer, which is
 * DESIGN-NOTES §9.6 resolved: what the folder *contains* is direct, what its
 * settings *reach* is recursive.
 */
function Header({
  doc,
  running,
  stopOnFailure,
  setStopOnFailure,
  onRun,
  onCancel,
}: {
  doc: FolderDocument;
  running: boolean;
  stopOnFailure: boolean;
  setStopOnFailure: (on: boolean) => void;
  onRun: () => void;
  onCancel: () => void;
}) {
  const { requests, subfolders, below } = doc.counts;
  return (
    <div className="shrink-0 border-b border-border px-4 py-3">
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-title text-fg-emphasis">
          {doc.path ? `${doc.name}/` : doc.name}
        </span>
        <span className="shrink-0 text-meta text-fg-dim">
          {requests} {requests === 1 ? "request" : "requests"}
          {subfolders > 0 ? ` · ${subfolders} ${subfolders === 1 ? "subfolder" : "subfolders"}` : ""}
          {below !== requests ? ` · ${below} below in all` : ""}
        </span>

        <div className="flex-1" />

        <label className="flex shrink-0 items-center gap-1.5 text-meta text-fg-dim">
          <Checkbox
            checked={stopOnFailure}
            onCheckedChange={(value) => setStopOnFailure(value === true)}
            aria-label="Stop on the first failure"
          />
          stop on failure
        </label>

        {running ? (
          <Button
            type="button"
            onClick={onCancel}
            className="h-[26px] shrink-0 gap-1.5 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
          >
            <X className="size-3" />
            Stop
          </Button>
        ) : (
          <Button
            type="button"
            disabled={below === 0}
            onClick={onRun}
            className="h-[26px] shrink-0 gap-1.5 rounded-md bg-primary px-2.5 text-ui font-semibold text-primary-foreground hover:bg-primary-hover disabled:opacity-40"
          >
            <Play className="size-3" />
            Run folder
            <span className="font-mono text-label opacity-70">{hint("⇧↵")}</span>
          </Button>
        )}
      </div>

      <p className="mt-1 text-meta text-fg-dim">
        {doc.settingsError ? (
          <span className="text-destructive">
            {doc.settingsPath}: {doc.settingsError}
          </span>
        ) : doc.settingsPath ? (
          <>
            Settings in <span className="font-mono text-fg-faint">{doc.settingsPath}</span> ·
            inherited by every request below, overridable per request
          </>
        ) : (
          <>
            No <span className="font-mono text-fg-faint">_folder.http</span> here, so this folder
            adds nothing of its own. Anything below still inherits from the folders above it.
          </>
        )}
      </p>
    </div>
  );
}

/** A folder tab, with the count the design puts beside it. */
function FolderTab({
  value,
  count,
  children,
}: {
  value: string;
  count?: number;
  children: React.ReactNode;
}) {
  return (
    <TabsTrigger
      value={value}
      className="h-8 gap-1.5 rounded-none border-0 border-b border-transparent px-0 text-ui text-fg-muted data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-fg-emphasis data-[state=active]:shadow-none"
    >
      {children}
      {count !== undefined && count > 0 ? (
        <span className="font-mono text-label text-fg-dim">{count}</span>
      ) : null}
    </TabsTrigger>
  );
}

/**
 * The README with a Preview/Edit toggle (screen 3a). There is no Docs tab:
 * the documentation is the overview.
 */
function ReadmePanel({
  doc,
  path,
  env,
  onSaved,
}: {
  doc: FolderDocument;
  path: string;
  env: string;
  onSaved: (doc: FolderDocument) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(doc.readme ?? "");
  const [error, setError] = useState<string | null>(null);

  // Follow the document when it is re-read, unless there is an unsaved edit.
  useEffect(() => {
    if (!editing) setDraft(doc.readme ?? "");
  }, [doc.readme, editing]);

  const dirty = editing && draft !== (doc.readme ?? "");

  async function save() {
    try {
      const next = await FolderService.SaveReadme(path, env, draft);
      if (next) onSaved(next);
      setEditing(false);
      setError(null);
    } catch (cause) {
      setError(String(cause));
    }
  }

  return (
    <div className="min-w-0">
      <div className="mb-3 flex items-baseline gap-2">
        <h2 className="shrink-0 text-ui text-fg-emphasis">Documentation</h2>
        <span className="min-w-0 flex-1 truncate font-mono text-meta text-fg-dim">
          {doc.readmePath ?? `${doc.path ? doc.path + "/" : ""}README.md`}
          {dirty ? " ·  unsaved" : ""}
        </span>
        <ToggleGroup
          type="single"
          value={editing ? "edit" : "preview"}
          onValueChange={(value) => value && setEditing(value === "edit")}
          className="shrink-0"
        >
          <ToggleGroupItem value="preview" className="h-6 px-2 text-ui">
            Preview
          </ToggleGroupItem>
          <ToggleGroupItem value="edit" className="h-6 px-2 text-ui">
            Edit
          </ToggleGroupItem>
        </ToggleGroup>
        {editing ? (
          <Button
            type="button"
            disabled={!dirty}
            onClick={() => void save()}
            className="h-6 shrink-0 rounded-md border border-border-control bg-control px-2 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            Save
          </Button>
        ) : null}
      </div>

      {error ? <p className="mb-2 text-meta text-destructive">{error}</p> : null}

      {editing ? (
        <div className="rounded-sm border border-border-control">
          <CodeEditor value={draft} onChange={setDraft} mode="text" />
        </div>
      ) : doc.readme ? (
        <MarkdownView source={doc.readme} />
      ) : (
        <p className="text-meta text-fg-dim">
          No <span className="font-mono text-fg-faint">README.md</span> here yet. Switch to Edit to
          write one — it is committed with the collection, so it is the folder's documentation for
          everyone on the branch.
        </p>
      )}
    </div>
  );
}

/**
 * The run, live. Every row is drawn as soon as the plan arrives and filled in
 * as each request finishes, so how far through the run is is legible from the
 * first frame.
 */
function RunPanel({ run }: { run: Run }) {
  const done = run.rows.filter((row) => row.result).length;
  const passed = run.rows.filter((row) => row.result?.passed).length;
  const summary = run.summary;

  return (
    <Panel
      title="Run"
      subtitle={
        run.error
          ? undefined
          : summary
            ? `${summary.passed}/${summary.total} passed · ${Math.round(summary.durationMs)} ms${
                summary.state === "stopped"
                  ? ` · stopped, ${summary.skipped} skipped`
                  : summary.state === "cancelled"
                    ? " · cancelled"
                    : ""
              }`
            : `${done}/${run.rows.length} · ${passed} passed`
      }
    >
      {run.error ? (
        <p className="text-meta text-destructive">{run.error}</p>
      ) : (
        run.rows.map((row, at) => (
          <div key={`${row.path}-${at}`} className="flex items-baseline gap-2 py-0.5">
            <span className="w-4 shrink-0 text-center">
              {!row.result ? (
                <CircleDashed className="size-3 text-fg-ghost" />
              ) : (
                <span className={row.result.passed ? "text-primary" : "text-destructive"}>
                  {row.result.passed ? "✓" : "✕"}
                </span>
              )}
            </span>
            <span
              className={cn(
                "min-w-0 flex-1 truncate font-mono text-ui",
                row.result ? "text-fg-secondary" : "text-fg-faint",
              )}
              title={row.path}
            >
              {row.result?.name || row.path}
            </span>
            {row.result ? (
              <>
                <span
                  className={cn(
                    "shrink-0 font-mono text-meta",
                    row.result.passed ? "text-primary" : "text-destructive",
                  )}
                  title={row.result.message}
                >
                  {row.result.statusCode || "—"}
                </span>
                <span className="shrink-0 font-mono text-meta text-fg-faint">
                  {Math.round(row.result.durationMs)} ms
                </span>
              </>
            ) : null}
          </div>
        ))
      )}
    </Panel>
  );
}
