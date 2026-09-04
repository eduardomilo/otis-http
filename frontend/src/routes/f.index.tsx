import { createFileRoute } from "@tanstack/react-router";

import { FolderView } from "@/components/folder/folder-view";

export const Route = createFileRoute("/f/")({
  component: RootFolderRoute,
});

/**
 * The collection root's folder view (screen 3a, with no folder name).
 *
 * The root is a folder whose node path is the empty string, and an empty
 * dynamic segment does not match `/f/$path` — the router answers Not Found.
 * So the root gets its own route, and `nodeLink("folder", "")` points here.
 *
 * It matters more than it looks: a root `_folder.http` is where inherited auth
 * for a whole collection lives, and it is what the Postman importer writes
 * when the export has collection-level auth. Without this route that file was
 * unreachable — the palette's "Open the collection root" 404'd, and so did the
 * Auth tab's link to where an inherited directive was declared.
 */
function RootFolderRoute() {
  return <FolderView path="" />;
}
