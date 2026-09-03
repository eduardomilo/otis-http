import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { useCollection } from "@/state/collection-context";
import { OrderService } from "@bindings/internal/services";
import type { OrderResult } from "@bindings/internal/services";

/**
 * Reordering, and what the strip under the tree says about it (screen 2a).
 *
 * Every ordering write goes through here — the drag, the folder menu's
 * Manual/Alphabetical, and ⌘Z — because all three have to agree about what is
 * undoable. The confirmation strip is the *return value* of a call rather than
 * an event: the window asked for the write, so it is already the one that
 * knows it happened, and a second channel for saying so would be a second
 * thing to keep in step.
 */

export interface OrderContextValue {
  /** The last change, for the strip. Null when there is nothing to report. */
  last: OrderResult | null;
  /** Dismisses the strip without undoing anything. */
  dismiss: () => void;
  /** True while a write is in flight, so a second drag cannot race it. */
  busy: boolean;
  /** The last refusal, shown in the strip in place of a confirmation. */
  error: string | null;
  /** Writes a folder's `.order` to list these node paths in this order. */
  reorder: (folder: string, paths: readonly string[]) => Promise<void>;
  /** Moves a row into another folder at a position. */
  move: (path: string, folder: string, index: number) => Promise<void>;
  /** Switches a folder between a manual order and alphabetical. */
  setMode: (folder: string, mode: "manual" | "alphabetical") => Promise<void>;
  /** ⌘Z. Reverts the last ordering change, or does nothing when there is none. */
  undo: () => Promise<void>;
  /** Whether ⌘Z has anything to revert. */
  canUndo: boolean;
}

const OrderContext = createContext<OrderContextValue | null>(null);

/** How long a confirmation stays up before it stops taking space. */
const STRIP_MS = 8000;

export function OrderProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const [last, setLast] = useState<OrderResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A strip describes one collection's file. Opening another makes it stale.
  useEffect(() => {
    setLast(null);
    setError(null);
  }, [collection?.path]);

  // The confirmation goes away on its own; the Undo it offers does not
  // disappear with it, because ⌘Z still works from the shell's key map. The
  // strip is a notification, not the only way back.
  useEffect(() => {
    if (!last) return;
    const timer = window.setTimeout(() => setLast(null), STRIP_MS);
    return () => window.clearTimeout(timer);
  }, [last]);

  const run = useCallback(async (work: () => Promise<OrderResult>) => {
    setBusy(true);
    setError(null);
    try {
      setLast(await work());
    } catch (err) {
      // A refusal is shown where the confirmation would have been: a reorder
      // that did not happen has to say so, or the tree silently snapping back
      // reads as a dropped drag.
      setLast(null);
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }, []);

  const reorder = useCallback(
    (folder: string, paths: readonly string[]) => run(() => OrderService.Reorder(folder, [...paths])),
    [run],
  );
  const move = useCallback(
    (path: string, folder: string, index: number) => run(() => OrderService.Move(path, folder, index)),
    [run],
  );
  const setMode = useCallback(
    (folder: string, mode: "manual" | "alphabetical") => run(() => OrderService.SetMode(folder, mode)),
    [run],
  );

  // ⌘Z is a shell-wide chord, so it fires whether or not anything is
  // undoable. Asking Go first keeps that from becoming a visible refusal:
  // nothing to undo is not an error, it is nothing happening.
  const undo = useCallback(async () => {
    if (!(await OrderService.CanUndo())) return;
    await run(() => OrderService.Undo());
  }, [run]);

  const value = useMemo<OrderContextValue>(
    () => ({
      last,
      dismiss: () => {
        setLast(null);
        setError(null);
      },
      busy,
      error,
      reorder,
      move,
      setMode,
      undo,
      canUndo: last?.canUndo ?? false,
    }),
    [last, busy, error, reorder, move, setMode, undo],
  );

  return <OrderContext.Provider value={value}>{children}</OrderContext.Provider>;
}

export function useOrder(): OrderContextValue {
  const value = useContext(OrderContext);
  if (!value) throw new Error("useOrder must be used inside an OrderProvider");
  return value;
}

function messageOf(err: unknown): string {
  if (err instanceof Error) return err.message;
  return typeof err === "string" ? err : "The order could not be saved.";
}
