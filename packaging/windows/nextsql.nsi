; NextSQL NSIS installer. Built by scripts/build-windows-installer.sh when makensis is present.
; Defines (passed with -D):
;   PRODUCT_VERSION, PRODUCT_VERSION_CSV, ARCH, STAGING, OUTFILE, ICO

!include "MUI2.nsh"
!include "x64.nsh"
!include "WinMessages.nsh"
!include "FileFunc.nsh"

!ifndef PRODUCT_VERSION
  !define PRODUCT_VERSION "0.1.0-dev"
!endif
!ifndef PRODUCT_VERSION_CSV
  !define PRODUCT_VERSION_CSV "0.1.0.0"
!endif
!ifndef ARCH
  !define ARCH "amd64"
!endif
!ifndef STAGING
  !error "STAGING must be defined"
!endif
!ifndef OUTFILE
  !define OUTFILE "NextSQL-setup.exe"
!endif

Name "NextSQL"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\NextSQL"
InstallDirRegKey HKLM "Software\NextSQL" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
BrandingText "NextSQL ${PRODUCT_VERSION}"
VIProductVersion "${PRODUCT_VERSION_CSV}"
VIAddVersionKey "ProductName" "NextSQL"
VIAddVersionKey "ProductVersion" "${PRODUCT_VERSION}"
VIAddVersionKey "FileDescription" "NextSQL installer"
VIAddVersionKey "CompanyName" "bzync"
VIAddVersionKey "LegalCopyright" "Copyright (c) 2026 bzync"
VIAddVersionKey "FileVersion" "${PRODUCT_VERSION}"

!ifdef ICO
  !define MUI_ICON "${ICO}"
  !define MUI_UNICON "${ICO}"
!endif
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var DataRoot

Function .onInit
  ${If} ${RunningX64}
    SetRegView 64
  ${EndIf}
  StrCpy $DataRoot "$PROGRAMDATA\NextSQL"
FunctionEnd

Section "Install"
  SetOutPath "$INSTDIR"
  File "${STAGING}\nextsql.exe"
  File "${STAGING}\nextsqld.exe"
  File "${STAGING}\nextsql-bench.exe"
  File "${STAGING}\README.txt"
  File "${STAGING}\COPYRIGHT"
  File "${STAGING}\uninstall.ps1"
  File /nonfatal "${STAGING}\nextsql.ico"
  File /nonfatal "${STAGING}\VERSION"

  CreateDirectory "$DataRoot"
  CreateDirectory "$DataRoot\data"
  CreateDirectory "$DataRoot\keys"
  CreateDirectory "$DataRoot\logs"

  IfFileExists "$DataRoot\nextsql.conf" skip_conf
    SetOutPath "$DataRoot"
    File "/oname=nextsql.conf" "${STAGING}\nextsql.conf"
  skip_conf:

  nsExec::ExecToLog 'icacls "$DataRoot\keys" /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F"'

  WriteRegStr HKLM "Software\NextSQL" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "DisplayName" "NextSQL"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "Publisher" "bzync"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "DisplayIcon" "$INSTDIR\nextsql.ico"
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" "NoRepair" 1
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ReadRegStr $0 HKLM "SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "Path"
  WriteRegExpandStr HKLM "SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "Path" "$0;$INSTDIR"
  SendMessage ${HWND_BROADCAST} ${WM_WININICHANGE} 0 "STR:Environment" /TIMEOUT=5000

  nsExec::ExecToLog 'sc.exe create NextSQL binPath= "$\"$INSTDIR\nextsqld.exe$\" --config $\"$DataRoot\nextsql.conf$\"" start= demand DisplayName= "NextSQL Database Server"'
  nsExec::ExecToLog 'sc.exe description NextSQL "Encrypted-by-default multimodel database"'

  CreateDirectory "$SMPROGRAMS\NextSQL"
  CreateShortCut "$SMPROGRAMS\NextSQL\NextSQL CLI.lnk" "$INSTDIR\nextsql.exe"
  CreateShortCut "$SMPROGRAMS\NextSQL\Uninstall.lnk" "$INSTDIR\Uninstall.exe"

  DetailPrint "Initialize with: nextsql init --data-dir $DataRoot\data --key-file $DataRoot\keys\root.key --user app --password-file <file>"
SectionEnd

Section "Uninstall"
  nsExec::ExecToLog 'sc.exe stop NextSQL'
  nsExec::ExecToLog 'sc.exe delete NextSQL'
  Delete "$SMPROGRAMS\NextSQL\NextSQL CLI.lnk"
  Delete "$SMPROGRAMS\NextSQL\Uninstall.lnk"
  RMDir "$SMPROGRAMS\NextSQL"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL"
  DeleteRegKey HKLM "Software\NextSQL"
  Delete "$INSTDIR\nextsql.exe"
  Delete "$INSTDIR\nextsqld.exe"
  Delete "$INSTDIR\nextsql-bench.exe"
  Delete "$INSTDIR\README.txt"
  Delete "$INSTDIR\COPYRIGHT"
  Delete "$INSTDIR\uninstall.ps1"
  Delete "$INSTDIR\nextsql.ico"
  Delete "$INSTDIR\VERSION"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DetailPrint "Data under $PROGRAMDATA\NextSQL was left in place."
SectionEnd
