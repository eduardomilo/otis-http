import { Check, TriangleAlert } from "lucide-react";

import { hint } from "@/lib/platform";
import { useOrder } from "@/state/order-context";

/**
 * The strip under the tree after a reorder (screen 2a).
 *
 * Two lines, which is the design's layout and also the only one that fits: an
 * accent check at the left, "Order saved to" above `orders/.order` in mono,
 * and "Undo" above "⌘Z" at the right. One line truncates the path to
 * `orders/.…` in a 302px sidebar, and the path is the whole point — the
 * screen's centre pane exists to say that an order is a file in the
 * repository, and knowing which file is how you go and look at it.
 *
 * Go sends the phrase and the paths separately for that reason: the window
 * does not take a sentence apart to find the file name inside it.
 *
 * A refusal takes the same place. A reorder that did not happen has to say so
 * — the tree snapping back with nothing said reads as a dropped drag.
 */
export function OrderStrip() {
  const { last, error, dismiss, undo, canUndo } = useOrder();
  if (!last && !error) return null;

  if (error) {
    return (
      <Frame icon={<TriangleAlert className="size-3 shrink-0 text-warning" />}>
        <span className="min-w-0 flex-1 truncate text-meta text-fg-secondary" title={error}>
          {error}
        </span>
        <button
          type="button"
          onClick={dismiss}
          className="shrink-0 text-meta text-fg-dim hover:text-fg-emphasis"
        >
          Dismiss
        </button>
      </Frame>
    );
  }

  const files = last!.files ?? [];
  return (
    <Frame icon={<Check className="size-3 shrink-0 text-primary" />}>
      <div className="min-w-0 flex-1">
        <p className="truncate text-meta text-fg-secondary">{last!.summary}</p>
        {files.length > 0 ? (
          // dir="rtl" around an ltr span truncates from the *start*, so a long
          // path loses its leading folders and keeps the file name — the half
          // that says which file this was.
          <p dir="rtl" className="truncate text-left font-mono text-meta text-fg-emphasis">
            <span dir="ltr">{files.join(", ")}</span>
          </p>
        ) : null}
      </div>
      {canUndo ? (
        <button
          type="button"
          onClick={() => void undo()}
          title="Take back the last ordering change"
          className="shrink-0 text-right text-meta text-fg-dim hover:text-fg-emphasis"
        >
          <span className="block">Undo</span>
          <span className="block font-mono text-label">{hint("Z")}</span>
        </button>
      ) : null}
    </Frame>
  );
}

function Frame({ icon, children }: { icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex shrink-0 items-center gap-1.5 border-t border-border px-1 py-2">
      {icon}
      {children}
    </div>
  );
}
