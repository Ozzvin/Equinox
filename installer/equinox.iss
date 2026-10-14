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
OutputBaseFilename=Equinox-Setup-{#AppVersion}
SetupIconFile=equinox.ico
UninstallDisplayIcon={app}\Equinox.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; .torrent and magnet: Explorer is told the associations changed (see the "assoc" tasks below)
ChangesAssociations=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
; Язык установщика выбирается по языку Windows (русский, иначе английский); окно выбора языка показывается, только
; если язык Windows не подошёл ни к одному. При обновлении (в том числе тихом, из самой программы) остаётся язык,
; выбранный при прошлой установке. Первым стоит английский: он и запасной вариант.
ShowLanguageDialog=auto
LanguageDetectionMethod=uilanguage
UsePreviousLanguage=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

; Тексты, которых нет в стандартных файлах языков.
[CustomMessages]
english.TaskDesktopIcon=Desktop icon
russian.TaskDesktopIcon=Значок на рабочем столе
english.TaskGroupShortcuts=Shortcuts:
russian.TaskGroupShortcuts=Ярлыки:
english.TaskAutostart=Start with Windows (minimized to the tray)
russian.TaskAutostart=Запускать вместе с Windows (сворачиваться в трей)
english.TaskGroupStartup=Startup:
russian.TaskGroupStartup=Запуск:
english.TaskAssocTorrent=Open .torrent files in Equinox
russian.TaskAssocTorrent=Открывать файлы .torrent в Equinox
english.TaskAssocMagnet=Open magnet links in Equinox
russian.TaskAssocMagnet=Открывать magnet-ссылки в Equinox
english.TaskGroupAssoc=Associations:
russian.TaskGroupAssoc=Связи:
english.UninstallDataPrompt=Also delete the settings, the list of torrents and the downloaded files from the folder:%n%1%nIf you click "No", they are kept and will be found at the next install.
russian.UninstallDataPrompt=Удалить также настройки, список раздач и скачанные файлы из папки:%n%1%nЕсли нажать «Нет», они сохранятся и будут найдены при следующей установке.

[Tasks]
Name: "desktopicon"; Description: "{cm:TaskDesktopIcon}"; GroupDescription: "{cm:TaskGroupShortcuts}"; Flags: unchecked
Name: "autostart"; Description: "{cm:TaskAutostart}"; GroupDescription: "{cm:TaskGroupStartup}"
; As Deluge's installer does. Windows 10/11 keeps a choice the user made in "Open with" or "Default apps" (UserChoice):
; there this only adds Equinox to the list and Windows asks once; where nothing was chosen yet, Equinox opens them.
Name: "assoctorrent"; Description: "{cm:TaskAssocTorrent}"; GroupDescription: "{cm:TaskGroupAssoc}"
Name: "assocmagnet"; Description: "{cm:TaskAssocMagnet}"; GroupDescription: "{cm:TaskGroupAssoc}"

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

; .torrent and magnet, for all users with a per-machine install (HKA: HKLM then, HKCU otherwise). The program's own ProgIDs
; (Equinox.Torrent, Equinox.Magnet: the same names RegisterHandlers in internal/desktop uses). A silent update from the
; program itself does not write them: what the user chose since stays as it is, and what the install wrote is still
; there. The .torrent default and the magnet command may be another program's by the time Equinox is uninstalled: they
; are removed in CurUninstallStepChanged only while they still lead to Equinox.
Root: HKA; Subkey: "Software\Classes\Equinox.Torrent"; ValueType: string; ValueName: ""; ValueData: "Torrent file"; Flags: uninsdeletekey; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\Equinox.Torrent\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: "{app}\Equinox.exe,0"; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\Equinox.Torrent\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\Equinox.exe"" ""%1"""; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\.torrent"; ValueType: string; ValueName: ""; ValueData: "Equinox.Torrent"; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\.torrent"; ValueType: string; ValueName: "Content Type"; ValueData: "application/x-bittorrent"; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\.torrent\OpenWithProgids"; ValueType: string; ValueName: "Equinox.Torrent"; ValueData: ""; Flags: uninsdeletevalue; Tasks: assoctorrent; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\Equinox.Magnet"; ValueType: string; ValueName: ""; ValueData: "URL:Magnet link"; Flags: uninsdeletekey; Tasks: assocmagnet; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\Equinox.Magnet"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""; Tasks: assocmagnet; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\Equinox.Magnet\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\Equinox.exe"" ""%1"""; Tasks: assocmagnet; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\magnet"; ValueType: string; ValueName: ""; ValueData: "URL:Magnet link"; Tasks: assocmagnet; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\magnet"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""; Tasks: assocmagnet; Check: not IsAutoUpdate
Root: HKA; Subkey: "Software\Classes\magnet\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\Equinox.exe"" ""%1"""; Tasks: assocmagnet; Check: not IsAutoUpdate

; What the program registers itself at its first start (RegisterHandlers: "Open with" and "Default apps", for the
; current user) goes with it too.
Root: HKCU; Subkey: "Software\Classes\Equinox.Torrent"; Flags: uninsdeletekey dontcreatekey
Root: HKCU; Subkey: "Software\Classes\Equinox.Magnet"; Flags: uninsdeletekey dontcreatekey
Root: HKCU; Subkey: "Software\Classes\Applications\Equinox.exe"; Flags: uninsdeletekey dontcreatekey
Root: HKCU; Subkey: "Software\Equinox"; Flags: uninsdeletekey dontcreatekey
Root: HKCU; Subkey: "Software\RegisteredApplications"; ValueName: "Equinox"; Flags: uninsdeletevalue dontcreatekey

[Run]
; The normal, interactive install: an unchecked-by-default box on the finish page. A silent
; install has no finish page, so a "postinstall" entry never runs there regardless of
; skipifsilent; the flag is kept anyway to document that this one is for interactive use only.
Filename: "{app}\Equinox.exe"; Description: "{cm:LaunchProgram,Equinox}"; Flags: nowait postinstall skipifsilent; Check: not IsAutoUpdate
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

const
  EVENT_MODIFY_STATE = $0002;
  EquinoxMutex = 'Local\EquinoxDesktop';
  EquinoxQuitEvent = 'Local\EquinoxDesktopQuit';

function OpenEventW(dwDesiredAccess: LongWord; bInheritHandle: Boolean; lpName: String): LongWord;
  external 'OpenEventW@kernel32.dll stdcall';
function SetEventAPI(hEvent: LongWord): Boolean;
  external 'SetEvent@kernel32.dll stdcall';
function CloseHandleAPI(hObject: LongWord): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';

// AskEquinoxToQuit signals the event the running app watches (see cmd/equinox/main_windows.go).
// True means the app was there to hear it.
function AskEquinoxToQuit(): Boolean;
var
  H: LongWord;
begin
  Result := False;
  H := OpenEventW(EVENT_MODIFY_STATE, False, EquinoxQuitEvent);
  if H <> 0 then
  begin
    Result := SetEventAPI(H);
    CloseHandleAPI(H);
  end;
end;

// Закрывает программу целиком, включая случай, когда она свёрнута в трей (тогда у неё нет
// окна, и штатный AppMutex/CloseApplications Inno Setup закрыть её не может).
//
// Сначала просим закрыться по-хорошему и ждём: только штатное завершение сбрасывает счётчики
// на диск и доводит до конца (или безопасно помечает) начатый перенос файлов раздачи.
// taskkill /F остаётся аварийным вариантом — он обрывает всё это на середине.
// Без /T: при автообновлении изнутри программы этот установщик — дочерний процесс самого
// Equinox.exe, и /T убил бы всё дерево процессов, включая себя же. WebView2 всё равно сам
// закрывает свои дочерние процессы, когда основной процесс завершается (job object).
procedure KillEquinox();
var
  ResultCode, I: Integer;
begin
  if not CheckForMutexes(EquinoxMutex) then Exit; // не запущена
  AskEquinoxToQuit();
  for I := 0 to 59 do // до ~30 секунд: столько может занять закрытие движка
  begin
    Sleep(500);
    if not CheckForMutexes(EquinoxMutex) then Exit; // ушла сама, чисто
  end;
  Exec(ExpandConstant('{cmd}'), '/C taskkill /F /IM Equinox.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
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
// ForgetAssociations takes back the .torrent default and the magnet command, in both branches (a per-machine install
// writes to HKLM, the program itself to HKCU), but only where they still lead to Equinox: another program may have
// taken them since.
procedure ForgetAssociations();
var
  Roots: array of Integer;
  V, Exe: String;
  I: Integer;
begin
  Exe := Lowercase(ExpandConstant('{app}\Equinox.exe'));
  Roots := [HKEY_LOCAL_MACHINE, HKEY_CURRENT_USER];
  for I := 0 to GetArrayLength(Roots) - 1 do
  begin
    if RegQueryStringValue(Roots[I], 'Software\Classes\.torrent', '', V) and (V = 'Equinox.Torrent') then
      RegDeleteValue(Roots[I], 'Software\Classes\.torrent', '');
    if RegQueryStringValue(Roots[I], 'Software\Classes\magnet\shell\open\command', '', V) and (Pos(Exe, Lowercase(V)) > 0) then
    begin
      RegDeleteKeyIncludingSubkeys(Roots[I], 'Software\Classes\magnet\shell');
      RegDeleteValue(Roots[I], 'Software\Classes\magnet', '');
      RegDeleteValue(Roots[I], 'Software\Classes\magnet', 'URL Protocol');
      RegDeleteKeyIfEmpty(Roots[I], 'Software\Classes\magnet');
    end;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Candidates: TArrayOfString;
  Found: String;
  I: Integer;
begin
  if CurUninstallStep = usUninstall then ForgetAssociations();
  if CurUninstallStep <> usPostUninstall then Exit;
  Candidates := [ExpandConstant('{app}\data'), ExpandConstant('{localappdata}\Equinox')];
  Found := '';
  for I := 0 to GetArrayLength(Candidates) - 1 do
    if DirExists(Candidates[I]) then
      Found := Found + Candidates[I] + #13#10;
  if Found = '' then Exit;
  if UninstallSilent then Exit;
  if MsgBox(FmtMessage(CustomMessage('UninstallDataPrompt'), [Found]),
            mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    for I := 0 to GetArrayLength(Candidates) - 1 do
      DelTree(Candidates[I], True, True, True);
end;
