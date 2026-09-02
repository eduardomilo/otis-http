/**
 * Platform differences the UI has to show. The OS comes from the Wails
 * runtime rather than the user agent, so it is the process's real platform.
 */

import { System } from "@wailsio/runtime";

/** True when running on macOS. */
export const isMac = System.IsMac();

/** True when running on Windows. */
export const isWindows = System.IsWindows();

/** What the platform calls its file manager, for the "Reveal in …" menu item. */
export const fileManagerName = isMac ? "Finder" : isWindows ? "Explorer" : "file manager";

/**
 * The label for the primary modifier in hint text: the Command glyph on
 * macOS, "Ctrl" everywhere else (DESIGN-NOTES §3 renders hints in mono 10px).
 */
export const modKey = isMac ? "⌘" : "Ctrl";

/** The Shift glyph, which macOS writes as a symbol and other platforms spell. */
export const shiftKey = isMac ? "⇧" : "Shift";

/**
 * Formats a keyboard hint: `hint("K")` is "⌘K" or "Ctrl+K", `hint("T", true)`
 * is "⌘⇧T" or "Ctrl+Shift+T".
 */
export function hint(key: string, shift = false): string {
  if (isMac) return `${modKey}${shift ? shiftKey : ""}${key}`;
  return `${modKey}+${shift ? "Shift+" : ""}${key}`;
}

/**
 * True when the event carries the platform's primary modifier: Command on
 * macOS, Control elsewhere. Checking metaKey on Windows would catch the
 * Windows key, and ctrlKey on macOS is a different chord entirely.
 */
export function hasModifier(event: KeyboardEvent | MouseEvent): boolean {
  return isMac ? event.metaKey : event.ctrlKey;
}
