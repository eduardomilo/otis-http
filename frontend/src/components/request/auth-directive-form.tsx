import { useRef } from "react";

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
import { verbatimText } from "@/lib/text-input";

/**
 * The form that edits one `# @auth` directive.
 *
 * Shared, because the same directive is written in two places for the same
 * reasons: a request's Override (screen 4b) and a folder's `_folder.http`
 * (screen 3a). A second copy would have drifted — the AWS argument hint alone
 * is the only place the five shapes docs/FORMAT.md §3.3 allows are written
 * down for the user.
 *
 * The scheme decides which fields exist, and each scheme gets the fields it
 * actually has: one for a bearer token, **two for basic** — a username and a
 * password — and the `key=value` line for aws. Basic had one field holding
 * "username password", which is the directive's text rather than the
 * credential's shape: picking Basic put a malformed line in the file
 * immediately, and setting a password meant knowing that it is "the rest of
 * the line". The `Writes` row is what keeps the promise the single field was
 * making, and it makes it for every scheme: the line that will be in the file
 * is on screen, and there is no second answer to what gets written.
 *
 * The mapping is lossless in both directions because §3.3 is: a username is
 * one whitespace-delimited field and the password is everything after it, so
 * two inputs and one line say exactly the same thing. What a username cannot
 * contain is a space, which is why that field drops them.
 *
 * §9 note: the design has no representation for the `aws` scheme (SCREENS.md
 * 4b, "The design has no representation for the aws scheme"), so its arguments
 * are edited as the `key=value` line §3.3 specifies, with the five shapes named
 * in the hint rather than invented as a form.
 *
 * `none` is deliberately not in the picker. Both callers already offer it as
 * their own "No auth" option, and a scheme select that could also say "none"
 * would be a second way to make the same choice on the same screen.
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
  const lower = scheme.toLowerCase();
  const known = AUTH_SCHEMES.some((s) => s.value === lower);

  const setScheme = (next: string) => {
    // The arguments do not carry across a scheme change: a bearer token is not
    // a username, and writing one into the other would produce a line that
    // parses and means something wrong.
    onChange(next === scheme ? value : `${next} `);
  };

  return (
    <div className="grid grid-cols-[80px_1fr] items-center gap-x-3 gap-y-2.5 text-ui">
      <Label>Type</Label>
      <Select value={known ? lower : "bearer"} onValueChange={setScheme}>
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

      {lower === "basic" ? (
        <BasicFields args={args} onChange={(next) => onChange(`${scheme} ${next}`)} />
      ) : (
        <>
          <Label>{authArgsLabel(lower)}</Label>
          <div>
            <Input
              {...verbatimText}
              value={args}
              onChange={(event) => onChange(`${scheme} ${event.target.value}`)}
              placeholder={authArgsPlaceholder(lower)}
              aria-label={authArgsLabel(lower)}
              className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
            />
            <p className="mt-1.5 text-meta text-fg-faint">{authArgsHint(lower)}</p>
          </div>
        </>
      )}

      <Label>Writes</Label>
      <span className="font-mono text-fg-secondary">
        <span className="text-fg-dim">{writesLabel}</span>
        <VariableText text={value.trim()} index={index} className="text-fg-secondary" />
      </span>
    </div>
  );
}

/**
 * `basic <username> [<password>]` as the two things it is.
 *
 * Reading splits on the first run of whitespace, which is what
 * `strings.Fields` does in Go, and takes one separator space off the rest —
 * not all of it, so a password can still be typed a space at a time.
 *
 * Writing needs a username to put the password after: `basic  <password>` is a
 * line whose *first* field is the password, so it would come back in the
 * username box, the two fields swapping under the person typing. So while
 * there is no username the password is held here instead of written, exactly
 * as the params table holds a row the URL cannot yet express — otherwise
 * backspacing over a username to retype it would take the password with it.
 * The `Writes` row shows the incomplete line, which is the truth: nothing is
 * sent until there is a user to send.
 */
function BasicFields({
  args,
  onChange,
}: {
  args: string;
  onChange: (args: string) => void;
}) {
  const rest = args.replace(/^\s+/, "");
  const username = rest.split(/\s+/)[0] ?? "";
  const inLine = rest.slice(username.length).replace(/^ /, "");

  // What was held is only this form's own doing, so it is dropped the moment
  // the line arrives from anywhere else — another request opening on the same
  // tab, the file changing on disk. `expect` is the line we last emitted;
  // anything else means these are somebody else's arguments.
  const held = useRef({ expect: "", password: "" });
  if (args !== held.current.expect) held.current = { expect: args, password: inLine };
  const password = username === "" ? held.current.password : inLine;

  const write = (user: string, pass: string) => {
    const next = user === "" || pass === "" ? user : `${user} ${pass}`;
    held.current = { expect: next, password: pass };
    onChange(next);
  };

  return (
    <>
      <Label>Username</Label>
      <Input
        {...verbatimText}
        // A username is one whitespace-delimited field (§3.3), so a space in
        // this box would be read back as the start of the password. Dropping
        // it is the format's own rule, applied where it can still be seen.
        value={username}
        onChange={(event) => write(event.target.value.replace(/\s+/g, ""), password)}
        placeholder="{{apiUser}}"
        aria-label="Username"
        className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
      />

      <Label>Password</Label>
      <div>
        <Input
          {...verbatimText}
          value={password}
          onChange={(event) => write(username, event.target.value)}
          placeholder="{{apiPassword}}"
          aria-label="Password"
          className="h-[26px] rounded-sm border-border-control bg-inset px-2 font-mono text-ui text-fg"
        />
        <p className="mt-1.5 text-meta text-fg-faint">
          {username === ""
            ? "A username comes first — basic <user> <password> — so nothing is written until there is one."
            : "The password is the rest of the line and may contain spaces. Sent as Authorization: Basic base64(user:password) — it is written into the file, so use a {{variable}} backed by the keychain for anything real."}
        </p>
      </div>
    </>
  );
}

function Label({ children }: { children: React.ReactNode }) {
  return <span className="text-ui text-fg-muted">{children}</span>;
}

function authArgsLabel(scheme: string): string {
  return scheme === "aws" ? "Arguments" : "Token";
}

function authArgsPlaceholder(scheme: string): string {
  return scheme === "aws" ? "profile=default region=us-east-1" : "{{apiKey}}";
}

function authArgsHint(scheme: string): string {
  return scheme === "aws"
    ? "key=value pairs: profile=, or key= with secret= and optional token=, plus region= and service=. profile= reads your own AWS credentials; committing real keys is discouraged."
    : "Exactly one token. Sent as Authorization: Bearer <token>.";
}
