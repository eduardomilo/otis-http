import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { VariableText } from "@/components/request/variable-text";
import type { VariableIndex } from "@/lib/variables";

/**
 * The form that edits one `# @auth` directive.
 *
 * Shared, because the same directive is written in two places for the same
 * reasons: a request's Override (screen 4b) and a folder's `_folder.http`
 * (screen 3a). A second copy would have drifted — the AWS argument hint alone
 * is the only place the five shapes docs/FORMAT.md §3.3 allows are written
 * down for the user.
 *
 * It edits the directive's **argument text** rather than a set of fields
 * mapped back onto it, so what the file will say is what is on screen — the
 * same argument the whole product makes about `.http` files. The scheme is a
 * select because it decides which fields exist; everything after it is text.
 *
 * §9 note: the design has no representation for the `aws` scheme (SCREENS.md
 * 4b, "The design has no representation for the aws scheme"), so its arguments
 * are edited as the `key=value` line §3.3 specifies, with the five shapes named
 * in the hint rather than invented as a form.
 */

export const AUTH_SCHEMES = [
  { value: "bearer", label: "Bearer token" },
  { value: "basic", label: "Basic" },
  { value: "aws", label: "AWS Signature V4" },
] as const;

export function AuthDirectiveForm({
  value,
  index,
  onChange,
  /** What the "Writes" row is prefixed with, e.g. "# @auth ". */
  writesLabel = "# @auth ",
}: {
  /** The whole directive argument: scheme, then its arguments. */
  value: string;
  index: VariableIndex;
  onChange: (value: string) => void;
  writesLabel?: string;
}) {
  // Only the *leading* whitespace comes off, and only one space separates the
  // scheme from its arguments. Trimming the tail here is what made the field
  // impossible to type a space into: `profile=dev ` came back as
  // `profile=dev`, so the next keystroke landed against the previous word and
  // `profile=dev region=x` could only ever be pasted. Interior and trailing
  // spaces are the user's, and an `@auth basic` password may legitimately end
  // in one.
  const leading = value.replace(/^\s+/, "");
  const scheme = leading.split(/\s+/)[0] ?? "";
  const args = leading.slice(scheme.length).replace(/^ /, "");
  const known = AUTH_SCHEMES.some((s) => s.value === scheme.toLowerCase());

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
          {AUTH_SCHEMES.map((s) => (
            <SelectItem key={s.value} value={s.value} className="text-ui">
              {s.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Label>{authArgsLabel(scheme)}</Label>
      <div>
        <Input
          value={args}
          onChange={(event) => onChange(`${scheme} ${event.target.value}`)}
          placeholder={authArgsPlaceholder(scheme)}
          aria-label={authArgsLabel(scheme)}
          className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
        />
        <p className="mt-1.5 text-meta text-fg-faint">{authArgsHint(scheme)}</p>
      </div>

      <Label>Writes</Label>
      <span className="font-mono text-fg-secondary">
        <span className="text-fg-dim">{writesLabel}</span>
        <VariableText text={value.trim()} index={index} className="text-fg-secondary" />
      </span>
    </div>
  );
}

function Label({ children }: { children: React.ReactNode }) {
  return <span className="text-ui text-fg-muted">{children}</span>;
}

export function authArgsLabel(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "Credentials";
    case "aws":
      return "Arguments";
    default:
      return "Token";
  }
}

export function authArgsPlaceholder(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "username password";
    case "aws":
      return "profile=default region=us-east-1";
    default:
      return "{{apiKey}}";
  }
}

export function authArgsHint(scheme: string): string {
  switch (scheme.toLowerCase()) {
    case "basic":
      return "A username, then the password, which is the rest of the line and may contain spaces. Sent as Authorization: Basic base64(user:password).";
    case "aws":
      return "key=value pairs: profile=, or key= with secret= and optional token=, plus region= and service=. profile= reads your own AWS credentials; committing real keys is discouraged.";
    default:
      return "Exactly one token. Sent as Authorization: Bearer <token>.";
  }
}
