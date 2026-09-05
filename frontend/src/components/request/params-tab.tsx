import { Plus } from "lucide-react";

import { VariableText } from "@/components/request/variable-text";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { splitUrl, withParams, type QueryParam } from "@/lib/query";
import { cn } from "@/lib/utils";
import type { VariableIndex } from "@/lib/variables";
import { verbatimText } from "@/lib/text-input";

/**
 * The Params tab: the URL's query string as a table.
 *
 * There is no second place params live — the URL is the truth, and this is a
 * view of it. Editing a row rewrites the query string; typing in the URL bar
 * changes the rows. The round trip is in lib/query.ts, which keeps each
 * parameter's original text so an untouched row is written back byte for byte
 * and the order the URL had is the order it keeps.
 *
 * **No enable checkbox.** The design does not draw this table (screen 1a shows
 * only the tab label and its count), and a query parameter has exactly one
 * place it can be written down: the URL. So "disabled" would have to live in
 * memory, vanish when the tab is switched, and be invisible in the diff — the
 * same objection DESIGN-NOTES §9.5 raises about disabling a local header.
 * Removing a parameter is the operation that survives a save, and it is in the
 * row menu.
 *
 * The grid is the request-header geometry of §4.5 — `24px 190px 1fr 56px`,
 * 28px rows — with the leading cell empty, so the key and value columns sit on
 * the same axis as the Headers tab and switching tabs shifts nothing.
 */

const GRID = "grid grid-cols-[24px_190px_1fr_56px] items-center gap-3";

export function ParamsTab({
  url,
  index,
  onUrlChange,
}: {
  url: string;
  index: VariableIndex;
  onUrlChange: (url: string) => void;
}) {
  const { params } = splitUrl(url);

  const write = (next: QueryParam[]) => onUrlChange(withParams(url, next));
  const patch = (at: number, change: Partial<QueryParam>) =>
    write(
      params.map((param, i) =>
        // An edited row loses its source text, so it is re-encoded rather than
        // written back verbatim.
        i === at ? { ...param, ...change, raw: undefined } : param,
      ),
    );

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto">
      <div
        className={cn(
          GRID,
          "sticky top-0 z-10 h-[26px] shrink-0 border-b border-border bg-background text-label text-fg-dim",
        )}
      >
        <span />
        <span>Key</span>
        <span>Value</span>
        <span />
      </div>

      {params.length === 0 ? (
        <p className="px-1 pt-2 text-meta text-fg-faint">
          No query parameters. Anything added here is written into the URL.
        </p>
      ) : (
        params.map((param, at) => (
          <div
            key={at}
            className={cn(
              GRID,
              "group h-[28px] shrink-0 border-b border-border-hairline hover:bg-inset",
            )}
          >
            <span />
            <input
              {...verbatimText}
              value={param.name}
              onChange={(event) => patch(at, { name: event.target.value })}
              aria-label="Parameter name"
              className="w-full bg-transparent font-mono text-ui text-fg outline-none"
            />
            <ValueField
              value={param.value}
              index={index}
              onChange={(value) => patch(at, { value })}
            />
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label="Parameter actions"
                className="justify-self-end px-1 tracking-widest text-fg-ghost opacity-0 group-hover:opacity-100 data-[state=open]:opacity-100 hover:text-fg-muted"
              >
                ···
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                className="min-w-[160px] rounded-md border-border-control bg-raised"
              >
                <DropdownMenuItem
                  onSelect={() => write(params.filter((_, i) => i !== at))}
                  className="text-ui text-destructive"
                >
                  Remove parameter
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ))
      )}

      <button
        type="button"
        onClick={() => write([...params, { name: "", value: "", enabled: true }])}
        className="mt-2 ml-9 flex h-6 w-fit items-center gap-1.5 rounded-sm border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:text-fg-emphasis"
      >
        <Plus className="size-3" />
        Add parameter
      </button>
    </div>
  );
}

/** The same transparent-input-over-styled-text field the Headers tab uses. */
function ValueField({
  value,
  index,
  onChange,
}: {
  value: string;
  index: VariableIndex;
  onChange: (value: string) => void;
}) {
  return (
    <div className="relative min-w-0">
      <input
        {...verbatimText}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-label="Parameter value"
        className="peer w-full bg-transparent font-mono text-ui text-transparent caret-primary outline-none focus:text-fg-secondary"
      />
      <VariableText
        text={value}
        index={index}
        className="pointer-events-none absolute inset-0 truncate text-ui text-fg-secondary peer-focus:invisible"
      />
    </div>
  );
}
