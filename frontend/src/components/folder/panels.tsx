import { Link } from "@tanstack/react-router";
import { Clock, FileText, Lock } from "lucide-react";

import { VariableText } from "@/components/request/variable-text";
import { Button } from "@/components/ui/button";
import { nodeLink } from "@/lib/paths";
import { cn } from "@/lib/utils";
import { relativeTime } from "@/lib/time";
import type { VariableIndex } from "@/lib/variables";
import type { FolderDocument, FolderOverride } from "@bindings/internal/services";

/**
 * The four panels of screen 3a's right column: Auth, Variables, Scripts and
 * Headers, each 12px/16px padded with a border between them (DESIGN-NOTES
 * §4.1).
 *
 * Every panel names where its values come from, which is §8.1's rule and the
 * whole argument of the folder view: a setting inherited by six requests that
 * does not say which file it lives in is a setting nobody can find.
 */

/** A panel: a heading, a subtitle, an action, and its rows. */
export function Panel({
  title,
  subtitle,
  action,
  children,
}: {
  title: string;
  subtitle?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="border-b border-border px-4 py-3">
      <div className="mb-2 flex items-baseline gap-2">
        <h2 className="shrink-0 text-ui text-fg-emphasis">{title}</h2>
        {subtitle ? (
          <span className="min-w-0 flex-1 truncate text-meta text-fg-dim">{subtitle}</span>
        ) : (
          <span className="flex-1" />
        )}
        {action}
      </div>
      {children}
    </section>
  );
}

/** A two-column detail row, `80px 1fr` for auth and `120px 1fr` for a variable. */
function Row({
  label,
  children,
  wide,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div
      className={cn(
        "grid items-baseline gap-3 py-1",
        wide ? "grid-cols-[120px_1fr]" : "grid-cols-[80px_1fr]",
      )}
    >
      <span className="text-meta text-fg-dim">{label}</span>
      <div className="min-w-0 text-ui text-fg-secondary">{children}</div>
    </div>
  );
}

/**
 * The Auth panel. Screen 3a's Overrides row is the honest half of an
 * inheritance model: "inherited by 6 requests" without naming the one that
 * opted out is telling you something untrue about that one.
 */
export function AuthPanel({
  doc,
  index,
  onEdit,
}: {
  doc: FolderDocument;
  index: VariableIndex;
  onEdit?: () => void;
}) {
  const auth = doc.auth;
  const overrides = (doc.overrides ?? []).filter((o) => o.what === "auth");
  const inheriting = doc.counts.below - overrides.length;

  return (
    <Panel
      title="Auth"
      subtitle={
        auth && auth.kind
          ? `inherited by ${inheriting} ${inheriting === 1 ? "request" : "requests"}`
          : undefined
      }
      action={onEdit ? <PanelAction onClick={onEdit}>Edit</PanelAction> : undefined}
    >
      {auth?.error ? (
        <p className="text-meta text-destructive">{auth.error}</p>
      ) : !auth || !auth.kind ? (
        <p className="text-meta text-fg-dim">
          No level declares any auth, so requests below go out unauthenticated. That is different
          from <span className="font-mono text-fg-faint">@auth none</span>, which is an explicit
          opt-out.
        </p>
      ) : (
        <>
          <Row label="Type">{auth.summary}</Row>
          {auth.token ? (
            <Row label="Token">
              <span className="flex min-w-0 items-center gap-2">
                <VariableText text={auth.token} index={index} className="truncate" />
                {auth.secret ? (
                  <span className="flex shrink-0 items-center gap-1 text-meta text-secret">
                    <Lock className="size-3" />
                    keychain
                  </span>
                ) : null}
              </span>
            </Row>
          ) : null}
          {auth.username ? <Row label="User">{auth.username}</Row> : null}
          {auth.profile ? <Row label="Profile">{auth.profile}</Row> : null}
          {auth.region ? <Row label="Region">{auth.region}</Row> : null}
          {auth.sends ? (
            <Row label="Sends">
              <span className="font-mono text-fg-dim">{auth.sends}</span>
            </Row>
          ) : null}
          <Row label="Declared in">
            <SourceLabel path={auth.source.path} line={auth.source.line} local={auth.local} />
          </Row>
          <Row label="Overrides">
            <Overrides overrides={overrides} />
          </Row>
        </>
      )}
    </Panel>
  );
}

/** The Headers panel: what every request below starts with. */
export function HeadersPanel({ doc, index }: { doc: FolderDocument; index: VariableIndex }) {
  const headers = doc.headers ?? [];
  return (
    <Panel
      title="Headers"
      subtitle={
        headers.length === 0
          ? undefined
          : `added to every request below · ${doc.counts.below} ${doc.counts.below === 1 ? "request" : "requests"}`
      }
    >
      {headers.length === 0 ? (
        <p className="text-meta text-fg-dim">
          No folder above here adds a header, so requests send only their own.
        </p>
      ) : (
        headers.map((header, at) => {
          const overrides = (doc.overrides ?? []).filter(
            (o) => o.what.toLowerCase() === header.name.toLowerCase(),
          );
          return (
            <div key={`${header.name}-${at}`} className="grid grid-cols-[150px_1fr] gap-3 py-1">
              <span className="truncate font-mono text-ui text-fg" title={header.name}>
                {header.name}
              </span>
              <div className="min-w-0">
                <VariableText
                  text={header.value}
                  index={index}
                  className="block truncate text-ui text-fg-secondary"
                />
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <SourceLabel
                    path={header.source.path}
                    line={header.source.line}
                    local={header.local}
                  />
                  {overrides.length > 0 ? (
                    <span className="text-meta text-modified">
                      {overrides.length} {overrides.length === 1 ? "override" : "overrides"}
                    </span>
                  ) : null}
                </div>
              </div>
            </div>
          );
        })
      )}
    </Panel>
  );
}

/**
 * The Variables panel — the one place in this view where the separation is
 * load-bearing rather than cosmetic.
 *
 * Two groups, with a visible boundary between them. **Committed** is in
 * `_folder.http`, in git, and shared with everyone on the branch. **Session**
 * is in memory on this machine, set by a run, and written nowhere — not to the
 * collection, not to the settings file, not to a log (docs/FORMAT.md §4.5).
 * They are values of different kinds with different lifetimes and different
 * audiences, and a reader who mistakes one for the other either commits a
 * scratch value or expects a teammate to have one they cannot see. The dashed
 * box is §2.2's "this is a different kind of thing from what is above it".
 */
export function VariablesPanel({
  doc,
  index,
  onClearSession,
  onAdd,
}: {
  doc: FolderDocument;
  index: VariableIndex;
  onClearSession?: () => void;
  onAdd?: () => void;
}) {
  const committed = doc.variables ?? [];
  const session = doc.session ?? [];

  return (
    <Panel
      title="Variables"
      subtitle="folder scope · beats the environment"
      action={onAdd ? <PanelAction onClick={onAdd}>Add</PanelAction> : undefined}
    >
      <div className="mb-1 flex items-center gap-1.5 text-meta text-fg-dim">
        <FileText className="size-3 shrink-0 text-fg-faint" />
        <span className="truncate">
          Committed
          {doc.settingsPath ? (
            <>
              {" · "}
              <span className="font-mono text-fg-faint">{doc.settingsPath}</span>
            </>
          ) : null}
          {" · shared with everyone on the branch"}
        </span>
      </div>

      {committed.length === 0 ? (
        <p className="py-1 text-meta text-fg-dim">None declared here or above.</p>
      ) : (
        committed.map((variable) => (
          <div key={variable.name} className="grid grid-cols-[120px_1fr] gap-3 py-0.5">
            <span className="truncate font-mono text-ui text-fg" title={variable.name}>
              {variable.name}
            </span>
            <div className="min-w-0">
              <VariableText
                text={variable.value}
                index={index}
                className="block truncate text-ui text-fg-secondary"
              />
              <SourceLabel
                path={variable.source.path}
                line={variable.source.line}
                local={variable.local}
              />
            </div>
          </div>
        ))
      )}

      {/* §2.2's dashed box: a different kind of thing from what is above it. */}
      <div className="mt-3 rounded-md border border-dashed border-border-control bg-raised px-3 py-2">
        <div className="mb-1 flex items-center gap-2">
          <Clock className="size-3 shrink-0 text-fg-faint" />
          <span className="min-w-0 flex-1 truncate text-meta text-fg-dim">
            Session · set by scripts · this machine only
          </span>
          {onClearSession ? (
            <Button
              type="button"
              disabled={session.length === 0}
              onClick={onClearSession}
              className="h-5 shrink-0 rounded-sm border border-border-control bg-transparent px-1.5 text-meta text-fg-muted hover:bg-control hover:text-fg-emphasis disabled:opacity-40"
            >
              Clear
            </Button>
          ) : null}
        </div>

        {session.length === 0 ? (
          <p className="py-0.5 text-meta text-fg-faint">
            Nothing yet. A post-response script sets one with{" "}
            <span className="font-mono">vars.folder.set(k, v)</span>.
          </p>
        ) : (
          session.map((value) => (
            <div
              key={value.name}
              className="grid grid-cols-[110px_1fr_auto] items-baseline gap-2 py-0.5"
            >
              <span className="truncate font-mono text-ui text-fg" title={value.name}>
                {value.name}
              </span>
              <span className="min-w-0 truncate font-mono text-ui text-fg-secondary">
                {value.value}
              </span>
              {/* Provenance is the whole account of a value that is in no
                  file, so the row shows both halves of it (§4.5). */}
              <span className="shrink-0 text-meta text-fg-faint">
                {value.origin ? `${value.origin} · ` : ""}
                {relativeTime(value.at)}
              </span>
            </div>
          ))
        )}

        <p className="mt-1.5 text-meta text-fg-faint">
          Not written to disk, not committed, not shared. Cleared on Clear or when the collection
          closes.
        </p>
      </div>
    </Panel>
  );
}

/**
 * The Scripts panel. A hook runs automatically; a module runs only when a hook
 * imports it — and the panel says which each one is rather than expecting the
 * reader to know that `_pre.js` is special and `idempotency.js` is not.
 */
export function ScriptsPanel({ doc }: { doc: FolderDocument }) {
  const scripts = doc.scripts ?? [];
  const hooks = scripts.filter((s) => s.hook);
  const modules = scripts.filter((s) => !s.hook);

  return (
    <Panel title="Scripts" subtitle="folder hooks · run around every request here">
      <p className="mb-2 text-meta text-fg-dim">
        <span className="font-mono text-fg-faint">_pre.js</span> and{" "}
        <span className="font-mono text-fg-faint">_post.js</span> in a folder run automatically.
        Any other <span className="font-mono text-fg-faint">.js</span> is a plain ES module:
        nothing runs it unless a hook imports it.
      </p>

      {hooks.length === 0 && modules.length === 0 ? (
        <p className="text-meta text-fg-dim">No scripts in this folder.</p>
      ) : null}

      {hooks.map((script) => (
        <div key={script.path} className="mb-3">
          <div className="mb-1 flex items-baseline gap-2">
            <span className="w-[92px] shrink-0 text-meta text-fg-dim">
              {script.hook === "pre" ? "pre-request" : "post-response"}
            </span>
            <Link
              {...nodeLink("script", script.path)}
              className="min-w-0 flex-1 truncate font-mono text-ui text-fg-emphasis hover:underline"
            >
              {script.path}
            </Link>
            <span className="shrink-0 text-meta text-fg-faint">
              {script.lines} {script.lines === 1 ? "line" : "lines"}
            </span>
          </div>
          {script.error ? (
            <p className="text-meta text-destructive">{script.error}</p>
          ) : (
            <pre className="overflow-x-auto rounded-sm border border-border-control bg-inset p-2.5 font-mono text-code leading-5 text-fg-secondary">
              {script.source?.replace(/\n+$/, "")}
            </pre>
          )}
        </div>
      ))}

      {modules.length > 0 ? (
        <div className="mt-2 border-t border-dashed border-border-control pt-2">
          <p className="mb-1 text-label tracking-[.06em] text-fg-dim uppercase">
            Modules · imported, never run on their own
          </p>
          {modules.map((script) => (
            <div key={script.path} className="flex items-baseline gap-2 py-0.5">
              <Link
                {...nodeLink("script", script.path)}
                className="min-w-0 flex-1 truncate font-mono text-ui text-fg-secondary hover:underline"
              >
                {script.path}
              </Link>
              <span className="shrink-0 text-meta text-fg-faint">
                {script.lines} {script.lines === 1 ? "line" : "lines"}
              </span>
            </div>
          ))}
        </div>
      ) : null}

      <p className="mt-2 text-meta text-fg-faint">
        A hook runs inside a JavaScript realm with no filesystem, no network and no timers
        (docs/FORMAT.md §9.3). Open one to read or edit it.
      </p>
    </Panel>
  );
}

/** `orders/_folder.http:3`, with "inherited" when it is not this folder's. */
function SourceLabel({
  path,
  line,
  local,
}: {
  path: string;
  line?: number;
  local: boolean;
}) {
  if (!path) return null;
  return (
    <span className="truncate text-label text-fg-faint">
      {local ? "" : "inherited · "}
      <span className="font-mono">
        {path}
        {line && line > 0 ? `:${line}` : ""}
      </span>
    </span>
  );
}

/** The Overrides row: which descendants do not take this setting. */
function Overrides({ overrides }: { overrides: FolderOverride[] }) {
  if (overrides.length === 0) {
    return <span className="text-meta text-fg-faint">None — every request below takes it.</span>;
  }
  return (
    <div className="flex flex-col gap-0.5">
      {overrides.map((override) => (
        <span key={`${override.path}-${override.what}`} className="truncate text-meta">
          <span className="font-mono text-fg-secondary">{override.path}</span>
          <span className="text-fg-dim"> {override.how}</span>
        </span>
      ))}
    </div>
  );
}

/** A panel's action: the design's quiet text button at the right of a heading. */
function PanelAction({
  children,
  onClick,
}: {
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="shrink-0 text-meta text-fg-muted hover:text-fg-emphasis"
    >
      {children}
    </button>
  );
}
