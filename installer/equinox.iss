; Установщик Equinox (Inno Setup 6). Собирается через build.ps1: ISCC /DAppVersion=<версия из файла VERSION> installer\equinox.iss
; По умолчанию ставит для текущего пользователя, права администратора не нужны; при запуске от
; имени администратора (или по явному выбору в диалоге установки) ставит в Program Files для всех
; пользователей. Программа сама находит доступную для записи папку данных в обоих случаях — см.
; app.DefaultStateDir в internal/app/app.go.

#ifndef AppVersion
  #define AppVersion "1.0.0"
#endif

[Setup]
AppId={{6F0B0C58-2C4B-4E0B-9D53-7A4E8B9A1D01}
AppName=Equinox
AppVersion={#AppVersion}
AppPublisher=Equinox
; {autopf} is {pf} (Program Files) in per-machine install mode and {localappdata}\Programs in
; per-user mode, switching automatically with the privilege the installer actually runs with —
; the "install for all users" dialog below (or right-click "Run as administrator") picks per-machine.
; The app itself now falls back to a per-user data folder if {app}\data is not writable (e.g. a
; standard user running a per-machine copy installed to Program Files by an administrator), so
; both modes work without the "access is denied" crash the per-machine mode used to cause.
DefaultDirName={autopf}\Equinox
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
; The normal, interactive install: an unchecked-by-default box on the finish page. A silent
; install has no finish page, so a "postinstall" entry never runs there regardless of
; skipifsilent; the flag is kept anyway to document that this one is for interactive use only.
Filename: "{app}\Equinox.exe"; Description: "Запустить Equinox"; Flags: nowait postinstall skipifsilent; Check: not IsAutoUpdate
; The app's own auto-update (internal/update) runs Setup with /VERYSILENT /autoupdate=1: this
; entry reopens Equinox once the silent install finishes. It needs no "postinstall" (there is
; no finish page to skip it from) and runasoriginaluser keeps it from launching as
; administrator when Setup itself ran elevated for a per-machine install.
Filename: "{app}\Equinox.exe"; Flags: nowait runasoriginaluser; Check: IsAutoUpdate

[Code]
function IsAutoUpdate(): Boolean;
begin
  Result := ExpandConstant('{param:autoupdate|0}') = '1';
end;

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

// Настройки, список раздач и загрузки обычно лежат в папке data рядом с программой; но если
// программу поставили для всех пользователей в Program Files, у обычного пользователя нет туда
// доступа на запись, и она сама переходит на папку %LOCALAPPDATA%\Equinox (см. DefaultStateDir в
// internal/app/app.go) — проверяем оба места. При удалении данные остаются, если пользователь не
// попросит иначе: раздачи не должны пропадать из-за переустановки.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Candidates: TArrayOfString;
  Found: String;
  I: Integer;
begin
  if CurUninstallStep <> usPostUninstall then Exit;
  Candidates := [ExpandConstant('{app}\data'), ExpandConstant('{localappdata}\Equinox')];
  Found := '';
  for I := 0 to GetArrayLength(Candidates) - 1 do
    if DirExists(Candidates[I]) then
      Found := Found + Candidates[I] + #13#10;
  if Found = '' then Exit;
  if UninstallSilent then Exit;
  if MsgBox('Удалить также настройки, список раздач и скачанные файлы из папки:' + #13#10 + Found + #13#10 +
            'Если нажать «Нет», они сохранятся и будут найдены при следующей установке.',
            mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    for I := 0 to GetArrayLength(Candidates) - 1 do
      DelTree(Candidates[I], True, True, True);
end;
