import { cn } from "@/lib/utils";
import type { Hunk, Line } from "@bindings/internal/diff";

/**
 * One hunk, in unified or split view.
 *
 * Geometry is DESIGN-NOTES §4.6: two 44px line-number gutters (old, new) with
 * a 1px divider after the second, then a 24px centred sign column, then the
 * text. Line numbers are `--fg-ghost` and unselectable. Colours are §2.6 —
 * added lines `#d1fae5` on an accent wash, removed `#fecaca` on a red wash,
 * context `--fg-muted`.
 *
 * The text is not syntax-highlighted. A diff of a `.http` file reads as
 * ordinary text, which is the point the whole product is arguing, and colour
 * here already means added/removed; a second colour channel over the top of
 * that would fight it.
 */

const GUTTER = "w-11 shrink-0 select-none pr-2.5 text-right font-mono text-code text-fg-ghost";

export function HunkView({
  hunk,
  split,
  header,
}: {
  hunk: Hunk;
  split: boolean;
  header: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      {header}
      {split ? <SplitLines hunk={hunk} /> : <UnifiedLines hunk={hunk} />}
    </div>
  );
}

function UnifiedLines({ hunk }: { hunk: Hunk }) {
  return (
    <div className="min-w-0">
      {(hunk.lines ?? []).map((line, at) => (
        <div key={at} className={cn("flex min-w-0 items-start", background(line.kind))}>
          <span className={GUTTER}>{line.old || ""}</span>
          <span className={cn(GUTTER, "border-r border-border")}>{line.new || ""}</span>
          <span
            className={cn(
              "w-6 shrink-0 select-none text-center font-mono text-code",
              line.kind === "+" ? "text-primary" : line.kind === "-" ? "text-destructive" : "",
            )}
          >
            {line.kind === " " ? "" : line.kind}
          </span>
          <span className={cn("min-w-0 flex-1 pr-4 font-mono text-code whitespace-pre", text(line.kind))}>
            {line.text || " "}
          </span>
        </div>
      ))}
    </div>
  );
}

/**
 * The split view, which the design names in its segmented control but never
 * draws.
 *
 * A removed line and the added line that replaced it sit on one row, so the
 * change is a sideways comparison rather than a vertical one. Runs are paired
 * in order and the shorter side is padded, which is what every split diff
 * does; a cell with nothing on that side is left blank rather than filled,
 * because the line number that does not apply is zero (see diff.Line).
 */
function SplitLines({ hunk }: { hunk: Hunk }) {
  return (
    <div className="min-w-0">
      {pair(hunk.lines ?? []).map((row, at) => (
        <div key={at} className="flex min-w-0 items-start">
          <Side line={row.old} />
          <div className="w-px shrink-0 self-stretch bg-border" />
          <Side line={row.new} />
        </div>
      ))}
    </div>
  );
}

function Side({ line }: { line: Line | null }) {
  const number = line ? (line.kind === "+" ? line.new : line.old) : 0;
  return (
    <div className={cn("flex min-w-0 flex-1 items-start", line ? background(line.kind) : "bg-inset/40")}>
      <span className={GUTTER}>{number || ""}</span>
      <span className={cn("min-w-0 flex-1 pr-4 font-mono text-code whitespace-pre", line ? text(line.kind) : "")}>
        {line ? line.text || " " : " "}
      </span>
    </div>
  );
}

/** A row of the split view: what is on each side, either possibly absent. */
interface Row {
  old: Line | null;
  new: Line | null;
}

/**
 * Pairs a hunk's lines into split-view rows.
 *
 * Context goes on both sides. A run of removals followed by additions is
 * zipped — the first removed line beside the first added one — and whichever
 * run is longer leaves blank cells opposite its tail.
 */
function pair(lines: Line[]): Row[] {
  const rows: Row[] = [];
  let at = 0;
  while (at < lines.length) {
    const line = lines[at];
    if (line.kind === " ") {
      rows.push({ old: line, new: line });
      at++;
      continue;
    }
    const removed: Line[] = [];
    const added: Line[] = [];
    while (at < lines.length && lines[at].kind === "-") removed.push(lines[at++]);
    while (at < lines.length && lines[at].kind === "+") added.push(lines[at++]);
    for (let i = 0; i < Math.max(removed.length, added.length); i++) {
      rows.push({ old: removed[i] ?? null, new: added[i] ?? null });
    }
  }
  return rows;
}

/** §2.6: added on an accent wash, removed on a red wash. */
function background(kind: string): string {
  if (kind === "+") return "bg-[rgba(52,211,153,.08)]";
  if (kind === "-") return "bg-[rgba(248,113,113,.08)]";
  return "";
}

/** §2.6: the text colours that go with those washes. */
function text(kind: string): string {
  if (kind === "+") return "text-[#d1fae5]";
  if (kind === "-") return "text-[#fecaca]";
  return "text-fg-muted";
}
