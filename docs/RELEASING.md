# Releasing Otis

How to cut a release, what "unsigned" costs the people installing it, and
exactly where code signing and notarization would go when they arrive.

`docs/BUILDING.md` is what a release is made *of*. This is the procedure.

## 1. Cutting one

```bash
# 1. The packaging metadata's version is bumped by hand. It is the source for
#    Info.plist and the Windows resource, and it is the one thing `git
#    describe` does not supply.
$EDITOR build/config.yml            # info.version: "0.2.0"
wails3 task common:update:build-assets
git add -A && git commit -m "release: 0.2.0"

# 2. Tag it. The tag is what the workflow triggers on, and `git describe`
#    turns it into the version stamped in the binary.
git tag -a v0.2.0 -m "otis v0.2.0"
git push origin main
git push origin v0.2.0
```

That is the whole release. `.github/workflows/release.yml` then:

1. runs `go vet`, `go test -race` and the frontend typecheck, and stops if any
   of them fails — a tag that does not pass its own tests should not become a
   release;
2. builds, signs (a no-op, §3) and packages on macOS, Windows and Linux, as
   three separate steps;
3. collects every artifact, writes **one** `SHA256SUMS` over all of them, and
   checks that each expected artifact is actually present by name;
4. creates the GitHub Release with generated notes under an install-and-verify
   preamble;
5. renders `build/homebrew/otis.rb.tmpl` against the published tarball's real
   hash and commits it to the tap.

**Dry-run first.** Actions → Release → *Run workflow* builds and uploads
everything as run artifacts and creates no release. Worth doing, because a tag
is the awkward part to take back.

`info.version` and the tag can drift, and it matters in one direction only:
the binary always self-reports `git describe`, so `otis --version` is right
regardless, and `darwin:stamp:plist` overwrites the bundle's
`CFBundleShortVersionString` with the same value. What `info.version` alone
decides is the Windows resource version. Keep them in step anyway.

## 2. What "unsigned" means, per platform

Nothing Otis ships is signed. That is a deliberate deferral — certificates
cost money and an Apple Developer account, and neither is worth buying before
there are users — and it has consequences the release notes state plainly
rather than letting people discover.

**The checksums are therefore the only integrity signal a release carries.**
`SHA256SUMS` is generated last, over the whole release, in one file a user can
`shasum -a 256 -c`. It is quoted in the release body as well as attached.

### macOS

The `.app` is **ad-hoc signed** (`codesign --sign -`), which is what lets it
run at all on the machine that built it, but it carries no Developer ID and is
not notarized. A user who downloads the DMG meets Gatekeeper:

> "Otis" cannot be opened because it is from an unidentified developer.

The way through is **right-click (or Control-click) the app → Open**, then
**Open** again in the dialog. Double-clicking does not offer that choice; the
right-click is what does. On recent macOS the alternative route is System
Settings → Privacy & Security → *Open Anyway*, which appears only after the
first refused attempt.

`brew install otis-http/otis/otis` **avoids this completely**, because
Homebrew does not set the `com.apple.quarantine` attribute on what it
downloads, and Gatekeeper's prompt is triggered by that attribute rather than
by the signature. That is why the README and the release notes lead with brew
on macOS. What brew does not give you is the bundle — no Dock icon, no
Launchpad entry, no `.http` association — because those come from
`Otis.app/Contents/Info.plist`. The DMG is for that.

### Windows

The installer is an unsigned NSIS executable, so SmartScreen shows:

> Windows protected your PC.

**More info → Run anyway.** The warning is reputation-based, so it softens as
a given file accumulates downloads and returns with every new release, since
each release is a new file.

### Linux

No signing is expected and none is missing. The deb and rpm are unsigned,
which is normal for packages distributed outside a distro's own repositories;
`apt` and `dnf` only insist on signatures for configured repositories.
`build/linux/Taskfile.yml` does carry `sign:deb` and `sign:rpm` tasks for PGP
signing, unused, if a repository is ever set up.

## 3. Where signing would go

Build, sign and package are **three separate steps** in both workflows, and
the middle one calls a task that currently only prints that it was skipped:

```yaml
- name: Build
  run: wails3 task build
- name: Sign                                   # <- this one
  run: wails3 task sign:${{ matrix.artifact }}
- name: Package
  run: wails3 task release:${{ matrix.artifact }}
```

So adding signing is an edit to one task per platform and no change to any
workflow's shape. The no-op tasks are `sign:darwin`, `sign:windows` and
`sign:linux` in the root `Taskfile.yml`.

### macOS: Developer ID and notarization

The real work already exists as `darwin:sign` and `darwin:sign:notarize`
(they wrap `wails3 tool sign`), so `sign:darwin` becomes a call to
`darwin:sign:notarize`. What is needed:

| Secret | What it is | Where it comes from |
| --- | --- | --- |
| `MACOS_CERTIFICATE` | Developer ID Application certificate and private key, exported as a base64-encoded `.p12` | Apple Developer account → Certificates → *Developer ID Application*, then export from Keychain Access with a password |
| `MACOS_CERTIFICATE_PASSWORD` | the password set on that `.p12` export | you choose it at export time |
| `MACOS_SIGNING_IDENTITY` | the identity's name, e.g. `Developer ID Application: Your Name (TEAMID)` | `security find-identity -v -p codesigning` after importing |
| `MACOS_NOTARY_APPLE_ID` | the Apple ID that owns the account | your Apple ID |
| `MACOS_NOTARY_PASSWORD` | an **app-specific password**, not the account password | appleid.apple.com → Sign-In and Security → App-Specific Passwords |
| `MACOS_NOTARY_TEAM_ID` | the ten-character team identifier | Apple Developer → Membership |

Requires a paid Apple Developer Program membership. The CI step has to import
the certificate into a temporary keychain first (`security create-keychain`,
`import`, `set-key-partition-list`), sign, then submit with
`xcrun notarytool submit --wait` and `xcrun stapler staple` the result. Sign
and staple the `.app` **before** the DMG is built, and sign the DMG too, or
the notarization does not travel with the download.

Notarization is the half that actually removes the Gatekeeper prompt.
Developer ID signing alone downgrades it rather than removing it.

### Windows: Authenticode

`windows:sign` and `windows:sign:installer` already exist and wrap
`wails3 tool sign`, so `sign:windows` becomes a call to the latter. Sign the
`.exe` and the installer — the installer contains the exe, and a user meets
both.

| Secret | What it is | Where it comes from |
| --- | --- | --- |
| `WINDOWS_CERTIFICATE` | code-signing certificate and key, base64-encoded `.pfx` | a CA — DigiCert, Sectigo, SSL.com |
| `WINDOWS_CERTIFICATE_PASSWORD` | the password on that `.pfx` | set when the certificate is issued or exported |

Since June 2023 all publicly-trusted code-signing keys must live on
FIPS-140-2 hardware, so a plain `.pfx` is only possible with a *cloud* signing
service (Azure Trusted Signing, DigiCert KeyLocker, SSL.com eSigner). Those
use their own CI actions and their own secrets rather than a certificate file;
whichever is chosen, it goes in the same step.

An EV certificate builds SmartScreen reputation immediately; an OV one
accumulates it over time, so the warning persists for a while after signing
starts.

### Linux: PGP

Only needed if a real apt or dnf repository is ever published. `sign:linux`
would call `linux:sign:packages`, and the key would be
`LINUX_PGP_PRIVATE_KEY` plus `LINUX_PGP_PASSPHRASE`. The public key then has
to be distributed and installed by users before the signature means anything,
which is most of the work.

## 4. The Homebrew tap

`brew install otis-http/otis/otis` resolves to the repository
**`otis-http/homebrew-otis`** — Homebrew's convention is that the tap
`user/name` lives in `user/homebrew-name`. It is a separate repo and needs
creating once:

```
otis-http/homebrew-otis
├── README.md
└── Formula/
    └── otis.rb        <- written by the release workflow; do not hand-edit
```

Bootstrap it:

```bash
gh repo create otis-http/homebrew-otis --public \
  -d "Homebrew tap for Otis"
git clone https://github.com/otis-http/homebrew-otis && cd homebrew-otis
mkdir -p Formula
# A placeholder is enough; the first release overwrites it.
printf '# Written by the Otis release workflow.\n' > Formula/otis.rb
git add -A && git commit -m "tap layout" && git push
```

The formula is rendered from `build/homebrew/otis.rb.tmpl` in this repo, so
that is the file to edit. Its header explains why Otis is a *formula* and not
a cask, and why it is macOS-only.

### The token

The tap is a different repository, and a workflow's own `GITHUB_TOKEN` cannot
write outside the repository it runs in. So the `homebrew` job needs:

| Secret | What it is |
| --- | --- |
| `HOMEBREW_TAP_TOKEN` | a fine-grained personal access token with **Contents: read and write** on `otis-http/homebrew-otis` and no other permission or repository |

Create it at GitHub → Settings → Developer settings → Personal access tokens →
Fine-grained tokens: *Only select repositories* → `otis-http/homebrew-otis`,
Repository permissions → Contents → Read and write. Then add it to this
repository at Settings → Secrets and variables → Actions → New repository
secret, named `HOMEBREW_TAP_TOKEN`.

**Without it the release still publishes.** The `homebrew` job checks for the
secret, emits a warning, and skips, leaving the formula a manual edit. Nothing
else in the release depends on it.

## 5. After publishing

The things worth checking, because none of them is covered by a test:

- [ ] `shasum -a 256 -c SHA256SUMS --ignore-missing` passes on a fresh
      download.
- [ ] The macOS DMG opens, the right-click → Open path works, and
      `otis --version` from the installed app reports the tag.
- [ ] `brew install otis-http/otis/otis` installs the new version with no
      Gatekeeper prompt (`brew update` first, and note that a just-pushed
      formula can take a moment).
- [ ] The Windows installer runs through SmartScreen's More info → Run anyway,
      and a `.http` file opens in Otis afterwards.
- [ ] The deb or rpm installs and `.http` files open in Otis.
- [ ] `go install -tags otis_cli github.com/otis-http/otis@v0.2.0` produces a
      working `otis run`.

## 6. There is no auto-updater

By deliberate deferral, as with signing. The update path is `brew upgrade`, a
new download, or `apt`/`dnf` on a reinstalled package. That is why
`otis --version` reports the commit and build date as well as the version, and
why the window shows the version at all (`DESIGN-NOTES` §9.18): with no
updater, "which version am I on" is a question a person has to be able to
answer, and the answer has to be complete enough to put in a bug report.

If an updater is added later, the natural shape is a check against the GitHub
Releases API with the download handed to the platform's own installer, and it
would need the artifacts to be signed first — an unsigned auto-update is a
worse idea than no auto-update.
