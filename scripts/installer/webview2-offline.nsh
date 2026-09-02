; PGSheet: offline WebView2 installation (spec section 2).
;
; Wails' own wails.webview2runtime macro embeds MicrosoftEdgeWebview2Setup.exe,
; the ~1.8MB Evergreen *Bootstrapper*, and runs it with /silent /install. Its
; own comment links to Microsoft's "online-only deployment" page, which is the
; problem: on a machine with no internet the bootstrapper cannot fetch the
; runtime, the install still reports success, and the application opens to a
; blank window.
;
; This macro is that one, with the payload swapped for the Evergreen
; *Standalone* installer (~130MB, vendored by scripts/fetch-webview2.ps1). The
; detection, the registry keys and the command-line flags are identical, so the
; only behavioural difference is that it works offline.
;
; Wails regenerates wails_tools.nsh on every build, so its macro cannot be
; edited in place. project.nsi is not regenerated, which is where the call is
; swapped: see scripts/installer/project.nsi.

!ifndef PGSHEET_WEBVIEW2_OFFLINE_NSH
!define PGSHEET_WEBVIEW2_OFFLINE_NSH

!define PGSHEET_WV2_PAYLOAD "..\webview2\MicrosoftEdgeWebView2RuntimeInstallerX64.exe"

!macro pgsheet.webview2offline
    SetRegView 64

    ; A non-empty pv under the per-machine key means the runtime is installed.
    ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $0 != ""
        Goto pgsheet_wv2_ok
    ${EndIf}

    ${If} ${REQUEST_EXECUTION_LEVEL} == "user"
        ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
        ${If} $0 != ""
            Goto pgsheet_wv2_ok
        ${EndIf}
    ${EndIf}

    SetDetailsPrint both
    DetailPrint "Installing the WebView2 runtime (bundled, no download needed)"
    SetDetailsPrint listonly

    InitPluginsDir
    CreateDirectory "$pluginsdir\webview2offline"
    SetOutPath "$pluginsdir\webview2offline"
    File "/oname=MicrosoftEdgeWebView2RuntimeInstaller.exe" "${PGSHEET_WV2_PAYLOAD}"
    ExecWait '"$pluginsdir\webview2offline\MicrosoftEdgeWebView2RuntimeInstaller.exe" /silent /install' $1

    ; Re-read rather than trusting the exit code: what matters is whether the
    ; runtime is now present, and a non-zero code from an install that in fact
    ; succeeded should not stop the installation.
    ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $0 == ""
        ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${EndIf}
    ${If} $0 == ""
        ; Nothing here sends the operator off to find a download. The runtime
        ; is inside this installer, so a failure at this point is a rights or
        ; policy problem on the machine, and saying so is more use than naming
        ; a build they would have to go and get.
        MessageBox MB_OK|MB_ICONEXCLAMATION \
            "PGSheet needs the Microsoft WebView2 runtime. A copy is included in this installer, so nothing has to be downloaded, but installing it returned error $1.$\n$\nThis usually means the account running the installer is not an administrator, or that policy on this machine blocks the runtime. Ask whoever administers the machine to run this installer."
        Abort
    ${EndIf}

    SetDetailsPrint both
    pgsheet_wv2_ok:
!macroend

!endif
