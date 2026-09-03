# Otis

A file-based HTTP client. A collection is a directory of `.http` files in your
repository: it versions with git, reviews as a diff, and works without an
account. The same binary is the desktop app and the `otis` command line.

Otis reads the format the JetBrains HTTP Client and the VS Code REST Client
already read, and adds nothing they reject — its extensions live in comment
directives and `{% %}` script blocks, both of which those tools tolerate. See
[`docs/FORMAT.md`](docs/FORMAT.md).

## Install

**Nothing Otis ships is signed** — not on macOS, not on Windows. That is a
deliberate deferral until there are enough users to justify the certificates,
and it means each platform warns you once. The `SHA256SUMS` file attached to
every [release](https://github.com/otis-http/otis/releases) is the only
integrity signal, so verify before installing:

```bash
shasum -a 256 -c SHA256SUMS --ignore-missing
```

### macOS

```bash
brew install otis-http/otis/otis
```

The smoothest path, and worth preferring: Homebrew does not quarantine what it
downloads, so an unsigned Otis installed this way **never raises Gatekeeper**.
You get the `otis` command, and running `otis` with no arguments opens the app.

What you do not get is a bundle — no Dock icon, no Launchpad entry, and no
`.http` file association, since those come from `Otis.app`. For those,
download `otis_<version>_darwin_universal.dmg` and drag Otis to Applications.
Because it is unsigned you will see:

> "Otis" cannot be opened because it is from an unidentified developer.

**Right-click (or Control-click) Otis → Open**, then **Open** again in the
dialog. Double-clicking will not offer that choice; the right-click is what
does. You do this once.

The DMG is a universal binary and needs macOS 12 or later.

### Windows

Download `otis_<version>_windows_amd64_setup.exe` and run it. SmartScreen will
say:

> Windows protected your PC.

**More info → Run anyway.** The installer bundles the WebView2 bootstrapper
and registers `.http` files with Otis.

For the command line, use `otis_<version>_windows_amd64.zip` and put `otis.exe`
on your PATH. It is a different link of the same source, and the reason is
worth knowing: the installed app is a GUI-subsystem binary, which Windows
never waits for, so its exit code is lost and `otis run` could not gate a CI
step. [`docs/BUILDING.md`](docs/BUILDING.md) §9 has the details.

### Linux

Download the `.deb` or the `.rpm`:

```bash
sudo apt install ./otis_<version>_linux_amd64.deb     # Debian, Ubuntu
sudo dnf install ./otis_<version>_linux_amd64.rpm     # Fedora, RHEL
```

Both register the `.http` file type. The `.AppImage` needs no install but
cannot register a file type — nothing about a single executable is installed
system-wide.

Otis links against **GTK4 and WebKitGTK 6.0**, which sets the floor at Ubuntu
24.04, Debian 13, Fedora 39 or RHEL 10. On anything older, use the command
line below; [`docs/BUILDING.md`](docs/BUILDING.md) §10 explains why there is no
build for the older WebKit.

### The command line, anywhere

```bash
go install -tags otis_cli github.com/otis-http/otis@latest
```

Pure Go — no cgo, no platform toolkit, no frontend bundle — so it works on any
platform a Go toolchain does, including a CI runner with nothing installed.
It gives you `otis ls`, `otis run` and `otis import`, and no window.

## Use

```bash
otis                       # open the app
otis .                     # open the app on the collection here
otis ls                    # print the collection as a tree
otis run orders/create-order.http -e staging
otis import postman collection.json -o ./requests
otis --version
```

`otis run` exits 0 on a response under 400, 1 on a 4xx or 5xx, and 2 on
anything that produced no response — a parse error, an unresolved variable, a
timeout. Secrets come from `OTIS_SECRET_*` environment variables in CI and
from the OS keychain on a developer's machine.

## What it is

- **A collection is a directory.** `.http` files, `_folder.http` for shared
  settings, `env/*.json` for environments, `.order` when you care about the
  order. Nothing is hidden in a database.
- **Secrets never touch the collection.** An environment file holds
  `{"$secret": "keychain"}`; the value lives in the OS keychain and a resolved
  secret value never leaves the Go process — not across a binding, not in a
  log line, not to a script, which sees an opaque handle.
- **Review is a diff.** Otis has a git view because a request collection that
  changes without review is how a team ends up with six versions of the same
  endpoint.
- **Scripts get a JavaScript realm and nothing else.** No filesystem, no
  process, no network, no timers.

## Documentation

| | |
| --- | --- |
| [`docs/FORMAT.md`](docs/FORMAT.md) | the on-disk format and the CLI. Authoritative. |
| [`docs/BUILDING.md`](docs/BUILDING.md) | how the binary is built and packaged, per platform |
| [`docs/RELEASING.md`](docs/RELEASING.md) | cutting a release; what unsigned costs; where signing would go |
| [`docs/design/DESIGN-NOTES.md`](docs/design/DESIGN-NOTES.md) | the design system. Authoritative for anything visual. |
| [`docs/design/SCREENS.md`](docs/design/SCREENS.md) | one section per screen |
| [`CLAUDE.md`](CLAUDE.md) | layout and the constraints that hold it together |

## Develop

```bash
wails3 dev                          # HMR on 127.0.0.1:9245
wails3 build                        # production binary in bin/
go test -race ./...
go vet ./...
npm --prefix frontend run typecheck
```

Verify with `wails3 build` and the binary in `bin/`, not only `wails3 dev`:
dev mode runs on localhost and hides packaging bugs — asset paths and hash
routing in particular.

## Licence

MIT.
