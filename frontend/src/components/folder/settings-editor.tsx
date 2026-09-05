import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";

import { AuthDirectiveForm } from "@/components/request/auth-directive-form";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  AUTH_DIRECTIVE,
  addHeader,
  addVariable,
  directiveValue,
  entryOf,
  headersOf,
  removeDirective,
  removeHeaderAt,
  removeVariableAt,
  sameFile,
  setDirective,
  setHeaderAt,
  setVariableAt,
  updateEntry,
  variablesOf,
} from "@/lib/http-file";
import { cn } from "@/lib/utils";
import type { VariableIndex } from "@/lib/variables";
import { verbatimText } from "@/lib/text-input";
import type { File, Request } from "@bindings/internal/httpfile";
import { FolderService } from "@bindings/internal/services";
import type { FolderDocument } from "@bindings/internal/services";

/**
 * Editing a folder's `_folder.http` — its auth, its headers and its variables.
 *
 * This is the write half of screen 3a, and until it existed the folder view
 * had none: the Auth panel's "Edit" and the Variables panel's "Add" both
 * navigated to `/r/<folder>/_folder.http`, and since `_folder.http` is
 * deliberately not a node in the tree (docs/FORMAT.md §2.1, CLAUDE.md) the
 * request editor answered "not in the collection". So folder-level auth — the
 * level AWS SigV4 actually belongs at, because it applies to a whole API —
 * could be read and never changed.
 *
 * It edits `doc.settings`, the parsed file Go already hands over for exactly
 * this, and returns it to `FolderService.Save`. Go's serializer stays the only
 * thing that writes the file.
 *
 * An explicit Save rather than the request editor's dirty-tab-and-⌘S, because
 * a folder is not a document tab: it has no tab to mark, and the README beside
 * it already works this way. Reverting is offered next to it, since the thing
 * being edited cascades to every request below and "what did I just change"
 * deserves an answer that is not the git diff.
 */

export type SettingsSection = "auth" | "variables" | "headers";

/**
 * The entry a folder with no `_folder.http` starts from: settings and no
 * request line, which is the form docs/FORMAT.md §1.9 defines and §2.3 uses.
 * Saving it is what brings the file into being.
 */
const EMPTY_SETTINGS_ENTRY: Request = {
  body: {},
  headers: [],
  variables: [],
  directives: [],
  comments: [],
};

export function FolderSettingsEditor({
  doc,
  path,
  env,
  section,
  index,
  onSaved,
}: {
  doc: FolderDocument;
  path: string;
  env: string;
  section: SettingsSection;
  index: VariableIndex;
  onSaved: (doc: FolderDocument) => void;
}) {
  const [draft, setDraft] = useState<File | null>(doc.settings ?? null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const dirty = !sameFile(draft, doc.settings ?? null);

  // Follow the document when it is re-read, unless there is an unsaved edit —
  // a run finishing re-reads it, and losing a half-typed token to that would
  // be its own bug.
  useEffect(() => {
    if (!dirty) setDraft(doc.settings ?? null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc.settings]);

  // A folder with no _folder.http has nothing to edit yet. Saving one is how
  // it comes into being, so the draft starts as a single settings entry.
  const file: File = draft ?? { requests: [EMPTY_SETTINGS_ENTRY] };
  const entry = entryOf(file, 0);

  if (doc.settingsError) {
    return (
      <Section title="Settings" file={doc.settingsPath}>
        <p className="text-meta text-destructive">{doc.settingsError}</p>
        <p className="mt-2 text-meta text-fg-faint">
          Otis will not edit a file it could not parse, because saving would
          rewrite it from a model that is missing whatever failed. Fix it in a
          text editor and this panel comes back.
        </p>
      </Section>
    );
  }
  if (!entry) return null;

  const edit = (fn: (e: Request) => Request) => {
    setDraft(updateEntry(file, 0, fn));
    setError(null);
  };

  async function save() {
    setSaving(true);
    try {
      const next = await FolderService.Save(path, env, file);
      if (next) onSaved(next);
      setError(null);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Section
      title={SECTION_TITLE[section]}
      file={doc.settingsPath ?? `${doc.path ? doc.path + "/" : ""}_folder.http`}
      dirty={dirty}
      action={
        <>
          <Button
            type="button"
            variant="ghost"
            disabled={!dirty || saving}
            onClick={() => setDraft(doc.settings ?? null)}
            className="h-6 shrink-0 px-2 text-ui text-fg-faint hover:text-fg-emphasis disabled:opacity-40"
          >
            Revert
          </Button>
          <Button
            type="button"
            disabled={!dirty || saving}
            onClick={() => void save()}
            className="h-6 shrink-0 rounded-md border border-border-control bg-control px-2 text-ui text-fg-secondary hover:bg-selected disabled:opacity-40"
          >
            Save
          </Button>
        </>
      }
    >
      {error ? <p className="mb-3 text-meta text-destructive">{error}</p> : null}

      {section === "auth" ? (
        <AuthSection entry={entry} index={index} edit={edit} />
      ) : section === "headers" ? (
        <HeadersSection entry={entry} edit={edit} />
      ) : (
        <VariablesSection entry={entry} edit={edit} />
      )}

      <p className="mt-4 text-meta text-fg-faint">
        Saving writes {doc.settingsPath ?? "_folder.http"} and every request below inherits it —{" "}
        {doc.counts?.below ?? 0} in all. It shows up in the diff like any other change.
      </p>
    </Section>
  );
}

const SECTION_TITLE: Record<SettingsSection, string> = {
  auth: "Auth",
  variables: "Variables",
  headers: "Headers",
};

/**
 * The folder's `# @auth` directive: inherit from above, declare one here, or
 * declare `none`.
 *
 * The three choices are the request editor's (screen 4b), one level up: a
 * folder with no directive inherits its parent's, which is the default and
 * writes nothing.
 */
function AuthSection({
  entry,
  index,
  edit,
}: {
  entry: Request;
  index: VariableIndex;
  edit: (fn: (e: Request) => Request) => void;
}) {
  const value = directiveValue(entry, AUTH_DIRECTIVE);
  const mode = value === undefined ? "inherit" : value.trim() === "none" ? "none" : "declare";
  const [remembered, setRemembered] = useState(
    value && value.trim() !== "none" ? value : "bearer ",
  );

  const choose = (next: string) => {
    if (next === "inherit") edit((e) => removeDirective(e, AUTH_DIRECTIVE));
    if (next === "none") edit((e) => setDirective(e, AUTH_DIRECTIVE, "none"));
    if (next === "declare") edit((e) => setDirective(e, AUTH_DIRECTIVE, remembered));
  };

  return (
    <RadioGroup value={mode} onValueChange={choose} className="gap-2.5">
      <Choice value="inherit" label="Inherit from above" aside="default · nothing written here" />
      <Choice value="declare" label="Declare auth here" aside="applies to every request below">
        {mode === "declare" ? (
          <div className="mt-3">
            <AuthDirectiveForm
              value={value ?? remembered}
              index={index}
              onChange={(next) => {
                setRemembered(next);
                edit((e) => setDirective(e, AUTH_DIRECTIVE, next));
              }}
            />
          </div>
        ) : null}
      </Choice>
      <Choice
        value="none"
        label="No auth"
        aside="writes @auth none · stops a parent's auth applying below"
      />
    </RadioGroup>
  );
}

/** One radio card, matching the request Auth tab's three. */
function Choice({
  value,
  label,
  aside,
  children,
}: {
  value: string;
  label: string;
  aside: string;
  children?: React.ReactNode;
}) {
  return (
    <label
      className={cn(
        "block rounded-md border px-3 py-2.5",
        "border-border-control bg-inset has-[[data-state=checked]]:border-primary",
      )}
    >
      <span className="flex items-center gap-2.5">
        <RadioGroupItem value={value} />
        <span className="text-ui text-fg-emphasis">{label}</span>
        <span className="ml-auto text-meta text-fg-faint">{aside}</span>
      </span>
      {children}
    </label>
  );
}

/** The folder's headers, added to every request below (docs/FORMAT.md §3.1). */
function HeadersSection({
  entry,
  edit,
}: {
  entry: Request;
  edit: (fn: (e: Request) => Request) => void;
}) {
  const headers = headersOf(entry);
  return (
    <Rows
      empty="No headers here. One added below goes out with every request in this folder and under it."
      addLabel="Add header"
      onAdd={() => edit((e) => addHeader(e, "", ""))}
      rows={headers.map((header, i) => ({
        key: `${i}`,
        name: header.name,
        value: header.value,
        namePlaceholder: "Header-Name",
        valuePlaceholder: "value",
        onName: (name) => edit((e) => setHeaderAt(e, i, { name })),
        onValue: (value) => edit((e) => setHeaderAt(e, i, { value })),
        onRemove: () => edit((e) => removeHeaderAt(e, i)),
      }))}
    />
  );
}

/**
 * The folder's committed `@name = value` declarations.
 *
 * Committed, and only those: the session values the panel beside this one
 * shows are in no file (docs/FORMAT.md §4.5) and there is nothing here to
 * write them to.
 */
function VariablesSection({
  entry,
  edit,
}: {
  entry: Request;
  edit: (fn: (e: Request) => Request) => void;
}) {
  const variables = variablesOf(entry);
  return (
    <Rows
      empty="Nothing declared here. A variable added below resolves for every request in this folder and under it, and is committed with the collection."
      addLabel="Add variable"
      onAdd={() => edit((e) => addVariable(e, "", ""))}
      rows={variables.map((variable, i) => ({
        key: `${i}`,
        name: variable.name,
        value: variable.value,
        namePlaceholder: "name",
        valuePlaceholder: "value",
        onName: (name) => edit((e) => setVariableAt(e, i, { name })),
        onValue: (value) => edit((e) => setVariableAt(e, i, { value })),
        onRemove: () => edit((e) => removeVariableAt(e, i)),
      }))}
    />
  );
}

interface Row {
  key: string;
  name: string;
  value: string;
  namePlaceholder: string;
  valuePlaceholder: string;
  onName: (name: string) => void;
  onValue: (value: string) => void;
  onRemove: () => void;
}

/** A name/value table with an add row, shared by headers and variables. */
function Rows({
  rows,
  empty,
  addLabel,
  onAdd,
}: {
  rows: Row[];
  empty: string;
  addLabel: string;
  onAdd: () => void;
}) {
  return (
    <div className="min-w-0">
      {rows.length === 0 ? (
        <p className="mb-3 max-w-[560px] text-meta text-fg-dim">{empty}</p>
      ) : (
        <div className="mb-2">
          <div className="grid grid-cols-[minmax(0,200px)_minmax(0,1fr)_24px] gap-x-3 border-b border-border-hairline pb-1.5">
            <Heading>Name</Heading>
            <Heading>Value</Heading>
            <span />
          </div>
          {rows.map((row) => (
            <div
              key={row.key}
              className="grid grid-cols-[minmax(0,200px)_minmax(0,1fr)_24px] items-center gap-x-3 border-b border-border-hairline py-1"
            >
              <Field
                value={row.name}
                placeholder={row.namePlaceholder}
                onChange={row.onName}
                aria-label="Name"
              />
              <Field
                value={row.value}
                placeholder={row.valuePlaceholder}
                onChange={row.onValue}
                aria-label="Value"
              />
              <Button
                type="button"
                variant="ghost"
                aria-label={`Remove ${row.name || "row"}`}
                onClick={row.onRemove}
                className="size-6 p-0 text-fg-ghost hover:text-destructive"
              >
                <Trash2 className="size-3" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <Button
        type="button"
        variant="ghost"
        onClick={onAdd}
        className="h-6 gap-1.5 px-0 text-ui text-fg-muted hover:text-fg-emphasis"
      >
        <Plus className="size-3" />
        {addLabel}
      </Button>
    </div>
  );
}

function Field({
  value,
  placeholder,
  onChange,
  ...rest
}: {
  value: string;
  placeholder: string;
  onChange: (value: string) => void;
} & React.AriaAttributes) {
  return (
    <Input
      {...verbatimText}
      {...rest}
      value={value}
      placeholder={placeholder}
      onChange={(event) => onChange(event.target.value)}
      className="h-[26px] min-w-0 rounded-sm border-transparent bg-transparent px-1.5 font-mono text-ui text-fg hover:border-border-control focus:border-border-control focus:bg-inset"
    />
  );
}

function Heading({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-label tracking-[.06em] text-fg-dim uppercase">{children}</span>
  );
}

/** The section frame: a title, the file it writes, and its actions. */
function Section({
  title,
  file,
  dirty,
  action,
  children,
}: {
  title: string;
  file?: string;
  dirty?: boolean;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <div className="mb-3 flex items-baseline gap-2">
        <h2 className="shrink-0 text-ui text-fg-emphasis">{title}</h2>
        <span className="min-w-0 flex-1 truncate font-mono text-meta text-fg-dim">
          {file}
          {dirty ? " ·  unsaved" : ""}
        </span>
        {action}
      </div>
      {children}
    </div>
  );
}
