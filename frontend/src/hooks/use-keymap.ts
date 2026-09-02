import { useEffect, useRef } from "react";

import { hasModifier } from "@/lib/platform";

/**
 * One binding in the shell's key map.
 *
 * `key` is compared against KeyboardEvent.key, case-insensitively, so "w"
 * matches whether or not Shift is held. Digits are their own characters.
 */
export interface Binding {
  key: string;
  /** Requires the platform modifier: Command on macOS, Control elsewhere. */
  mod?: boolean;
  /** Requires Shift. When omitted, Shift must be *up*. */
  shift?: boolean;
  run: () => void;
}

/**
 * Registers the shell's keyboard shortcuts on the window.
 *
 * There is exactly one of these, in the shell. Components do not bind their
 * own shortcuts: a key map spread across the tree cannot be reasoned about,
 * and two components would eventually claim the same chord.
 *
 * Two rules about focus, and they are different rules:
 *
 *   - **Text fields.** A binding that needs the platform modifier fires
 *     wherever focus is; one that does not, does not fire in a text field. A
 *     chord cannot be mistaken for typing, but a bare "b" can, and the sidebar
 *     must not toggle while somebody names a header. Phase B excluded fields
 *     outright, which was free when no pane was an editor; from increment 10
 *     the centre pane is one, and ⌘W or ⌘S that stop working once the caret is
 *     in the body would just read as broken.
 *
 *   - **Overlays.** Nothing fires inside the command palette or a dialog,
 *     modifier or not: an overlay owns the keyboard while it is open, and ⌘W
 *     there must not close a document behind it. The cost is that the palette
 *     cannot be reopened from inside itself — press Escape first.
 *
 * CodeMirror is the one place a shortcut is bound in a second place. Its
 * content is contenteditable and it handles keys before they reach the window,
 * so the request editor hands ⌘S and ⌘↵ to the editor as a CodeMirror keymap
 * calling the same functions this map calls. That is one shortcut with one
 * behaviour reached by two routes, not a second key map.
 */
export function useKeymap(bindings: Binding[]): void {
  // The handler is registered once and reads the latest bindings through a
  // ref, so re-rendering the shell does not detach and reattach the listener.
  const latest = useRef(bindings);
  latest.current = bindings;

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented || event.repeat) return;
      const target = event.target;
      if (isOverlay(target)) return;
      const typing = isTextField(target);

      for (const binding of latest.current) {
        if (typing && !binding.mod) continue;
        if (event.key.toLowerCase() !== binding.key.toLowerCase()) continue;
        if (Boolean(binding.mod) !== hasModifier(event)) continue;
        if (Boolean(binding.shift) !== event.shiftKey) continue;
        if (event.altKey) continue;
        event.preventDefault();
        binding.run();
        return;
      }
    }

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);
}

/** True when the event target takes text. */
function isTextField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  return target.isContentEditable;
}

/**
 * True when the target sits inside an overlay that owns the keyboard — the
 * command palette or any dialog. No binding fires there, modifier or not.
 */
function isOverlay(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.closest('[role="dialog"], [data-slot="command"]') !== null;
}
