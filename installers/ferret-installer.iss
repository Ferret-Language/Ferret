#define MyAppId "{{1E61A1A0-24B2-4DE0-8CA8-2BEE310F0B19}}"
#define MyAppName "Ferret"
#define MyAppPublisher "Ferret-Language"
#define MyBootstrapVersion "0.1.0"
#define MyAppURL "https://github.com/Ferret-Language/Ferret"
#define MyUninstallRegSubkey "Software\Microsoft\Windows\CurrentVersion\Uninstall\" + MyAppId + "_is1"

[Setup]
AppId={#MyAppId}
AppName={#MyAppName}
AppVersion={#MyBootstrapVersion}
AppVerName={#MyAppName}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={code:GetDefaultInstallDir}
DisableProgramGroupPage=yes
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
OutputDir=dist
OutputBaseFilename=FerretSetup
SetupIconFile=ferret_icon.ico
WizardImageFile=ferret_icon.bmp
WizardSmallImageFile=ferret_icon.bmp
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
ChangesEnvironment=yes
ArchiveExtraction=full
CreateAppDir=yes
UninstallFilesDir={code:GetDefaultInstallDir}
UsePreviousAppDir=no
UsePreviousTasks=no
UninstallLogMode=new
UninstallDisplayIcon={app}\bin\ferret.exe
VersionInfoVersion={#MyBootstrapVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName} Windows bootstrap installer
VersionInfoProductName={#MyAppName}
VersionInfoProductVersion={#MyBootstrapVersion}

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"

[Types]
Name: "full"; Description: "Full installation"
Name: "compact"; Description: "Compiler only"
Name: "custom"; Description: "Custom installation"; Flags: iscustom

[Components]
Name: "compiler"; Description: "Ferret compiler"; Types: full compact custom; Flags: fixed
Name: "toolchain"; Description: "Toolchain"; Types: full custom

[Tasks]
Name: "addpath"; Description: "Add Ferret to PATH"; Flags: checkedonce

[Files]
Source: "scripts\Resolve-FerretRelease.ps1"; Flags: dontcopy

[Dirs]
Name: "{app}"

[UninstallDelete]
Type: filesandordirs; Name: "{app}"
Type: filesandordirs; Name: "{app}.new"

[Code]
var
  ReleaseSourcePage: TInputOptionWizardPage;
  ReleaseTagPage: TInputQueryWizardPage;
  DownloadPage: TDownloadWizardPage;
  ExtractionPage: TExtractionWizardPage;
  ResolveTimerId: LongWord;
  ResolveInProgress: Boolean;
  ResolveRequestedVersion: String;
  ResolveManifestPath: String;
  ResolveStartedTick: LongWord;
  LoadedRequestedVersion: String;
  LoadedReleaseTag: String;
  LoadedResolvedArch: String;
  LoadedCompilerName: String;
  LoadedCompilerUrl: String;
  LoadedCompilerSha256: String;
  LoadedCompilerDownloadSize: Int64;
  LoadedToolchainName: String;
  LoadedToolchainUrl: String;
  LoadedToolchainSha256: String;
  LoadedToolchainDownloadSize: Int64;
  ReleaseMetadataLoaded: Boolean;
  LastSuggestedDir: String;
  SelectedInstallDir: String;
  InstallCompleted: Boolean;
  InstalledReleaseTag: String;
  InstalledAppDir: String;
  InstalledResolvedArch: String;

function GetStateFilePath(const BaseDir: String): String;
begin
  Result := AddBackslash(BaseDir) + 'install-state.ini';
end;

function NormalizePathForComparison(const Value: String): String;
begin
  Result := Trim(Value);
  while (Length(Result) > 3) and (Copy(Result, Length(Result), 1) = '\') do
    Delete(Result, Length(Result), 1);
  Result := Lowercase(Result);
end;

procedure ValidateInstallDirOrFail(const DirName: String);
var
  NormalizedDir: String;
  WindowsDir: String;
  SystemDir: String;
  RootDir: String;
begin
  NormalizedDir := NormalizePathForComparison(DirName);
  WindowsDir := NormalizePathForComparison(ExpandConstant('{win}'));
  SystemDir := NormalizePathForComparison(ExpandConstant('{sys}'));
  RootDir := NormalizePathForComparison(AddBackslash(ExtractFileDrive(DirName)));

  if (NormalizedDir = '') or
     (NormalizedDir = RootDir) or
     (NormalizedDir = WindowsDir) or
     (NormalizedDir = SystemDir) then
  begin
    RaiseException('Refusing to install into an unsafe directory: ' + DirName);
  end;
end;

function AddQuotesIfNeeded(const Value: String): String;
begin
  Result := '"' + Value + '"';
end;

function TryGetCmdSwitchValue(const SwitchName: String; var Value: String): Boolean;
var
  CmdTail: String;
  UpperCmdTail: String;
  Prefix: String;
  ValuePos: Integer;
  EndPos: Integer;
begin
  Result := False;
  Value := '';
  CmdTail := GetCmdTail;
  UpperCmdTail := Uppercase(CmdTail);

  Prefix := '/' + Uppercase(SwitchName) + '=';
  ValuePos := Pos(Prefix, UpperCmdTail);
  if ValuePos = 0 then
  begin
    Prefix := '-' + Uppercase(SwitchName) + '=';
    ValuePos := Pos(Prefix, UpperCmdTail);
  end;

  if ValuePos = 0 then
    Exit;

  ValuePos := ValuePos + Length(Prefix);
  if (ValuePos <= Length(CmdTail)) and (Copy(CmdTail, ValuePos, 1) = '"') then
  begin
    Inc(ValuePos);
    EndPos := ValuePos;
    while (EndPos <= Length(CmdTail)) and (Copy(CmdTail, EndPos, 1) <> '"') do
      Inc(EndPos);
  end
  else
  begin
    EndPos := ValuePos;
    while (EndPos <= Length(CmdTail)) and (Copy(CmdTail, EndPos, 1) <> ' ') do
      Inc(EndPos);
  end;

  Value := Trim(Copy(CmdTail, ValuePos, EndPos - ValuePos));
  Result := Value <> '';
end;

function GetSelectedReleaseTag: String;
begin
  if ReleaseSourcePage.Values[0] then
    Result := 'latest'
  else
    Result := Trim(ReleaseTagPage.Values[0]);
end;

function GetRecommendedInstallDir: String;
begin
  if IsAdminInstallMode then
    Result := ExpandConstant('{autopf}\Ferret')
  else
    Result := ExpandConstant('{localappdata}\Programs\Ferret');
end;

function GetDefaultInstallDir(Param: String): String;
begin
  if not TryGetCmdSwitchValue('DIR', Result) then
    Result := GetRecommendedInstallDir;
end;

procedure UpdateSuggestedInstallDir;
var
  SuggestedDir: String;
  CurrentDir: String;
begin
  SuggestedDir := GetRecommendedInstallDir;
  CurrentDir := WizardForm.DirEdit.Text;

  if (CurrentDir = '') or
     (NormalizePathForComparison(CurrentDir) = NormalizePathForComparison(LastSuggestedDir)) then
    WizardForm.DirEdit.Text := SuggestedDir;

  LastSuggestedDir := SuggestedDir;
end;

function ReadManifestValue(const ManifestPath, SectionName, KeyName: String): String;
begin
  Result := GetIniString(SectionName, KeyName, '', ManifestPath);
  if Result = '' then
    RaiseException(Format('Missing "%s" value in [%s] from %s.', [KeyName, SectionName, ManifestPath]));
end;

function SetTimer(hWnd, nIDEvent, uElapse, lpTimerFunc: LongWord): LongWord;
external 'SetTimer@user32.dll stdcall';

function KillTimer(hWnd, nIDEvent: LongWord): Bool;
external 'KillTimer@user32.dll stdcall';

function GetTickCount: LongWord;
external 'GetTickCount@kernel32.dll stdcall';

procedure ResolveTimerProc(Arg1, Arg2, Arg3, Arg4: LongWord); forward;

function ReadManifestInt64Value(const ManifestPath, SectionName, KeyName: String): Int64;
var
  ValueText: String;
begin
  ValueText := ReadManifestValue(ManifestPath, SectionName, KeyName);
  try
    Result := StrToInt64(ValueText);
  except
    RaiseException(Format('Invalid integer value for "%s" in [%s] from %s.', [KeyName, SectionName, ManifestPath]));
  end;
end;

procedure ParseReleaseManifest(
  const ManifestPath: String;
  var ReleaseTag: String;
  var ResolvedArch: String;
  var CompilerName: String;
  var CompilerUrl: String;
  var CompilerSha256: String;
  var CompilerDownloadSize: Int64;
  var ToolchainName: String;
  var ToolchainUrl: String;
  var ToolchainSha256: String;
  var ToolchainDownloadSize: Int64);
begin
  if not FileExists(ManifestPath) then
    RaiseException('Release metadata manifest was not generated.');

  ReleaseTag := ReadManifestValue(ManifestPath, 'release', 'tag');
  ResolvedArch := ReadManifestValue(ManifestPath, 'release', 'resolved_arch');
  CompilerName := ReadManifestValue(ManifestPath, 'compiler', 'name');
  CompilerUrl := ReadManifestValue(ManifestPath, 'compiler', 'url');
  CompilerSha256 := ReadManifestValue(ManifestPath, 'compiler', 'sha256');
  CompilerDownloadSize := ReadManifestInt64Value(ManifestPath, 'compiler', 'download_size');
  ToolchainName := ReadManifestValue(ManifestPath, 'toolchain', 'name');
  ToolchainUrl := ReadManifestValue(ManifestPath, 'toolchain', 'url');
  ToolchainSha256 := ReadManifestValue(ManifestPath, 'toolchain', 'sha256');
  ToolchainDownloadSize := ReadManifestInt64Value(ManifestPath, 'toolchain', 'download_size');
end;

procedure LoadReleaseMetadataFromManifest(const RequestedVersion, ManifestPath: String);
begin
  ParseReleaseManifest(
    ManifestPath,
    LoadedReleaseTag,
    LoadedResolvedArch,
    LoadedCompilerName,
    LoadedCompilerUrl,
    LoadedCompilerSha256,
    LoadedCompilerDownloadSize,
    LoadedToolchainName,
    LoadedToolchainUrl,
    LoadedToolchainSha256,
    LoadedToolchainDownloadSize);

  LoadedRequestedVersion := RequestedVersion;
  ReleaseMetadataLoaded := True;
end;

procedure EnsureReleaseMetadataLoaded;
var
  HelperPath: String;
  ManifestPath: String;
  Params: String;
  RequestedVersion: String;
  ResultCode: Integer;
begin
  RequestedVersion := GetSelectedReleaseTag;
  if ReleaseMetadataLoaded and (LoadedRequestedVersion = RequestedVersion) then
    Exit;

  ExtractTemporaryFile('Resolve-FerretRelease.ps1');
  HelperPath := ExpandConstant('{tmp}\Resolve-FerretRelease.ps1');
  ManifestPath := ExpandConstant('{tmp}\ferret-release.ini');

  if FileExists(ManifestPath) then
    DeleteFile(ManifestPath);

  Params :=
    '-NoLogo -NoProfile -ExecutionPolicy Bypass -File ' + AddQuotesIfNeeded(HelperPath) +
    ' -Version ' + AddQuotesIfNeeded(RequestedVersion) +
    ' -Repo "Ferret-Language/Ferret" -OutputPath ' + AddQuotesIfNeeded(ManifestPath);

  Log('Resolving Ferret release manifest.');
  if not Exec(
    ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    Params,
    '',
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode) then
  begin
    RaiseException('Failed to launch PowerShell while resolving Ferret release metadata.');
  end;

  if ResultCode <> 0 then
    RaiseException(Format('Failed to resolve Ferret release metadata. PowerShell exited with code %d.', [ResultCode]));

  LoadReleaseMetadataFromManifest(RequestedVersion, ManifestPath);
end;

function GetSelectedDownloadSize: Int64;
begin
  Result := LoadedCompilerDownloadSize;
  if WizardIsComponentSelected('toolchain') then
    Result := Result + LoadedToolchainDownloadSize;
end;

function GetEstimatedRequiredDiskSpace: Int64;
begin
  Result := GetSelectedDownloadSize * 2;
end;

function FormatBytes(const Bytes: Int64): String;
begin
  if Bytes >= 1073741824 then
    Result := Format('%.1f GB', [Bytes / 1073741824.0])
  else if Bytes >= 1048576 then
    Result := Format('%.1f MB', [Bytes / 1048576.0])
  else if Bytes >= 1024 then
    Result := Format('%.1f KB', [Bytes / 1024.0])
  else
    Result := IntToStr(Bytes) + ' bytes';
end;

procedure UpdateDiskSpaceCaption;
var
  CaptionText: String;
begin
  if not ReleaseMetadataLoaded then
    Exit;
  CaptionText :=
    'Estimated download: ' + FormatBytes(GetSelectedDownloadSize) +
    '    Estimated free space during install: ' + FormatBytes(GetEstimatedRequiredDiskSpace);
  WizardForm.DiskSpaceLabel.Caption := CaptionText;
  WizardForm.ComponentsDiskSpaceLabel.Caption := CaptionText;
end;

procedure ShowResolvingSizeCaption;
begin
  WizardForm.DiskSpaceLabel.Caption := 'Estimating size...';
  WizardForm.ComponentsDiskSpaceLabel.Caption := 'Estimating size...';
  WizardForm.Refresh;
end;

procedure ShowResolveFailedCaption(const ErrorText: String);
begin
  WizardForm.DiskSpaceLabel.Caption := 'Size unavailable';
  WizardForm.ComponentsDiskSpaceLabel.Caption := 'Size unavailable';
  Log('Failed to resolve release size metadata: ' + ErrorText);
end;

procedure StopResolveTimer;
begin
  if ResolveTimerId <> 0 then
  begin
    KillTimer(0, ResolveTimerId);
    ResolveTimerId := 0;
  end;
end;

procedure StartResolveTimer;
begin
  StopResolveTimer;
  ResolveTimerId := SetTimer(0, 0, 300, CreateCallback(@ResolveTimerProc));
end;

procedure StartReleaseMetadataResolve;
var
  HelperPath: String;
  Params: String;
  ResultCode: Integer;
begin
  if ResolveInProgress then
    Exit;

  if ReleaseMetadataLoaded and (LoadedRequestedVersion = GetSelectedReleaseTag) then
  begin
    UpdateDiskSpaceCaption;
    Exit;
  end;

  ResolveRequestedVersion := GetSelectedReleaseTag;
  ReleaseMetadataLoaded := False;
  ShowResolvingSizeCaption;

  ExtractTemporaryFile('Resolve-FerretRelease.ps1');
  HelperPath := ExpandConstant('{tmp}\Resolve-FerretRelease.ps1');
  ResolveManifestPath := ExpandConstant('{tmp}\ferret-release-' + IntToStr(GetTickCount) + '.ini');

  if FileExists(ResolveManifestPath) then
    DeleteFile(ResolveManifestPath);

  Params :=
    '-NoLogo -NoProfile -ExecutionPolicy Bypass -File ' + AddQuotesIfNeeded(HelperPath) +
    ' -Version ' + AddQuotesIfNeeded(ResolveRequestedVersion) +
    ' -Repo "Ferret-Language/Ferret" -OutputPath ' + AddQuotesIfNeeded(ResolveManifestPath);

  if not Exec(
    ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    Params,
    '',
    SW_HIDE,
    ewNoWait,
    ResultCode) then
  begin
    ShowResolveFailedCaption('Failed to launch PowerShell.');
    Exit;
  end;

  ResolveStartedTick := GetTickCount;
  ResolveInProgress := True;
  StartResolveTimer;
end;

procedure ResolveTimerProc(Arg1, Arg2, Arg3, Arg4: LongWord);
begin
  if not ResolveInProgress then
  begin
    StopResolveTimer;
    Exit;
  end;

  if FileExists(ResolveManifestPath) then
  begin
    ResolveInProgress := False;
    StopResolveTimer;
    try
      LoadReleaseMetadataFromManifest(ResolveRequestedVersion, ResolveManifestPath);
      UpdateDiskSpaceCaption;
    except
      ShowResolveFailedCaption(GetExceptionMessage);
    end;
    Exit;
  end;

  if (GetTickCount - ResolveStartedTick) > 30000 then
  begin
    ResolveInProgress := False;
    StopResolveTimer;
    ShowResolveFailedCaption('Timed out while loading release metadata.');
  end;
end;

procedure ComponentsListClickCheck(Sender: TObject);
begin
  UpdateDiskSpaceCaption;
end;

procedure InstallTypeChange(Sender: TObject);
begin
  UpdateDiskSpaceCaption;
end;

function OnDownloadProgress(const Url, FileName: String; const Progress, ProgressMax: Int64): Boolean;
begin
  Result := True;
end;

function OnExtractionProgress(const ArchiveName, FileName: String; const Progress, ProgressMax: Int64): Boolean;
begin
  Result := True;
end;

procedure EnsureDirectoryDeleted(const DirName: String);
begin
  if DirExists(DirName) then
  begin
    Log('Removing directory: ' + DirName);
    if not DelTree(DirName, True, True, True) then
      RaiseException('Failed to remove directory: ' + DirName);
  end;
end;

procedure PrepareStageDirectory(const StageDir: String);
var
  ParentDir: String;
begin
  ParentDir := ExtractFileDir(StageDir);
  if not DirExists(ParentDir) then
    if not ForceDirectories(ParentDir) then
      RaiseException('Failed to create installation parent directory: ' + ParentDir);

  EnsureDirectoryDeleted(StageDir);

  if not ForceDirectories(StageDir) then
    RaiseException('Failed to create staging directory: ' + StageDir);
end;

procedure NormalizeCompilerLayout(const StageDir: String);
var
  RootExe: String;
  BinDir: String;
  BinExe: String;
begin
  BinDir := AddBackslash(StageDir) + 'bin';
  BinExe := AddBackslash(BinDir) + 'ferret.exe';
  RootExe := AddBackslash(StageDir) + 'ferret.exe';

  if not FileExists(BinExe) and FileExists(RootExe) then
  begin
    if not DirExists(BinDir) then
      if not ForceDirectories(BinDir) then
        RaiseException('Failed to create compiler bin directory: ' + BinDir);

    if not RenameFile(RootExe, BinExe) then
      RaiseException('Failed to place ferret.exe into the bin directory.');
  end;

  if not FileExists(BinExe) then
    RaiseException('ferret.exe was not found after extracting the compiler archive.');
end;

function PathValueWithoutEntry(const CurrentValue, EntryToRemove: String): String;
var
  Remaining: String;
  Segment: String;
  DelimiterPos: Integer;
begin
  Result := '';
  Remaining := CurrentValue;

  while Remaining <> '' do
  begin
    DelimiterPos := Pos(';', Remaining);
    if DelimiterPos = 0 then
    begin
      Segment := Trim(Remaining);
      Remaining := '';
    end
    else
    begin
      Segment := Trim(Copy(Remaining, 1, DelimiterPos - 1));
      Delete(Remaining, 1, DelimiterPos);
    end;

    if (Segment <> '') and
       (NormalizePathForComparison(Segment) <> NormalizePathForComparison(EntryToRemove)) then
    begin
      if Result <> '' then
        Result := Result + ';';
      Result := Result + Segment;
    end;
  end;
end;

procedure WritePathValueToRegistry(const RootKey: Integer; const SubkeyName, ValueName, ValueData: String);
begin
  if ValueData = '' then
  begin
    RegDeleteValue(RootKey, SubkeyName, ValueName);
    Exit;
  end;

  if not RegWriteExpandStringValue(RootKey, SubkeyName, ValueName, ValueData) then
    RaiseException('Failed to update the PATH environment variable.');
end;

procedure EnsurePathEntryPresent(const RootKey: Integer; const SubkeyName, EntryToAdd: String);
var
  CurrentPath: String;
  UpdatedPath: String;
begin
  if not RegQueryStringValue(RootKey, SubkeyName, 'Path', CurrentPath) then
    CurrentPath := '';

  UpdatedPath := PathValueWithoutEntry(CurrentPath, EntryToAdd);
  if UpdatedPath <> '' then
    UpdatedPath := UpdatedPath + ';' + EntryToAdd
  else
    UpdatedPath := EntryToAdd;

  WritePathValueToRegistry(RootKey, SubkeyName, 'Path', UpdatedPath);
end;

procedure RemovePathEntry(const RootKey: Integer; const SubkeyName, EntryToRemove: String);
var
  CurrentPath: String;
begin
  if not RegQueryStringValue(RootKey, SubkeyName, 'Path', CurrentPath) then
    Exit;

  WritePathValueToRegistry(
    RootKey,
    SubkeyName,
    'Path',
    PathValueWithoutEntry(CurrentPath, EntryToRemove));
end;

function GetPathRootKeyForScope(const ScopeName: String): Integer; forward;
function GetPathSubkeyForScope(const ScopeName: String): String; forward;

procedure RemoveManagedPathFromInstallation(const InstallDir: String);
var
  StatePath: String;
  PathScope: String;
  BinDir: String;
begin
  StatePath := GetStateFilePath(InstallDir);
  if not FileExists(StatePath) then
    Exit;

  PathScope := GetIniString('install', 'path_scope', 'none', StatePath);
  if PathScope = 'none' then
    Exit;

  BinDir := GetIniString('install', 'bin_dir', AddBackslash(InstallDir) + 'bin', StatePath);
  RemovePathEntry(
    GetPathRootKeyForScope(PathScope),
    GetPathSubkeyForScope(PathScope),
    BinDir);
end;

function GetPathRootKeyForScope(const ScopeName: String): Integer;
begin
  if ScopeName = 'machine' then
    Result := HKLM
  else
    Result := HKCU;
end;

function GetPathSubkeyForScope(const ScopeName: String): String;
begin
  if ScopeName = 'machine' then
    Result := 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment'
  else
    Result := 'Environment';
end;

procedure WriteInstallState(
  const BaseDir: String;
  const ReleaseTag: String;
  const ResolvedArch: String;
  const PathScope: String);
var
  StatePath: String;
begin
  StatePath := GetStateFilePath(BaseDir);
  SetIniString('install', 'release_tag', ReleaseTag, StatePath);
  SetIniString('install', 'resolved_arch', ResolvedArch, StatePath);
  SetIniString('install', 'path_scope', PathScope, StatePath);
  SetIniString('install', 'bin_dir', AddBackslash(BaseDir) + 'bin', StatePath);
end;

function GetInstallRootKey: Integer;
begin
  if IsAdminInstallMode then
    Result := HKLM
  else
    Result := HKCU;
end;

procedure UpdateUninstallRegistryDisplay(const AppDir: String; const ReleaseTag: String);
var
  SubkeyNames: TArrayOfString;
  SubkeyName: String;
  UninstallString: String;
  UninstallExe: String;
  I: Integer;
begin
  UninstallExe := AddBackslash(AppDir) + 'unins000.exe';

  if not RegGetSubkeyNames(GetInstallRootKey, 'Software\Microsoft\Windows\CurrentVersion\Uninstall', SubkeyNames) then
    Exit;

  for I := 0 to GetArrayLength(SubkeyNames) - 1 do
  begin
    SubkeyName := 'Software\Microsoft\Windows\CurrentVersion\Uninstall\' + SubkeyNames[I];
    if RegQueryStringValue(GetInstallRootKey, SubkeyName, 'UninstallString', UninstallString) and
       (Pos(NormalizePathForComparison(UninstallExe), NormalizePathForComparison(UninstallString)) > 0) then
    begin
      RegWriteStringValue(GetInstallRootKey, SubkeyName, 'DisplayVersion', ReleaseTag);
      RegWriteStringValue(GetInstallRootKey, SubkeyName, 'DisplayIcon', AddBackslash(AppDir) + 'bin\ferret.exe');
      RegWriteStringValue(GetInstallRootKey, SubkeyName, 'InstallLocation', AppDir);
      Exit;
    end;
  end;
end;

procedure PerformInstallation;
var
  RequestedVersion: String;
  StageDir: String;
  AppDir: String;
  ToolchainDir: String;
  CompilerArchivePath: String;
  ToolchainArchivePath: String;
  PathScope: String;
begin
  RequestedVersion := GetSelectedReleaseTag;
  if ResolveInProgress and (ResolveRequestedVersion = RequestedVersion) then
    RaiseException('Release metadata is still loading. Please wait a moment and try again.');
  AppDir := Trim(SelectedInstallDir);
  if AppDir = '' then
    AppDir := Trim(WizardForm.DirEdit.Text);
  if AppDir = '' then
    AppDir := GetRecommendedInstallDir;

  ValidateInstallDirOrFail(AppDir);
  StageDir := AppDir + '.new';
  try

  EnsureReleaseMetadataLoaded;

  InstalledReleaseTag := LoadedReleaseTag;
  InstalledResolvedArch := LoadedResolvedArch;

  PrepareStageDirectory(StageDir);

  DownloadPage.Clear;
  DownloadPage.ShowBaseNameInsteadOfUrl := True;
  DownloadPage.Add(LoadedCompilerUrl, LoadedCompilerName, LoadedCompilerSha256);

    if WizardIsComponentSelected('toolchain') then
      DownloadPage.Add(LoadedToolchainUrl, LoadedToolchainName, LoadedToolchainSha256);

  DownloadPage.Show;
  try
    DownloadPage.Download;
  finally
    DownloadPage.Hide;
  end;

  CompilerArchivePath := AddBackslash(ExpandConstant('{tmp}')) + LoadedCompilerName;
  ToolchainArchivePath := AddBackslash(ExpandConstant('{tmp}')) + LoadedToolchainName;
  ToolchainDir := AddBackslash(StageDir) + 'toolchain';

  ExtractionPage.Clear;
  ExtractionPage.ShowArchiveInsteadOfFile := True;
  ExtractionPage.Add(CompilerArchivePath, StageDir, True);
    if WizardIsComponentSelected('toolchain') then
      ExtractionPage.Add(ToolchainArchivePath, ToolchainDir, True);

  ExtractionPage.Show;
  try
    ExtractionPage.Extract;
  finally
    ExtractionPage.Hide;
  end;

  NormalizeCompilerLayout(StageDir);

    if WizardIsTaskSelected('addpath') then
    begin
    if IsAdminInstallMode then
      PathScope := 'machine'
    else
      PathScope := 'user';
  end
  else
  begin
    PathScope := 'none';
  end;

    RemoveManagedPathFromInstallation(AppDir);
    EnsureDirectoryDeleted(AppDir);
    if not RenameFile(StageDir, AppDir) then
      RaiseException('Failed to activate the new installation directory.');

    WriteInstallState(AppDir, LoadedReleaseTag, LoadedResolvedArch, PathScope);

    if PathScope <> 'none' then
      EnsurePathEntryPresent(
        GetPathRootKeyForScope(PathScope),
        GetPathSubkeyForScope(PathScope),
        AddBackslash(AppDir) + 'bin');

    InstalledAppDir := AppDir;
    InstallCompleted := True;
  except
    EnsureDirectoryDeleted(StageDir);
    RaiseException(GetExceptionMessage);
  end;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := False;
  if PageID = ReleaseTagPage.ID then
    Result := ReleaseSourcePage.Values[0];
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;

  if CurPageID = ReleaseTagPage.ID then
  begin
    if Trim(ReleaseTagPage.Values[0]) = '' then
    begin
      MsgBox('Enter a Ferret release tag such as v0.0.7, or go back and choose the latest release.', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  if CurPageID = wpSelectComponents then
    StartReleaseMetadataResolve;

  if CurPageID = wpSelectDir then
  begin
    UpdateSuggestedInstallDir;
    if ReleaseMetadataLoaded then
      UpdateDiskSpaceCaption
    else
      StartReleaseMetadataResolve;
  end;

  if CurPageID = wpReady then
  begin
    if ReleaseMetadataLoaded then
      UpdateDiskSpaceCaption
    else
      StartReleaseMetadataResolve;
  end;

  if CurPageID = wpReady then
    SelectedInstallDir := WizardForm.DirEdit.Text;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
    PerformInstallation
  else if (CurStep = ssPostInstall) and InstallCompleted then
    UpdateUninstallRegistryDisplay(InstalledAppDir, InstalledReleaseTag);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  InstallDir: String;
  StatePath: String;
  PathScope: String;
  BinDir: String;
begin
  if CurUninstallStep <> usUninstall then
    Exit;

  InstallDir := ExtractFileDir(ExpandConstant('{uninstallexe}'));
  Log('Uninstall using install directory: ' + InstallDir);
  StatePath := GetStateFilePath(InstallDir);
  if not FileExists(StatePath) then
  begin
    Log('Install state file not found: ' + StatePath);
    Exit;
  end;

  PathScope := GetIniString('install', 'path_scope', 'none', StatePath);
  Log('Recorded PATH scope: ' + PathScope);
  if PathScope = 'none' then
    Exit;

  BinDir := GetIniString('install', 'bin_dir', AddBackslash(InstallDir) + 'bin', StatePath);
  Log('Removing PATH entry: ' + BinDir);
  RemovePathEntry(
    GetPathRootKeyForScope(PathScope),
    GetPathSubkeyForScope(PathScope),
    BinDir);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  EnsureReleaseMetadataLoaded;

  if not TryGetCmdSwitchValue('DIR', SelectedInstallDir) then
    SelectedInstallDir := Trim(WizardForm.DirEdit.Text);

  if SelectedInstallDir = '' then
    SelectedInstallDir := ExpandConstant('{app}');

  Log('Prepared installation directory: ' + SelectedInstallDir);
  Result := '';
end;

function UpdateReadyMemo(Space, NewLine, MemoUserInfoInfo, MemoDirInfo, MemoTypeInfo,
  MemoComponentsInfo, MemoGroupInfo, MemoTasksInfo: String): String;
begin
  EnsureReleaseMetadataLoaded;

  Result := '';
  Result := Result + 'Release:' + NewLine;
  Result := Result + Space + LoadedReleaseTag + ' (' + LoadedResolvedArch + ')' + NewLine + NewLine;
  Result := Result + MemoDirInfo + NewLine;
  Result := Result + Space + WizardForm.DirEdit.Text + NewLine + NewLine;
  Result := Result + MemoTypeInfo + NewLine;
  Result := Result + Space + WizardSetupType(False) + NewLine + NewLine;
  Result := Result + MemoComponentsInfo + NewLine;
  Result := Result + Space + WizardSelectedComponents(False) + NewLine + NewLine;

  if WizardSelectedTasks(False) <> '' then
  begin
    Result := Result + MemoTasksInfo + NewLine;
    Result := Result + Space + WizardSelectedTasks(False) + NewLine + NewLine;
  end;

  Result := Result + 'Size Summary:' + NewLine;
  Result := Result + Space + 'Estimated download: ' + FormatBytes(GetSelectedDownloadSize) + NewLine;
  Result := Result + Space + 'Estimated free space during install: ' + FormatBytes(GetEstimatedRequiredDiskSpace) + NewLine;
end;

procedure InitializeWizard;
begin
  ReleaseSourcePage := CreateInputOptionPage(
    wpWelcome,
    'Release Source',
    'Choose what the installer should download.',
    'Use the latest Ferret release by default, or install a specific tag.',
    True,
    False);
  ReleaseSourcePage.Add('Install the latest release');
  ReleaseSourcePage.Add('Install a specific tag');
  ReleaseSourcePage.SelectedValueIndex := 0;

  ReleaseTagPage := CreateInputQueryPage(
    ReleaseSourcePage.ID,
    'Release Tag',
    'Enter the Ferret release tag to install.',
    'Examples: v0.0.7, v0.1.0');
  ReleaseTagPage.Add('Release tag:', False);

  DownloadPage := CreateDownloadPage(
    'Downloading Ferret',
    'The installer is downloading the selected Ferret packages.',
    @OnDownloadProgress);
  ExtractionPage := CreateExtractionPage(
    'Extracting Ferret',
    'The installer is unpacking the selected Ferret packages.',
    @OnExtractionProgress);

  WizardForm.ComponentsList.OnClickCheck := @ComponentsListClickCheck;
  WizardForm.TypesCombo.OnChange := @InstallTypeChange;

  LastSuggestedDir := GetRecommendedInstallDir;
end;

procedure DeinitializeSetup;
begin
  StopResolveTimer;
end;
