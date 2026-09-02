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

import { OtisEvent } from "@/lib/events.gen";
import { useCollection } from "@/state/collection-context";
import { useEnvironments } from "@/state/environment-context";
import { SendService } from "@bindings/internal/services";
import type { ResponseMeta, SendFailure, SendStarted } from "@bindings/internal/services";
import type { SessionValue } from "@bindings/internal/resolve";

/**
 * Sends and what came back.
 *
 * One send per request at a time: sending again supersedes what was there,
 * which is what a Send button means. State is keyed by the request's path
 * rather than by send ID because that is what the pane showing it is keyed by —
 * the ID is the handle Go needs, not the one the window is organised around.
 *
 * The body is not here. It stays in Go and is paged by `use-response-body`;
 * this holds only the metadata, which is small and arrives in one event.
 */

export type SendPhase = "in-flight" | "complete" | "failed" | "cancelled";

export interface Send {
  path: string;
  /** Go's handle on the send. Null between asking and being told. */
  sendId: string | null;
  phase: SendPhase;
  /** When the send was asked for, for the elapsed timer. */
  startedAt: number;
  /** What went on the wire, once Go has said. Null while resolving. */
  started: SendStarted | null;
  meta: ResponseMeta | null;
  failure: SendFailure | null;
}

interface SendContextValue {
  /** The current send for a request, or undefined if it has not been sent. */
  get: (path: string) => Send | undefined;
  /** Sends a request, superseding any send in flight for it. */
  send: (path: string) => Promise<void>;
  /** Stops the send in flight for a request. */
  cancel: (path: string) => Promise<void>;
  /** Forgets a request's response and frees its body in Go. */
  forget: (path: string) => void;
  /** The variables runs have set (docs/FORMAT.md §4.5). */
  sessionVars: SessionValue[];
  /** Forgets every session variable. */
  clearSessionVars: () => Promise<void>;
}

const SendContext = createContext<SendContextValue | null>(null);

export function SendProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const [sends, setSends] = useState<Record<string, Send>>({});
  const [sessionVars, setSessionVars] = useState<SessionValue[]>([]);

  // The active environment. "" is a real state, not a placeholder: a request
  // then resolves against its file and folder scopes only (docs/FORMAT.md
  // §4.2).
  const { active: env } = useEnvironments();

  const latest = useRef(sends);
  latest.current = sends;

  /**
   * Applies an event to the send it belongs to.
   *
   * Matched by path, not by ID, because an event can arrive *before* the ID
   * does: a request that fails to resolve emits send:error from Go's goroutine
   * while the Send call that would have returned the ID is still in flight
   * here. A send whose ID is known and differs is stale — a superseded send
   * finishing after a newer one started — and is dropped.
   */
  const apply = useCallback(
    (path: string, sendId: string, patch: (send: Send) => Send) => {
      setSends((current) => {
        const existing = current[path];
        if (!existing) return current;
        if (existing.sendId !== null && existing.sendId !== sendId) return current;
        return { ...current, [path]: patch({ ...existing, sendId }) };
      });
    },
    [],
  );

  useEffect(() => {
    const offs = [
      Events.On(OtisEvent.SendStarted, (event) => {
        const started = event.data as SendStarted;
        apply(started.path, started.sendId, (send) => ({ ...send, started }));
      }),
      Events.On(OtisEvent.SendComplete, (event) => {
        const meta = event.data as ResponseMeta;
        apply(meta.path, meta.sendId, (send) => ({
          ...send,
          phase: "complete",
          meta,
          failure: null,
        }));
      }),
      Events.On(OtisEvent.SendError, (event) => {
        const failure = event.data as SendFailure;
        apply(failure.path, failure.sendId, (send) => ({
          ...send,
          phase: failure.kind === "cancelled" ? "cancelled" : "failed",
          failure,
          meta: null,
        }));
      }),
      Events.On(OtisEvent.SessionVarsChanged, () => {
        void SendService.SessionVars().then((values) => setSessionVars(values ?? []));
      }),
    ];
    void SendService.SessionVars().then((values) => setSessionVars(values ?? []));
    return () => offs.forEach((off) => off());
  }, [apply]);

  const send = useCallback(
    async (path: string) => {
      const previous = latest.current[path];
      // Sending again supersedes: the old send is stopped rather than left to
      // finish into a pane that is no longer showing it.
      if (previous?.phase === "in-flight" && previous.sendId) {
        void SendService.Cancel(previous.sendId);
      }
      if (previous?.meta?.sendId) void SendService.Discard(previous.meta.sendId);

      setSends((current) => ({
        ...current,
        [path]: {
          path,
          sendId: null,
          phase: "in-flight",
          startedAt: Date.now(),
          started: null,
          meta: null,
          failure: null,
        },
      }));
      try {
        const sendId = await SendService.Send(path, env);
        setSends((current) => {
          const existing = current[path];
          // The events may already have landed and finished this send; only
          // the ID is filled in here, never the phase.
          if (!existing || existing.sendId !== null) return current;
          return { ...current, [path]: { ...existing, sendId } };
        });
      } catch (err) {
        // Send itself refused — the file is not a request, or the collection
        // is gone. There is no send ID and no event coming.
        setSends((current) => {
          const existing = current[path];
          if (!existing) return current;
          return {
            ...current,
            [path]: {
              ...existing,
              phase: "failed",
              failure: {
                sendId: "",
                path,
                kind: "collection",
                message: messageOf(err),
                durationMs: 0,
                at: new Date().toISOString(),
              } as SendFailure,
            },
          };
        });
      }
    },
    [env],
  );

  const cancel = useCallback(async (path: string) => {
    const existing = latest.current[path];
    if (!existing?.sendId) return;
    await SendService.Cancel(existing.sendId);
  }, []);

  const forget = useCallback((path: string) => {
    const existing = latest.current[path];
    if (existing?.meta?.sendId) void SendService.Discard(existing.meta.sendId);
    if (existing?.phase === "in-flight" && existing.sendId) void SendService.Cancel(existing.sendId);
    setSends((current) => {
      if (!current[path]) return current;
      const next = { ...current };
      delete next[path];
      return next;
    });
  }, []);

  const clearSessionVars = useCallback(async () => {
    await SendService.ClearSessionVars();
  }, []);

  // Closing a collection closes its responses with it. Go drops the bodies on
  // its own side; this drops what the window was showing.
  useEffect(() => {
    if (collection && !collection.path) {
      setSends({});
      setSessionVars([]);
    }
  }, [collection]);

  const value = useMemo<SendContextValue>(
    () => ({
      get: (path) => sends[path],
      send,
      cancel,
      forget,
      sessionVars,
      clearSessionVars,
    }),
    [sends, send, cancel, forget, sessionVars, clearSessionVars],
  );

  return <SendContext.Provider value={value}>{children}</SendContext.Provider>;
}

export function useSends(): SendContextValue {
  const value = useContext(SendContext);
  if (!value) throw new Error("useSends must be used inside a SendProvider");
  return value;
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
