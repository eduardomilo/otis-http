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
| `@auth ...` | Authentication; see section 3 *(planned, Increment 3)*. |

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

is a file reference. The parser records the path relative to the `.http`
file and does not read it. A `< ./path` line that is part of a larger body
(for example inside a multipart boundary) is left as body text; resolving
it is the sender's job *(planned)*.

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
Several blocks of each kind may appear and are kept in order. Execution is
defined in a later increment *(planned)*.

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

## 2. Collections *(planned, Increment 2)*

### 2.1 Layout

A collection is a directory. Sub-directories are folders. Every `*.http`
file except `_folder.http` is a request.

### 2.2 Ordering (`.order`)

One optional `.order` file per directory: a plain list of entry names, one
per line, directories with a trailing `/`. Listed entries come first, in
the listed order; unlisted entries follow, sorted alphabetically. A missing
`.order` means alphabetical. `.order` is never rewritten on add, only on
an explicit reorder.

### 2.3 Folder settings (`_folder.http`)

Same syntax as a request file, normally a single entry without a request
line (section 1.9). Its headers, variables and directives cascade to every
request below it.

## 3. Inheritance *(planned, Increment 3)*

## 4. Variables and environments *(planned, Increment 4)*

## 5. Secrets *(planned, Increment 4)*
