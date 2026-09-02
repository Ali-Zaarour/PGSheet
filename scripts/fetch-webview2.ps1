<#
.SYNOPSIS
    Vendors the WebView2 runtime payloads needed to build an installer that
    works on a machine with no internet access.

.DESCRIPTION
    PGSheet must install and run offline (spec section 2). The WebView2 "Evergreen
    Bootstrapper" cannot be used for this: it is a ~2MB stub that downloads the
    runtime from Microsoft at install time, so on a disconnected machine the
    install appears to succeed and the app opens to a blank window.

    This script downloads the *Evergreen Standalone Installer* - the full,
    offline installer, about 250MB as of this writing - into
    build/windows/webview2/, where the NSIS installer picks it up. This is the
    only step of the build that needs internet, and it runs on the build
    machine, never on an operator machine.

    The Fixed Version runtime, used for the portable no-admin ZIP, has no
    stable download URL. Download it once from
    https://developer.microsoft.com/microsoft-edge/webview2/ (choose Fixed
    Version, x64), extract it, and pass -FixedVersionPath to have it copied in.

.EXAMPLE
    ./scripts/fetch-webview2.ps1
    ./scripts/fetch-webview2.ps1 -FixedVersionPath C:\downloads\FixedVersionRuntime
#>
[CmdletBinding()]
param(
    [string]$FixedVersionPath
)

$ErrorActionPreference = 'Stop'

$root   = Split-Path -Parent $PSScriptRoot
$outDir = Join-Path $root 'build\windows\webview2'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# Permanent Microsoft fwlink for the Evergreen Standalone Installer (x64).
$standaloneUrl = 'https://go.microsoft.com/fwlink/?linkid=2124701'
$standalone    = Join-Path $outDir 'MicrosoftEdgeWebView2RuntimeInstallerX64.exe'

if (Test-Path $standalone) {
    Write-Host "Standalone installer already present: $standalone"
} else {
    Write-Host "Downloading WebView2 Evergreen Standalone Installer (about 250MB)..."
    Invoke-WebRequest -Uri $standaloneUrl -OutFile $standalone -UseBasicParsing
}

$size = [math]::Round((Get-Item $standalone).Length / 1MB, 1)
if ($size -lt 50) {
    throw "Downloaded file is only ${size}MB. That is the bootstrapper, not the standalone installer - it will fail on an offline machine. Check the URL."
}
Write-Host "OK: standalone installer ${size}MB"

if ($FixedVersionPath) {
    $fixedOut = Join-Path $outDir 'FixedVersionRuntime'
    Write-Host "Copying Fixed Version runtime from $FixedVersionPath ..."
    Copy-Item -Path $FixedVersionPath -Destination $fixedOut -Recurse -Force
    Write-Host "OK: fixed version runtime at $fixedOut"
} else {
    Write-Host "Fixed Version runtime not supplied - the portable ZIP cannot be built."
    Write-Host "Download it from https://developer.microsoft.com/microsoft-edge/webview2/ and re-run with -FixedVersionPath."
}

Write-Host ""
Write-Host "build/windows/webview2/ is git-ignored. Re-run this script on any new build machine."
