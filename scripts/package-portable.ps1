<#
.SYNOPSIS
    Builds the portable, no-admin PGSheet ZIP with a Fixed Version WebView2
    runtime beside the binary.

.DESCRIPTION
    Strategy B from spec section 2. For operator machines where the user has no
    administrator rights and so cannot install the shared WebView2 runtime.
    The app finds the bundled runtime through WEBVIEW2_BROWSER_EXECUTABLE_FOLDER,
    set by the launcher written here.

    Trade-off, deliberately accepted: a Fixed Version runtime never
    auto-updates, so security patching becomes our responsibility. Prefer the
    standard installer wherever the operator can run it.

    Requires ./scripts/fetch-webview2.ps1 -FixedVersionPath to have been run.
#>
[CmdletBinding()]
param(
    # Provenance only; the release number is compiled in from internal/version.
    [string]$BuildStamp = ''
)

$ErrorActionPreference = 'Stop'

$root    = Split-Path -Parent $PSScriptRoot
$fixed   = Join-Path $root 'build\windows\webview2\FixedVersionRuntime'
$staging = Join-Path $root 'build\bin\portable'

# The release number lives in one place. Reading it here rather than taking a
# parameter keeps the ZIP's name and the version the app reports in step 7 from
# ever disagreeing.
$versionGo = Get-Content (Join-Path $root 'internal\version\version.go') -Raw
if ($versionGo -notmatch 'Version\s*=\s*"([^"]+)"') {
    throw "Could not read Version from internal/version/version.go"
}
$version = $Matches[1]
$zip     = Join-Path $root "build\bin\PGSheet-$version-portable-x64.zip"

if (-not (Test-Path $fixed)) {
    throw "Fixed Version runtime not found at $fixed. Run ./scripts/fetch-webview2.ps1 -FixedVersionPath <path> first."
}

Write-Host "Building binary..."
# -skipembedcreate: Wails v2.10.2 runs a static analysis
# pass to find //go:embed directives, and the x/tools it pins cannot read
# Go 1.27 export data. The step only creates missing embed directories, and
# frontend/dist is committed, so skipping it changes nothing here.
# One backtick continues the line. Two is an escaped backtick, which ends the
# command and leaves -ldflags to be run as one of its own.
#
# wails writes its progress to stderr, and Windows PowerShell turns that into
# error records that 'Stop' treats as fatal, so a successful build would abort
# the script. Judge it by the exit code.
$previous = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& wails build -platform windows/amd64 -webview2 error -skipembedcreate `
    -ldflags "-X pgsheet/internal/version.build=$BuildStamp"
$code = $LASTEXITCODE
$ErrorActionPreference = $previous
if ($code -ne 0) { throw "wails build failed with exit code $code" }

if (Test-Path $staging) { Remove-Item -Recurse -Force $staging }
New-Item -ItemType Directory -Force -Path $staging | Out-Null

Copy-Item (Join-Path $root 'build\bin\PGSheet.exe') $staging
Copy-Item $fixed (Join-Path $staging 'webview2') -Recurse

# Launcher: point WebView2 at the bundled runtime, then start the app.
$launcher = @'
@echo off
set "WEBVIEW2_BROWSER_EXECUTABLE_FOLDER=%~dp0webview2"
start "" "%~dp0PGSheet.exe" %*
'@
Set-Content -Path (Join-Path $staging 'PGSheet.cmd') -Value $launcher -Encoding ascii

$readme = @'
PGSheet - portable build

Run PGSheet.cmd, not PGSheet.exe. The .cmd points the application at the
WebView2 runtime bundled in the webview2 folder, which is why this build needs
no installation and no administrator rights.

Nothing is installed and nothing is written outside this folder. The
application needs no internet access; it connects only to the PostgreSQL server
you enter on the first screen.
'@
Set-Content -Path (Join-Path $staging 'README.txt') -Value $readme -Encoding ascii

if (Test-Path $zip) { Remove-Item -Force $zip }
Compress-Archive -Path (Join-Path $staging '*') -DestinationPath $zip

Write-Host "OK: $zip"
