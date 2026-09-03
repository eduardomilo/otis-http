# Writes SHA256SUMS over everything in a directory.
#
# The POSIX branch of release:checksums uses sha256sum or shasum, and a
# Windows runner has neither. Get-FileHash is built in.
#
# The format is the one `sha256sum -c` and `shasum -c` both read: lowercase
# hash, two spaces, bare file name. Sorted by name and with no directory
# component, so the file is identical whichever machine produced it and
# verification works from the directory a user downloaded into.

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)] [string] $DistDir
)

$ErrorActionPreference = "Stop"

Push-Location $DistDir
try {
  Remove-Item -Force SHA256SUMS -ErrorAction SilentlyContinue
  $lines = Get-ChildItem -File |
    Where-Object { $_.Name -ne "SHA256SUMS" } |
    Sort-Object Name |
    ForEach-Object {
      "{0}  {1}" -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
    }
  # ASCII with no BOM: a UTF-8-BOM file makes the first line unparseable to
  # `shasum -c`, which is the one thing this file exists to support.
  $enc = New-Object System.Text.ASCIIEncoding
  [System.IO.File]::WriteAllLines((Join-Path (Get-Location) "SHA256SUMS"), $lines, $enc)
  $lines | Write-Output
}
finally {
  Pop-Location
}
