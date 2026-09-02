import { createFileRoute } from "@tanstack/react-router";

import { Placeholder } from "@/components/shell/placeholder";

export const Route = createFileRoute("/diff/$path")({
  component: DiffView,
});

/** The git diff view (screen 1b). */
function DiffView() {
  const { path } = Route.useParams();
  return <Placeholder kind="Diff" path={path} phase="Phase D" />;
}
