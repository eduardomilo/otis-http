# Stages the Windows release artifacts into dist/.
#
# A PowerShell script rather than inline commands because go-task interprets
# every `cmd:` through an embedded POSIX shell on all platforms, so a
# PowerShell block written inline would be parsed as sh and fail — and the
# tools the POSIX version uses (`cp`, `zip`) are not reliably on PATH on a
# Windows runner. PowerShell always is, and Compress-Archive and Copy-Item are
# in every supported version.
#
# Called by release:windows in the root Taskfile.yml, which owns the artifact
# names; this script is only the file handling.

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)] [string] $BinDir,
  [Parameter(Mandatory = $true)] [string] $DistDir,
  [Parameter(Mandatory = $true)] [string] $AppName,
  [Parameter(Mandatory = $true)] [string] $Version,
  [Parameter(Mandatory = $true)] [string] $Arch
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

# The NSIS installer. Its own name comes from project.nsi, which uses the
# product name and the architecture.
$installer = Join-Path $BinDir "Otis-$Arch-installer.exe"
if (-not (Test-Path $installer)) {
  throw "no installer at $installer - did windows:package run?"
}
Copy-Item $installer (Join-Path $DistDir "${AppName}_${Version}_windows_${Arch}_setup.exe") -Force

# The zip holds the *console* build, renamed to otis.exe: it is what goes on
# PATH, and windows:build:cli explains why the installer's binary cannot be
# the one a shell calls.
$cli = Join-Path $BinDir "$AppName-cli.exe"
if (-not (Test-Path $cli)) {
  throw "no CLI binary at $cli - did windows:build:cli run?"
}
$stage = Join-Path $BinDir "ziproot"
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force -Path $stage | Out-Null
try {
  Copy-Item $cli (Join-Path $stage "$AppName.exe") -Force
  Compress-Archive -Path (Join-Path $stage "*") -Force `
    -DestinationPath (Join-Path $DistDir "${AppName}_${Version}_windows_${Arch}.zip")
}
finally {
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}
