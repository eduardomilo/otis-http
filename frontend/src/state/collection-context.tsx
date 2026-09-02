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
import type { CollectionInfo, Tree } from "@bindings/internal/services";
import type { State as GitState } from "@bindings/internal/git";
import { OtisEvent } from "@/lib/events.gen";
import { useSettings } from "@/state/settings-context";

/**
 * The open collection and its tree.
 *
 * Go owns both — CollectionService holds the current collection, walks it, and
 * watches the directory — so a file edited in another editor, a branch
 * switched in a terminal, a directory dropped on the window and a collection
 * reopened at launch all arrive here the same way. Nothing polls.
 */

interface CollectionContextValue {
  /** The open collection, or null while the first read is in flight. */
  collection: CollectionInfo | null;
  /** True once a collection is open. */
  isOpen: boolean;
  /** The tree, or null before the first walk completes. */
  tree: Tree | null;
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
  const [tree, setTree] = useState<Tree | null>(null);
  const [error, setError] = useState<string | null>(null);
  const restored = useRef(false);

  useEffect(() => {
    void CollectionService.Current().then(async (current) => {
      setCollection(current);
      // A collection Go already had open (a window reopened, a dropped
      // directory that arrived before this listener) still needs its tree.
      if (current.path) setTree(await CollectionService.Tree().catch(() => null));
    });

    const unsubscribes = [
      Events.On(OtisEvent.CollectionOpened, (event) => {
        setCollection(event.data);
        if (!event.data.path) setTree(null);
      }),
      // The watcher re-walked after a change on disk. The whole tree arrives,
      // git statuses included, so the rows and their dots never disagree.
      Events.On(OtisEvent.CollectionChanged, (event) => {
        setTree(event.data);
      }),
      // HEAD or the index moved: a commit, a stage, or a branch switch. Only
      // the git half changes, so the tree structure is left alone.
      Events.On(OtisEvent.GitChanged, (event) => {
        setTree((current) => (current ? withGit(current, event.data) : current));
      }),
    ];
    return () => unsubscribes.forEach((off) => off());
  }, []);

  const open = useCallback(async (path: string) => {
    try {
      const opened = await CollectionService.Open(path);
      setCollection(opened.collection);
      setTree(opened.tree);
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
    setTree(null);
  }, []);

  const value = useMemo<CollectionContextValue>(
    () => ({
      collection,
      isOpen: Boolean(collection?.path),
      tree,
      open,
      openViaDialog,
      close,
      error,
      clearError: () => setError(null),
    }),
    [collection, tree, open, openViaDialog, close, error],
  );

  return <CollectionContext.Provider value={value}>{children}</CollectionContext.Provider>;
}

export function useCollection(): CollectionContextValue {
  const value = useContext(CollectionContext);
  if (!value) throw new Error("useCollection must be used inside a CollectionProvider");
  return value;
}

/**
 * Replaces a tree's git state and re-stamps every node from it.
 *
 * A git-only change (a commit, a stage) does not touch the files, so re-
 * walking would be wasted work — but every row's status letter and every
 * folder's dirty dot has to move, and those live on the nodes.
 */
function withGit(tree: Tree, git: GitState): Tree {
  const statuses = git.statuses ?? {};
  const restamp = (node: Tree["root"]): Tree["root"] => {
    const children = (node.children ?? []).map(restamp);
    const settings = node.settings
      ? { ...node.settings, gitStatus: statuses[node.settings.path] ?? "" }
      : undefined;
    const gitStatus = statuses[node.path] ?? "";
    // Only a tracked change raises the dot on a folder; see propagates() in
    // internal/services/tree.go, which this has to agree with.
    const propagates = (status?: string) => status === "M" || status === "D";
    const modified =
      children.some((child) => child.modified || propagates(child.gitStatus)) ||
      propagates(settings?.gitStatus);
    return { ...node, children, settings, gitStatus, modified };
  };
  return { ...tree, git, root: restamp(tree.root) };
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
