import { useLayoutEffect, useRef, useState } from "react";
import { Folder, X } from "lucide-react";

import { methodColor } from "@/lib/method";
import { nodeDisplayName } from "@/lib/paths";
import { findNode } from "@/lib/tree";
import { cn } from "@/lib/utils";
import { useCollection } from "@/state/collection-context";
import type { Tab } from "@/state/tabs-context";
import type { Node } from "@bindings/internal/services";
import { useTabs } from "@/state/tabs-context";

/**
 * The document tab bar: 34px, one tab per open request or folder
 * (DESIGN-NOTES §4.1, screen 1a).
 *
 * Hand-rolled rather than Radix Tabs, which models one-of-N panel switching
 * and cannot do closeable, overflowing document tabs (DESIGN-NOTES §7.3).
 *
 * The active tab takes --bg and an accent underline; the inactive ones sit on
 * the strip.
 *
 * The strip scrolls when the tabs outgrow it, and two things follow from that.
 * Its scrollbar is hidden (`no-scrollbar`): §5's 8px bar is right for a pane,
 * but inside a 34px strip it eats a quarter of the height. And **activating a
 * tab scrolls it into view** — without that, clicking a request in the tree
 * activates a tab you cannot see, which reads as the click having done
 * nothing. Activation comes from four places (the tree, the palette, a
 * forwarded file open, and the tab itself), so the scroll belongs here, keyed
 * on which tab is active, rather than in each caller.
 *
 * Tabs reorder by dragging, which §6 names as the thing Radix `Tabs` could not
 * do and the design never draws. It borrows the tree's vocabulary rather than
 * inventing one (DESIGN-NOTES §7.7): the dragged tab dims, and a single accent
 * line marks the edge it would land on.
 */
export function TabBar() {
  const { tabs, activePath, closeTab, openTab, moveTab } = useTabs();
  const { tree } = useCollection();
  const active = useRef<HTMLDivElement | null>(null);
  const strip = useRef<HTMLDivElement | null>(null);
  // The tab being dragged and the gap it would drop into. The gap is an index
  // between tabs — 0 is before the first, tabs.length is after the last.
  const [dragging, setDragging] = useState<string | null>(null);
  const [gap, setGap] = useState<number | null>(null);

  // A layout effect, not an effect: this runs in the same frame as the
  // activation, so an off-screen tab is never painted off-screen first.
  // `nearest` on both axes scrolls the strip the minimum needed and leaves the
  // rest of the window alone.
  useLayoutEffect(() => {
    active.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [activePath]);

  /**
   * Which gap a pointer at `clientX` is nearest.
   *
   * Measured off the rendered tabs rather than tracked during the drag,
   * because the strip scrolls: an index computed from a stored offset would
   * be wrong the moment the pointer reaches an edge and the strip moves.
   */
  const gapAt = (clientX: number): number => {
    const element = strip.current;
    if (!element) return 0;
    const rects = [...element.querySelectorAll('[role="tab"]')].map((t) =>
      t.getBoundingClientRect(),
    );
    for (let i = 0; i < rects.length; i++) {
      if (clientX < rects[i].left + rects[i].width / 2) return i;
    }
    return rects.length;
  };

  const onTabPointerDown = (event: React.PointerEvent, path: string) => {
    // Left button only, and never from the close button, which has its own job.
    if (event.button !== 0) return;
    const startX = event.clientX;
    let armed = false;

    const move = (e: PointerEvent) => {
      // A drag starts only after the pointer has actually travelled, so a
      // click that wobbles a pixel still selects the tab instead of
      // reordering the strip.
      if (!armed && Math.abs(e.clientX - startX) < 4) return;
      if (!armed) {
        armed = true;
        setDragging(path);
      }
      setGap(gapAt(e.clientX));
    };
    const up = (e: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      if (armed) {
        // The click that would otherwise follow the drop is not a selection.
        e.preventDefault();
        moveTab(path, gapAt(e.clientX));
      }
      setDragging(null);
      setGap(null);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  return (
    <div
      ref={strip}
      className="no-scrollbar flex h-[var(--tab-bar-height)] shrink-0 items-stretch overflow-x-auto border-b border-border bg-background"
    >
      {tabs.map((tab, i) => (
        <TabButton
          key={tab.path}
          tab={tab}
          ref={tab.path === activePath ? active : undefined}
          // The method and the display name come from the tree rather than
          // from the path: docs/FORMAT.md §2.1 prefers the @name directive
          // over the file name, and only the parsed file knows it.
          node={tree ? findNode(tree.root, tab.path) : undefined}
          active={tab.path === activePath}
          dragging={dragging === tab.path}
          // The one insertion line, on whichever edge the drop is nearest.
          // Only ever on one tab, so two adjacent tabs never both draw it.
          line={
            dragging === null || gap === null
              ? null
              : gap === i
                ? "before"
                : gap === tabs.length && i === tabs.length - 1
                  ? "after"
                  : null
          }
          onActivate={() => openTab(tab.path, tab.kind)}
          onClose={() => void closeTab(tab.path)}
          onGrab={(event) => onTabPointerDown(event, tab.path)}
        />
      ))}
    </div>
  );
}

function TabButton({
  tab,
  node,
  active,
  onActivate,
  onClose,
  onGrab,
  dragging,
  line,
  ref,
}: {
  tab: Tab;
  node: Node | undefined;
  active: boolean;
  onActivate: () => void;
  onClose: () => void;
  onGrab: (event: React.PointerEvent) => void;
  /** This tab is the one being dragged: dimmed, as a dragged tree row is. */
  dragging: boolean;
  /** The single insertion line, when it belongs on this tab's edge. */
  line: "before" | "after" | null;
  /** Set on the active tab only, so the bar can scroll it into view. */
  ref?: React.Ref<HTMLDivElement>;
}) {
  const label = node?.name ?? nodeDisplayName(tab.path);
  return (
    <div
      ref={ref}
      role="tab"
      aria-selected={active}
      tabIndex={-1}
      onClick={onActivate}
      // Middle-click closes, the way every editor does it. The auxclick
      // listener is on the tab rather than the close button so the whole tab
      // is the target.
      onAuxClick={(event) => {
        if (event.button === 1) {
          event.preventDefault();
          onClose();
        }
      }}
      onPointerDown={onGrab}
      className={cn(
        // The transparent left/right borders are reserved, not decorative:
        // the insertion line replaces one of them, so a tab's width never
        // changes when the line appears and the strip does not shift
        // mid-drag (the same trick the tree rows use, DESIGN-NOTES §7.7).
        "group flex min-w-0 shrink-0 cursor-default items-center gap-2 border-x border-transparent border-r-border px-3 select-none",
        line === "before" && "border-l-primary",
        line === "after" && "border-r-primary",
        dragging
          ? "bg-inset text-fg-faint opacity-40"
          : active
            ? "bg-background text-fg-emphasis shadow-[inset_0_-1px_0_var(--accent)]"
            : "text-fg-muted hover:bg-control",
      )}
    >
      {tab.kind === "folder" ? (
        <Folder className="size-3 shrink-0 text-fg-muted" />
      ) : (
        <span
          className={cn(
            "shrink-0 font-mono text-label font-medium tracking-[.02em]",
            methodColor(node?.method),
          )}
        >
          {node?.method}
        </span>
      )}

      <span className={cn("truncate text-ui", active && "font-medium")}>{label}</span>

      {/* Screen 1a draws an amber dot *in place of* the close × on a dirty
          tab, and taken literally that leaves a tab with unsaved changes no
          close control at all — the one tab you cannot shut with the mouse.
          SCREENS.md lists "tab close" among the interactions the static design
          cannot show, so the resting state is the design's dot and hovering
          the tab swaps in the ×, the way every editor does it.

          Both share one fixed-size box, so the tab's width does not change
          under the pointer and the strip never shifts. The button is always in
          the tree and only made transparent, so it stays focusable rather than
          being a control a keyboard user can never reach (DESIGN-NOTES §9.14
          makes that argument about the hunk controls). */}
      <span className="relative flex size-3 shrink-0 items-center justify-center">
        {tab.dirty ? (
          <span
            aria-label="Unsaved changes"
            title="Unsaved changes"
            className="size-1.5 rounded-full bg-modified group-hover:opacity-0"
          />
        ) : null}
        <button
          type="button"
          aria-label={tab.dirty ? `Close ${label} — unsaved changes` : `Close ${label}`}
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => {
            event.stopPropagation();
            onClose();
          }}
          className={cn(
            "absolute inset-0 flex items-center justify-center text-fg-faint hover:text-fg-emphasis",
            tab.dirty && "opacity-0 group-hover:opacity-100 focus-visible:opacity-100",
          )}
        >
          <X className="size-3" />
        </button>
      </span>
    </div>
  );
}
