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
 * Nothing fires while focus is in a text field or inside the command palette.
 * That is deliberate and specified: typing "b" in the filter input must not
 * toggle the sidebar, and ⌘W in the palette must not close a document behind
 * it. The cost is that the palette cannot be opened from inside the filter
 * input — press Escape first.
 */
export function useKeymap(bindings: Binding[]): void {
  // The handler is registered once and reads the latest bindings through a
  // ref, so re-rendering the shell does not detach and reattach the listener.
  const latest = useRef(bindings);
  latest.current = bindings;

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented || event.repeat) return;
      if (isTypingTarget(event.target)) return;

      for (const binding of latest.current) {
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

/**
 * True when the event target takes text, or sits inside an overlay that owns
 * the keyboard — the command palette and any other dialog.
 */
function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (target.isContentEditable) return true;
  return target.closest('[role="dialog"], [data-slot="command"]') !== null;
}
