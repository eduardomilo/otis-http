import { createFileRoute } from "@tanstack/react-router";

import { FolderView } from "@/components/folder/folder-view";

export const Route = createFileRoute("/f/$path")({
  component: FolderRoute,
});

/** A folder document (screen 3a). "" is the collection root. */
function FolderRoute() {
  const { path } = Route.useParams();
  return <FolderView path={path} />;
}
