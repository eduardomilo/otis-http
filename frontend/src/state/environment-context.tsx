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

import { EnvironmentService } from "@bindings/internal/services";
import type { Environments, EnvironmentSummary, KeychainState } from "@bindings/internal/services";
import { OtisEvent } from "@/lib/events.gen";
import { useCollection } from "@/state/collection-context";

/**
 * The collection's environments and which one is active.
 *
 * Go owns the list, the same way it owns the tree: it reads `env/*.json`,
 * writes them, and holds the active name in the settings file. An environment
 * edited in another editor, one Otis wrote itself, and a switch made from the
 * title strip all arrive here as one event carrying the whole list.
 *
 * No secret value is ever in this state. A row says a secret exists, whether
 * this machine has a value for it, and the public keychain key — never the
 * value (docs/FORMAT.md §5).
 */

interface EnvironmentContextValue {
  /** Every environment in the collection, sorted by name. */
  environments: EnvironmentSummary[];
  /** The active environment's name, "" for none. */
  active: string;
  /** The active environment's row, or null when none is active. */
  activeEnvironment: EnvironmentSummary | null;
  /** Whether this machine has a credential store Otis can reach. */
  keychain: KeychainState;
  /** Makes an environment active; "" deactivates. */
  activate: (name: string) => Promise<void>;
  /** Re-reads the list. Changes normally arrive as an event. */
  refresh: () => Promise<void>;
  /** The last failure, or null. */
  error: string | null;
  clearError: () => void;
}

const EnvironmentContext = createContext<EnvironmentContextValue | null>(null);

const EMPTY: Environments = { active: "", items: [], keychain: { available: false } };

export function EnvironmentProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const [state, setState] = useState<Environments>(EMPTY);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setState((await EnvironmentService.List()) ?? EMPTY);
    } catch (cause) {
      setError(String(cause));
    }
  }, []);

  // Re-read when the collection changes: environments belong to one
  // collection, and the active name is cleared when it switches.
  useEffect(() => {
    void refresh();
  }, [collection?.path, refresh]);

  useEffect(() => {
    const off = Events.On(OtisEvent.EnvironmentsChanged, (event) => {
      setState(event.data ?? EMPTY);
    });
    return () => off();
  }, []);

  const activate = useCallback(async (name: string) => {
    try {
      setState((await EnvironmentService.Activate(name)) ?? EMPTY);
      setError(null);
    } catch (cause) {
      setError(String(cause));
    }
  }, []);

  const value = useMemo<EnvironmentContextValue>(() => {
    const items = state.items ?? [];
    return {
      environments: items,
      active: state.active ?? "",
      activeEnvironment: items.find((e) => e.name === state.active) ?? null,
      keychain: state.keychain ?? { available: false },
      activate,
      refresh,
      error,
      clearError: () => setError(null),
    };
  }, [state, activate, refresh, error]);

  return <EnvironmentContext.Provider value={value}>{children}</EnvironmentContext.Provider>;
}

export function useEnvironments(): EnvironmentContextValue {
  const value = useContext(EnvironmentContext);
  if (!value) throw new Error("useEnvironments must be used inside an EnvironmentProvider");
  return value;
}
