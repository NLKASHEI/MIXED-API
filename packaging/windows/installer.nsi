; MIXED-API per-user Windows installer built with NSIS.
; Uses HKCU and LocalAppData: no admin privileges or system-wide hooks.
Unicode true
!include "MUI2.nsh"
!define PRODUCT_NAME "MIXED-API"
!define PRODUCT_VERSION "0.2.0"

Name "\${PRODUCT_NAME}"
OutFile "MIXED-API-Windows-x64-Setup.exe"
InstallDir "$LOCALAPPDATA\MIXED-API"
InstallDirRegKey HKCU "Software\MIXED-API" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show
VIProductVersion "0.2.0.0"
VIAddVersionKey "ProductName" "MIXED-API"
VIAddVersionKey "FileDescription" "MIXED-API one-click installer"
VIAddVersionKey "FileVersion" "0.2.0"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\MIXED-API.exe"
!define MUI_FINISHPAGE_RUN_TEXT "安装完成后启动 MIXED-API"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "安装 MIXED-API" SecMain
    SetShellVarContext current
    SetOutPath "$INSTDIR"
    File "windows\MIXED-API.exe"
    File "Start-MIXED-API.cmd"
    File "Start-MIXED-API-LAN.cmd"
    File "README.md"

    CreateDirectory "$SMPROGRAMS\MIXED-API"
    CreateShortCut "$SMPROGRAMS\MIXED-API\MIXED-API.lnk" "$INSTDIR\MIXED-API.exe"
    CreateShortCut "$SMPROGRAMS\MIXED-API\局域网模式.lnk" "$INSTDIR\Start-MIXED-API-LAN.cmd"
    CreateShortCut "$SMPROGRAMS\MIXED-API\卸载 MIXED-API.lnk" "$INSTDIR\Uninstall.exe"
    CreateShortCut "$DESKTOP\MIXED-API.lnk" "$INSTDIR\MIXED-API.exe"

    WriteUninstaller "$INSTDIR\Uninstall.exe"
    WriteRegStr HKCU "Software\MIXED-API" "InstallDir" "$INSTDIR"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "DisplayName" "MIXED-API"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "UninstallString" '"$INSTDIR\Uninstall.exe"'
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "DisplayIcon" "$INSTDIR\MIXED-API.exe"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "DisplayVersion" "\${PRODUCT_VERSION}"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "Publisher" "NLKASHEI"
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "NoModify" 1
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API" "NoRepair" 1
SectionEnd

Section "Uninstall"
    SetShellVarContext current
    Delete "$DESKTOP\MIXED-API.lnk"
    Delete "$SMPROGRAMS\MIXED-API\MIXED-API.lnk"
    Delete "$SMPROGRAMS\MIXED-API\局域网模式.lnk"
    Delete "$SMPROGRAMS\MIXED-API\卸载 MIXED-API.lnk"
    RMDir "$SMPROGRAMS\MIXED-API"
    Delete "$INSTDIR\MIXED-API.exe"
    Delete "$INSTDIR\Start-MIXED-API.cmd"
    Delete "$INSTDIR\Start-MIXED-API-LAN.cmd"
    Delete "$INSTDIR\README.md"
    Delete "$INSTDIR\Uninstall.exe"
    ; NEVER delete mixed-api.json: it contains API keys and the user's setup.
    RMDir "$INSTDIR"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\MIXED-API"
    DeleteRegKey HKCU "Software\MIXED-API"
SectionEnd
