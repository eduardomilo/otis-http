import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { SendService } from "@bindings/internal/services";
import type { BodyLine, BodyView } from "@bindings/internal/services";
// ViewKind is a generated enum, not a union of strings, so it is imported
// as a value: the two members are what the Pretty/Raw toggle switches between.
import { ViewKind } from "@bindings/internal/response";
import type { Fold } from "@bindings/internal/response";

/**
 * The response body, a screenful at a time.
 *
 * The body stays in Go (see internal/response for why). This asks for the
 * lines the virtualizer is about to draw, caches them, and keeps the mapping
 * between what is on screen and what is in the document — which differ as soon
 * as a node is collapsed.
 *
 * Nothing here holds the whole body, at any size. The cache is bounded, so
 * scrolling through 40 MB costs a constant amount of memory in the window.
 */

/** How many lines to fetch either side of the visible window. */
const OVERSCAN = 120;

/** How many lines to keep cached. Well over any viewport, far under a body. */
const CACHE_LIMIT = 3000;

/** A collapsed node: source lines (from, to] are hidden. */
export interface Collapsed {
  from: number;
  to: number;
}

export interface ResponseBody {
  /** The rendering being shown. */
  view: ViewKind;
  setView: (view: ViewKind) => void;
  /** What Go says about this rendering, or null while it is being built. */
  info: BodyView | null;
  /** True while the first View call for a rendering is outstanding. */
  loading: boolean;
  /** How many rows the list has, with collapsed nodes taken out. */
  rows: number;
  /** The line at a row, or undefined until it has been fetched. */
  line: (row: number) => BodyLine | undefined;
  /** The document line a row shows, for the line-number gutter. */
  sourceLine: (row: number) => number;
  /** Asks for the lines a row range needs. Safe to call every render. */
  request: (firstRow: number, lastRow: number) => void;
  /** Whether a fold is collapsed. */
  isCollapsed: (fold: Fold) => boolean;
  /** Collapses or expands a fold. */
  toggle: (fold: Fold) => void;
  /** Expands everything. */
  expandAll: () => void;
}

export function useResponseBody(
  sendId: string | null,
  preferPretty: boolean,
): ResponseBody {
  const [view, setViewState] = useState<ViewKind>(
    preferPretty ? ViewKind.Pretty : ViewKind.Raw,
  );
  // A new response chooses its own default rendering, because the pane is
  // mounted before there is a response to ask about: `preferPretty` is false
  // on the first render whatever the body turns out to be, and useState only
  // reads its initial value once. Keyed on the send ID so that switching to
  // Raw and then sending again starts from Pretty rather than remembering a
  // choice made about a different response.
  const lastSend = useRef<string | null>(null);
  if (sendId !== lastSend.current) {
    lastSend.current = sendId;
    const wanted = preferPretty ? ViewKind.Pretty : ViewKind.Raw;
    if (wanted !== view) setViewState(wanted);
  }
  const [info, setInfo] = useState<BodyView | null>(null);
  const [loading, setLoading] = useState(false);
  const [collapsed, setCollapsed] = useState<Collapsed[]>([]);
  // The cache is a ref, not state: a fetch that fills it must not re-render on
  // its own — `version` does that once per batch instead.
  const cache = useRef(new Map<number, BodyLine>());
  const [, bump] = useState(0);
  const pending = useRef(new Set<number>());

  // A new response, or a new rendering of it, invalidates everything.
  useEffect(() => {
    cache.current.clear();
    pending.current.clear();
    setCollapsed([]);
    setInfo(null);
    if (!sendId) return;
    let cancelled = false;
    setLoading(true);
    // This is the call that makes Go format the body. It can take a moment on
    // a large one, which is why the pane shows a formatting state rather than
    // an empty one.
    void SendService.View(sendId, view)
      .then((next) => {
        if (cancelled) return;
        setInfo(next);
        setLoading(false);
      })
      .catch(() => {
        if (cancelled) return;
        setInfo(null);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [sendId, view]);

  // A rendering that turns out not to exist falls back to the raw one, which
  // always does. Go says which in `unavailable`.
  useEffect(() => {
    if (view === ViewKind.Pretty && info?.unavailable) setViewState(ViewKind.Raw);
  }, [view, info?.unavailable]);

  const total = info?.lines ?? 0;

  /** How many source lines the collapsed ranges hide. */
  const hidden = useMemo(
    () => collapsed.reduce((sum, range) => sum + (range.to - range.from), 0),
    [collapsed],
  );
  const rows = Math.max(0, total - hidden);

  /**
   * The document line a row shows.
   *
   * Walks the collapsed ranges in order, adding each one's hidden length as it
   * is passed. The ranges are kept sorted and non-overlapping, and there are
   * as many of them as the user has clicked chevrons — a handful — so a walk
   * is cheaper than any index would be.
   */
  const sourceLine = useCallback(
    (row: number) => {
      let source = row;
      for (const range of collapsed) {
        if (source > range.from) source += range.to - range.from;
        else break;
      }
      return source;
    },
    [collapsed],
  );

  const line = useCallback((row: number) => cache.current.get(sourceLine(row)), [sourceLine]);

  /**
   * Fetches what a row range needs.
   *
   * The rows map to source lines that are contiguous only while nothing is
   * collapsed, so the range is walked into runs and each run is fetched. In
   * practice that is one call; it is more only when a collapsed node sits
   * inside the viewport.
   */
  const request = useCallback(
    (firstRow: number, lastRow: number) => {
      if (!sendId || total === 0) return;
      const from = Math.max(0, firstRow - OVERSCAN);
      const to = Math.min(rows - 1, lastRow + OVERSCAN);
      if (to < from) return;

      // The source lines wanted, grouped into contiguous runs.
      const runs: Array<{ from: number; to: number }> = [];
      for (let row = from; row <= to; row++) {
        const source = sourceLine(row);
        if (cache.current.has(source) || pending.current.has(source)) continue;
        const last = runs[runs.length - 1];
        if (last && source === last.to + 1) last.to = source;
        else runs.push({ from: source, to: source });
      }
      if (runs.length === 0) return;

      for (const run of runs) {
        for (let source = run.from; source <= run.to; source++) pending.current.add(source);
        const count = run.to - run.from + 1;
        void SendService.Lines(sendId, view, run.from, count)
          .then((chunk) => {
            for (const [offset, entry] of (chunk.lines ?? []).entries()) {
              cache.current.set(chunk.from + offset, entry);
            }
            evict(cache.current, run.from);
            bump((n) => n + 1);
          })
          .catch(() => {
            // A failed page is left uncached so scrolling back retries it. It
            // means the response was discarded under us, and the pane will
            // have been told separately.
          })
          .finally(() => {
            for (let source = run.from; source <= run.to; source++) {
              pending.current.delete(source);
            }
          });
      }
    },
    [sendId, view, rows, total, sourceLine],
  );

  const isCollapsed = useCallback(
    (fold: Fold) => collapsed.some((range) => range.from === fold.line),
    [collapsed],
  );

  const toggle = useCallback((fold: Fold) => {
    setCollapsed((current) => {
      const existing = current.findIndex((range) => range.from === fold.line);
      if (existing >= 0) return current.filter((_, i) => i !== existing);
      // A node inside one that is already collapsed cannot be clicked, so the
      // ranges never overlap; keeping them sorted is all that is needed.
      const next = [...current, { from: fold.line, to: fold.end }];
      next.sort((a, b) => a.from - b.from);
      return next;
    });
  }, []);

  const expandAll = useCallback(() => setCollapsed([]), []);

  const setView = useCallback((next: ViewKind) => setViewState(next), []);

  return {
    view,
    setView,
    info,
    loading,
    rows,
    line,
    sourceLine,
    request,
    isCollapsed,
    toggle,
    expandAll,
  };
}

/**
 * Keeps the cache bounded by dropping the lines furthest from where the reader
 * is. Without this, scrolling to the end of a 40 MB body would have pulled
 * every line into the window — the thing paging exists to prevent.
 */
function evict(cache: Map<number, BodyLine>, near: number) {
  if (cache.size <= CACHE_LIMIT) return;
  const keys = [...cache.keys()].sort((a, b) => Math.abs(b - near) - Math.abs(a - near));
  for (const key of keys) {
    if (cache.size <= CACHE_LIMIT) break;
    cache.delete(key);
  }
}
