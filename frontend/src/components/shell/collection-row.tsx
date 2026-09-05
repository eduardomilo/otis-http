import { Link } from "@tanstack/react-router";
import { FolderCog, Plus } from "lucide-react";

import { nodeLink } from "@/lib/paths";
import { cn } from "@/lib/utils";
import type { Node } from "@bindings/internal/services";

/**
 * The collection itself, as a row above the tree (DESIGN-NOTES §9.40).
 *
 * The root is where a whole collection's auth, headers and variables live —
 * it is what the Postman importer writes when an export has collection-level
 * auth — and it was reachable only from the command palette and from the
 * "Edit in the collection root" button on an Auth tab. §9.26 fixed those two
 * *working*; it left them the only ways in, on the grounds that the design
 * says the root is not a tree row and its name belongs in the title strip.
 * That turned out not to be enough: every other folder opens its settings by
 * being clicked in the sidebar, and the one folder that holds settings for
 * everything is the one you cannot click.
 *
 * So it is a row, and deliberately **not a tree row**: no chevron, no drag, no
 * context menu, no indent level of its own — the tree below still starts at
 * depth 0. It is a link that happens to look like the rows under it, which is
 * the whole point, plus the word `settings` because the row has one job and
 * nothing else in the sidebar says what a folder view is for.
 *
 * The `+` marker is the same one a folder row carries (§9.7): it means "this
 * folder has a `_folder.http`", so the row also answers whether the
 * collection has any shared settings yet.
 */
export function CollectionRow({
  name,
  root,
  active,
}: {
  /** The collection's display name, as the title strip shows it. */
  name: string;
  /** The root node, for the shared-settings marker. */
  root: Node | undefined;
  /** Whether the root folder view is what the centre pane is showing. */
  active: boolean;
}) {
  return (
    <Link
      {...nodeLink("folder", "")}
      title={
        root?.settings
          ? `The whole collection's shared settings, in ${root.settings.path}`
          : "The whole collection's shared auth, headers and variables"
      }
      className={cn(
        // Square, and the same selected treatment a tree row gets: it is not
        // one, but when it is what the centre pane is showing it has to read
        // as the selected row, or two things look selected at once.
        "flex h-[var(--row-height)] shrink-0 items-center gap-1.5 px-1 select-none",
        active
          ? "bg-selected text-fg-emphasis shadow-[inset_2px_0_0_var(--accent)]"
          : "text-fg-secondary hover:bg-control",
      )}
    >
      <FolderCog className="size-3 shrink-0 text-fg-dim" />
      <span className={cn("truncate text-ui", active ? "font-medium" : "text-fg-muted")}>
        {name}
      </span>
      {root?.settings ? (
        <Plus className="size-2.5 shrink-0 text-fg-ghost" aria-label="Has shared settings" />
      ) : null}
      <span className="ml-auto shrink-0 pl-2 text-micro text-fg-faint">settings</span>
    </Link>
  );
}
