import { createFileRoute } from "@tanstack/react-router";

import { Placeholder } from "@/components/shell/placeholder";

export const Route = createFileRoute("/r/$path")({
  component: RequestView,
});

/**
 * A request. $path is the node's collection-relative ID; TanStack encodes and
 * decodes the segment, so the param is the raw ID (see lib/paths.ts).
 */
function RequestView() {
  const { path } = Route.useParams();
  return <Placeholder kind="Request" path={path} phase="Phase C" />;
}
