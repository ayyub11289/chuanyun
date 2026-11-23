Unicode True
!include "MUI2.nsh"

!ifndef VERSION
  !define VERSION "1.5.1"
!endif
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Chuanyun"

Name "穿云"
OutFile "Chuanyun-Setup-${VERSION}-x64.exe"
InstallDir "$LOCALAPPDATA\Chuanyun"
InstallDirRegKey HKCU "Software\Chuanyun" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "穿云 ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "穿云"
VIAddVersionKey /LANG=2052 "FileDescription" "穿云安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "穿云"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\Chuanyun.exe"
!define MUI_FINISHPAGE_RUN_TEXT "立即运行穿云"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

!macro StopApp
  ; 正在运行的穿云会锁住 exe，sing-box 也会占着端口。
  nsExec::Exec 'taskkill /F /IM Chuanyun.exe'
  Pop $0
  Sleep 800
!macroend

Section "穿云"
  !insertmacro StopApp
  SetOutPath "$INSTDIR"
  File "Chuanyun.exe"
  File "README.md"
  File "示例-proxy-nodes.json"
  File "LICENSE"
  SetOutPath "$INSTDIR\bin"
  File "bin\sing-box.exe"
  SetOutPath "$INSTDIR\rule-set"
  File "rule-set\geosite-cn.srs"
  File "rule-set\geoip-cn.srs"
  SetOutPath "$INSTDIR\third_party\sing-box"
  File "third_party\sing-box\LICENSE"
  File "third_party\sing-box\GPL-3.0.txt"
  SetOutPath "$INSTDIR\third_party\noto"
  File "third_party\noto\OFL.txt"
  ; 安装版把设置放在 %APPDATA%\Chuanyun，不能带便携标记。
  Delete "$INSTDIR\portable.txt"

  SetOutPath "$INSTDIR"
  CreateShortcut "$SMPROGRAMS\穿云.lnk" "$INSTDIR\Chuanyun.exe" "" "$INSTDIR\Chuanyun.exe" 0
  CreateShortcut "$DESKTOP\穿云.lnk" "$INSTDIR\Chuanyun.exe" "" "$INSTDIR\Chuanyun.exe" 0

  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\Chuanyun" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "穿云"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "穿云"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\Chuanyun.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  !insertmacro StopApp
  ; 开机自启的计划任务是以最高权限建的，普通权限删不掉时会静默失败。
  nsExec::Exec 'schtasks /Delete /F /TN Chuanyun'
  Pop $0

  Delete "$INSTDIR\Chuanyun.exe"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\示例-proxy-nodes.json"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\bin"
  RMDir /r "$INSTDIR\rule-set"
  RMDir /r "$INSTDIR\third_party"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\穿云.lnk"
  Delete "$DESKTOP\穿云.lnk"

  MessageBox MB_YESNO|MB_ICONQUESTION "是否同时删除节点和设置（%APPDATA%\Chuanyun）？$\r$\n选「否」可保留，重装后不用重新导入。" /SD IDNO IDNO keep
    RMDir /r "$APPDATA\Chuanyun"
  keep:
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegValue HKCU "Software\Chuanyun" "InstallDir"
SectionEnd
