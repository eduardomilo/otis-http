import { ArrowDownToLine } from "lucide-react";

import { VariableText } from "@/components/request/variable-text";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  disableInheritedHeader,
  headersOf,
  INHERIT_MARKER,
  isInheritDisabled,
  overrideHeader,
  removeHeaderAt,
  removeHeadersNamed,
  setHeaderAt,
} from "@/lib/http-file";
import { cn } from "@/lib/utils";
import type { VariableIndex } from "@/lib/variables";
import { verbatimText } from "@/lib/text-input";
import type { Header, Request } from "@bindings/internal/httpfile";
import type { AuthHeader, Document } from "@bindings/internal/services";
import type { Inherited } from "@bindings/internal/resolve";

/**
 * The Headers tab (screen 4a) — the clearest statement of the inheritance
 * model in the design.
 *
 * Two groups in one table. THIS REQUEST lists the file's own headers and edits
 * them; INHERITED lists what every folder above offered, each row naming the
 * file it came from, with Override and Off.
 *
 * Two things the design does not draw, both of which fall out of increment 10
 * being able to *undo* what it writes:
 *
 *   - An inherited row that a nearer level overrode or switched off is still a
 *     row, marked with what happened to it. Screen 4a shows a request where
 *     nothing has been overridden yet, so it has no such row — but if Off only
 *     removed the row, there would be no way back from `!inherit` except
 *     editing the file by hand.
 *   - An `!inherit` marker in THIS REQUEST is drawn as an unchecked row rather
 *     than as a header with the literal value `!inherit`, because that is what
 *     it means (docs/FORMAT.md §3.2) and unchecking is how the user got there.
 *
 * Table geometry is DESIGN-NOTES §4.5: `24px 190px 1fr 56px`, 28px rows, a
 * 26px heading row; group labels are §8.6's uppercase micro-labels.
 */

const GRID = "grid grid-cols-[24px_190px_1fr_56px] items-center gap-3";

export function HeadersTab({
  document,
  entry,
  index,
  onEdit,
}: {
  document: Document;
  entry: Request;
  index: VariableIndex;
  onEdit: (fn: (entry: Request) => Request) => void;
}) {
  const local = headersOf(entry);
  const inherited = document.inherited ?? [];
  const auth = document.authHeader;
  // The auth row belongs to whichever group its @auth came from (screen 4a
  // puts an inherited one under INHERITED with an AUTH tag).
  const localAuth = auth?.local ? auth : null;
  const inheritedAuth = auth && !auth.local ? auth : null;

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

      <GroupLabel label="This request" path={document.path} />
      {local.length === 0 && !localAuth ? (
        <Empty>No headers in this file.</Empty>
      ) : (
        local.map((header, at) => (
          <LocalRow
            key={`${header.name}-${at}`}
            header={header}
            index={index}
            onChange={(patch) => onEdit((e) => setHeaderAt(e, at, patch))}
            onRemove={() => onEdit((e) => removeHeaderAt(e, at))}
          />
        ))
      )}
      {localAuth ? <AuthRow auth={localAuth} index={index} /> : null}

      {inherited.length > 0 || inheritedAuth ? (
        <>
          {/* §2.2: a dashed rule marks "this is a different kind of thing
              from what is above it". */}
          <div className="mt-2 flex items-center gap-2 border-t border-dashed border-border-control pt-2">
            <div className={cn(GRID, "flex-1")}>
              <ArrowDownToLine className="size-3 justify-self-end text-fg-dim" />
              <span className="text-label tracking-[.06em] text-fg-dim uppercase">Inherited</span>
              <span className="font-mono text-label text-fg-faint">
                {inheritedSource(inherited, inheritedAuth)}
              </span>
              <span className="justify-self-end text-meta whitespace-nowrap text-fg-faint">
                Added at send time
              </span>
            </div>
          </div>
          {inherited.map((row, at) => (
            <InheritedRow
              key={`${row.source.path}-${row.name}-${at}`}
              row={row}
              index={index}
              requestPath={document.path}
              disabledHere={isInheritDisabled(entry, row.name)}
              onEdit={onEdit}
            />
          ))}
          {inheritedAuth ? <AuthRow auth={inheritedAuth} index={index} /> : null}
        </>
      ) : null}

      <p className="mt-3 max-w-[640px] px-1 text-meta text-fg-dim">
        Override copies the header into this file with its current value; the folder entry stops
        applying here. Off records{" "}
        <code className="rounded-sm bg-control px-1 font-mono text-fg-secondary">
          Header: {INHERIT_MARKER}
        </code>{" "}
        in this file so the change is visible in the diff.
      </p>
    </div>
  );
}

/** §8.6: a 10px uppercase label with the relevant file path beside it. */
function GroupLabel({ label, path }: { label: string; path: string }) {
  return (
    <div className={cn(GRID, "h-[26px] shrink-0")}>
      <span />
      <span className="text-label tracking-[.06em] text-fg-dim uppercase">{label}</span>
      <span className="font-mono text-label text-fg-faint">{path}</span>
      <span />
    </div>
  );
}

/**
 * One of the file's own headers.
 *
 * The enable checkbox is **disabled**, and says why on hover.
 *
 * DESIGN-NOTES §9.5 is explicit that this is unresolved: the design puts a
 * checkbox on every row, including local ones, and docs/FORMAT.md defines no
 * way to write a disabled header into a `.http` file. `!inherit` is not it —
 * §3.2 gives that value one meaning, "remove the inherited header of this
 * name", and using it for a local header that shadows nothing would be
 * inventing syntax and would lose the value that was there. So the checkbox
 * renders, in its checked state, and does nothing; removing the header is in
 * the row menu, which is an operation the format does have.
 *
 * A row that *is* an `!inherit` marker is a different thing and is drawn as
 * one: struck through, with the way back offered on the matching inherited row.
 */
function LocalRow({
  header,
  index,
  onChange,
  onRemove,
}: {
  header: Header;
  index: VariableIndex;
  onChange: (patch: Partial<Header>) => void;
  onRemove: () => void;
}) {
  const marker = header.value.trim() === INHERIT_MARKER;
  return (
    <div
      className={cn(GRID, "group h-[28px] shrink-0 border-b border-border-hairline hover:bg-inset")}
    >
      <Checkbox
        checked={!marker}
        disabled
        aria-label={`${header.name} enabled`}
        title={
          marker
            ? `${header.name}: ${INHERIT_MARKER} — the inherited header is switched off; turn it back on in the Inherited group`
            : "A .http file has no way to write a disabled local header (docs/FORMAT.md, DESIGN-NOTES §9.5). Remove it from the row menu instead."
        }
        className="justify-self-end disabled:opacity-100"
      />
      <input
        {...verbatimText}
        value={header.name}
        onChange={(event) => onChange({ name: event.target.value })}
        aria-label="Header name"
        className={cn(
          "w-full bg-transparent font-mono text-ui outline-none",
          marker ? "text-fg-faint line-through" : "text-fg",
        )}
      />
      {marker ? (
        <span className="truncate text-meta text-fg-faint italic">
          {INHERIT_MARKER} · the inherited header of this name is not sent
        </span>
      ) : (
        <ValueField value={header.value} index={index} onChange={(value) => onChange({ value })} />
      )}
      <RowMenu onRemove={onRemove} />
    </div>
  );
}

/**
 * An editable value that still shows its `{{variable}}` tokens.
 *
 * The styled text sits under a transparent input rather than in it: an input
 * cannot hold spans, and mounting a CodeMirror per table row to get one token
 * coloured would cost more than the whole tab. Both layers are the same mono
 * 12px on the same box, so the glyphs line up; the styled layer hides while
 * the field has focus, so a caret is never drawn over a duplicate.
 */
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
        aria-label="Header value"
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

/**
 * One header a folder above offered, with what became of it.
 *
 * Override and Off are the chips of §4.4. A row this file switched off swaps
 * them for the way back, so nothing this tab writes is a one-way door. A row
 * some *other* level settled is read-only here: changing it means editing that
 * level, and the row says which one.
 */
function InheritedRow({
  row,
  index,
  requestPath,
  disabledHere,
  onEdit,
}: {
  row: Inherited;
  index: VariableIndex;
  /** This file's node path, to tell "off here" from "off further up". */
  requestPath: string;
  /** True when this file carries the `!inherit` marker for the row's name. */
  disabledHere: boolean;
  onEdit: (fn: (entry: Request) => Request) => void;
}) {
  const sent = row.state === "sent";
  const settledHere = row.by?.path === requestPath;
  // Only what this file decided can be undone from this file.
  const editable = sent || (row.state === "off" && disabledHere);

  return (
    <div className={cn(GRID, "h-[28px] shrink-0 border-b border-border-hairline hover:bg-inset")}>
      <Checkbox
        checked={sent}
        disabled={!editable}
        onCheckedChange={(checked) =>
          onEdit((e) =>
            checked ? removeHeadersNamed(e, row.name) : disableInheritedHeader(e, row.name),
          )
        }
        aria-label={`${row.name} enabled`}
        className="justify-self-end"
      />
      <span
        className={cn(
          "truncate font-mono text-ui",
          sent ? "text-fg-muted" : "text-fg-faint line-through",
        )}
        title={row.name}
      >
        {row.name}
      </span>
      <div className="flex min-w-0 items-center gap-2">
        <VariableText
          text={row.value}
          index={index}
          className={cn("truncate text-ui", sent ? "text-fg-dim" : "text-fg-ghost")}
        />
        <span className="shrink-0 font-mono text-label text-fg-faint" title={row.source.path}>
          {row.source.path}
        </span>
        {sent ? null : (
          <span className="shrink-0 text-meta whitespace-nowrap text-fg-faint italic">
            {row.state === "off" ? "off" : "overridden"}
            {row.by ? (settledHere ? " here" : ` in ${row.by.path}`) : ""}
          </span>
        )}
      </div>
      <div className="flex items-center justify-end gap-1">
        {sent ? (
          <>
            <Chip onClick={() => onEdit((e) => overrideHeader(e, row.name, row.value))}>
              Override
            </Chip>
            <Chip onClick={() => onEdit((e) => disableInheritedHeader(e, row.name))}>Off</Chip>
          </>
        ) : editable ? (
          <Chip onClick={() => onEdit((e) => removeHeadersNamed(e, row.name))}>Inherit</Chip>
        ) : null}
      </div>
    </div>
  );
}

/**
 * The `Authorization` header an `@auth` becomes at send time, tagged AUTH
 * (screen 4a). It is a row rather than a hidden extra because it is on the
 * wire, and the tab's whole claim is that it lists what will be sent — but it
 * is not editable here: the Auth tab is where `@auth` is written.
 */
function AuthRow({ auth, index }: { auth: AuthHeader; index: VariableIndex }) {
  return (
    <div className={cn(GRID, "h-[28px] shrink-0 border-b border-border-hairline hover:bg-inset")}>
      <Checkbox checked disabled aria-label="Authorization enabled" className="justify-self-end" />
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-mono text-ui text-fg-muted">{auth.name}</span>
        {/* §3: a 9px sans micro-tag with .06em tracking. */}
        <span className="shrink-0 rounded-sm border border-border-control px-1 text-micro tracking-[.06em] text-fg-dim">
          AUTH
        </span>
      </span>
      <div className="flex min-w-0 items-center gap-2">
        <VariableText text={auth.value} index={index} className="truncate text-ui text-fg-dim" />
        <span className="shrink-0 font-mono text-label text-fg-faint">
          {auth.source.path} · Auth tab
        </span>
      </div>
      <span />
    </div>
  );
}

/** §4.4's inline chip: 10–11px, radius 3px, a --border-control edge. */
function Chip({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="rounded-sm border border-border-control px-1.5 text-meta text-fg-muted hover:bg-control hover:text-fg-emphasis"
    >
      {children}
    </button>
  );
}

/** The `···` row menu (DESIGN-NOTES §6: a DropdownMenu on ghost text). */
function RowMenu({ onRemove }: { onRemove: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Header actions"
        className="justify-self-end px-1 tracking-widest text-fg-ghost opacity-0 group-hover:opacity-100 data-[state=open]:opacity-100 hover:text-fg-muted"
      >
        ···
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[160px] rounded-md border-border-control bg-raised">
        <DropdownMenuItem onSelect={onRemove} className="text-ui text-destructive">
          Remove header
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="h-[28px] px-1 pt-1.5 text-meta text-fg-faint">{children}</p>;
}

/**
 * The file named beside the INHERITED label. One folder is the common case and
 * what screen 4a shows; several levels get a count instead of a list, since
 * every row already names its own source.
 */
function inheritedSource(rows: readonly Inherited[], auth: AuthHeader | null): string {
  const paths = new Set(rows.map((row) => row.source.path));
  if (auth) paths.add(auth.source.path);
  const list = [...paths];
  return list.length === 1 ? list[0] : `${list.length} levels`;
}
