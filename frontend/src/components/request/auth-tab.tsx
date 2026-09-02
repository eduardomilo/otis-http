import { useMemo } from "react";
import { Link } from "@tanstack/react-router";
import { Lock, PenLine } from "lucide-react";

import { VariableText } from "@/components/request/variable-text";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { AUTH_DIRECTIVE, directiveValue, removeDirective, setDirective } from "@/lib/http-file";
import { nodeLink, nodeParentPath } from "@/lib/paths";
import { cn } from "@/lib/utils";
import { referencedNames, type VariableIndex } from "@/lib/variables";
import type { Request } from "@bindings/internal/httpfile";
import type { Auth } from "@bindings/internal/resolve";
import type { Document } from "@bindings/internal/services";

/**
 * The Auth tab (screen 4b): three exclusive options in a 680px column.
 *
 * Which one is selected is read off the file, not held in state: it is the
 * request's own `@auth` directive, or its absence (docs/FORMAT.md §3.3). So
 * the radio group *is* the file, and switching an option writes or removes one
 * line — which is what the copy under each option promises.
 *
 * The selected card expands (DESIGN-NOTES §7.5): accent border, `--bg-raised`
 * fill, and a detail grid inside. Inherit shows the resolved config read-only
 * with its source; Override shows the editable form, prefilled from the
 * inherited values, because the common case is the same scheme with a
 * different token.
 */

type AuthMode = "inherit" | "override" | "none";

/** The schemes `@auth` accepts (§3.3). */
const SCHEMES = [
  { value: "bearer", label: "Bearer token" },
  { value: "basic", label: "Basic" },
  { value: "aws", label: "AWS Signature V4" },
] as const;

export function AuthTab({
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
  const own = directiveValue(entry, AUTH_DIRECTIVE);
  const mode: AuthMode =
    own === undefined ? "inherit" : own.trim().toLowerCase() === "none" ? "none" : "override";

  const inherited = document.inheritedAuth;
  const folder = nodeParentPath(document.path);

  /**
   * Choosing Override prefills from what is inherited, so a user who wants
   * "the same bearer with my token" starts from the folder's line instead of an
   * empty block. With nothing inherited it starts as a bearer with no token,
   * which is the form that then asks for exactly one thing.
   */
  const prefill = useMemo(() => directiveFor(inherited) ?? "bearer ", [inherited]);

  const choose = (next: AuthMode) => {
    onEdit((e) => {
      switch (next) {
        case "inherit":
          // Nothing is written to this file: the absence of @auth *is* the
          // inherit case, so the option removes the directive rather than
          // recording a choice.
          return removeDirective(e, AUTH_DIRECTIVE);
        case "none":
          return setDirective(e, AUTH_DIRECTIVE, "none");
        case "override":
          return setDirective(e, AUTH_DIRECTIVE, prefill);
      }
    });
  };

  return (
    <div className="min-h-0 flex-1 overflow-auto py-3">
      <RadioGroup
        value={mode}
        onValueChange={(next) => choose(next as AuthMode)}
        className="max-w-[680px] gap-2"
      >
        <Card
          mode="inherit"
          selected={mode === "inherit"}
          title="Inherit from folder"
          source={inherited?.source.path}
          note="default · nothing written to this file"
        >
          {inherited ? (
            <InheritedDetail auth={inherited} index={index} folder={folder} document={document} />
          ) : (
            <p className="text-meta text-fg-dim">
              No folder above this request declares <Code>@auth</Code>, so nothing is sent. The
              resolution order is request → folder → parent folders.
            </p>
          )}
        </Card>

        <Card
          mode="override"
          selected={mode === "override"}
          title="Override for this request"
          note={`writes an @auth block into ${baseName(document.path)} · folder auth stops applying here`}
        >
          <OverrideForm
            value={own ?? prefill}
            index={index}
            onChange={(value) => onEdit((e) => setDirective(e, AUTH_DIRECTIVE, value))}
          />
        </Card>

        <Card
          mode="none"
          selected={mode === "none"}
          title="No auth"
          note="writes @auth none · request goes out unauthenticated"
        >
          <p className="text-meta text-fg-dim">
            <Code>@auth none</Code> is an explicit opt-out, distinct from no <Code>@auth</Code> at
            all: the request sends nothing even though a folder above declares auth.
          </p>
        </Card>
      </RadioGroup>

      <p className="mt-3 max-w-[680px] text-meta text-fg-dim">
        Resolution order: request → folder → parent folders. The first one that sets auth wins.
        Whatever is chosen here is visible in the file and in the diff.
      </p>

      {document.inheritError ? (
        <p className="mt-3 max-w-[680px] text-meta text-warning">{document.inheritError}</p>
      ) : null}
    </div>
  );
}

/**
 * One option. Selected, it takes the accent border and `--bg-raised` and shows
 * its detail; unselected, it is a single row (§7.5).
 */
function Card({
  mode,
  selected,
  title,
  source,
  note,
  children,
}: {
  mode: AuthMode;
  selected: boolean;
  title: string;
  source?: string;
  note: string;
  children: React.ReactNode;
}) {
  const id = `auth-${mode}`;
  return (
    <div
      className={cn(
        "rounded-md border",
        selected ? "border-primary bg-raised" : "border-border-control bg-transparent",
      )}
    >
      <div className="flex items-center gap-2.5 px-3 py-2.5">
        <RadioGroupItem value={mode} id={id} />
        <label htmlFor={id} className="flex min-w-0 flex-1 items-center gap-2">
          <span
            className={cn(
              "shrink-0 text-ui",
              selected ? "font-medium text-fg-emphasis" : "text-fg-secondary",
            )}
          >
            {title}
          </span>
          {source ? <span className="truncate font-mono text-ui text-fg-dim">{source}</span> : null}
          <span className="ml-auto shrink-0 pl-3 text-meta text-fg-faint">{note}</span>
        </label>
      </div>
      {selected ? <div className="border-t border-border px-3 py-3">{children}</div> : null}
    </div>
  );
}

/**
 * The inherited config, read-only, with its source and an "Edit in <folder>"
 * action — the detail grid of screen 4b, `80px 1fr` per DESIGN-NOTES §4.5.
 *
 * The `Sends` row is masked. A bearer token's value never appears here: for a
 * `{{variable}}` there is nothing to show but the reference, and for anything
 * else showing it would put a credential on screen in a pane the user did not
 * ask to see one in. The design masks it too (`Bearer ••••••••`).
 */
function InheritedDetail({
  auth,
  index,
  folder,
  document,
}: {
  auth: Auth;
  index: VariableIndex;
  folder: string;
  document: Document;
}) {
  return (
    <div className="grid grid-cols-[80px_1fr] items-start gap-x-3 gap-y-2.5 text-ui">
      <Label>Type</Label>
      <span className="text-fg-secondary">{schemeLabel(auth.kind)}</span>

      {auth.kind === "bearer" ? (
        <>
          <Label>Token</Label>
          <TokenValue text={auth.token ?? ""} index={index} />
        </>
      ) : null}

      {auth.kind === "basic" ? (
        <>
          <Label>User</Label>
          <VariableText text={auth.username ?? ""} index={index} className="text-fg-secondary" />
          <Label>Password</Label>
          <Masked />
        </>
      ) : null}

      {auth.kind === "aws" ? (
        <>
          <Label>Credentials</Label>
          <span className="font-mono text-fg-secondary">
            {auth.profile ? `profile=${auth.profile}` : auth.accessKey ? "static key" : "default chain"}
          </span>
          {auth.region || auth.service ? (
            <>
              <Label>Scope</Label>
              <span className="font-mono text-fg-secondary">
                {[auth.region && `region=${auth.region}`, auth.service && `service=${auth.service}`]
                  .filter(Boolean)
                  .join(" ")}
              </span>
            </>
          ) : null}
        </>
      ) : null}

      <Label>Sends</Label>
      <span className="font-mono text-fg-secondary">
        {document.authHeader ? (
          <>
            {document.authHeader.name}:{" "}
            {auth.kind === "aws" ? (
              <span className="text-fg-dim">{document.authHeader.value}</span>
            ) : (
              <>
                {schemeWord(auth.kind)} <Masked />
              </>
            )}
          </>
        ) : (
          <span className="text-fg-dim">
            nothing — this request sends its own Authorization header
          </span>
        )}
      </span>

      <span />
      <div className="flex items-center gap-2.5">
        <Link
          {...nodeLink("folder", folder)}
          className="flex h-6 items-center gap-1.5 rounded-sm border border-border-control bg-control px-2.5 text-ui text-fg-secondary hover:text-fg-emphasis"
        >
          <PenLine className="size-3" />
          Edit in {folder === "" ? "the collection root" : `${folder}/`}
        </Link>
        <span className="text-meta text-fg-faint">changes apply to every request below it</span>
      </div>
    </div>
  );
}

/**
 * The Override form.
 *
 * It edits the directive's argument text rather than a set of fields mapped
 * back onto it, so what the file will say is what is on screen — the same
 * argument the whole product makes about `.http` files. The scheme is a select
 * because it decides which fields exist; everything after it is text.
 *
 * §9 note: the design has no representation for the `aws` scheme (SCREENS.md
 * 4b, "The design has no representation for the aws scheme"), so its arguments
 * are edited as the `key=value` line docs/FORMAT.md §3.3 specifies, with the
 * five shapes named in the hint rather than invented as a form.
 */
function OverrideForm({
  value,
  index,
  onChange,
}: {
  value: string;
  index: VariableIndex;
  onChange: (value: string) => void;
}) {
  const scheme = value.trim().split(/\s+/)[0] ?? "";
  const args = value.trim().slice(scheme.length).trim();
  const known = SCHEMES.some((s) => s.value === scheme.toLowerCase());

  const setScheme = (next: string) => {
    // The arguments do not carry across a scheme change: a bearer token is not
    // a username, and writing one into the other would produce a line that
    // parses and means something wrong.
    onChange(next === scheme ? value : `${next} `);
  };

  return (
    <div className="grid grid-cols-[80px_1fr] items-center gap-x-3 gap-y-2.5 text-ui">
      <Label>Type</Label>
      <Select value={known ? scheme.toLowerCase() : "bearer"} onValueChange={setScheme}>
        <SelectTrigger className="h-[26px] w-[200px] rounded-sm border-border-control bg-inset px-2 text-ui">
          <SelectValue />
        </SelectTrigger>
        <SelectContent className="rounded-md border-border-control bg-raised">
          {SCHEMES.map((s) => (
            <SelectItem key={s.value} value={s.value} className="text-ui">
              {s.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Label>{argsLabel(scheme)}</Label>
      <div>
        <Input
          value={args}
          onChange={(event) => onChange(`${scheme} ${event.target.value}`)}
          placeholder={argsPlaceholder(scheme)}
          aria-label={argsLabel(scheme)}
          className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
        />
        <p className="mt-1.5 text-meta text-fg-faint">{argsHint(scheme)}</p>
      </div>

      <Label>Writes</Label>
      <span className="font-mono text-fg-secondary">
        <span className="text-fg-dim"># @auth </span>
        <VariableText text={value.trim()} index={index} className="text-fg-secondary" />
      </span>

      {referencedNames(args).length > 0 ? (
        <>
          <span />
          <p className="text-meta text-fg-faint">
            A <Code>{"{{variable}}"}</Code> here resolves at send time, so a token can live in the
            environment — and in the keychain — instead of in this file.
          </p>
        </>
      ) : null}
    </div>
  );
}

/**
 * A bearer token. A reference keeps its token styling; a secret-backed one gets
 * the lock and the storage label §8.3 requires, and never a masked value that
 * would imply the string could be revealed here.
 */
function TokenValue({ text, index }: { text: string; index: VariableIndex }) {
  const names = referencedNames(text);
  const secret = names.find((name) => index.get(name)?.secret);
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-2">
      <VariableText text={text} index={index} className="text-fg-secondary" />
      {secret ? (
        <span className="flex shrink-0 items-center gap-1 text-meta text-secret">
          <Lock className="size-3" />
          keychain
        </span>
      ) : null}
    </span>
  );
}

/** §8.3: a masked value with 2px tracking, so the dots read as a field. */
function Masked() {
  return <span className="tracking-[2px] text-fg-dim">••••••••</span>;
}

function Label({ children }: { children: React.ReactNode }) {
  return <span className="text-ui text-fg-muted">{children}</span>;
}

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded-sm bg-control px-1 font-mono text-fg-secondary">{children}</code>
  );
}

/** The directive text that reproduces an auth, for prefilling Override. */
function directiveFor(auth: Auth | null | undefined): string | undefined {
  if (!auth) return undefined;
  switch (auth.kind) {
    case "bearer":
      return `bearer ${auth.token ?? ""}`;
    case "basic":
      return `basic ${auth.username ?? ""} ${auth.password ?? ""}`.trimEnd();
    case "aws": {
      const args = [
        auth.profile && `profile=${auth.profile}`,
        auth.accessKey && `key=${auth.accessKey}`,
        auth.region && `region=${auth.region}`,
        auth.service && `service=${auth.service}`,
      ].filter(Boolean);
      // secret= and token= are not carried across: they are credential
      // material, they never leave Go (internal/resolve keeps them unexported),
      // and prefilling them from something the frontend cannot see is not
      // possible anyway.
      return `aws ${args.join(" ")}`.trimEnd();
    }
    case "none":
      // "none" above is not something to prefill an override from; the user
      // asked for an override, which means they want auth.
      return undefined;
  }
  return undefined;
}

function schemeLabel(kind: string): string {
  return SCHEMES.find((s) => s.value === kind)?.label ?? kind;
}

function schemeWord(kind: string): string {
  return kind === "bearer" ? "Bearer" : kind === "basic" ? "Basic" : kind;
}

function argsLabel(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "Credentials";
    case "aws":
      return "Arguments";
    default:
      return "Token";
  }
}

function argsPlaceholder(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "username password";
    case "aws":
      return "profile=default region=us-east-1";
    default:
      return "{{apiKey}}";
  }
}

function argsHint(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "A username, then the password, which is the rest of the line and may contain spaces. Sent as Authorization: Basic base64(user:password).";
    case "aws":
      return "key=value pairs: profile=, or key= with secret= and optional token=, plus region= and service=. profile= reads your own AWS credentials; committing real keys is discouraged.";
    default:
      return "Exactly one token. Sent as Authorization: Bearer <token>.";
  }
}

function baseName(path: string): string {
  const cut = path.lastIndexOf("/");
  return cut === -1 ? path : path.slice(cut + 1);
}
