/**
 * A route that exists but has no content yet.
 *
 * Increment 8 wires every route in the shell so navigation, tabs and the
 * status bar can be exercised end to end; what each route actually renders
 * arrives in Phase C and D.
 */
export function Placeholder({ kind, path, phase }: { kind: string; path: string; phase: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-4">
      <p className="font-mono text-ui text-fg-secondary">{path}</p>
      <p className="text-meta text-fg-faint">
        {kind} · arrives in {phase}
      </p>
    </div>
  );
}
