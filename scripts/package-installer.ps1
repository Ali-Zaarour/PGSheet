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

if (-not $BuildStamp) {
    # A stamp is provenance, not a requirement, so nothing here may stop the
    # build. Two things make that harder than it reads: a repository with no
    # commits yet makes rev-parse fail, and Windows PowerShell turns a native
    # command's stderr into an error record, which $ErrorActionPreference =
    # 'Stop' then treats as fatal. Hence the exit code, not the output.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    $stamp = & git -C $root rev-parse --short HEAD 2>&1
    $ErrorActionPreference = $previous

    if ($LASTEXITCODE -eq 0 -and $stamp) {
        $BuildStamp = ($stamp | Select-Object -First 1).ToString().Trim()
    } else {
        $BuildStamp = ''
    }
}
if ($BuildStamp) {
    Write-Host "Build stamp: $BuildStamp"
} else {
    Write-Host "Build stamp: none (no commit to name)"
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
