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
import { OtisEvent } from "@/lib/events.gen";
import { LogService } from "@bindings/internal/services";
import type { LogEntry } from "@bindings/internal/services";

/**
 * The activity log: everything Otis tried and could not do.
 *
 * Before this there was nowhere for such a thing to go. A clipboard write that
 * refused, a reveal that could not find the file manager, a duplicate that hit
 * a permission error — each went to `console.error` in a webview whose console
 * nobody opens, or to a Wails logger writing to a stderr the packaged app does
 * not have. The tree's own helper said as much in a comment.
 *
 * The list lives in Go so that the window's failures and Go's own end up in
 * one place in one order. This context is the window's half: it holds a copy
 * for rendering, follows the `log:appended` event, and gives every component
 * one function to report through.
 *
 * `unread` is what the status bar counts. It is cleared by *opening* the log
 * rather than by time, because the point of a log is that a failure is
 * usually noticed after the fact.
 */

export interface LogContextValue {
  entries: LogEntry[];
  /** Errors since the log was last opened. */
  unread: number;
  /** Records a failure the window hit. Detail is the error, when there is one. */
  log: (message: string, detail?: string) => void;
  /** Marks everything seen. The popover calls it when it opens. */
  markRead: () => void;
  clear: () => Promise<void>;
}

const LogContext = createContext<LogContextValue | null>(null);

export function LogProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  // When the log was last opened. Everything newer than this is unread.
  const [readAt, setReadAt] = useState(0);

  useEffect(() => {
    void LogService.Entries()
      .then((list) => setEntries(list ?? []))
      .catch(() => {});
    return Events.On(OtisEvent.LogAppended, (event) => {
      const entry = event.data as LogEntry;
      // A clear arrives as a zero-valued entry: there is no id 0, so this is
      // unambiguous and costs no second event name.
      if (!entry?.id) {
        setEntries([]);
        setReadAt(0);
        return;
      }
      setEntries((current) => [entry, ...current].slice(0, 200));
    });
  }, []);

  const log = useCallback((message: string, detail?: string) => {
    // Optimism is wrong here: the entry the window renders is the one Go
    // recorded, so a failure to record shows up as a missing line rather than
    // as a line that is not really in the log.
    void LogService.Record("error", "window", message, detail ?? "").catch(() => {});
  }, []);

  const clear = useCallback(async () => {
    await LogService.Clear().catch(() => {});
    setEntries([]);
    setReadAt(0);
  }, []);

  const markRead = useCallback(() => setReadAt(Date.now()), []);

  const unread = useMemo(
    () => entries.filter((entry) => entry.level === "error" && Date.parse(entry.at) > readAt).length,
    [entries, readAt],
  );

  const value = useMemo<LogContextValue>(
    () => ({ entries, unread, log, markRead, clear }),
    [entries, unread, log, markRead, clear],
  );
  return <LogContext.Provider value={value}>{children}</LogContext.Provider>;
}

export function useLog(): LogContextValue {
  const value = useContext(LogContext);
  if (!value) throw new Error("useLog must be used inside a LogProvider");
  return value;
}
