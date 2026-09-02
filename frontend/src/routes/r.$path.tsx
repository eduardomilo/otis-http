import { createFileRoute } from "@tanstack/react-router";

import { RequestEditor } from "@/components/request/request-editor";

export const Route = createFileRoute("/r/$path")({
  component: RequestView,
});

/**
 * A request. $path is the node's collection-relative ID; TanStack encodes and
 * decodes the segment, so the param is the raw ID (see lib/paths.ts).
 */
function RequestView() {
  const { path } = Route.useParams();
  // Keyed on the path so switching documents remounts the editor rather than
  // re-using its panel selection and editor state for a different file.
  return <RequestEditor key={path} path={path} />;
}
