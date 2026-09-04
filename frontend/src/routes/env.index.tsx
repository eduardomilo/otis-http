import { useState } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";

import { NewEnvironmentDialog } from "@/components/environment/environment-list";
import { Button } from "@/components/ui/button";
import { useEnvironments } from "@/state/environment-context";

export const Route = createFileRoute("/env/")({
  component: EnvironmentIndex,
});

/**
 * The environment editor with no environment named (screen 1c, before there
 * is one).
 *
 * It exists because "edit environments" has to lead somewhere when a
 * collection has none — which is every collection until somebody makes the
 * first one, and every collection the Postman importer creates from an export
 * with no environments. Before this, the palette's command was *hidden* in
 * that case and the title strip's "Edit environments…" navigated to `/`, so
 * there was no way to create the first environment in the window at all.
 *
 * With environments present it sends you to the first, so `/env` is a single
 * destination both callers can use without knowing which case they are in.
 */
function EnvironmentIndex() {
  const navigate = useNavigate();
  const { environments } = useEnvironments();
  const [creating, setCreating] = useState(false);

  const first = environments[0]?.name;
  if (first) {
    // Replace, so Back does not bounce off this route on the way out.
    void navigate({ to: "/env/$name", params: { name: first }, replace: true });
    return null;
  }

  return (
    <div className="flex h-full flex-col items-center justify-center px-8">
      <div className="max-w-[420px]">
        <h2 className="text-ui text-fg-emphasis">No environments yet</h2>
        <p className="mt-1 text-meta text-fg-dim">
          An environment is a JSON file in <span className="font-mono">env/</span> holding the
          values a request resolves <span className="font-mono">{"{{variables}}"}</span> against —
          a host, an account id, an API key. A collection does not need one, and requests that
          name no variables work without it.
        </p>
        <p className="mt-2 text-meta text-fg-faint">
          The file is committed with the collection. A value marked secret is stored per-machine
          in the OS keychain and is never written to it.
        </p>
        <Button
          type="button"
          onClick={() => setCreating(true)}
          className="mt-4 h-6 rounded-md border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:bg-selected"
        >
          New environment
        </Button>
      </div>
      <NewEnvironmentDialog open={creating} onOpenChange={setCreating} />
    </div>
  );
}
