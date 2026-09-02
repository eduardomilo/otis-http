/**
 * Platform differences the UI has to show.
 *
 * The OS comes from the Wails runtime rather than the user agent, so it is the
 * process's real platform — but it cannot be read at module load. `IsMac()` and
 * friends read `window._wails.environment`, which the runtime script fills in,
 * and the bundle's modules can evaluate before it does. A value captured at
 * import time is therefore `false` whenever the race goes the wrong way, and
 * stays `false` for the life of the window.
 *
 * That is not cosmetic. `hasModifier` decides whether a shortcut looks for
 * Command or Control, so losing the platform loses ⌘S, ⌘W, ⌘K and every other
 * chord at once. So nothing here is a constant: the platform is looked up on
 * use, cached only once the runtime has actually answered, and falls back to
 * the user agent until then rather than to "not macOS".
 */

import { System } from "@wailsio/runtime";

type Platform = "mac" | "windows" | "linux";

/** The runtime's answer, once it has one. */
let resolved: Platform | undefined;

/** What the Wails runtime says, or undefined before it is ready. */
function fromRuntime(): Platform | undefined {
  if (System.IsMac()) return "mac";
  if (System.IsWindows()) return "windows";
  if (System.IsLinux()) return "linux";
  return undefined;
}

/**
 * A guess from the user agent, for the window between the first render and the
 * runtime being ready. It is never cached: the runtime is still the authority
 * and gets to correct this as soon as it can answer.
 */
function fromNavigator(): Platform {
  const ua = navigator.userAgent;
  if (/Mac|iPhone|iPad/.test(ua)) return "mac";
  if (/Win/.test(ua)) return "windows";
  return "linux";
}

/** The platform this window is running on. */
export function platform(): Platform {
  if (resolved) return resolved;
  resolved = fromRuntime();
  return resolved ?? fromNavigator();
}

/** True when running on macOS. */
export function isMac(): boolean {
  return platform() === "mac";
}

/** True when running on Windows. */
export function isWindows(): boolean {
  return platform() === "windows";
}

/** What the platform calls its file manager, for the "Reveal in …" menu item. */
export function fileManagerName(): string {
  switch (platform()) {
    case "mac":
      return "Finder";
    case "windows":
      return "Explorer";
    case "linux":
      return "file manager";
  }
}

/**
 * The label for the primary modifier in hint text: the Command glyph on
 * macOS, "Ctrl" everywhere else (DESIGN-NOTES §3 renders hints in mono 10px).
 */
export function modKey(): string {
  return isMac() ? "⌘" : "Ctrl";
}

/** The Shift glyph, which macOS writes as a symbol and other platforms spell. */
export function shiftKey(): string {
  return isMac() ? "⇧" : "Shift";
}

/**
 * Formats a keyboard hint: `hint("K")` is "⌘K" or "Ctrl+K", `hint("T", true)`
 * is "⌘⇧T" or "Ctrl+Shift+T".
 */
export function hint(key: string, shift = false): string {
  if (isMac()) return `⌘${shift ? "⇧" : ""}${key}`;
  return `Ctrl+${shift ? "Shift+" : ""}${key}`;
}

/**
 * True when the event carries the platform's primary modifier: Command on
 * macOS, Control elsewhere. Checking metaKey on Windows would catch the
 * Windows key, and ctrlKey on macOS is a different chord entirely.
 */
export function hasModifier(event: KeyboardEvent | MouseEvent): boolean {
  return isMac() ? event.metaKey : event.ctrlKey;
}
