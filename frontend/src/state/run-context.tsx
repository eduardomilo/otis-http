import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Events } from "@wailsio/runtime";

import { SendService } from "@bindings/internal/services";
import type { RunComplete, RunResult, RunStarted } from "@bindings/internal/services";
import { OtisEvent } from "@/lib/events.gen";
import { useEnvironments } from "@/state/environment-context";
import { useDocuments } from "@/state/documents-context";
import { useTabs } from "@/state/tabs-context";

/**
 * A folder run (screen 3a's "Run folder").
 *
 * Go owns the sequence and reports itself over events: the whole plan first,
 * then a result per request as it finishes, then the summary. The plan arrives
 * up front so the window can draw every row immediately and fill them in — a
 * run of twenty requests that reveals itself one row at a time tells you
 * nothing about how far through it is.
 *
 * One run at a time, per folder. Starting a second one for the same folder
 * replaces the first in this state; the first is cancelled in Go.
 */

/** One row of a run, drawn from the plan and filled in by its result. */
export interface RunRow {
  path: string;
  result: RunResult | null;
}

export interface Run {
  runId: string;
  folder: string;
  rows: RunRow[];
  stopOnFailure: boolean;
  /** The summary, or null while the run is still going. */
  summary: RunComplete | null;
  error: string | null;
}

interface RunContextValue {
  /** The run for a folder, or null when it has never been run. */
  runFor: (folder: string) => Run | null;
  /** Starts a run. stopOnFailure is remembered per folder. */
  start: (folder: string, stopOnFailure: boolean) => Promise<void>;
  /** Cancels a run in flight. */
  cancel: (folder: string) => Promise<void>;
}

const RunContext = createContext<RunContextValue | null>(null);

export function RunProvider({ children }: { children: ReactNode }) {
  // A draft lives only in the window, and Go runs the files on disk.
  const { tabs } = useTabs();
  const { save } = useDocuments();
  const { active: env } = useEnvironments();
  const [runs, setRuns] = useState<Record<string, Run>>({});
  // Which folder a run id belongs to, so a result can be routed without the
  // event having to carry the folder on every message.
  const folderOf = useRef<Record<string, string>>({});

  useEffect(() => {
    const unsubscribes = [
      Events.On(OtisEvent.RunStarted, (event) => {
        const started = event.data as RunStarted;
        folderOf.current[started.runId] = started.folder;
        setRuns((current) => ({
          ...current,
          [started.folder]: {
            runId: started.runId,
            folder: started.folder,
            rows: (started.requests ?? []).map((path) => ({ path, result: null })),
            stopOnFailure: started.stopOnFailure,
            summary: null,
            error: null,
          },
        }));
      }),
      Events.On(OtisEvent.RunResult, (event) => {
        const result = event.data as RunResult;
        const folder = folderOf.current[result.runId];
        if (folder === undefined) return;
        setRuns((current) => {
          const run = current[folder];
          if (!run || run.runId !== result.runId) return current;
          const rows = run.rows.slice();
          if (rows[result.index]) rows[result.index] = { path: result.path, result };
          return { ...current, [folder]: { ...run, rows } };
        });
      }),
      Events.On(OtisEvent.RunComplete, (event) => {
        const summary = event.data as RunComplete;
        const folder = folderOf.current[summary.runId] ?? summary.folder;
        setRuns((current) => {
          const run = current[folder];
          if (!run || run.runId !== summary.runId) return current;
          return { ...current, [folder]: { ...run, summary } };
        });
      }),
    ];
    return () => unsubscribes.forEach((off) => off());
  }, []);

  /**
   * Writes the drafts of every open request the run is about to send.
   *
   * Scoped to the folder deliberately: saving an unrelated dirty request
   * because you pressed Run on a different folder would be a write nobody
   * asked for.
   */
  const persistUnder = useCallback(
    async (folder: string) => {
      const prefix = folder === "" ? "" : folder + "/";
      for (const tab of tabs) {
        if (!tab.dirty || !tab.path.startsWith(prefix)) continue;
        if (!(await save(tab.path))) return false;
      }
      return true;
    },
    [tabs, save],
  );

  const start = useCallback(
    async (folder: string, stopOnFailure: boolean) => {
      // The same rule a single send follows: Go runs the files on disk, so a
      // request with unsaved edits would run its last saved version and the
      // run would report a result for something other than what is on screen.
      // Only the drafts inside this folder are written — a run is not a reason
      // to save a request it is not going to send.
      if (!(await persistUnder(folder))) return;
      try {
        await SendService.RunFolder(folder, env, stopOnFailure);
      } catch (cause) {
        // A run that never started has no id and no rows, so the failure is
        // recorded against the folder itself.
        setRuns((current) => ({
          ...current,
          [folder]: {
            runId: "",
            folder,
            rows: [],
            stopOnFailure,
            summary: null,
            error: String(cause),
          },
        }));
      }
    },
    [env],
  );

  const cancel = useCallback(async (folder: string) => {
    const runId = Object.entries(folderOf.current).find(([, f]) => f === folder)?.[0];
    if (!runId) return;
    await SendService.Cancel(runId).catch(() => undefined);
  }, []);

  const value = useMemo<RunContextValue>(
    () => ({ runFor: (folder) => runs[folder] ?? null, start, cancel }),
    [runs, start, cancel],
  );

  return <RunContext.Provider value={value}>{children}</RunContext.Provider>;
}

export function useRuns(): RunContextValue {
  const value = useContext(RunContext);
  if (!value) throw new Error("useRuns must be used inside a RunProvider");
  return value;
}
