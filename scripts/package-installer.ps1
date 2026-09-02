<#
.SYNOPSIS
    Builds the Windows installer, with the WebView2 runtime bundled so it
    installs on a machine with no internet.

.DESCRIPTION
    Three things have to be in place before `wails build -nsis` runs, and all
    three are easy to get silently wrong:

      1. build/windows/webview2/ holds the Evergreen *Standalone* installer.
         scripts/fetch-webview2.ps1 puts it there. Without it the installer
         still builds, and still fails on an offline machine.

      2. build/windows/installer/project.nsi is our copy, not wails' default.
         The default embeds the online bootstrapper. wails does not regenerate
         project.nsi once it exists, so copying ours in is enough.

      3. webview2-offline.nsh sits beside it, since project.nsi includes it.

    This script does all three, verifies the result, and refuses to produce an
    installer that would fail offline.

.EXAMPLE
    ./scripts/fetch-webview2.ps1
    ./scripts/package-installer.ps1
#>
[CmdletBinding()]
param(
    # Provenance only. The release number comes from internal/version and is
    # compiled in; this records which commit produced the binary.
    [string]$BuildStamp = ''
)

$ErrorActionPreference = 'Stop'

$root      = Split-Path -Parent $PSScriptRoot
$payload   = Join-Path $root 'build\windows\webview2\MicrosoftEdgeWebView2RuntimeInstallerX64.exe'
$installer = Join-Path $root 'build\windows\installer'
$sources   = Join-Path $PSScriptRoot 'installer'

# A stamp is never added on its own. It used to be derived from the current
# commit whenever the caller passed nothing, which meant every release reported
# itself as "1.0.0-beta+4e4e261" in the About box and in the header of every
# generated .sql file. The commit is already recoverable from the release page,
# so the number an operator reads is the release number and nothing else.
#
# Pass -BuildStamp explicitly to mark a one-off build handed to someone for
# testing.
if ($BuildStamp) {
    Write-Host "Build stamp: $BuildStamp (this build will report $BuildStamp after the version)"
} else {
    Write-Host "Build stamp: none, the application will report the release number alone"
}

# --- 1. the offline runtime -------------------------------------------------

if (-not (Test-Path $payload)) {
    throw "WebView2 offline installer not found at $payload.`nRun ./scripts/fetch-webview2.ps1 first."
}

$payloadMB = [math]::Round((Get-Item $payload).Length / 1MB, 1)
if ($payloadMB -lt 50) {
    throw "$payload is only ${payloadMB}MB. That is the bootstrapper, not the standalone installer, and it will fail on an offline machine."
}
Write-Host "WebView2 offline runtime: ${payloadMB}MB"

# --- 2. our installer script ------------------------------------------------

New-Item -ItemType Directory -Force -Path $installer | Out-Null
Copy-Item (Join-Path $sources 'project.nsi')          $installer -Force
Copy-Item (Join-Path $sources 'webview2-offline.nsh') $installer -Force
Write-Host "Installed our project.nsi and webview2-offline.nsh."

# --- 2b. the application icon ------------------------------------------------

# build/ is git-ignored, so a fresh clone has no build/appicon.png. wails then
# quietly generates its own default icon, and the released application ships
# with the wails logo in its title bar and taskbar instead of ours. That is
# what happened to 1.0.0-beta.
#
# genicon rebuilds appicon.png from scripts/icon/appicon.png, which is tracked,
# and deletes build/windows/icon.ico so wails regenerates that too.
Write-Host "Rebuilding the application icon..."
$previous = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& go run ./scripts/genicon
$code = $LASTEXITCODE
$ErrorActionPreference = $previous
if ($code -ne 0) { throw "go run ./scripts/genicon failed with exit code $code" }

$appicon = Join-Path $root 'build\appicon.png'
if (-not (Test-Path $appicon)) {
    throw "genicon did not produce $appicon. The build would ship the wails default icon."
}

# --- 3. build ---------------------------------------------------------------

# -webview2 error   forbids the runtime download path at run time, so a missing
#                   runtime is an actionable message rather than a blank window.
# -skipembedcreate  works around wails v2.10.2 failing its static analysis pass
#                   under Go 1.27. Safe because frontend/dist is committed.
# wails reports its progress on stderr, and Windows PowerShell turns a native
# command's stderr into error records, which 'Stop' makes fatal. A successful
# build would abort the script. The exit code is the thing to judge it by.
$previous = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& wails build -platform windows/amd64 -nsis -webview2 error -skipembedcreate `
    -ldflags "-X pgsheet/internal/version.build=$BuildStamp"
$code = $LASTEXITCODE
$ErrorActionPreference = $previous
if ($code -ne 0) { throw "wails build failed with exit code $code" }

# --- 4. verify what came out ------------------------------------------------

# wails regenerates wails_tools.nsh every build, so confirm our call survived
# rather than assuming it did.
$nsi = Get-Content (Join-Path $installer 'project.nsi') -Raw
if ($nsi -notmatch 'pgsheet\.webview2offline') {
    throw "project.nsi does not call pgsheet.webview2offline. The installer would embed the online bootstrapper and fail offline."
}

$output = Get-ChildItem (Join-Path $root 'build\bin') -Filter '*installer*.exe' |
          Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $output) {
    throw "No installer was produced. Is NSIS installed and makensis on PATH?"
}

$sizeMB = [math]::Round($output.Length / 1MB, 1)
if ($sizeMB -lt 100) {
    Write-Warning "The installer is only ${sizeMB}MB. With the runtime bundled it should be about 250MB; check that project.nsi embedded the payload."
}

Write-Host ""
Write-Host "Installer: $($output.FullName) (${sizeMB}MB)"
Write-Host "It installs the WebView2 runtime from its own payload, so it needs no internet."
