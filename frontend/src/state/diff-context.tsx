import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { Events } from "@wailsio/runtime";

import { DiffService } from "@bindings/internal/services";
import type { Change, Overview } from "@bindings/internal/diff";
import { OtisEvent } from "@/lib/events.gen";
import { useCollection } from "@/state/collection-context";

/**
 * The git diff view's state (screen 1b): what changed, and the operations a
 * review performs.
 *
 * Every mutating call answers with the overview as it now is, so the changes
 * list never has to guess what a stage or a discard did. Changes made outside
 * Otis — a commit in a terminal, a branch switch — arrive as `git:changed`,
 * which the watcher raises.
 */

interface DiffContextValue {
  /** The overview, or null before the first read. */
  overview: Overview | null;
  changes: Change[];
  /** Re-reads the view. Changes normally arrive as an event. */
  refresh: () => Promise<void>;
  stage: (path: string) => Promise<void>;
  unstage: (path: string) => Promise<void>;
  stageAll: () => Promise<void>;
  stageHunks: (path: string, hunks: number[]) => Promise<void>;
  unstageHunks: (path: string, hunks: number[]) => Promise<void>;
  /** Destructive. Go refuses without the confirm flag this passes. */
  discardFile: (path: string) => Promise<void>;
  /** Destructive. */
  discardHunks: (path: string, hunks: number[]) => Promise<void>;
  commit: (message: string) => Promise<void>;
  /** The last failure, or null. */
  error: string | null;
  clearError: () => void;
  /** A revision counter, bumped whenever the view changed, so an open file
   *  diff knows to re-read itself. */
  revision: number;
}

const DiffContext = createContext<DiffContextValue | null>(null);

export function DiffProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const [overview, setOverview] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);

  const settle = useCallback((next: Overview | null) => {
    setOverview(next);
    setError(null);
    setRevision((n) => n + 1);
  }, []);

  const refresh = useCallback(async () => {
    if (!collection?.path) {
      setOverview(null);
      return;
    }
    try {
      settle((await DiffService.Overview()) ?? null);
    } catch (cause) {
      setError(String(cause));
    }
  }, [collection?.path, settle]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // HEAD or the index moved: a commit, a stage, or a branch switch, whether
  // Otis did it or a terminal did.
  useEffect(() => {
    const off = Events.On(OtisEvent.GitChanged, () => {
      void refresh();
    });
    return () => off();
  }, [refresh]);

  // A file written outside Otis changes the diff too.
  useEffect(() => {
    const off = Events.On(OtisEvent.CollectionChanged, () => {
      void refresh();
    });
    return () => off();
  }, [refresh]);

  const run = useCallback(
    async (call: () => Promise<Overview | null>) => {
      try {
        settle(await call());
      } catch (cause) {
        setError(String(cause));
        // The operation failed, so the view on screen may no longer be true.
        void refresh();
      }
    },
    [settle, refresh],
  );

  const value = useMemo<DiffContextValue>(
    () => ({
      overview,
      changes: overview?.changes ?? [],
      refresh,
      stage: (path) => run(() => DiffService.Stage(path)),
      unstage: (path) => run(() => DiffService.Unstage(path)),
      stageAll: () => run(() => DiffService.StageAll()),
      stageHunks: (path, hunks) => run(() => DiffService.StageHunks(path, hunks)),
      unstageHunks: (path, hunks) => run(() => DiffService.UnstageHunks(path, hunks)),
      // The confirm flag is passed here because the dialog that gates this
      // has already been answered. Go refuses the call without it, which is
      // the actual safety — see DiffService.DiscardFile.
      discardFile: (path) => run(() => DiffService.DiscardFile(path, true)),
      discardHunks: (path, hunks) => run(() => DiffService.DiscardHunks(path, hunks, true)),
      commit: async (message) => {
        try {
          const result = await DiffService.Commit(message);
          settle(result?.overview ?? null);
        } catch (cause) {
          setError(String(cause));
        }
      },
      error,
      clearError: () => setError(null),
      revision,
    }),
    [overview, refresh, run, settle, error, revision],
  );

  return <DiffContext.Provider value={value}>{children}</DiffContext.Provider>;
}

export function useDiff(): DiffContextValue {
  const value = useContext(DiffContext);
  if (!value) throw new Error("useDiff must be used inside a DiffProvider");
  return value;
}
