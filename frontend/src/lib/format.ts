/**
 * Formatting the numbers the response pane shows.
 *
 * They are read at a glance and next to each other, so they are formatted for
 * comparison rather than precision: `184 ms`, `1.2 KB`, `14:32:07` (screen
 * 1a). Three significant figures at most, and a unit that changes with the
 * magnitude rather than a long number with a fixed one.
 */

/** A duration in milliseconds, as `184 ms`, `1.2 s`, `2.5 min`. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1) return `${Math.round(ms * 1000)} µs`;
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${trim(ms / 1000)} s`;
  return `${trim(ms / 60_000)} min`;
}

/** A byte count, as `0 B`, `1.2 KB`, `40.0 MB`. Powers of 1024. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${trim(value)} ${units[unit]}`;
}

/** A count of lines, thousands-separated: a 40 MB body has millions. */
export function formatCount(n: number): string {
  return n.toLocaleString();
}

/** The wall-clock time a response arrived, as `14:32:07`. */
export function formatClock(iso: string | undefined): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  return at.toLocaleTimeString(undefined, { hour12: false });
}

/** One decimal place, without a trailing `.0`. */
function trim(value: number): string {
  const rounded = value < 10 ? Math.round(value * 10) / 10 : Math.round(value);
  return String(rounded);
}

/**
 * The colour a status code takes.
 *
 * DESIGN-NOTES §2.4 gives the accent to "good" states and names `200 OK` as
 * one; §2.6 gives amber to warning and red to danger. 3xx is amber rather than
 * accent because a redirect is something to notice — the response shown is not
 * the one that was asked for.
 */
export function statusColor(code: number): string {
  if (code === 0) return "text-fg-dim";
  if (code < 300) return "text-primary";
  if (code < 400) return "text-warning";
  if (code < 500) return "text-destructive";
  return "text-destructive";
}
