import { useEffect, useState } from "react";
import { Loader2, Lock } from "lucide-react";

import { BodyView } from "@/components/response/body-view";
import { FailureView } from "@/components/response/failure-view";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useResponseBody } from "@/hooks/use-response-body";
import { formatBytes, formatClock, formatDuration, statusColor } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useSends } from "@/state/send-context";
import { useTabs } from "@/state/tabs-context";
import type { Cookie, ResponseMeta, SentHeader } from "@bindings/internal/services";

/**
 * The response pane (screen 1a): a 34px header row with the status, duration,
 * size and clock, then Body / Headers / Cookies / Tests.
 *
 * It follows the active tab rather than taking a path, because that is what
 * the design shows: one response pane, showing whatever the centre pane is
 * showing. Each request keeps its own response, so switching tabs and coming
 * back shows what came back, not an empty pane.
 */

type Panel = "body" | "headers" | "cookies" | "tests";

export function ResponsePane() {
  const { activePath } = useTabs();
  const { get, cancel } = useSends();
  const send = activePath ? get(activePath) : undefined;
  const [panel, setPanel] = useState<Panel>("body");

  const meta = send?.meta ?? null;
  const body = useResponseBody(meta?.sendId ?? null, meta?.body.hasPretty ?? false);

  if (!send) {
    return (
      <Frame>
        <p className="px-4 py-3 text-meta text-fg-faint">No response.</p>
      </Frame>
    );
  }

  if (send.phase === "in-flight") {
    return (
      <Frame header={<InFlightHeader startedAt={send.startedAt} />}>
        <InFlight send={send} onCancel={() => void cancel(send.path)} />
      </Frame>
    );
  }

  if (send.failure) {
    return (
      <Frame header={<FailureHeader kind={send.failure.kind} ms={send.failure.durationMs} />}>
        <FailureView failure={send.failure} />
      </Frame>
    );
  }

  if (!meta) {
    return (
      <Frame>
        <p className="px-4 py-3 text-meta text-fg-faint">No response.</p>
      </Frame>
    );
  }

  const cookies = meta.cookies ?? [];
  const headers = meta.headers ?? [];

  return (
    <Frame header={<StatusRow meta={meta} />}>
      <Tabs
        value={panel}
        onValueChange={(value) => setPanel(value as Panel)}
        className="min-h-0 flex-1"
      >
        <div className="flex shrink-0 items-center justify-between border-b border-border px-4">
          <TabsList>
            <TabsTrigger value="body">Body</TabsTrigger>
            <TabsTrigger value="headers">
              Headers <Count value={headers.length} />
            </TabsTrigger>
            <TabsTrigger value="cookies">
              Cookies <Count value={cookies.length} />
            </TabsTrigger>
            {/* Tests arrive with the script engine in Phase D. The tab is
                here because the strip's layout is final. */}
            <TabsTrigger value="tests" disabled title="Tests run in Phase D">
              Tests
            </TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="body" className="flex min-h-0 flex-col">
          <BodyView body={body} info={meta.body} />
        </TabsContent>
        <TabsContent value="headers" className="flex min-h-0 flex-col">
          <HeaderTable headers={headers} meta={meta} />
        </TabsContent>
        <TabsContent value="cookies" className="flex min-h-0 flex-col">
          <CookieTable cookies={cookies} />
        </TabsContent>
        <TabsContent value="tests" className="flex min-h-0 flex-col">
          <p className="px-4 py-3 text-meta text-fg-faint">
            Tests run in Phase D, from a <code className="font-mono">{"> {% %}"}</code> block.
          </p>
        </TabsContent>
      </Tabs>
    </Frame>
  );
}

/** The pane's frame: a 34px header bar and the content below it (§4.1). */
function Frame({ header, children }: { header?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex h-[var(--tab-bar-height)] shrink-0 items-center gap-3 border-b border-border px-4">
        {header}
      </div>
      <div className="flex min-h-0 flex-1 flex-col">{children}</div>
    </div>
  );
}

/**
 * Status, duration, size, and the clock on the right (screen 1a). The status
 * takes its colour from the class, with a dot beside it in the same colour.
 */
function StatusRow({ meta }: { meta: ResponseMeta }) {
  const color = statusColor(meta.statusCode);
  return (
    <>
      <span className={cn("size-1.5 shrink-0 rounded-full", color.replace("text-", "bg-"))} />
      <span className={cn("shrink-0 font-mono text-ui font-medium", color)}>{meta.status}</span>
      <span className="shrink-0 font-mono text-ui text-fg-muted">
        {formatDuration(meta.durationMs)}
      </span>
      <span className="shrink-0 font-mono text-ui text-fg-muted">{formatBytes(meta.size)}</span>
      {meta.redirects && meta.redirects.length > 0 ? (
        <span
          className="shrink-0 font-mono text-label text-warning"
          title={meta.redirects.map((r) => `${r.statusCode} → ${r.location}`).join("\n")}
        >
          {meta.redirects.length} hop{meta.redirects.length === 1 ? "" : "s"}
        </span>
      ) : null}
      <span className="ml-auto shrink-0 font-mono text-ui text-fg-faint">
        {formatClock(meta.at)}
      </span>
    </>
  );
}

function InFlightHeader({ startedAt }: { startedAt: number }) {
  return (
    <>
      <Loader2 className="size-3 shrink-0 animate-spin text-fg-dim" />
      <span className="shrink-0 font-mono text-ui text-fg-muted">Sending</span>
      <Elapsed startedAt={startedAt} className="shrink-0 font-mono text-ui text-fg-dim" />
    </>
  );
}

function FailureHeader({ kind, ms }: { kind: string; ms: number }) {
  const cancelled = kind === "cancelled";
  return (
    <>
      <span
        className={cn("size-1.5 shrink-0 rounded-full", cancelled ? "bg-fg-dim" : "bg-destructive")}
      />
      <span
        className={cn(
          "shrink-0 font-mono text-ui font-medium",
          cancelled ? "text-fg-muted" : "text-destructive",
        )}
      >
        {cancelled ? "Cancelled" : "No response"}
      </span>
      <span className="shrink-0 font-mono text-ui text-fg-muted">{formatDuration(ms)}</span>
    </>
  );
}

/**
 * The in-flight state: what is being waited for, how long it has been, and
 * Cancel.
 */
function InFlight({
  send,
  onCancel,
}: {
  send: NonNullable<ReturnType<ReturnType<typeof useSends>["get"]>>;
  onCancel: () => void;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 px-4 py-4">
      {send.started ? (
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="shrink-0 font-mono text-label font-medium tracking-[.02em] text-fg-muted">
            {send.started.method}
          </span>
          {/* Masked: the URL can carry a secret in a query parameter. */}
          <span className="min-w-0 truncate font-mono text-ui text-fg-secondary">
            {send.started.url}
          </span>
        </div>
      ) : (
        <p className="text-meta text-fg-dim">Resolving variables…</p>
      )}
      <div className="flex items-center gap-2.5">
        <Elapsed startedAt={send.startedAt} className="font-mono text-display text-fg-emphasis" />
        <button
          type="button"
          onClick={onCancel}
          className="h-6 rounded-sm border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:text-fg-emphasis"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

/**
 * The elapsed timer.
 *
 * Its own component with its own interval, so ticking re-renders one span
 * rather than the pane. 100ms: fast enough to read as running, slow enough
 * that it is not what the app is spending its time on.
 */
function Elapsed({ startedAt, className }: { startedAt: number; className?: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 100);
    return () => clearInterval(timer);
  }, []);
  return <span className={className}>{formatDuration(Math.max(0, now - startedAt))}</span>;
}

/**
 * The response headers, and under them what Otis sent.
 *
 * The request side is here because it is the answer to "is that really what
 * went out" — the Headers tab in the request view is a prediction, and this is
 * the record. Secrets are masked; Go marks which values it masked.
 */
function HeaderTable({ headers, meta }: { headers: SentHeader[]; meta: ResponseMeta }) {
  const sent = meta.request.headers ?? [];
  return (
    <div className="min-h-0 flex-1 overflow-auto px-4 py-2">
      <Group label="Response">
        {headers.map((header, i) => (
          <Row key={`${header.name}-${i}`} name={header.name} value={header.value} />
        ))}
      </Group>
      <div className="mt-3 border-t border-dashed border-border-control pt-2">
        <Group label="Sent" note={`${sent.length} header${sent.length === 1 ? "" : "s"}`}>
          {sent.map((header, i) => (
            <Row
              key={`${header.name}-${i}`}
              name={header.name}
              value={header.value}
              secret={header.secret}
            />
          ))}
        </Group>
      </div>
      {meta.warnings && meta.warnings.length > 0 ? (
        <ul className="mt-3 space-y-1">
          {meta.warnings.map((warning, i) => (
            <li key={i} className="text-meta text-warning">
              {warning}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function CookieTable({ cookies }: { cookies: Cookie[] }) {
  if (cookies.length === 0) {
    return (
      <p className="px-4 py-3 text-meta text-fg-faint">
        No cookies were set. Otis keeps them in memory for the collection and never writes them to
        disk.
      </p>
    );
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto px-4 py-2">
      {cookies.map((cookie, i) => (
        <div key={`${cookie.name}-${i}`} className="border-b border-border-hairline py-1.5">
          <Row name={cookie.name} value={cookie.value} />
          <p className="mt-0.5 pl-[150px] font-mono text-label text-fg-faint">
            {[
              cookie.domain && `domain=${cookie.domain}`,
              cookie.path && `path=${cookie.path}`,
              cookie.expires && `expires=${cookie.expires}`,
              cookie.maxAge ? `max-age=${cookie.maxAge}` : null,
              cookie.secure && "Secure",
              cookie.httpOnly && "HttpOnly",
              cookie.sameSite && `SameSite=${cookie.sameSite}`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
        </div>
      ))}
    </div>
  );
}

/** §4.5's folder-headers geometry, `150px 1fr`. */
function Row({ name, value, secret }: { name: string; value: string; secret?: boolean }) {
  return (
    <div className="grid grid-cols-[150px_1fr] items-baseline gap-3 py-0.5">
      <span className="truncate font-mono text-ui text-fg" title={name}>
        {name}
      </span>
      <span className="flex min-w-0 items-baseline gap-1.5">
        <span className="min-w-0 font-mono text-ui break-all text-fg-secondary">{value}</span>
        {secret ? (
          <span
            className="flex shrink-0 items-center gap-0.5 text-label text-secret"
            title="A secret; the real value went on the wire and never reaches this window"
          >
            <Lock className="size-2.5" />
            secret
          </span>
        ) : null}
      </span>
    </div>
  );
}

function Group({
  label,
  note,
  children,
}: {
  label: string;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <section>
      <header className="flex items-baseline gap-2 pb-1">
        {/* §8.6: 10px uppercase, .06em tracking. */}
        <span className="text-label tracking-[.06em] text-fg-dim uppercase">{label}</span>
        {note ? <span className="font-mono text-label text-fg-faint">{note}</span> : null}
      </header>
      {children}
    </section>
  );
}

function Count({ value }: { value: number }) {
  if (value === 0) return null;
  return <span className="font-mono text-label font-medium text-fg-dim">{value}</span>;
}
