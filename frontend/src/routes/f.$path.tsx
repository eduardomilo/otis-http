import { createFileRoute } from "@tanstack/react-router";

import { Placeholder } from "@/components/shell/placeholder";

export const Route = createFileRoute("/f/$path")({
  component: FolderView,
});

/** A folder document (screen 3a). */
function FolderView() {
  const { path } = Route.useParams();
  return <Placeholder kind="Folder" path={path || "(collection root)"} phase="Phase D" />;
}
