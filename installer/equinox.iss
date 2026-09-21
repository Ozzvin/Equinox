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
; Пока программа запущена (окно или трей), установка просит её закрыть.
AppMutex=Local\EquinoxDesktop
CloseApplications=yes

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
