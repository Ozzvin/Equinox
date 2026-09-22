; Установщик Equinox (Inno Setup 6). Собирается через build.ps1: ISCC /DAppVersion=<версия из файла VERSION> installer\equinox.iss
; Ставит для текущего пользователя, права администратора не нужны.

#ifndef AppVersion
  #define AppVersion "1.0.0"
#endif

[Setup]
AppId={{6F0B0C58-2C4B-4E0B-9D53-7A4E8B9A1D01}
AppName=Equinox
AppVersion={#AppVersion}
AppPublisher=Equinox
; {localappdata}\Programs, not {autopf}: {autopf} switches to the real (admin-only) Program Files
; the moment the installer happens to run elevated (right-click "Run as administrator", or Windows
; elevates it on its own), and the app has no admin rights afterwards to write its own data folder
; there. This path is always writable by the current user, whichever way the installer was started.
DefaultDirName={localappdata}\Programs\Equinox
DefaultGroupName=Equinox
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir=..\dist
OutputBaseFilename=Equinox-Setup
SetupIconFile=equinox.ico
UninstallDisplayIcon={app}\Equinox.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "Значок на рабочем столе"; GroupDescription: "Ярлыки:"; Flags: unchecked
Name: "autostart"; Description: "Запускать вместе с Windows (сворачиваться в трей)"; GroupDescription: "Запуск:"

[Files]
Source: "..\dist\Equinox.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\Equinox"; Filename: "{app}\Equinox.exe"
Name: "{autodesktop}\Equinox"; Filename: "{app}\Equinox.exe"; Tasks: desktopicon

; Тот же элемент автозапуска, что включает пункт в меню трея. Удаляется вместе с программой,
; даже если его включили уже после установки.
[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "Equinox"; ValueData: """{app}\Equinox.exe"" -hidden"; Flags: uninsdeletevalue; Tasks: autostart
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueName: "Equinox"; Flags: uninsdeletevalue dontcreatekey

[Run]
Filename: "{app}\Equinox.exe"; Description: "Запустить Equinox"; Flags: nowait postinstall skipifsilent

[Code]
// Закрывает программу целиком, включая случай, когда она свёрнута в трей (тогда у неё нет
// окна, и штатный AppMutex/CloseApplications Inno Setup закрыть её не может — деинсталлятор
// просто откажется работать, пока процесс жив). taskkill убивает процесс и его дочерние
// (WebView2) независимо от того, открыто окно или нет.
procedure KillEquinox();
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{cmd}'), '/C taskkill /F /IM Equinox.exe /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function InitializeSetup(): Boolean;
begin
  KillEquinox();
  Result := True;
end;

function InitializeUninstall(): Boolean;
begin
  KillEquinox();
  Result := True;
end;

// Настройки, список раздач и загрузки лежат в папке data рядом с программой. При удалении они
// остаются, если пользователь не попросит иначе: раздачи не должны пропадать из-за переустановки.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep <> usPostUninstall then Exit;
  DataDir := ExpandConstant('{app}\data');
  if not DirExists(DataDir) then Exit;
  if UninstallSilent then Exit;
  if MsgBox('Удалить также настройки, список раздач и скачанные файлы из папки:' + #13#10 + DataDir + #13#10#13#10 +
            'Если нажать «Нет», они сохранятся и будут найдены при следующей установке.',
            mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    DelTree(DataDir, True, True, True);
end;
