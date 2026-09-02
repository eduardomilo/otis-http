import { useMemo, useState } from "react";

import { modeForContentType, MODE_LABEL } from "@/components/editor/otis-theme";
import { AuthTab } from "@/components/request/auth-tab";
import { BodyTab } from "@/components/request/body-tab";
import { ConflictBanner } from "@/components/request/conflict-banner";
import { HeadersTab } from "@/components/request/headers-tab";
import { ParamsTab } from "@/components/request/params-tab";
import { ScriptsTab } from "@/components/request/scripts-tab";
import { UrlBar } from "@/components/request/url-bar";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AUTH_DIRECTIVE, directiveValue, entryOf, updateEntry } from "@/lib/http-file";
import { splitUrl } from "@/lib/query";
import { cn } from "@/lib/utils";
import { indexVariables } from "@/lib/variables";
import { useDocument, useDocuments } from "@/state/documents-context";
import { useSends } from "@/state/send-context";
import type { Request } from "@bindings/internal/httpfile";
import type { Document } from "@bindings/internal/services";

/**
 * The centre pane for `/r/$path` (screens 1a, 4a, 4b).
 *
 * URL bar, then the five sub-tabs, then whichever panel is showing. The
 * document — the file, its inheritance and its variable index — comes from the
 * documents provider; this component is about arranging it, and every edit is
 * one immutable transform of the draft.
 *
 * The right side of the sub-tab strip carries the tab-specific context screen
 * 1a and 4a put there: the effective content type on Body, the
 * "N sent · N local · N inherited" tally on Headers, the auth source on Auth.
 */

type PanelKey = "params" | "headers" | "body" | "auth" | "scripts";

export function RequestEditor({ path }: { path: string }) {
  const state = useDocument(path);
  const { edit, save, reload, keepMine } = useDocuments();
  const { send, get: getSend } = useSends();
  const [panel, setPanel] = useState<PanelKey>("body");
  const inFlight = getSend(path)?.phase === "in-flight";

  const document = state?.loaded;
  const entry = entryOf(state?.draft, document?.index ?? 0);

  // Go's slices arrive as null when empty, so the list is normalised once
  // here rather than at every use.
  const variables = useMemo(() => document?.variables ?? [], [document?.variables]);
  const index = useMemo(() => indexVariables(variables), [variables]);

  const onEdit = (fn: (entry: Request) => Request) =>
    edit(path, (file) => updateEntry(file, document?.index ?? 0, fn));

  if (!state || (state.busy && !document)) {
    return <Centered>Loading…</Centered>;
  }
  if (state.error && !document) {
    return <Centered tone="warning">{state.error}</Centered>;
  }
  if (document?.parseError) {
    return <ParseFailure document={document} />;
  }
  if (!document || !entry) {
    return <Centered>This file contains no request.</Centered>;
  }

  const url = entry.url ?? "";
  const contentType = effectiveContentType(document, entry);
  const mode = modeForContentType(contentType);
  const counts = document.counts;
  const paramCount = splitUrl(url).params.length;
  const scriptCount = (entry.preScripts?.length ?? 0) + (entry.postScripts?.length ?? 0);

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

      <UrlBar
        method={entry.method ?? "GET"}
        url={url}
        variables={variables}
        inFlight={inFlight}
        onMethodChange={(method) => onEdit((e) => ({ ...e, method }))}
        onUrlChange={(next) => onEdit((e) => ({ ...e, url: next }))}
        onSend={() => void send(path)}
        onSave={() => void save(path)}
      />

      <Tabs
        value={panel}
        onValueChange={(value) => setPanel(value as PanelKey)}
        className="min-h-0 flex-1 px-4"
      >
        <div className="flex shrink-0 items-center justify-between border-b border-border">
          <TabsList>
            <TabsTrigger value="params">
              Params <Count value={paramCount} />
            </TabsTrigger>
            <TabsTrigger value="headers">
              Headers <Count value={counts.sent} />
            </TabsTrigger>
            <TabsTrigger value="body">Body</TabsTrigger>
            <TabsTrigger value="auth">Auth</TabsTrigger>
            <TabsTrigger value="scripts">
              Scripts <Count value={scriptCount} />
            </TabsTrigger>
          </TabsList>
          <StripContext panel={panel} document={document} entry={entry} contentType={contentType} />
        </div>

        <TabsContent value="params" className="flex min-h-0 flex-col">
          <ParamsTab
            url={url}
            index={index}
            onUrlChange={(next) => onEdit((e) => ({ ...e, url: next }))}
          />
        </TabsContent>
        <TabsContent value="headers" className="flex min-h-0 flex-col">
          <HeadersTab document={document} entry={entry} index={index} onEdit={onEdit} />
        </TabsContent>
        <TabsContent value="body" className="flex min-h-0 flex-col">
          <BodyTab
            entry={entry}
            mode={mode}
            variables={variables}
            onEdit={onEdit}
            onSend={() => void send(path)}
            onSave={() => void save(path)}
          />
        </TabsContent>
        <TabsContent value="auth" className="flex min-h-0 flex-col">
          <AuthTab document={document} entry={entry} index={index} onEdit={onEdit} />
        </TabsContent>
        <TabsContent value="scripts" className="flex min-h-0 flex-col">
          <ScriptsTab entry={entry} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

/** §3: tab counts are mono 10px, weight 500. */
function Count({ value }: { value: number }) {
  if (value === 0) return null;
  return <span className="font-mono text-label font-medium text-fg-dim">{value}</span>;
}

/** The right side of the sub-tab strip, which says something different per tab. */
function StripContext({
  panel,
  document,
  entry,
  contentType,
}: {
  panel: PanelKey;
  document: Document;
  entry: Request;
  contentType: string | undefined;
}) {
  const shared = "shrink-0 pl-4 text-meta text-fg-dim";
  switch (panel) {
    case "headers": {
      const { sent, local, inherited } = document.counts;
      return (
        <span className={cn(shared, "font-mono")}>
          {sent} sent · {local} local · {inherited} inherited
        </span>
      );
    }
    case "body":
      return (
        <span className={cn(shared, "font-mono")}>
          {contentType ?? MODE_LABEL[modeForContentType(contentType)]}
        </span>
      );
    case "auth": {
      const own = directiveValue(entry, AUTH_DIRECTIVE);
      if (own === undefined) {
        return (
          <span className={shared}>
            {document.inheritedAuth ? (
              <>
                Inherited from{" "}
                <span className="font-mono">{document.inheritedAuth.source.path}</span>
              </>
            ) : (
              "No auth declared anywhere above this request"
            )}
          </span>
        );
      }
      return (
        <span className={shared}>
          {own.trim().toLowerCase() === "none" ? "No auth · this file" : "Overridden in this file"}
        </span>
      );
    }
    case "params":
    case "scripts":
      return <span />;
  }
}

/**
 * The effective `Content-Type`, which is what the body editor's mode follows.
 *
 * The *effective* one, not the file's own: a folder above may set it (§3.1),
 * and the header the server will see is the one the editor should believe. The
 * draft's own header wins over the loaded document's, so switching the type
 * changes the mode before the file is saved.
 */
function effectiveContentType(document: Document, entry: Request): string | undefined {
  const own = (entry.headers ?? []).find((h) => h.name.toLowerCase() === "content-type");
  if (own) return own.value;
  return document.effective?.headers?.find((h) => h.name.toLowerCase() === "content-type")?.value;
}

/** A file the parser rejected. The text is the only way to repair it. */
function ParseFailure({ document }: { document: Document }) {
  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-4">
      <div className="shrink-0 rounded-md border border-border-danger bg-destructive/5 px-3 py-2">
        <p className="text-ui text-destructive">{document.parseError}</p>
        <p className="mt-1 text-meta text-fg-dim">
          Otis could not parse this file, so there is nothing to edit as fields. The text is below,
          exactly as it is on disk.
        </p>
      </div>
      <pre className="min-h-0 flex-1 overflow-auto rounded-md border border-border-control bg-inset p-3 font-mono text-code text-fg-secondary">
        {document.raw}
      </pre>
    </div>
  );
}

function Centered({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: "warning";
}) {
  return (
    <div className="flex h-full items-center justify-center px-4">
      <p className={cn("text-meta", tone === "warning" ? "text-warning" : "text-fg-faint")}>
        {children}
      </p>
    </div>
  );
}
