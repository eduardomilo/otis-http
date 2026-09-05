import { createFileRoute } from "@tanstack/react-router";

import { ScriptEditor } from "@/components/script/script-editor";

export const Route = createFileRoute("/s/$path")({
  component: ScriptView,
});

/**
 * A `.js` file. $path is the node's collection-relative ID; TanStack encodes
 * and decodes the segment, so the param is the raw ID (see lib/paths.ts).
 */
function ScriptView() {
  const { path } = Route.useParams();
  // Keyed on the path, so switching scripts remounts the editor rather than
  // showing one file's text under another file's name for a frame.
  return <ScriptEditor key={path} path={path} />;
}
