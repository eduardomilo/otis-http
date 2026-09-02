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

import { CollectionService, DialogService } from "@bindings/internal/services";
import type { CollectionInfo } from "@bindings/internal/services";
import { OtisEvent } from "@/lib/events.gen";
import { useSettings } from "@/state/settings-context";

/**
 * Which collection the window is showing.
 *
 * Go owns this state — CollectionService holds it and emits
 * events.CollectionOpened whenever it changes — so a directory dropped on the
 * window, one chosen in the native dialog and one reopened at launch all
 * arrive here by the same path.
 */

interface CollectionContextValue {
  /** The open collection, or null while the first read is in flight. */
  collection: CollectionInfo | null;
  /** True once a collection is open. */
  isOpen: boolean;
  /** Opens a directory as a collection. */
  open: (path: string) => Promise<void>;
  /** Shows the native directory picker, then opens what was chosen. */
  openViaDialog: () => Promise<void>;
  /** Returns to the empty state. */
  close: () => Promise<void>;
  /** The last open failure, shown in the empty state; null when there is none. */
  error: string | null;
  /** Dismisses the error. */
  clearError: () => void;
}

const CollectionContext = createContext<CollectionContextValue | null>(null);

export function CollectionProvider({ children }: { children: ReactNode }) {
  const { settings } = useSettings();
  const [collection, setCollection] = useState<CollectionInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const restored = useRef(false);

  useEffect(() => {
    void CollectionService.Current().then(setCollection);
    return Events.On(OtisEvent.CollectionOpened, (event) => {
      setCollection(event.data);
    });
  }, []);

  const open = useCallback(async (path: string) => {
    try {
      setCollection(await CollectionService.Open(path));
      setError(null);
    } catch (err) {
      setError(messageOf(err));
      throw err;
    }
  }, []);

  // Reopen the last collection once, as soon as both the settings and the
  // current (empty) collection are known.
  useEffect(() => {
    if (restored.current || !settings || !collection) return;
    restored.current = true;
    if (collection.path || !settings.lastCollection) return;
    // A directory that has since been deleted or renamed simply does not
    // reopen; it shows as missing in the recents list instead of raising an
    // error over something the user did not ask for right now.
    void open(settings.lastCollection).catch(() => setError(null));
  }, [settings, collection, open]);

  const openViaDialog = useCallback(async () => {
    const chosen = await DialogService.OpenDirectory();
    if (!chosen) return; // cancelled
    await open(chosen);
  }, [open]);

  const close = useCallback(async () => {
    await CollectionService.Close();
    setCollection({ path: "", name: "" });
  }, []);

  const value = useMemo<CollectionContextValue>(
    () => ({
      collection,
      isOpen: Boolean(collection?.path),
      open,
      openViaDialog,
      close,
      error,
      clearError: () => setError(null),
    }),
    [collection, open, openViaDialog, close, error],
  );

  return <CollectionContext.Provider value={value}>{children}</CollectionContext.Provider>;
}

export function useCollection(): CollectionContextValue {
  const value = useContext(CollectionContext);
  if (!value) throw new Error("useCollection must be used inside a CollectionProvider");
  return value;
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
