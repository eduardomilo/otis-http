import { useEffect, useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight } from "lucide-react";

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { formatBytes, formatCount } from "@/lib/format";
import { TOKEN_CLASS, tokenizeJsonLine, tokenizeXmlLine } from "@/lib/json-tokens";
import { cn } from "@/lib/utils";
import type { ResponseBody } from "@/hooks/use-response-body";
import { ViewKind } from "@bindings/internal/response";
import type { BodyInfo } from "@bindings/internal/services";

/**
 * The response body (screen 1a): line numbers in a 40px gutter, a 14px fold
 * column with `▾`/`▸`, a collapsed node showing an item count, and a
 * Pretty/Raw segmented control.
 *
 * A virtualized list rather than an editor. The lines come from Go a viewport
 * at a time (see use-response-body), so there is no document here to give an
 * editor — which is the whole reason a 40 MB response scrolls at all.
 *
 * Metrics: 20px line height and a 40px gutter with 8px of right padding
 * (DESIGN-NOTES §4.3, §4.6).
 */

const LINE_HEIGHT = 20;

export function BodyView({ body, info }: { body: ResponseBody; info: BodyInfo }) {
  const scroller = useRef<HTMLDivElement>(null);

  const virtualizer = useVirtualizer({
    count: body.rows,
    getScrollElement: () => scroller.current,
    estimateSize: () => LINE_HEIGHT,
    overscan: 20,
  });

  const items = virtualizer.getVirtualItems();

  // Ask Go for what is about to be drawn. In an effect rather than during
  // render because it sets state when the lines arrive.
  useEffect(() => {
    if (items.length === 0) return;
    body.request(items[0].index, items[items.length - 1].index);
  }, [items, body]);

  if (!info.utf8) {
    return (
      <Notice>
        {formatBytes(info.size)} of binary data. It is not text, so there is nothing to show —
        a response redirect (<Code>{">> ./file"}</Code>) is how to keep it.
      </Notice>
    );
  }
  if (info.size === 0) {
    return <Notice>No body.</Notice>;
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center justify-between px-4 py-1">
        <span className="font-mono text-label text-fg-faint">
          {body.info
            ? `${formatCount(body.info.lines)} line${body.info.lines === 1 ? "" : "s"}`
            : ""}
        </span>
        <ToggleGroup
          type="single"
          value={body.view}
          onValueChange={(next) => next && body.setView(next as typeof body.view)}
        >
          {/* Pretty is offered only when there is one; Go says so. */}
          <ToggleGroupItem value={ViewKind.Pretty} disabled={!info.hasPretty}>
            Pretty
          </ToggleGroupItem>
          <ToggleGroupItem value={ViewKind.Raw}>Raw</ToggleGroupItem>
        </ToggleGroup>
      </div>

      {body.loading ? (
        <Notice>Formatting {formatBytes(info.size)}…</Notice>
      ) : body.view === ViewKind.Pretty && body.info?.unavailable ? (
        <Notice tone="warning">{body.info.unavailable}</Notice>
      ) : (
        <div ref={scroller} className="min-h-0 flex-1 overflow-auto">
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {items.map((item) => (
              <Row
                key={item.key}
                row={item.index}
                top={item.start}
                body={body}
                kind={info.kind}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function Row({
  row,
  top,
  body,
  kind,
}: {
  row: number;
  top: number;
  body: ResponseBody;
  kind: BodyInfo["kind"];
}) {
  const line = body.line(row);
  const source = body.sourceLine(row);
  const fold = line?.fold;
  const collapsed = fold ? body.isCollapsed(fold) : false;

  return (
    <div
      className="absolute flex w-full items-start hover:bg-inset"
      style={{ top, height: LINE_HEIGHT }}
    >
      {/* §4.6: 40px line numbers with 8px right padding, in --fg-ghost. */}
      <span className="w-10 shrink-0 pr-2 text-right font-mono text-code text-fg-ghost select-none">
        {source + 1}
      </span>
      {/* §4.6: a 14px fold column. */}
      <span className="flex w-3.5 shrink-0 justify-center">
        {fold ? (
          <button
            type="button"
            aria-label={collapsed ? "Expand" : "Collapse"}
            onClick={() => body.toggle(fold)}
            className="text-fg-ghost hover:text-fg-muted"
          >
            {collapsed ? (
              <ChevronRight className="size-3" />
            ) : (
              <ChevronDown className="size-3" />
            )}
          </button>
        ) : null}
      </span>
      <div className="min-w-0 flex-1 pr-4 font-mono text-code whitespace-pre">
        {line === undefined ? (
          // A row whose line has not arrived yet. A skeleton rather than a
          // blank, so a fast scroll does not look like an empty document.
          <span className="inline-block h-3 w-40 translate-y-1 rounded-sm bg-inset" />
        ) : (
          <>
            <Tokens text={line.text} kind={kind} />
            {collapsed && fold ? (
              <span className="ml-2 rounded-sm border border-border-control bg-control px-1 font-sans text-label text-fg-dim">
                {fold.count} {fold.object ? (fold.count === 1 ? "key" : "keys") : fold.count === 1 ? "item" : "items"}
              </span>
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}

/** One line, coloured. §2.7 for JSON; XML borrows the same roles. */
function Tokens({ text, kind }: { text: string; kind: BodyInfo["kind"] }) {
  if (kind === "text") return <span className="text-fg-secondary">{text}</span>;
  const tokens = kind === "xml" ? tokenizeXmlLine(text) : tokenizeJsonLine(text);
  return (
    <>
      {tokens.map((token, i) => (
        <span key={i} className={TOKEN_CLASS[token.role]}>
          {token.text}
        </span>
      ))}
    </>
  );
}

function Notice({ children, tone }: { children: React.ReactNode; tone?: "warning" }) {
  return (
    <p
      className={cn(
        "px-4 py-3 text-meta",
        tone === "warning" ? "text-warning" : "text-fg-faint",
      )}
    >
      {children}
    </p>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return <code className="rounded-sm bg-control px-1 font-mono text-fg-secondary">{children}</code>;
}
