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

import { UnsavedChangesDialog, type DiscardChoice } from "@/components/request/unsaved-changes-dialog";
import { OtisEvent } from "@/lib/events.gen";
import { useCollection } from "@/state/collection-context";
import { useTabs } from "@/state/tabs-context";
import { ScriptService } from "@bindings/internal/services";
import type { ScriptDocument } from "@bindings/internal/services";

/**
 * Open script documents — the `.js` files of docs/FORMAT.md §2.4.
 *
 * The same shape as `documents-context` and deliberately not the same
 * provider. What is being edited is different in kind: a request is a *parsed
 * model* that Go serializes back, and a script is **text**, written byte for
 * byte because Otis has no opinion about JavaScript formatting. Folding the
 * two together would mean a draft that is sometimes a `File` and sometimes a
 * string, and every consumer of `get()` learning which.
 *
 * What they do share is the tab strip: the dirty dot, ⌘S, and the question
 * asked before a dirty tab closes. That is why `addCloseGuard` takes a list —
 * with one slot, whichever provider mounted second replaced the other's guard
 * and a dirty tab of that kind closed without asking.
 */

export interface ScriptState {
  path: string;
  /** The file as Go last gave it. Null until the load completes. */
  loaded: ScriptDocument | null;
  /** The text the editor is editing. Null until the load completes. */
  draft: string | null;
  dirty: boolean;
  busy: boolean;
  error: string | null;
  /** The file as it now stands on disk, when it changed under unsaved edits. */
  conflict: ScriptDocument | null;
}

interface ScriptsContextValue {
  get: (path: string) => ScriptState | undefined;
  /** Loads a script if it is not already loaded. Safe to call every render. */
  open: (path: string) => void;
  edit: (path: string, text: string) => void;
  save: (path: string) => Promise<boolean>;
  /** Writes the active tab's script, if it is one and it is dirty. */
  saveActive: () => Promise<boolean>;
  reload: (path: string) => Promise<void>;
  keepMine: (path: string) => void;
}

const ScriptsContext = createContext<ScriptsContextValue | null>(null);

const LOADING: Omit<ScriptState, "path"> = {
  loaded: null,
  draft: null,
  dirty: false,
  busy: true,
  error: null,
  conflict: null,
};

interface DiscardRequest {
  path: string;
  resolve: (choice: DiscardChoice) => void;
}

export function ScriptsProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const { activePath, setDirty, addCloseGuard } = useTabs();
  const [scripts, setScripts] = useState<Record<string, ScriptState>>({});
  const [discard, setDiscard] = useState<DiscardRequest | null>(null);

  const saving = useRef(new Set<string>());
  const latest = useRef(scripts);
  latest.current = scripts;

  const setOne = useCallback((path: string, patch: Partial<ScriptState>) => {
    setScripts((current) =>
      current[path] ? { ...current, [path]: { ...current[path], ...patch } } : current,
    );
  }, []);

  const load = useCallback(
    async (path: string) => {
      try {
        const doc = await ScriptService.Load(path);
        setScripts((current) => ({
          ...current,
          [path]: {
            path,
            loaded: doc,
            draft: doc?.text ?? "",
            dirty: false,
            busy: false,
            error: null,
            conflict: null,
          },
        }));
      } catch (err) {
        setOne(path, { busy: false, error: messageOf(err) });
      }
    },
    [setOne],
  );

  const open = useCallback(
    (path: string) => {
      if (latest.current[path]) return;
      setScripts((current) =>
        current[path] ? current : { ...current, [path]: { path, ...LOADING } },
      );
      void load(path);
    },
    [load],
  );

  const edit = useCallback((path: string, text: string) => {
    setScripts((current) => {
      const existing = current[path];
      if (!existing || existing.draft === null || text === existing.draft) return current;
      return { ...current, [path]: { ...existing, draft: text, dirty: text !== existing.loaded?.text } };
    });
  }, []);

  const save = useCallback(
    async (path: string): Promise<boolean> => {
      const existing = latest.current[path];
      if (existing?.draft === null || existing === undefined || existing.busy) return false;
      saving.current.add(path);
      setOne(path, { busy: true, error: null });
      try {
        const doc = await ScriptService.Save(path, existing.draft);
        setScripts((current) =>
          current[path]
            ? {
                ...current,
                [path]: {
                  ...current[path],
                  loaded: doc,
                  // The draft stays as it is rather than taking what came
                  // back: Go wrote these bytes verbatim, so the two are the
                  // same text, and replacing it would move the caret.
                  dirty: false,
                  busy: false,
                  error: null,
                  conflict: null,
                },
              }
            : current,
        );
        return true;
      } catch (err) {
        setOne(path, { busy: false, error: messageOf(err) });
        return false;
      } finally {
        saving.current.delete(path);
      }
    },
    [setOne],
  );

  const saveActive = useCallback(() => {
    const existing = activePath ? latest.current[activePath] : undefined;
    if (!existing?.dirty) return Promise.resolve(false);
    return save(activePath);
  }, [activePath, save]);

  const reload = useCallback(
    async (path: string) => {
      setOne(path, { busy: true, error: null, conflict: null });
      await load(path);
    },
    [load, setOne],
  );

  const keepMine = useCallback((path: string) => {
    setScripts((current) => {
      const existing = current[path];
      if (!existing?.conflict) return current;
      return {
        ...current,
        [path]: {
          ...existing,
          loaded: existing.conflict,
          conflict: null,
          dirty: existing.draft !== existing.conflict.text,
        },
      };
    });
  }, []);

  const forget = useCallback((path: string) => {
    setScripts((current) => {
      if (!current[path]) return current;
      const next = { ...current };
      delete next[path];
      return next;
    });
  }, []);

  /** A change on disk, handled exactly as a request document handles one. */
  const reconcile = useCallback(
    async (path: string) => {
      let fresh: ScriptDocument;
      try {
        fresh = await ScriptService.Load(path);
      } catch (err) {
        setOne(path, { error: messageOf(err) });
        return;
      }
      setScripts((current) => {
        const existing = current[path];
        if (!existing?.loaded || saving.current.has(path)) return current;
        if (fresh.text === existing.loaded.text) {
          // The text is the same; what it *is* may not be. A `.pre.js`
          // becomes a hook the moment the request beside it exists
          // (docs/FORMAT.md §2.4), so the header has to follow that.
          return { ...current, [path]: { ...existing, loaded: fresh } };
        }
        if (!existing.dirty) {
          return { ...current, [path]: { ...existing, loaded: fresh, draft: fresh.text, conflict: null } };
        }
        return { ...current, [path]: { ...existing, conflict: fresh } };
      });
    },
    [setOne],
  );

  useEffect(() => {
    return Events.On(OtisEvent.CollectionChanged, () => {
      for (const state of Object.values(latest.current)) {
        if (saving.current.has(state.path) || !state.loaded) continue;
        void reconcile(state.path);
      }
    });
  }, [reconcile]);

  useEffect(() => {
    for (const state of Object.values(scripts)) setDirty(state.path, state.dirty);
  }, [scripts, setDirty]);

  useEffect(() => {
    if (collection && !collection.path) setScripts({});
  }, [collection]);

  useEffect(() => {
    return addCloseGuard(async (path) => {
      const existing = latest.current[path];
      // Not a script tab, or a clean one. `forget` is a no-op for a path this
      // provider never held, which is what lets both guards coexist.
      if (!existing?.dirty) {
        forget(path);
        return true;
      }
      const choice = await new Promise<DiscardChoice>((resolve) => setDiscard({ path, resolve }));
      if (choice === "cancel") return false;
      if (choice === "save" && !(await save(path))) return false;
      forget(path);
      return true;
    });
  }, [addCloseGuard, save, forget]);

  const value = useMemo<ScriptsContextValue>(
    () => ({ get: (path) => scripts[path], open, edit, save, saveActive, reload, keepMine }),
    [scripts, open, edit, save, saveActive, reload, keepMine],
  );

  return (
    <ScriptsContext.Provider value={value}>
      {children}
      <UnsavedChangesDialog
        path={discard?.path ?? null}
        onAnswer={(choice) => {
          discard?.resolve(choice);
          setDiscard(null);
        }}
      />
    </ScriptsContext.Provider>
  );
}

export function useScripts(): ScriptsContextValue {
  const value = useContext(ScriptsContext);
  if (!value) throw new Error("useScripts must be used inside a ScriptsProvider");
  return value;
}

/** A binding error, without the "RuntimeError:" the runtime prefixes. */
function messageOf(err: unknown): string {
  return String(err).replace(/^RuntimeError:\s*/, "");
}
