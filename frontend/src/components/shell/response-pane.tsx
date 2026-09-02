/**
 * The response pane: a 34px header bar and the response body
 * (DESIGN-NOTES §4.1, screen 1a).
 *
 * Increment 8 builds the frame only. Sending a request and rendering what
 * comes back is Phase C.
 */
export function ResponsePane() {
  return (
    <div className="flex h-full flex-col bg-background">
      <div className="h-[var(--tab-bar-height)] shrink-0 border-b border-border" />
      <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
        <p className="text-meta text-fg-faint">No response.</p>
      </div>
    </div>
  );
}
