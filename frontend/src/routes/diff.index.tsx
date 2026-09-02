import { useEffect } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";

import { useDiff } from "@/state/diff-context";

export const Route = createFileRoute("/diff/")({
  component: DiffIndex,
});

/**
 * The diff view with no file chosen (screen 1b).
 *
 * With changes to show it goes straight to the first one, which is what the
 * design draws — the screen never appears with the centre pane empty and a
 * populated list beside it. With nothing to show it says so, because "no
 * changes" and "not a repository" are both normal states, not errors.
 */
function DiffIndex() {
  const navigate = useNavigate();
  const { overview, changes } = useDiff();

  useEffect(() => {
    if (changes.length === 0) return;
    void navigate({ to: "/diff/$path", params: { path: changes[0].path }, replace: true });
  }, [changes, navigate]);

  return (
    <div className="flex h-full items-center justify-center px-8">
      <p className="max-w-[520px] text-center text-meta text-fg-dim">
        {!overview
          ? null
          : !overview.repository
            ? "This collection is not in a git repository. It still works perfectly well — a collection is a directory of files, and Otis only ever shows you what git thinks."
            : changes.length === 0
              ? "Nothing has changed since the last commit."
              : null}
      </p>
    </div>
  );
}
