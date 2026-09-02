import { createFileRoute } from "@tanstack/react-router";

import { DiffView } from "@/components/diff/diff-view";

export const Route = createFileRoute("/diff/$path")({
  component: DiffFileView,
});

/** One file's diff (screen 1b). */
function DiffFileView() {
  const { path } = Route.useParams();
  return <DiffView path={path} />;
}
