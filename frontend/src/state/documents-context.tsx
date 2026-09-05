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
import { sameFile } from "@/lib/http-file";
import { useCollection } from "@/state/collection-context";
import { useEnvironments } from "@/state/environment-context";
import { useTabs } from "@/state/tabs-context";
import { RequestService } from "@bindings/internal/services";
import type { Document } from "@bindings/internal/services";
import type { File } from "@bindings/internal/httpfile";

/**
 * Open request documents.
 *
 * A document's draft lives here rather than in the route component because a
 * route unmounts when you switch tabs, and unsaved edits must survive that —
 * the tab bar's dirty dot is the promise that they do. Go holds the file; this
 * holds what the editor has done to it since.
 *
 * Nothing here touches disk. Loading, serializing and writing are all
 * RequestService calls, and the serializer that produces the bytes is Go's, so
 * a saved file has the one canonical layout of docs/FORMAT.md §1.13.
 */

/** What the editor needs to know about one open document. */
export interface DocumentState {
  path: string;
  /** The document as Go last gave it: the file on disk, plus its inheritance. */
  loaded: Document | null;
  /** The parsed model the editor is editing. Null until the load completes. */
  draft: File | null;
  /** True when the draft differs structurally from what was loaded. */
  dirty: boolean;
  /** True while a load or a save is in flight. */
  busy: boolean;
  /** A load or save failure, shown in place of the editor. */
  error: string | null;
  /**
   * The file as it now stands on disk, when it changed under unsaved edits.
   * Null when there is no conflict; the banner offers Reload or Keep mine.
   */
  conflict: Document | null;
}

interface DocumentsContextValue {
  /** The state of one document, or undefined before it is opened. */
  get: (path: string) => DocumentState | undefined;
  /** Loads a document if it is not already loaded. Safe to call every render. */
  open: (path: string) => void;
  /** Applies an edit to a draft. */
  edit: (path: string, fn: (file: File) => File) => void;
  /** Writes a draft. Resolves to true when the file was written. */
  save: (path: string) => Promise<boolean>;
  /** Writes the active tab's draft, if it has one and it is dirty. */
  saveActive: () => Promise<boolean>;
  /** Re-reads a document from disk, discarding unsaved edits. */
  reload: (path: string) => Promise<void>;
  /** Dismisses a conflict banner and keeps the draft. */
  keepMine: (path: string) => void;
  /** Drops a document's state. Called when its tab closes. */
  forget: (path: string) => void;
}

const DocumentsContext = createContext<DocumentsContextValue | null>(null);

/** The state of a document that has been asked for but not answered yet. */
const LOADING: Omit<DocumentState, "path"> = {
  loaded: null,
  draft: null,
  dirty: false,
  busy: true,
  error: null,
  conflict: null,
};

/** A pending "this tab has unsaved changes" question. */
interface DiscardRequest {
  path: string;
  resolve: (choice: DiscardChoice) => void;
}

export function DocumentsProvider({ children }: { children: ReactNode }) {
  const { collection } = useCollection();
  const { activePath, setDirty, addCloseGuard } = useTabs();
  const [documents, setDocuments] = useState<Record<string, DocumentState>>({});
  const [discard, setDiscard] = useState<DiscardRequest | null>(null);

  // The active environment; "" means the file and folder scopes only, which
  // is what "no environment active" means (docs/FORMAT.md §4.2).
  const { active: env } = useEnvironments();

  // Paths with a write in flight. A save re-walks the collection and announces
  // it, which would otherwise come back as a conflict with the very bytes that
  // were just written.
  const saving = useRef(new Set<string>());

  // The latest documents, for callbacks that must stay stable: the close guard
  // is installed once, and the change listener is attached once.
  const latest = useRef(documents);
  latest.current = documents;

  const setOne = useCallback((path: string, patch: Partial<DocumentState>) => {
    setDocuments((current) =>
      current[path] ? { ...current, [path]: { ...current[path], ...patch } } : current,
    );
  }, []);

  const load = useCallback(
    async (path: string) => {
      try {
        const doc = await RequestService.Load(path, env);
        setDocuments((current) => ({
          ...current,
          [path]: {
            path,
            loaded: doc,
            draft: doc.file ?? null,
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
    [env, setOne],
  );

  const open = useCallback(
    (path: string) => {
      if (latest.current[path]) return;
      setDocuments((current) =>
        current[path] ? current : { ...current, [path]: { path, ...LOADING } },
      );
      void load(path);
    },
    [load],
  );

  const edit = useCallback((path: string, fn: (file: File) => File) => {
    setDocuments((current) => {
      const existing = current[path];
      if (!existing?.draft) return current;
      const draft = fn(existing.draft);
      if (draft === existing.draft) return current;
      return {
        ...current,
        [path]: { ...existing, draft, dirty: !sameFile(draft, existing.loaded?.file) },
      };
    });
  }, []);

  const save = useCallback(
    async (path: string): Promise<boolean> => {
      const existing = latest.current[path];
      if (!existing?.draft || existing.busy) return false;
      saving.current.add(path);
      setOne(path, { busy: true, error: null });
      try {
        const doc = await RequestService.Save(path, env, existing.draft);
        setDocuments((current) =>
          current[path]
            ? {
                ...current,
                // The draft becomes what came back rather than staying as it
                // was: Go reparsed the file it wrote, so the line numbers and
                // the canonical ordering are now the ones on disk. Keeping the
                // pre-save draft would leave the document dirty against itself.
                [path]: {
                  ...current[path],
                  loaded: doc,
                  draft: doc.file ?? null,
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
    [env, setOne],
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
    setDocuments((current) => {
      const existing = current[path];
      if (!existing?.conflict) return current;
      // The conflict is dismissed, and the file it carried becomes the
      // baseline: the draft is now a change against what is on disk, not
      // against what was there when the tab opened. Without this, saving later
      // would overwrite the newer file without ever mentioning it again.
      return {
        ...current,
        [path]: {
          ...existing,
          loaded: existing.conflict,
          conflict: null,
          dirty: !sameFile(existing.draft, existing.conflict.file),
        },
      };
    });
  }, []);

  const forget = useCallback((path: string) => {
    setDocuments((current) => {
      if (!current[path]) return current;
      const next = { ...current };
      delete next[path];
      return next;
    });
  }, []);

  /**
   * A change on disk.
   *
   * The event carries the whole tree, not the paths that changed, so each open
   * document is re-read and compared: a handful of small files per debounced
   * batch, against a diff protocol that would have to be right about renames
   * and reorders before it saved anything (see events.CollectionChanged).
   *
   * A document with no unsaved edits takes the new file silently — that is
   * increment 9's live tree, applied to an open editor. A dirty one raises the
   * conflict banner and lets the user choose.
   */
  const reconcile = useCallback(
    async (path: string) => {
      let fresh: Document;
      try {
        fresh = await RequestService.Load(path, env);
      } catch (err) {
        // The file was deleted or renamed under us. The tab stays open with
        // its draft: saving it would recreate the file, which is a reasonable
        // thing to want and the user's decision, not this handler's.
        setOne(path, { error: messageOf(err) });
        return;
      }
      setDocuments((current) => {
        const existing = current[path];
        if (!existing?.loaded || saving.current.has(path)) return current;
        if (fresh.raw === existing.loaded.raw) {
          // This file did not change; something around it did. The
          // inheritance and the variable index may have moved with it, so
          // those are taken while the draft is left exactly as it is.
          return {
            ...current,
            [path]: { ...existing, loaded: { ...fresh, file: existing.loaded.file } },
          };
        }
        if (!existing.dirty) {
          return {
            ...current,
            [path]: { ...existing, loaded: fresh, draft: fresh.file ?? null, conflict: null },
          };
        }
        return { ...current, [path]: { ...existing, conflict: fresh } };
      });
    },
    [env, setOne],
  );

  useEffect(() => {
    return Events.On(OtisEvent.CollectionChanged, () => {
      for (const state of Object.values(latest.current)) {
        if (saving.current.has(state.path) || !state.loaded) continue;
        void reconcile(state.path);
      }
    });
  }, [reconcile]);

  // The tab bar's dirty dot (screen 1a: an amber dot in place of the ×).
  useEffect(() => {
    for (const state of Object.values(documents)) setDirty(state.path, state.dirty);
  }, [documents, setDirty]);

  // Closing a collection closes its documents with it.
  useEffect(() => {
    if (collection && !collection.path) setDocuments({});
  }, [collection]);

  // Closing a dirty tab asks first. The guard lives here because this is what
  // knows a document is dirty; the tab bar only knows the dot is on.
  useEffect(() => {
    return addCloseGuard(async (path) => {
      const existing = latest.current[path];
      // Not a request tab, or a clean one. `forget` is a no-op for a path
      // this provider never held, which is what lets several guards coexist.
      if (!existing?.dirty) {
        forget(path);
        return true;
      }
      const choice = await new Promise<DiscardChoice>((resolve) =>
        setDiscard({ path, resolve }),
      );
      if (choice === "cancel") return false;
      if (choice === "save" && !(await save(path))) return false;
      forget(path);
      return true;
    });
  }, [addCloseGuard, save, forget]);

  const value = useMemo<DocumentsContextValue>(
    () => ({
      get: (path) => documents[path],
      open,
      edit,
      save,
      saveActive,
      reload,
      keepMine,
      forget,
    }),
    [documents, open, edit, save, saveActive, reload, keepMine, forget],
  );

  return (
    <DocumentsContext.Provider value={value}>
      {children}
      <UnsavedChangesDialog
        path={discard?.path ?? null}
        onAnswer={(choice) => {
          discard?.resolve(choice);
          setDiscard(null);
        }}
      />
    </DocumentsContext.Provider>
  );
}

export function useDocuments(): DocumentsContextValue {
  const value = useContext(DocumentsContext);
  if (!value) throw new Error("useDocuments must be used inside a DocumentsProvider");
  return value;
}

/** One open document, loading it on first use. */
export function useDocument(path: string): DocumentState | undefined {
  const { get, open } = useDocuments();
  useEffect(() => {
    open(path);
  }, [path, open]);
  return get(path);
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
