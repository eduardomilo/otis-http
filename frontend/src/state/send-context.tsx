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

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { OtisEvent } from "@/lib/events.gen";
import { nodeDisplayName } from "@/lib/paths";
import { findNode } from "@/lib/tree";
import { useCollection } from "@/state/collection-context";
import { useEnvironments } from "@/state/environment-context";
import { useDocuments } from "@/state/documents-context";
import { SendService } from "@bindings/internal/services";
import type {
  ResponseMeta,
  ScriptConsole,
  ScriptTest,
  SendFailure,
  SendStarted,
} from "@bindings/internal/services";
import type { ConsoleLine, TestResult } from "@bindings/internal/script";
import type { EnvironmentSummary } from "@bindings/internal/services";
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
  /**
   * Tests and console output as they stream in, before the send completes.
   *
   * Separate from `meta`, which carries the complete set: these are the live
   * view of a suite still running, and once the send completes `meta` is the
   * record. Sparse by index while it runs, because a result fills in the row
   * the plan already drew.
   */
  tests: TestResult[];
  console: ConsoleLine[];
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
  /**
   * The requests sent this session, most recent first — what the command
   * palette's `:` mode lists (screen 2c).
   *
   * This session's, deliberately. A "recent" from last week carrying a status
   * code from last week is worse than no row at all: it invites you to trust
   * a verdict about a server that has been deployed six times since. They are
   * forgotten with the window, like the cookie jar and the session variables.
   */
  recents: Recent[];
}

/** One past send, for the palette's Recent group. */
export interface Recent {
  path: string;
  name: string;
  method: string;
  /** 0 when the send produced no response. */
  statusCode: number;
  /** The failure's one-line message, when there was no response. */
  message: string;
  durationMs: number;
  /** When it finished, as an ISO string, for relativeTime. */
  at: string;
}

const SendContext = createContext<SendContextValue | null>(null);

export function SendProvider({ children }: { children: ReactNode }) {
  const { collection, tree: walked } = useCollection();
  const [sends, setSends] = useState<Record<string, Send>>({});
  const [sessionVars, setSessionVars] = useState<SessionValue[]>([]);
  // Most recent first, one row per path: the palette lists requests, not
  // individual attempts, so sending the same request twice moves its row up
  // rather than adding a second.
  const [recents, setRecents] = useState<Recent[]>([]);

  // The active environment. "" is a real state, not a placeholder: a request
  // then resolves against its file and folder scopes only (docs/FORMAT.md
  // §4.2).
  const { active: env, activeEnvironment } = useEnvironments();
  // Sends sit inside DocumentsProvider, so the draft is reachable from here.
  const { get: getDocument, save } = useDocuments();

  const latest = useRef(sends);
  latest.current = sends;

  // The tree, in a ref: the event handlers below name a recent from its node
  // and would otherwise have to resubscribe on every re-walk. A ref reads the
  // current tree without making the subscription depend on it.
  const tree = useRef(walked);
  tree.current = walked;

  /**
   * What to call a request in the Recent list, and which method to colour it
   * with.
   *
   * Taken from the node rather than from the send, because a send that never
   * reached the wire has no method to report and its `# @name` is what the
   * rest of the palette calls it — a Recent row reading `create-order-00`
   * beside a Requests row reading `create-order v2` would be two names for
   * one file.
   */
  const describe = useCallback((path: string): { name: string; method: string } => {
    const root = tree.current?.root;
    const node = root ? findNode(root, path) : undefined;
    return {
      name: node?.name || nodeDisplayName(path),
      method: node?.method ?? "",
    };
  }, []);

  // A path names a file inside one collection, so switching forgets the
  // recents — the same reason the tabs and the active environment are
  // cleared.
  useEffect(() => {
    setRecents([]);
  }, [collection?.path]);

  // One row per path: sending the same request twice moves its row up rather
  // than adding a second, because the palette lists requests and not attempts.
  const remember = useCallback((entry: Recent) => {
    setRecents((current) => [
      entry,
      ...current.filter((r) => r.path !== entry.path),
    ].slice(0, MAX_RECENTS));
  }, []);

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
        // A new send starts with nothing streamed: the last one's tests and
        // console output belong to the last one.
        apply(started.path, started.sendId, (send) => ({
          ...send, started, tests: [], console: [],
        }));
      }),
      Events.On(OtisEvent.SendComplete, (event) => {
        const meta = event.data as ResponseMeta;
        apply(meta.path, meta.sendId, (send) => ({
          ...send,
          phase: "complete",
          meta,
          failure: null,
        }));
        const named = describe(meta.path);
        remember({
          ...named,
          path: meta.path,
          // What actually went on the wire wins over the file's method: a
          // pre-request script may have changed it (docs/FORMAT.md §9.5).
          method: meta.request?.method || named.method,
          statusCode: meta.statusCode,
          message: "",
          durationMs: meta.durationMs,
          at: meta.at,
        });
      }),
      Events.On(OtisEvent.SendError, (event) => {
        const failure = event.data as SendFailure;
        apply(failure.path, failure.sendId, (send) => ({
          ...send,
          phase: failure.kind === "cancelled" ? "cancelled" : "failed",
          failure,
          meta: null,
        }));
        // A send that failed is still a send you made, and the palette saying
        // so is more use than a gap where the row would be.
        if (failure.kind !== "cancelled") {
          remember({
            ...describe(failure.path),
            path: failure.path,
            statusCode: 0,
            message: failure.message,
            durationMs: failure.durationMs,
            at: failure.at,
          });
        }
      }),
      // Tests and console output stream as they happen (docs/FORMAT.md §9.9),
      // so a long suite fills in rather than appearing all at once when the
      // send completes. The complete set arrives on SendComplete too, which
      // is what a tab reopened later reads — these are for watching it run.
      Events.On(OtisEvent.ScriptTest, (event) => {
        const streamed = event.data as ScriptTest;
        apply(streamed.path, streamed.sendId, (send) => {
          const tests = [...(send.tests ?? [])];
          tests[streamed.result.index] = streamed.result;
          return { ...send, tests };
        });
      }),
      Events.On(OtisEvent.ScriptConsole, (event) => {
        const streamed = event.data as ScriptConsole;
        apply(streamed.path, streamed.sendId, (send) => ({
          ...send,
          console: [...(send.console ?? []), streamed.line],
        }));
      }),
      Events.On(OtisEvent.SessionVarsChanged, () => {
        void SendService.SessionVars().then((values) => setSessionVars(values ?? []));
      }),
    ];
    void SendService.SessionVars().then((values) => setSessionVars(values ?? []));
    return () => offs.forEach((off) => off());
  }, [apply, describe, remember]);

  const reallySend = useCallback(
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
          tests: [],
          console: [],
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

  // A send an environment asked to confirm, waiting for an answer. One at a
  // time: the dialog is modal.
  const [pending, setPending] = useState<string | null>(null);

  /**
   * Sends, unless the active environment asked to be confirmed first
   * (docs/FORMAT.md §4.3).
   *
   * The gate is here rather than at each call site, so the Send button, ⌘↵ in
   * the editor, the palette's ⌘↵ and a folder run all get it. A safety feature
   * with a hole in it is not one — and "this environment is the dangerous one"
   * is a fact about the environment, not about which button you pressed.
   */
  /**
   * Writes a request's draft before it is sent.
   *
   * Go resolves a send from the collection **on disk** — `SendService.Send`
   * finds the node in `collections.Loaded()` — so a request with unsaved
   * edits sent the last saved version, silently. The editor showed one
   * request and Send ran another, which is how a 404 arrives for a URL you
   * are looking at and cannot see anything wrong with.
   *
   * It lives here rather than in the Send button for the reason the
   * confirm-before-send gate does: the button, the shell's ⌘↵, the palette's
   * ⌘↵ and anything added later all have to be covered, and a check in one
   * caller is a gate the next caller forgets.
   *
   * A save that fails stops the send. Sending anyway would be the original
   * bug with an error message in front of it.
   */
  const persist = useCallback(
    async (path: string) => {
      const document = getDocument(path);
      if (!document?.dirty) return true;
      return save(path);
    },
    [getDocument, save],
  );

  const send = useCallback(
    async (path: string) => {
      // Before the confirmation, not after: the dialog names the resolved URL,
      // and Go resolves it from disk. Asking first would describe the version
      // being replaced.
      if (!(await persist(path))) return;
      if (activeEnvironment?.confirmBeforeSend) {
        setPending(path);
        return;
      }
      await reallySend(path);
    },
    [activeEnvironment?.confirmBeforeSend, persist, reallySend],
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
      recents,
    }),
    [sends, send, cancel, forget, sessionVars, clearSessionVars, recents],
  );

  return (
    <SendContext.Provider value={value}>
      {children}
      <ConfirmSendDialog
        path={pending}
        environment={activeEnvironment}
        onCancel={() => setPending(null)}
        onConfirm={() => {
          const path = pending;
          setPending(null);
          if (path) void reallySend(path);
        }}
      />
    </SendContext.Provider>
  );
}

/**
 * The confirmation an environment asked for (docs/FORMAT.md §4.3).
 *
 * It names the environment and the request, because "are you sure?" is not a
 * question anybody can answer — what makes this worth stopping for is that
 * the environment is production and the request is the one that mutates.
 */
function ConfirmSendDialog({
  path,
  environment,
  onCancel,
  onConfirm,
}: {
  path: string | null;
  environment: EnvironmentSummary | null;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={path !== null} onOpenChange={(open) => !open && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            Send to <span className="font-mono">{environment?.name}</span>?
          </AlertDialogTitle>
          <AlertDialogDescription>
            <span className="font-mono text-fg-secondary">{path}</span> resolves against{" "}
            <span className="font-mono text-fg-secondary">{environment?.path}</span>, which asks to
            be confirmed before every send.
            {environment?.description ? (
              <>
                {" "}
                <span className="text-modified">{environment.description}</span>
              </>
            ) : null}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            Send
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function useSends(): SendContextValue {
  const value = useContext(SendContext);
  if (!value) throw new Error("useSends must be used inside a SendProvider");
  return value;
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** How many past sends the palette's Recent group remembers. */
const MAX_RECENTS = 20;

/**
 * A request's display name from its path, for a recent row.
 *
 * The path rather than the tree, because a recent has to survive the file
 * being renamed or deleted underneath it: the row then still says what was
 * sent, and opening it reports the file is gone rather than the row silently
 * vanishing.
 */
