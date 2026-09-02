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

import { SettingsService } from "@bindings/internal/services";
import type { Panes, Settings, Tabs } from "@bindings/internal/settings";
import { OtisEvent } from "@/lib/events.gen";

/**
 * Persisted settings.
 *
 * This is the only durable store the frontend has: localStorage and
 * sessionStorage are banned, so pane sizes, open tabs and recent collections
 * all round-trip through SettingsService to a JSON file in the OS config
 * directory.
 *
 * Both sides write. Go owns the recents list and the last collection; the
 * window owns the panes and the tabs. So the window never writes the whole
 * document — there is one call per field it owns, each a read-modify-write in
 * Go — because a whole-document write would discard the recents Go recorded
 * while a save was still pending here.
 *
 * The saves are debounced: dragging a pane divider produces a settings change
 * per frame, and each one is a file write.
 */

const SAVE_DEBOUNCE_MS = 300;

interface SettingsContextValue {
  /** The current settings, or null until the first read completes. */
  settings: Settings | null;
  /** Records the pane geometry. */
  savePanes: (panes: Panes) => void;
  /** Records the open tabs and the active one. */
  saveTabs: (tabs: Tabs) => void;
  /** Re-reads the settings from Go, flushing any pending save first. */
  reload: () => Promise<void>;
}

const SettingsContext = createContext<SettingsContextValue | null>(null);

/** One debounced writer per field, so the two never queue behind each other. */
interface Writer<T> {
  timer: ReturnType<typeof setTimeout> | null;
  pending: T | null;
}

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const panes = useRef<Writer<Panes>>({ timer: null, pending: null });
  const tabs = useRef<Writer<Tabs>>({ timer: null, pending: null });

  const flush = useCallback(async () => {
    const writes: Promise<unknown>[] = [];
    if (panes.current.timer !== null) clearTimeout(panes.current.timer);
    if (tabs.current.timer !== null) clearTimeout(tabs.current.timer);
    panes.current.timer = null;
    tabs.current.timer = null;
    if (panes.current.pending) writes.push(SettingsService.SetPanes(panes.current.pending));
    if (tabs.current.pending) writes.push(SettingsService.SetTabs(tabs.current.pending));
    panes.current.pending = null;
    tabs.current.pending = null;
    await Promise.all(writes);
  }, []);

  const reload = useCallback(async () => {
    await flush();
    setSettings(await SettingsService.Get());
  }, [flush]);

  useEffect(() => {
    void reload();
    // Go changes the settings on its own when a collection opens (recents,
    // last collection); this is how the window finds out.
    return Events.On(OtisEvent.SettingsChanged, () => {
      void reload();
    });
  }, [reload]);

  // Never leave a debounced save unwritten when the window goes away.
  useEffect(() => {
    const onUnload = () => {
      if (panes.current.pending) void SettingsService.SetPanes(panes.current.pending);
      if (tabs.current.pending) void SettingsService.SetTabs(tabs.current.pending);
    };
    window.addEventListener("beforeunload", onUnload);
    return () => {
      window.removeEventListener("beforeunload", onUnload);
      onUnload();
    };
  }, []);

  const savePanes = useCallback((value: Panes) => {
    setSettings((current) => (current ? { ...current, panes: value } : current));
    schedule(panes.current, value, SettingsService.SetPanes);
  }, []);

  const saveTabs = useCallback((value: Tabs) => {
    setSettings((current) => (current ? { ...current, tabs: value } : current));
    schedule(tabs.current, value, SettingsService.SetTabs);
  }, []);

  const value = useMemo<SettingsContextValue>(
    () => ({ settings, savePanes, saveTabs, reload }),
    [settings, savePanes, saveTabs, reload],
  );

  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>;
}

function schedule<T>(writer: Writer<T>, value: T, write: (value: T) => unknown): void {
  writer.pending = value;
  if (writer.timer !== null) clearTimeout(writer.timer);
  writer.timer = setTimeout(() => {
    writer.timer = null;
    const pending = writer.pending;
    writer.pending = null;
    if (pending !== null) void write(pending);
  }, SAVE_DEBOUNCE_MS);
}

export function useSettings(): SettingsContextValue {
  const value = useContext(SettingsContext);
  if (!value) throw new Error("useSettings must be used inside a SettingsProvider");
  return value;
}
