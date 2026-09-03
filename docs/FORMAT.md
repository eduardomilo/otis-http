# Otis on-disk format

This document is the authoritative specification of everything Otis reads
from and writes to a collection directory. If the code and this document
disagree, that is a bug. Sections marked *(planned)* describe later
increments and are not yet implemented.

Otis collections are plain directories of `.http` files, the format read by
the JetBrains HTTP Client and the VS Code REST Client extension. Otis adds
nothing those tools reject: its extensions live in comment directives
(`# @name ...`) and in `{% %}` script blocks, both of which those tools
already tolerate.

## 1. Request files (`*.http`)

### 1.1 Structure

A file is a sequence of **entries** separated by lines beginning with
`###`. Otis convention is one entry per file; the parser accepts any
number, and the collection layer reports more than one request per file as
a warning, never an error.

An entry has, in order:

```
### <title>                      separator; optional for the first entry
<preamble>                       comments, directives, variables, pre-request scripts
METHOD URL [HTTP/version]        request line; optional (see 1.9)
Header-Name: value               zero or more headers
                                 one blank line
<body>                           optional
> {% ... %}                      zero or more post-response scripts
>> ./file                        optional response redirect
```

Blank lines are insignificant everywhere except that the first blank line
after the headers starts the body.

Line endings may be LF or CRLF. A leading UTF-8 byte-order mark is
ignored. Otis writes LF.

### 1.2 Separators and titles

A line starting with `###` ends the previous entry. Text after the `###`,
trimmed, is the entry's **title**. An entry with no content at all (for
example a trailing `###` at the end of a file) is dropped.

### 1.3 Comments

Outside a body, a line starting with `#` or `//` is a comment. Comment text
is kept verbatim (including leading whitespace after the marker) and is
written back on save. Inside a body, `#` and `//` lines are body text.

Comments that appear between headers are preserved but are written back
above the request line on save.

### 1.4 Directives

A comment whose first token is `@name` is a **directive**:

```
# @name Create user
// @no-redirect
# @timeout 30
```

Grammar: `("#" | "//") WS* "@" NAME (WS+ VALUE)?`. NAME is
`[A-Za-z][A-Za-z0-9_.-]*`. VALUE is the rest of the line, trimmed; it is
empty for flag directives. When a directive is repeated, the last one wins.

Directives Otis understands today:

| Directive | Meaning |
| --- | --- |
| `@name VALUE` | The request's display name. Falls back to the separator title, then to the file name (collection layer). |
| `@auth ...` | Authentication; see section 3. Inherited from `_folder.http`. |
| `@no-redirect` | Return the first 3xx response instead of following it. |
| `@no-cookie-jar` | Send and store no cookies for this request. |
| `@timeout SECONDS` | Overall timeout for the exchange, a positive number (decimals allowed). Default 30. A non-positive or non-numeric value is an error. |

`@no-redirect`, `@no-cookie-jar` and `@timeout` apply to the request they
appear in only; they are not inherited.

Unknown directives are preserved and ignored.

### 1.5 Variables

`@name = value` declares a file-level variable. Grammar:
`"@" NAME WS* "=" WS* VALUE`, where NAME is any run of characters other
than whitespace and `=`, and VALUE is the rest of the line, trimmed (it may
be empty). The parser records declarations where they appear; scope and
resolution are defined in section 4 *(planned, Increment 4)*. `{{name}}`
references are left untouched by the parser everywhere they occur.

### 1.6 Request line

```
METHOD URL [HTTP/version]
```

METHOD is one or more uppercase ASCII letters; any such token is accepted
(custom methods are legal). If the line starts with something that looks
like a URL rather than a method (contains `://`, or starts with `/` or
`{{`), the method is **GET**. Otis always writes the method.

The URL may continue on following lines that are indented and start with
`?` or `&` (JetBrains and VS Code style); the pieces are concatenated with
no whitespace. Otis writes the URL on one line.

Anything after the URL other than an `HTTP/...` version token is an error.

### 1.7 Headers

Headers follow the request line until the first blank line. Grammar:
`TOKEN ":" WS* VALUE WS*` where TOKEN uses the RFC 7230 token characters.
Header names keep their original casing and order; values are trimmed.
Lookups by name are case-insensitive. Duplicate names are allowed and are
all kept.

### 1.8 Body

The body starts after the first blank line following the headers and ends
at the first of: a `###` separator, a `> {%` or `> ./file` post-response
handler, a `>>` redirect, or end of file.

Leading and trailing blank lines are **not** part of the body. Everything
else is verbatim: indentation, trailing spaces, internal blank lines, and
lines starting with `#`. The body is stored without its final line
terminator, so `{}` and `{}⏎` are the same body.

**Body from file.** A body consisting of exactly one line of the form

```
< ./relative/path
<@ ./relative/path            variables inside the file are resolved
<@latin1 ./relative/path      ...with an explicit charset
```

is a file reference. The parser records the path and does not read it. At
send time the path is resolved relative to the `.http` file's directory (an
absolute path is used as-is) and the file's bytes become the body. With the
`<@` form, `{{variables}}` in the file content are resolved with the
request's scope (section 4); with plain `<` the content is sent verbatim. A
charset argument is recorded but not converted: the bytes are sent as-is
and a warning is raised.

A `< ./path` line that is part of a larger body (for example inside a
multipart boundary) is body text and is sent literally.

### 1.9 Entries without a request line

An entry may have no request line. It then consists only of comments,
directives, variables and headers, and describes settings rather than a
request. This is the form used by `_folder.http` (section 2.3). A header
line where a request line is expected starts such an entry.

### 1.10 Scripts

Scripts are Otis-specific in content but use the JetBrains delimiters so
other tools ignore them. Otis is **not** compatible with the JetBrains
script API.

```
< {% ... %}     pre-request script, before the request line
> {% ... %}     post-response script, after the body
> ./handler.js  post-response script in an external file
```

A block may be on one line or span many. The text between `{%` and `%}`
is captured verbatim, newlines included, together with the line number of
the opening marker. Nothing but whitespace may follow `%}` on its line.
Several blocks of each kind may appear and are kept in order. What they can do,
and the order they run in, is section 9.

### 1.11 Response redirect

`>> ./path` asks for the response body to be written to a file;
`>>! ./path` overwrites an existing file. At most one per entry.

### 1.12 Errors

Parse errors carry a 1-based line number and are reported as
`path: line N: message`. A file that fails to parse still appears in the
collection tree, marked broken *(Increment 2)*.

### 1.13 Canonical form

Otis writes files in a canonical layout. Parsing a canonical file and
writing it back produces identical bytes, so editing a request in Otis
never produces a whitespace-only diff. Files written by other tools are
parsed faithfully but may be reformatted on the first save; the structure
(every field in section 1) is preserved exactly.

Canonical layout of an entry:

1. `### title` if the entry has a title or is not the first in the file.
   Entries are separated by one blank line.
2. Preamble items in their original relative order. Items added
   programmatically (no source line) go after the existing ones in the
   order comments, variables, directives, pre-request scripts.
3. `METHOD URL [VERSION]` on one line.
4. Headers as `Name: value`.
5. A blank line, then the body, then a newline. Body-from-file is written
   as `< path`, `<@ path` or `<@charset path`.
6. A blank line, then post-response scripts and the redirect, each on its
   own line(s). Scripts are written as `> {%` + text + `%}`.

## 2. Collections

A collection is a directory. Loading a collection never modifies it.

### 2.1 Layout

| Entry | Meaning |
| --- | --- |
| directory | a folder |
| `*.http` | a request (section 1) |
| `_folder.http` | the folder's settings (section 2.3); not a request |
| `*.js` | a script (section 2.4) |
| `.order` | the folder's display order (section 2.2) |
| `env/` at the root | environments (section 4); not part of the tree |
| `README.md` | a folder's documentation (section 2.5); not part of the tree |
| anything else | ignored: other file types, and any name starting with `.` |

Every node has a stable **ID**: its path relative to the collection root
with `/` separators and no trailing slash, for example `users/create.http`
or `users`. The root's ID is the empty string.

A request node's **display name** is the `@name` directive, then the `###`
title, then the file name without `.http`. A folder's display name is the
directory name. A request node's **method label** is the method of the
first entry with a request line.

### 2.2 Ordering (`.order`)

Each directory may hold one `.order` file: a plain list of names, one per
line, directories with a trailing slash. Blank lines are ignored and lines
starting with `#` are comments.

```
# Auth first, then the CRUD folder, then the smoke test.
auth/
users/
smoke.http
```

Rules:

- Listed entries come first, in the listed order. Folders and requests are
  ordered together; a folder may sit between two requests.
- Unlisted entries follow, sorted alphabetically: case-insensitive, with
  byte order as the tie-break. Folders and requests are mixed in that sort.
- A missing `.order` means everything is alphabetical.
- A line matches an entry when it equals the entry's exact name
  (`create.http`, `users/`). As a convenience a bare line with no slash and
  no `.http` suffix (`create`) matches the file `create.http`, or failing
  that the directory `create`. Otis always writes exact names.
- A line that matches nothing produces a warning and is otherwise ignored.
  A line that repeats an earlier match produces a warning; the first
  occurrence wins.
- `.order` is **never rewritten** when a request or folder is added. Only an
  explicit reorder writes it, and it then lists every entry exactly once.

A reorder is a drag in the sidebar, or the folder menu's Manual/Alphabetical
(screen 2a). What Otis writes:

```
# Order maintained by Otis. Drag rows in the sidebar to change it.
# Unlisted entries sort alphabetically after these.
cancel-order.http
create-order.http
fixtures/
```

Exact names, one per line, a trailing slash on a folder, and every entry of
the directory listed once — a partial list would leave the rest to sort
alphabetically after it, silently moving rows the drag never touched. The two
comment lines are the only thing in the file that is not a name, and they are
fixed: nothing in a `.order` Otis wrote depends on when it wrote it, so two
reorders producing the same order produce the same bytes.

Everything else leaves the file alone. Adding a request does not touch it; the
new file is unlisted and therefore sorts alphabetically after the listed ones,
which is the whole mechanism. Saving a request, running a folder, importing —
none of them write it. An import into a directory that already holds a
`.order` is refused rather than merged (`.order` is the one hidden file that
counts as content for that check); `--force` overwrites it along with
everything else in the directory, which is what that flag says.

Switching a folder to alphabetical **deletes** its `.order`. Deleting the file
by hand does the same thing, which is why there is no other representation of
"this folder is alphabetical" — the absence of the file is it.

Moving an entry between folders rewrites the destination's `.order` (the
arrival has to have a position) and the source's (the departure has to leave
the list). A source folder that had no `.order` does not acquire one: its
remaining entries are alphabetical, which is what they already were.

### 2.3 Folder settings (`_folder.http`)

`_folder.http` uses the request-file syntax (section 1) and normally holds
a single entry without a request line (section 1.9): comments, variables,
directives and headers. Those cascade to every request in the folder and
its sub-folders; section 3 defines how. A request line in `_folder.http` is
ignored with a warning. A `_folder.http` that fails to parse produces a
warning and contributes no settings; the folder still appears.

### 2.4 Scripts (`*.js`)

A `.js` file in a collection is a script. Scripts are part of the tree,
because a file that changes what a request does while staying invisible is
exactly what this format exists to avoid. Two kinds, told apart by name:

| Name | Kind | When it runs |
| --- | --- | --- |
| `_pre.js` | folder hook | before every request in the folder and below |
| `_post.js` | folder hook | after every response in the folder and below |
| `<name>.pre.js` | request hook | before `<name>.http` only |
| `<name>.post.js` | request hook | after `<name>.http` only |
| anything else | module | never on its own; only when a hook imports it |

- A **hook** runs automatically. A **module** is a plain ES module: nothing
  runs it unless a hook imports it. The convention is to keep modules in a
  `lib/` directory at the root, but that is a convention and not a rule —
  what decides the kind is the name, and a surface showing a script says
  which kind it is rather than leaving the reader to know the convention.
- `<name>.pre.js` is a request hook only when `<name>.http` is in the same
  directory. `utils.pre.js` beside no `utils.http` is a module with an
  unfortunate name; calling it a hook would say it runs when nothing will
  ever run it.
- A script's display name keeps its `.js`, unlike a request's, so a tree row
  reads as the file it is.
- Scripts are listed in `.order` like any other entry (section 2.2).

What a script can do, the sandbox it runs in, and the order hooks run in are
section 9.

### 2.5 Folder documentation (`README.md`)

A folder may hold a `README.md`. It is the folder's documentation and is
rendered in the folder view. It is not part of the request tree and has no
effect on any request.

### 2.6 Warnings

Loading is lenient. The following are warnings, not errors; the tree is
always produced.

| Code | Condition | Effect on the tree |
| --- | --- | --- |
| `parse-error` | a request file or `_folder.http` fails to parse | request node marked broken with the error; folder loses its settings |
| `multiple-requests` | a request file has more than one entry with a request line | first request is used |
| `no-request-line` | a request file (not `_folder.http`) has no request line | node appears with no method |
| `folder-has-request` | `_folder.http` contains a request line | the request line is ignored |
| `order-missing` | `.order` names an entry that does not exist | line ignored |
| `order-duplicate` | `.order` lists an entry twice | first occurrence wins |
| `unreadable` | a directory or `.order` could not be read | subtree or order skipped |

Warning paths are relative to the collection root.

## 3. Inheritance

A request's effective headers and auth are computed by walking the
`_folder.http` files from the collection root down to the request's folder,
then applying the request itself. Every effective value records its
**provenance**: the file (relative to the root) and line it came from.

Inheritance is purely structural. `{{variables}}` in header values and auth
arguments are resolved afterwards (section 4).

### 3.1 Headers

Levels are applied outermost first. At each level, in two passes:

1. Every header name the level mentions removes the inherited headers of
   that name. Names compare case-insensitively.
2. The level's own headers are appended in file order.

Consequences:

- **Nearest definition wins.** A header defined in `users/_folder.http`
  replaces the same header from `_folder.http`; a header in the request
  replaces both.
- **Override is total.** There is no way to send two values of one header
  from different levels. Duplicates *within* one level are all kept and
  sent in file order.
- **Order.** Effective headers are ordered root-most first, request last; an
  overriding header takes the position of its own level, not the position
  of the header it replaced.

### 3.2 Disabling an inherited header (`!inherit`)

A header whose value is exactly `!inherit` removes the inherited header of
that name and is itself **not sent**:

```
# users/_folder.http
X-Tenant: !inherit
```

A nearer level may define the header again. `!inherit` where nothing above
defines the header is a no-op. Every marker is recorded, with the headers it
removed, so a review or the UI can show what was switched off and where.

### 3.3 Auth (`# @auth`)

```
# @auth bearer <token>
# @auth basic <username> [<password>]
# @auth aws [profile=<name>] [key=<id> secret=<key> [token=<session>]] [region=<r>] [service=<s>]
# @auth none
```

- The scheme is case-insensitive. `bearer` takes exactly one token. `basic`
  takes a username and an optional password; the password is the rest of
  the line and may contain spaces. `none` takes nothing.
- `aws` signs the request with AWS Signature Version 4. Its arguments are
  `key=value` pairs, each at most once, none empty:
  - **Credentials** come from one of two sources. `profile=<name>` uses that
    profile from `~/.aws/config` and `~/.aws/credentials` through the AWS
    SDK default chain, so SSO, assume-role, `credential_process` and MFA
    sessions all work as they do for the `aws` CLI. With no `profile=` and
    no keys, the default chain applies (`AWS_PROFILE`, environment
    variables, the `default` profile). `key=` and `secret=` give explicit
    static credentials, optionally with `token=` for a session; they cannot
    be combined with `profile=`, and one of `key=`/`secret=` without the
    other is an error. Explicit `secret=` and `token=` values are always
    treated as secrets for masking, whatever variable they came from.
    Committing real keys in a collection is discouraged; prefer
    `profile=`, or a secret reference (section 5) if keys are unavoidable.
  - **Region** is `region=`, else the profile's region, else derived from
    the host. **Service** is `service=`, else derived from the host. Hosts of
    the form `<...>.<service>.<region>.amazonaws.com` and the legacy
    `<service>.amazonaws.com` (region `us-east-1`) are recognised. When
    either cannot be determined the request fails with an error saying
    which argument to add.
  - The signature covers every header being sent plus `X-Amz-Date` and
    `X-Amz-Content-Sha256` (the SHA-256 of the body, always signed; there
    is no unsigned-payload mode). Temporary credentials add
    `X-Amz-Security-Token`. Presigned query-string signing is not
    supported.
  - Credential lookups are cached per collection session in memory. A
    failed lookup names the profile and the SDK's reason, never key
    material.
  - **Consent.** `profile=` and the default chain read the user's own AWS
    credentials from the machine. That is appropriate when the user runs
    Otis directly. Any surface that acts on behalf of something else, such
    as the MCP server *(planned)*, must gate this scheme behind explicit
    per-environment consent.
- The **nearest** `@auth` wins; within one file the last directive wins.
- `none` is an explicit opt-out: the request sends no auth even though a
  folder above declares one. It is distinct from *absent*, which means no
  level declared anything.
- `@auth` is not a header. At send time `bearer <token>` becomes
  `Authorization: Bearer <token>` and `basic <user> <password>` becomes
  `Authorization: Basic base64(user:password)`; `none` adds nothing.
- If the effective headers (section 3.1) already contain `Authorization`
  at any level, that header is sent and the `@auth` is dropped.
- A malformed `@auth` (missing token, unknown scheme, `none` with
  arguments) is an **error** naming the file and line, not a warning.

### 3.4 Broken folder files

A `_folder.http` that fails to parse contributes nothing to inheritance
(and raises a collection warning, section 2.6). Levels below it still
apply.

## 4. Variables and environments

### 4.1 References

A reference is `{{name}}`; whitespace inside the braces is allowed
(`{{ name }}`). `name` matches `[A-Za-z_$][A-Za-z0-9_.-]*`. Anything else
between double braces is literal text, not an error.

References are resolved in the URL, header values (after inheritance,
section 3), `@auth` arguments, and the raw body. They are **not** resolved
in `< ./path` body file paths, in script blocks, or inside a body loaded
from a file unless the `<@` form was used (section 1.8).

### 4.2 Scopes

A name is looked up in this order; the first scope that defines it wins:

1. `@var` declarations in the request file. Within a file the last
   declaration wins.
2. For each folder from the request's own up to the root:
   a. the folder's **session** value for the name (section 4.5);
   b. `@var` declarations in that folder's `_folder.http`.
3. The **session** value for the active environment (section 4.5), then the
   active environment itself (section 4.3).
4. Builtins (section 4.4).

Levels 2 and 3 interleave the session layer with the committed one rather than
stacking it above: nearest definition wins, and at one level a session value
beats the file. So `orders/_folder.http` declaring `currency` still beats a
session value set for the root, and a session value set for `orders/` beats
`orders/_folder.http`.

A file-scoped value may itself contain references; they are resolved
recursively against the full scope. A value that refers back to itself,
directly or through other variables, is an error naming the chain
(`variable cycle: a -> b -> a`). Environment values, secrets and session
values are literal and are not expanded.

Every name that cannot be resolved is collected, and one error lists all
of them in first-use order (`unresolved variables: host, token`). Unknown
`$builtins` are reported the same way.

Every resolved value records its provenance: the scope it came from and, for
file scopes, the file and line.

### 4.3 Environments (`env/<name>.json`)

Environments live in `env/` at the collection root and are not part of the
request tree. Each is a flat JSON object:

```json
{
  "baseUrl": "https://dev.example.com",
  "port": 8443,
  "debug": true,
  "token": {"$secret": "keychain"}
}
```

- Values must be strings, numbers, booleans, or a secret reference. Numbers
  and booleans are used as written (`8443`, `true`). `null`, arrays and any
  other object are errors naming the key.
- `{"$secret": "keychain"}` is a **secret reference** (section 5). No other
  backend name is accepted.
- The environment name is the file name without `.json`. It must not
  contain path separators.
- Keys beginning with `$` are **reserved**. `$otis` is the only one defined
  (below); any other is an error naming the key. Nothing reserved is a
  variable: `$otis` is not in scope, and `{{$otis}}` is an unresolved
  reference like any other unknown `$builtin` (section 4.4).

**Environment settings (`$otis`).** An environment may carry its own
settings under the reserved key `$otis`, an object with these fields, all
optional:

```json
{
  "$otis": {"confirmBeforeSend": true, "description": "production"},
  "baseUrl": "https://api.acme.dev"
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `confirmBeforeSend` | boolean | Ask for a confirmation before every send resolved against this environment. This is what marks production. |
| `description` | string | A one-line note shown beside the environment. |

An unknown field inside `$otis` is preserved and ignored, the same way an
unknown directive is (section 1.4).

These are **committed**, deliberately. Whether an environment is the
dangerous one is a fact about the environment, not a per-machine
preference: the whole team should get the same confirmation on the same
environment, a new clone should get it without configuring anything, and
turning it off should show up in review. Which environment is *active*, by
contrast, is per-machine and is not in the collection at all — one person
works against staging while another is on local, from the same branch — so
it lives in the settings file.

**Canonical form.** Otis writes an environment file with a two-space
indent, one key per line, in the file's own key order with new keys
appended after it (sorted), each value in the JSON shape it was read as,
and `$otis` first when there is one. Parsing a canonical file and writing
it back produces identical bytes, so changing one variable does not
reshuffle a teammate's file or turn `8443` into `"8443"` — the same bargain
section 1.13 makes for a `.http` file.

### 4.4 Builtins

| Reference | Value |
| --- | --- |
| `{{$uuid}}` | a random version-4 UUID |
| `{{$timestamp}}` | current Unix time in seconds |
| `{{$isoTimestamp}}` | current time as RFC 3339 in UTC, e.g. `2026-09-02T15:04:05Z` |
| `{{$randomInt}}` | a random integer from 0 to 1000 inclusive |

Builtins are evaluated separately for each occurrence: two `{{$uuid}}` in
one request yield two different values. JetBrains' parameterised forms such
as `{{$random.integer(0, 100)}}` are not supported and are reported as
unresolved.

### 4.5 Session variables

A **session variable** is a value a run sets, held in memory for as long as the
collection is open. It is the only value in Otis that is in no file.

```
vars.session.set("orderId", body.id)    sets it for the request's folder
```

- **Scope.** A session variable belongs to one folder — visible to every
  request in it and below it. Its place in resolution is section 4.2. There is
  no session variable at request scope: a value that lives for one execution is
  `vars.request` (section 9.4) and never reaches this layer.
- **Not `vars.folder`.** The call that sets one is named for its *lifetime*,
  not for the thing that keys it. `_folder.http` declares committed variables
  with `@name = value`, and a call named `vars.folder.set` reads as setting one
  of those — it does not, it sets a value that is in no file. Section 9.4 has
  the whole argument.
- The environment scope of resolution (section 4.2, step 3) has no writer in
  the script API and is reserved: `vars.env.set` writes the committed
  environment file instead, which is a different thing with a different
  lifetime (section 9.4).
- **Never written.** Not to the collection, not to the settings file, not to a
  log, not to an export. Closing the collection forgets every session
  variable, and so does an explicit clear. A teammate who pulls the branch
  sees no trace of one, which is what makes it safe for the id of an order
  somebody created against staging five minutes ago.
- **Provenance.** Every session variable records the request whose run set it
  and the time it was set. Those are the whole account of a value that is in
  no file, so a surface showing session variables shows both.
- **Literal.** The value is used exactly as it was set. It is data a run
  produced, not a template somebody wrote, so `{{` arriving in a response
  cannot reach into the variable scope.
- **Not a way to avoid committing configuration.** A session variable is
  scratch state between requests. Anything a teammate needs belongs in
  `_folder.http` or in an environment, where it is reviewable.

Setting one is a script's job: `vars.session.set` (section 9.4). The scope,
the store and the read-only surface arrived in Increment 11 and the writer in
Increment 15.

## 5. Secrets

Secret values never live in the collection. A committed environment file
holds only the reference `{"$secret": "keychain"}`; the value is stored on
the user's machine and looked up by the key

```
<collection>/<env>/<name>
```

where `<collection>` is the collection's **display name**, `<env>` the
environment name and `<name>` the variable name.

The display name is the root directory's base name, except for a
dot-directory, where it is the parent's name: a collection rooted at
`~/code/acme-api/.requests` is `acme-api`, so its staging API key is stored
under `acme-api/staging/apiKey`. The parent's name rather than the literal
one, because a collection kept beside the code it exercises is conventionally
called `.requests` — and keying on that would give every such collection on
the machine the same key, so two projects would share one entry per
environment and variable, and opening the second would resolve the first's
credential. The cost of the rule is that moving or renaming the directory a
collection lives in moves its secrets, and they have to be set again.

Rules:

- A secret reference whose value is not in the store is an error naming the
  key, never the value.
- Resolution never places a secret value in an error message, a warning,
  or the list of variables used; the list marks the variable as secret
  instead.
- Anything derived from a resolved request that leaves the Go process must
  be passed through masking, which replaces every secret value used by the
  request with `•••••`.
- Scripts receive an opaque handle to a secret, never the string
  (section 9.7).

Only an in-memory store exists today; the OS keychain backend is a later
increment. Until it lands, the CLI stocks the in-memory store from
environment variables named `OTIS_SECRET_<NAME>`, which is also how CI is
expected to supply secrets. The suffix is matched leniently: both it and the
variable name are upper-cased and every character that is not a letter or a
digit becomes `_`, so `OTIS_SECRET_API_KEY` supplies `apiKey`, `api-key` or
`api.key`.

## 6. Sending

What happens when a resolved request is sent:

- The HTTP version on the request line is informational; the client
  negotiates the protocol.
- Headers are sent in effective order (section 3.1). A `Host` header sets
  the request's Host rather than being sent as a plain header.
- Redirects are followed up to 10 hops unless `@no-redirect` is set; each
  hop (URL, status, location) is recorded. Exceeding the limit is an error.
- Cookies are kept in a per-collection session in memory only and are never
  written to disk. `@no-cookie-jar` bypasses the session for one request.
- The whole exchange is bounded by `@timeout` (default 30 s). A timeout is
  an error, not a response.
- The response body is read fully into memory. A 4xx or 5xx status is a
  response, not an error; the CLI maps it to exit code 1.
- Recorded timing: DNS, connect, TLS handshake, time to first byte and
  total. Values describe the last hop; DNS and connect are zero on a reused
  connection.

## 7. Postman import

`otis import postman` converts a Postman Collection v2.1 export (the v2.0
auth shape is also accepted) into a collection laid out as above. The
output is meant to be committed as-is and read in review, so the importer
prefers explicit, boring files over clever ones.

| Postman | Otis |
| --- | --- |
| collection | the output directory; collection auth, variables and description go to `_folder.http` |
| folder | a directory; folder auth, variables and description go to its `_folder.http`. An otherwise empty folder gets a `_folder.http` with a comment so the directory exists. |
| request | `<slug>.http`, one per file, with `# @name <original name>` first, then the description as `#` comments |
| item order | `.order` in every directory, listing every child by exact name |
| pre-request / test script | `_pre.js` / `_post.js` beside `_folder.http`, or `<slug>.pre.js` / `<slug>.post.js` beside a request. Raw, untranslated, not executed. |
| environment export | `env/<slug>.json`; variables of type `secret` become `{"$secret": "keychain"}` and their values are **not** imported |

**Slugs.** Names are lower-cased; runs of anything other than ASCII letters
and digits become one hyphen; non-ASCII letters are dropped. Collisions get
`-2`, `-3`, ... in Postman order. An empty slug becomes `request` or
`folder`.

**URLs.** Structured URLs are rebuilt from their parts (protocol, host,
port, path, enabled query parameters, hash); a string URL is used as-is.
Postman path variables (`:id`) become `{{id}}` with an `@id = <value>`
declaration when Postman carried a value. Disabled query parameters,
headers, form fields and bodies are dropped and listed in the report.

**Bodies.** `raw` is verbatim; `urlencoded` becomes `key=value&...` with
values percent-encoded except for `{{references}}`; `formdata` becomes a
multipart body with the boundary `----OtisFormBoundary`, file fields as
`< <path>` lines; `file` becomes `< <path>`; `graphql` becomes the JSON
envelope Postman sends. When Postman implied a `Content-Type` (raw language,
form modes, GraphQL) and none was set, it is added. File paths are the
exporting machine's paths and are flagged for fixing.

**Auth.** `bearer`, `basic` and `noauth` map to `@auth`. `apikey` in a
header becomes a header; in the query string it is appended to the request
URL (and skipped with a note at folder level, where there is no URL).
`awsv4` becomes `@auth aws key=... secret=... [token=...] [region=...]
[service=...]`; a literal secret key or session token is replaced by
`{{awsSecretKey}}` / `{{awsSessionToken}}` so no AWS key material is written
to disk, and the report suggests `profile=` instead. Other types (OAuth2,
digest, NTLM, ...) are skipped with a `# TODO` comment in the file. Literal
credentials of any kind are flagged in the report.

**Dynamic variables.** `{{$guid}}` and `{{$randomUUID}}` become
`{{$uuid}}`; `{{$timestamp}}`, `{{$isoTimestamp}}` and `{{$randomInt}}` are
kept. Other `{{$...}}` forms are left as written and flagged.

**Safety.** The importer refuses a non-empty output directory unless forced,
never writes a value marked secret, and never writes `.order` for a
directory it did not create.

## 8. Command line

The same binary is the desktop app and the CLI: with no arguments it opens
the window, with arguments it runs a command.

```
otis ls [dir]                                       list a collection as a tree
otis run <file.http> [-e env] [--json]              resolve and send one request
otis import postman <file.json> -o <dir> [--env f]  import a Postman export
otis version                                        the build identity
otis <path>                                         open the window on a path
```

`otis version` and `otis --version` print the same block: the version, the
commit it was built from, the build date, the Go toolchain and the target.

```
otis v0.2.0
commit    1a2b3c4
built     2026-09-03T10:04:00Z
go        go1.25.5
platform  darwin/arm64
```

All five rather than just the version, because Otis ships no auto-updater, so
"what are you running" has to be answerable by hand and complete enough for a
bug report. A binary that the release pipeline did not stamp — `go install`,
or a plain `go build` — reports `dev` with `commit unknown` rather than
printing blanks.

**A path opens the window.** An argument that is not a flag and not the name
of a command, and that names a file or directory that exists, is a path to
show rather than a command line: `otis .` opens the window on the collection
in the working directory, and `otis orders/create-order.http` opens it on that
request. Anything else is a command, so a mistyped one still gets the usage
error it deserves.

This exists because a file association gives Otis no choice. Windows and Linux
hand a double-clicked file to the app as `argv[1]`, so a `.http` file arrives
looking exactly like a command line, and the two have to be told apart.
`otis .` is the same rule read the other way round, and it is what every editor
does.

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | success; for `run`, a response with status < 400 |
| 1 | `run` only: the server answered 4xx or 5xx |
| 2 | anything else: usage, parse, resolve, network or timeout errors |

`otis run` takes a file path and finds the collection root above it, so
inheritance and `env/` work from anywhere. A directory holding `env/` is the
root; otherwise the root is the highest ancestor still reachable through
directories carrying a `_folder.http` or `.order`, never crossing a
directory that holds `.git`. `-C` overrides the discovered root.

Resolved request headers are printed **masked** (section 5). Masking is
presentation only: the real value is what goes on the wire. Response bodies
are printed exactly as received, never reformatted; a body that is not valid
UTF-8 is summarised in text output and base64-encoded under `bodyBase64` in
`--json`.

## 9. The script API

Scripts are where a collection stops being data and starts being a program,
so this section is the part of the format that most needs to be right the
first time: once people write scripts against it, it cannot change.

Syntax is section 1.10 (`< {% %}` and `> {% %}` blocks) and section 2.4
(`_pre.js`, `_post.js`, `<name>.pre.js`, `<name>.post.js`, and modules). This
section is what those scripts can do.

Otis is **not** compatible with the JetBrains or Postman script APIs. It uses
their delimiters so their tools ignore its blocks; the contents are Otis'.

### 9.1 What runs, and in what order

A send runs two phases. **Pre-request**, outermost first:

1. `_pre.js` in the collection root
2. `_pre.js` in each folder on the way down, root-most first, ending with the
   request's own folder
3. `<name>.pre.js` beside `<name>.http`
4. each `< {% %}` block in the request file, in file order

**Post-response**, the exact reverse:

1. each `> {% %}` block in the request file, in file order
2. `<name>.post.js`
3. `_post.js` in the request's own folder
4. `_post.js` in each folder outwards, ending with the collection root

The order is the one that lets a folder set up what its requests need before
any of them run and assert on the result after: outermost prepares first and
concludes last, and the request's own block sits nearest the request. A
`_pre.js` at the root can mint a token every request will use; a `_post.js` at
the root can assert something every response must satisfy.

Every script in a phase shares one JavaScript realm for that send, so a value
one hook puts on `vars` is visible to the next. The realm is created for the
send and destroyed with it: nothing survives from one send to the next except
what a script deliberately writes to a scope that outlives it (section 9.4).

### 9.2 Scripts and variable resolution

**Pre-request hooks run before `{{variable}}` resolution.** This is what makes
a folder header like `Idempotency-Key: {{idemKey}}` work when `_pre.js` is
what sets `idemKey`.

The consequence is worth stating plainly: in a pre-request hook,
`request.url`, the header values and the body are the **template**, with their
`{{...}}` references unresolved. A hook that wants a resolved value asks for
it — `vars.get("baseUrl")` resolves — rather than reading it off `request`.

The full pipeline of a send:

1. Inheritance is computed (section 3). Structural only; nothing is resolved.
2. Pre-request scripts run, in the order of 9.1. They may set variables and
   change the request.
3. `{{variables}}` are resolved (section 4), now including whatever the
   scripts set.
4. The request is prepared and sent (section 6).
5. Post-response scripts and tests run, in the order of 9.1.

### 9.3 The sandbox

A script gets a plain JavaScript realm and nothing else. There is no
filesystem, no process, no network, and no clock you can wait on:

| Absent, and never to be added | Why |
| --- | --- |
| `require`, dynamic `import()` | modules are static and resolved by Otis (section 9.8) |
| `fetch`, `XMLHttpRequest`, `WebSocket` | a script may shape the request Otis is about to send, not send one of its own |
| `process`, `os`, `fs`, `child_process` | no filesystem, no environment, no spawning |
| `setTimeout`, `setInterval` | a send is synchronous; a script that could wait could hang one forever |

What is there: the JavaScript standard library (`Object`, `Array`, `String`,
`Number`, `Math`, `JSON`, `Date`, `RegExp`, `Map`, `Set`, `Promise`, `Proxy`,
`BigInt`, …), plus exactly these:

| Global | What it is |
| --- | --- |
| `console` | `log`, `info`, `warn`, `error`, `debug`. Captured, masked (section 9.7) and shown in the window; never written to a file. |
| `crypto.randomUUID()` | a random version-4 UUID, the one thing a script genuinely cannot do for itself |
| `request` | the request about to be sent (section 9.5); pre-request only |
| `response` | what came back (section 9.6); post-response only |
| `vars` | the variable scopes (section 9.4) |
| `secrets` | opaque handles to secret values (section 9.7) |
| `test`, `expect` | assertions (section 9.9) |

**Timeout.** Each phase gets its own budget, five seconds by default,
overridden per request with `# @script-timeout SECONDS`. Exceeding it is a
hard kill: the interpreter is interrupted wherever it is, the send fails with
a timeout naming the phase, and no partial result is kept. An infinite loop is
therefore an error with a message, not a hung window. The directive takes a
positive number; a non-positive or non-numeric value is an error, exactly as
`@timeout` is (section 1.4).

### 9.4 Variables: `vars`

Three scopes, and they are three *lifetimes*. That is the axis that matters,
so it is the axis the names describe:

| Call | Lifetime | Written where |
| --- | --- | --- |
| `vars.request.set(k, v)` | this send only | nowhere |
| `vars.session.set(k, v)` | until the collection closes | nowhere — memory, this machine |
| `vars.env.set(k, v)` | until somebody changes it | `env/<active>.json`, **committed** |

- **`vars.request`** is scratch for one execution: a value one hook hands to
  the next, or to a `{{reference}}` in this request. It never reaches the
  layer section 4.5 describes and is forgotten the moment the send ends.
- **`vars.session`** is a session variable (section 4.5), keyed by the
  request's own folder: visible to every request in that folder and below it.
  In memory, on this machine, written nowhere.
- **`vars.env`** writes the active environment file. It is the one call in the
  API that changes a committed file, and it is deliberately the loudest: the
  change lands in `env/<name>.json`, shows up in `git diff`, and is reviewed
  like anything else. With no active environment it is an error, because there
  is no file to write.

**`vars.session` is not `vars.folder`.** An earlier draft of this section, and
the design it came from, called the middle scope `folder`, and that was a
mistake worth being explicit about: `_folder.http` declares committed
variables with `@name = value`, and a call named `vars.folder.set` reads as
setting one of *those*. It does not — it sets a value that is in no file at
all. The scope is still keyed by the folder, and section 4.5 still calls the
thing a session variable; the API now says the same word.

**Reading.** `vars.get(k)` resolves a name exactly as a `{{reference}}` in
the file would (section 4.2): request declarations, then each folder from the
request's own out to the root with its session values interleaved, then the
active environment, then the builtins. Nothing else in the API takes a
shortcut around that order, so a value read in a script and a value
interpolated into a header are always the same value.

`vars.<scope>.get(k)` reads one scope without falling through, which is how a
script asks "did *I* set this?" rather than "what would resolve here?".

- Keys must be names a `{{reference}}` can use (section 4.1). Anything else is
  an error naming the key, because a key nothing can reference would be a
  value nothing can read.
- Values are converted to strings the way JavaScript would, except that
  `null`, `undefined` and a function are errors: they are almost always a bug
  rather than an intention, and a header reading `undefined` is a worse
  outcome than a message.
- A secret handle (section 9.7) may **not** be stored in a variable. The
  handle is the thing that keeps a value out of places it should not be, and a
  scope is one of those places.

### 9.5 The request: `request`

Available in a pre-request script only. Reading it in a post-response script
gives the request as it was sent, masked.

| Member | What it does |
| --- | --- |
| `request.method` | get or set the method |
| `request.url` | get or set the URL — **the template**, see section 9.2 |
| `request.headers.get(name)` | case-insensitive |
| `request.headers.set(name, value)` | replaces every header of that name |
| `request.headers.add(name, value)` | appends, keeping any existing |
| `request.headers.remove(name)` | removes every header of that name |
| `request.headers.names()` | the names, in send order |
| `request.body` | get or set the raw body |
| `request.path` | the request's node path, read-only |
| `request.name` | the request's display name, read-only |

A header set by a script wins over the file and over inheritance: it is the
last word before resolution. `{{references}}` in a value a script sets are
resolved in step 3 like any other, so a script may write a template.

### 9.6 The response: `response`

Available in a post-response script only.

| Member | What it is |
| --- | --- |
| `response.status` | the status code, a number |
| `response.statusText` | `"Created"` |
| `response.ok` | `status < 400` |
| `response.headers.get(name)` / `.names()` | case-insensitive |
| `response.body` | the body as text |
| `response.json()` | the body parsed; throws with the parse error if it is not JSON |
| `response.timings` | `{ dns, connect, tls, ttfb, total }`, milliseconds |
| `response.size` | the body's size in bytes |

### 9.7 Secrets: `secrets`

`secrets.ref("apiKey")` returns an **opaque handle**, never a string. It is
the only way a script may touch a secret, and the handle's whole purpose is
that the value cannot get out of the places it belongs.

- Everywhere JavaScript would turn it into text — `String(h)`, `` `${h}` ``,
  `"" + h`, `console.log(h)`, `JSON.stringify(h)`, a thrown error's message, a
  test's failure output — it yields `[secret:apiKey]`. The real value is not
  reachable from JavaScript at all.
- A handle may be given as **a whole header value or as the whole body**. Otis
  substitutes the real value when it prepares the request, after every script
  has run, and masks it again in everything the window is shown (section 5).
- Composition without stringifying: `secrets.ref("apiKey").prefix("Bearer ")`
  and `.suffix(s)` return new handles. That is how `Authorization: Bearer
  <secret>` is expressed from a script — though `@auth bearer {{apiKey}}` in
  the file is the better way to say it, and needs no script at all.
- `secrets.has("apiKey")` reports whether a value is stored on this machine,
  which is a question with a safe answer.
- A handle cannot be stored in a variable (section 9.4), compared, or
  inspected. `secrets.ref` on a name the active environment does not declare
  as a secret is an error naming the key.

### 9.8 Modules

`import` resolves to files **inside the collection** and nowhere else. There
is no package registry, no node_modules, and no network.

```js
import { idempotencyKey } from "../lib/idempotency.js";
import * as assert from "../lib/assert.js";
import format from "../lib/format.js";
```

- A specifier is a relative path from the importing file, must end in `.js`,
  and must resolve inside the collection root. A bare specifier (`"lodash"`),
  an absolute path, a URL, or anything that escapes the root is an error
  naming the specifier.
- A module is evaluated once per send, whatever imports it, and its exports
  are shared. An import cycle is an error naming the chain.
- A module gets the same sandbox as a hook, minus `request`, `response`,
  `test` and `expect`: it is a library, not a step. `vars`, `secrets`,
  `console` and `crypto` are there.
- The supported syntax is a **subset**, and a form outside it is an error
  naming the file and line rather than being silently misread:
  `import {a, b as c} from "..."`, `import * as ns from "..."`,
  `import d from "..."`, `export function`, `export const`/`let`/`var`,
  `export default`, and `export { a, b as c }`. Declarations must start at the
  beginning of a line.

### 9.9 Tests: `test` and `expect`

```js
test("201 created", () => expect(response.status).toBe(201));
test("two line items", () => {
  expect(response.json().line_items).toHaveLength(2);
});
```

`test(name, fn)` runs `fn` immediately and records whether it threw. A test
that throws is a failure carrying the message; a test that returns is a pass.
Results reach the window as each one finishes, so a long suite fills in rather
than appearing at the end. `test` is available in post-response scripts only:
a test is an assertion about a response.

`expect(actual)` gives:

| Matcher | Passes when |
| --- | --- |
| `.toBe(expected)` | strictly equal (`===`) |
| `.toEqual(expected)` | deeply equal |
| `.toBeTruthy()` / `.toBeFalsy()` | JavaScript truthiness |
| `.toBeDefined()` / `.toBeUndefined()` | `!== undefined` / `=== undefined` |
| `.toBeNull()` | `=== null` |
| `.toContain(item)` | a string contains a substring, or an array an element |
| `.toHaveLength(n)` | `.length === n` |
| `.toMatch(re)` | a string matches a regular expression |
| `.toBeGreaterThan(n)` / `.toBeLessThan(n)` | numeric comparison |
| `.not` | inverts any of the above |

A failure message names the matcher, what was expected and what was received,
with every secret handle masked. Deliberately small: a matcher set that grows
to cover everything is a second language to learn, and `expect(x).toBe(true)`
around an ordinary JavaScript expression covers the rest.

### 9.10 Errors

A script that throws fails the send. The failure carries the file, the line
and the message — `orders/_pre.js:3: idempotencyKey is not a function` — and
is classified as a script failure rather than a transport one, so the response
pane can say which phase it was and show the console output that led up to it.

A post-response script that throws does **not** discard the response: the
response arrived, and hiding it because a script about it failed would lose
the thing you need in order to fix the script.
