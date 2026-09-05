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

import { MCPService } from "@bindings/internal/services";
import type { MCPConfirmation, MCPResolved, MCPStatus } from "@bindings/internal/services";
import { OtisEvent } from "@/lib/events.gen";

/**
 * The agent server's state, and the confirmation it is waiting on.
 *
 * Go owns all of it. The window cannot grant a capability to itself, cannot
 * read the token, and cannot answer a confirmation for anything other than the
 * one Go asked about — the answer is matched on an id Go minted (docs/MCP.md
 * §6.4).
 *
 * There is deliberately no polling. A tool call is *blocked* on the
 * confirmation with a 60-second deadline running, so `mcp:confirm` arrives the
 * moment there is something to ask, and `mcp:confirm-resolved` closes the
 * dialog when the answer no longer goes anywhere — the deadline passed, or the
 * kill switch was thrown. A dialog whose answer is discarded is worse than no
 * dialog, because the next thing the person does is click it.
 */

/**
 * One operation waiting on a person.
 *
 * The generated type is the contract — it is `mcpserver.Confirmation`, which
 * `confirm.go` fills in — so this is an alias rather than a second copy. A
 * hand-written mirror would be a second answer to what a person is being told,
 * and the one that drifted would be the one on screen.
 */
export type AgentConfirmation = MCPConfirmation;

interface MCPContextValue {
  status: MCPStatus | null;
  /** The confirmation on screen, or null. */
  confirmation: AgentConfirmation | null;
  /** Why a confirmation closed itself, for the one frame before it goes. */
  resolvedReason: string | null;
  answer: (id: string, approve: boolean) => Promise<void>;
  setEnabled: (on: boolean) => Promise<void>;
  setCapability: (name: "read" | "run" | "write", on: boolean) => Promise<void>;
  setAlwaysConfirmSends: (on: boolean) => Promise<void>;
  setPersistAuditLog: (on: boolean) => Promise<void>;
  disconnect: () => Promise<void>;
  /**
   * Puts the client configuration on the clipboard. Go writes it: the block
   * carries a bearer token and the webview's clipboard API does not work
   * from a custom scheme anyway (MCPService.CopyClientBlock).
   */
  copyClientBlock: () => Promise<boolean>;
  /** The last failure from a switch, for the popover to show. */
  error: string | null;
  clearError: () => void;
}

const MCPContext = createContext<MCPContextValue | null>(null);

export function MCPProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<MCPStatus | null>(null);
  const [confirmation, setConfirmation] = useState<AgentConfirmation | null>(null);
  const [resolvedReason, setResolvedReason] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setStatus(await MCPService.Status());
    } catch (cause) {
      setError(String(cause));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    const offChanged = Events.On(OtisEvent.MCPChanged, (event) => {
      if (event.data) setStatus(event.data);
    });
    const offConfirm = Events.On(OtisEvent.MCPConfirm, (event) => {
      if (!event.data) return;
      setResolvedReason(null);
      setConfirmation(event.data);
    });
    const offResolved = Events.On(OtisEvent.MCPConfirmResolved, (event) => {
      const resolved: MCPResolved | undefined = event.data;
      if (!resolved) return;
      setConfirmation((current) => {
        if (current?.id !== resolved.id) return current;
        // A reason means nobody answered, so it is worth saying; an answer
        // closes the dialog at once, because the person is already looking
        // at what they did.
        setResolvedReason(resolved.reason ?? null);
        return null;
      });
    });
    return () => {
      offChanged();
      offConfirm();
      offResolved();
    };
  }, []);

  const answer = useCallback(async (id: string, approve: boolean) => {
    setConfirmation((current) => (current?.id === id ? null : current));
    await MCPService.Answer(id, approve);
  }, []);

  // Each switch returns the new status, so the chip is right immediately
  // rather than after the event round-trips.
  const apply = useCallback(async (call: () => Promise<MCPStatus | null>) => {
    setError(null);
    try {
      const next = await call();
      if (next) setStatus(next);
    } catch (cause) {
      setError(String(cause));
      // The switch failed, so re-read rather than leaving the UI showing a
      // state Go never reached.
      void refresh();
    }
  }, [refresh]);

  const value = useMemo<MCPContextValue>(
    () => ({
      status,
      confirmation,
      resolvedReason,
      answer,
      setEnabled: (on) => apply(() => MCPService.SetEnabled(on)),
      setCapability: (name, on) => apply(() => MCPService.SetCapability(name, on)),
      setAlwaysConfirmSends: (on) => apply(() => MCPService.SetAlwaysConfirmSends(on)),
      setPersistAuditLog: (on) => apply(() => MCPService.SetPersistAuditLog(on)),
      disconnect: async () => {
        setError(null);
        try {
          await MCPService.Disconnect();
        } catch (cause) {
          setError(String(cause));
        }
        void refresh();
      },
      copyClientBlock: async () => {
        setError(null);
        try {
          await MCPService.CopyClientBlock();
          return true;
        } catch (cause) {
          setError(String(cause));
          return false;
        }
      },
      error,
      clearError: () => setError(null),
    }),
    [status, confirmation, resolvedReason, answer, apply, refresh, error],
  );

  return <MCPContext.Provider value={value}>{children}</MCPContext.Provider>;
}

export function useMCP(): MCPContextValue {
  const value = useContext(MCPContext);
  if (!value) throw new Error("useMCP must be used inside an MCPProvider");
  return value;
}
