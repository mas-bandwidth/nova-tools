# runner-windows.ps1: a WSL machine's Windows-side GitHub Actions runners, driven by the github-runner role's
# tasks/wsl-windows.yml through WSL interop (powershell.exe run from the machine's WSL login, which acts with the Windows
# account that started WSL, elevated). The role stages this file to C:\ProgramData\nova-runner-stage and calls one verb
# at a time; every line it prints starts with a word the role matches. fleet/README.md, "Windows runners of a WSL machine".
#
#   install   -Dir -Cache -Version -Sha [-PathList]   the pinned runner zip in -Cache (its sha256 checked before it is
#             used), unpacked into -Dir where config.cmd is missing (a dir with files but no config.cmd, an
#             interrupted unpack, is moved aside first: PARTIAL <dir> -> <where>), and the .env lines the role owns (PATH, GOCACHE,
#             DOTNET_INSTALL_DIR, DOTNET_CLI_TELEMETRY_OPTOUT) set in place, every other line (the CI-rules hooks) kept:
#             DOWNLOADED, EXTRACTED, ENV-CHANGED <dir> when something moved
#   register  -Dir -Url -Name -Labels                 stdin carries ONE registration token; it reaches config.cmd as
#             ACTIONS_RUNNER_INPUT_TOKEN, never an argument, a file or a line of output. Registers the runner as a
#             Windows service (config.cmd --runasservice --replace: NT AUTHORITY\NETWORK SERVICE, automatic start):
#             REGISTERED. The role calls it only for a runner that is not whole (no .runner, no service, or not in
#             GitHub's list): a half registration (.runner there) is repaired first, its .runner and credentials moved
#             into <dir>\retired-<yyyymmdd>\ and its service removed: REPAIR <dir>
#   ensure    -Dir                                    its service starts at boot and is running: SERVICE <dir> ok|started,
#             or NO-SERVICE <dir> (the role counts it as not registered, and the next run repairs it)
#   keepalive -MachineId -User                        the running distro whose /etc/machine-id is -MachineId held running
#             from boot (tasks/wsl-keepalive.yml): the task "WSL2 keepalive (<distro>)" runs wsl.exe -d <distro> -u <user>
#             --exec /bin/sleep infinity as this Windows account, logon type S4U (no password stored), at boot and each
#             minute after while it is not running; set when it differs, started when it is not running:
#             KEEPALIVE <distro> ok|set|started|set,started task=<name>, or NO-DISTRO (exit 6)
#   pagefile  -SizeMb                                 C:\pagefile.sys fixed at -SizeMb (initial = maximum), system management
#             off: PAGEFILE ok|set size_mb=<n> active_mb=<the size Windows has allocated now> restart=owed|no, then
#             COMMIT limit_gb=<n> free_gb=<n>. A set that Windows has not applied yet says restart=owed; nothing here
#             restarts the machine.
#   account   -Account -SecretEnv                     an admin runner's local account. Its password is the environment
#             variable -SecretEnv names (nova-secrets exec, then WSLENV through interop), removed from this process's
#             environment as it is read; never an argument, a file or a line of output. The account is created (password
#             never expires, the user cannot change it), or its password set where the stored one does not log it on as
#             a service; put in Administrators by SID (S-1-5-32-544); granted SeServiceLogonRight and denied interactive
#             and remote-interactive logon (SeDenyInteractiveLogonRight, SeDenyRemoteInteractiveLogonRight) through the
#             LSA: ACCOUNT <account> account=created|updated|ok rights=ok|+<right>,... admin=added|ok
#   logon     -Dir -Account -SecretEnv [-Relogon]     the runner's service in -Dir logs on as .\<account> (Win32_Service
#             Change, the password from the environment as above), restarted when running; the account given full
#             control of -Dir. A service already logging on as the account is left alone unless -Relogon (the account's
#             password was set): LOGON <dir> ok|set account=.\<account> acl=ok|set service=<name>, or NO-SERVICE <dir>
#             (exit 7)
param(
  [Parameter(Mandatory = $true)][ValidateSet('install', 'register', 'ensure', 'keepalive', 'pagefile', 'account', 'logon')][string]$Action,
  [string]$Dir = '',
  [string]$Cache = '',
  [string]$Version = '',
  [string]$Sha = '',
  [string]$PathList = '',
  [string]$Url = '',
  [string]$Name = '',
  [string]$Labels = '',
  [string]$MachineId = '',
  [string]$User = '',
  [int]$SizeMb = 0,
  [string]$Account = '',
  [string]$SecretEnv = '',
  [switch]$Relogon
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Need-Admin {
  $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { 'NEEDS-ELEVATION'; exit 3 }
}
function Read-Token {
  $t = [Console]::In.ReadLine()
  if (-not $t -or $t.Trim().Length -lt 20) { 'NO-TOKEN on stdin'; exit 4 }
  return $t.Trim()
}
# Service-Of DIR: the runner's Windows service, found by its directory in the service's path.
function Service-Of([string]$d) {
  Get-CimInstance Win32_Service | Where-Object { $_.PathName -like "*$d\*" } | Select-Object -First 1
}
# Move-Aside PATH ROOT: PATH moved under ROOT, keeping its path without the drive (C:\a\b ->
# <root>\a\b); never deleted.
function Move-Aside([string]$p, [string]$root) {
  $to = Join-Path $root ($p -replace '^[A-Za-z]:\\', '')
  if (Test-Path -LiteralPath $to) { $to = "$to.$([DateTime]::UtcNow.ToString('HHmmss'))" }
  New-Item -ItemType Directory -Force -Path (Split-Path $to) | Out-Null
  Move-Item -LiteralPath $p -Destination $to
  return $to
}
# Invoke-Config ARGS: config.cmd in $Dir with the token from stdin in its environment only; its exit code.
function Invoke-Config([string[]]$a) {
  $tok = Read-Token
  Push-Location $Dir
  try {
    $env:ACTIONS_RUNNER_INPUT_TOKEN = $tok
    # its lines go straight to stdout, so the function's only output is the exit code
    # config.cmd prints in the console code page (a check mark is not UTF-8): ASCII only, or ansible refuses the result
    & .\config.cmd @a | ForEach-Object { [Console]::Out.WriteLine("CONFIG " + ("$_" -replace '[^\x20-\x7E]', '?')) }
    return $LASTEXITCODE
  } finally {
    Remove-Item Env:\ACTIONS_RUNNER_INPUT_TOKEN -ErrorAction SilentlyContinue
    $tok = $null
    Pop-Location
  }
}

# Take-Secret NAME: the value of the environment variable NAME, removed from this process's environment as it is read.
# It is never printed, written, or passed to a program: only to .NET and CIM calls inside this process.
function Take-Secret([string]$n) {
  if ($n -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { [Console]::Out.WriteLine('BAD-ARGS -SecretEnv'); exit 2 }
  $v = [Environment]::GetEnvironmentVariable($n, 'Process')
  [Environment]::SetEnvironmentVariable($n, $null, 'Process')
  if (-not $v) { [Console]::Out.WriteLine("NO-SECRET $n in the environment (nova-secrets exec --only $n -- env WSLENV=$n ...)"); exit 4 }
  return $v
}
function Test-AccountName([string]$a) { return $a -cmatch '^[a-z][a-z0-9-]{0,19}$' }
# Add-AccountApi: the LSA account-rights calls and a service-type logon check, which PowerShell has no cmdlet for.
function Add-AccountApi {
  if ('NovaAccount' -as [type]) { return }
  Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class NovaAccount {
  [StructLayout(LayoutKind.Sequential)]
  struct LsaString { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
  [StructLayout(LayoutKind.Sequential)]
  struct LsaAttributes { public int Length; public IntPtr RootDirectory; public IntPtr ObjectName; public uint Attributes; public IntPtr SecurityDescriptor; public IntPtr SecurityQualityOfService; }
  [DllImport("advapi32.dll")] static extern uint LsaOpenPolicy(IntPtr system, ref LsaAttributes attributes, uint access, out IntPtr policy);
  [DllImport("advapi32.dll")] static extern uint LsaAddAccountRights(IntPtr policy, byte[] sid, LsaString[] rights, uint count);
  [DllImport("advapi32.dll")] static extern uint LsaEnumerateAccountRights(IntPtr policy, byte[] sid, out IntPtr rights, out uint count);
  [DllImport("advapi32.dll")] static extern uint LsaFreeMemory(IntPtr buffer);
  [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr policy);
  [DllImport("advapi32.dll")] static extern int LsaNtStatusToWinError(uint status);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern bool LogonUserW(string user, string domain, string password, int type, int provider, out IntPtr token);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
  const uint PolicyAccess = 0x00000810; // POLICY_LOOKUP_NAMES | POLICY_CREATE_ACCOUNT, what LsaAddAccountRights needs
  const uint NoRights = 0xC0000034;     // STATUS_OBJECT_NAME_NOT_FOUND: the account holds no rights yet
  const int ServiceLogonType = 5;       // LOGON32_LOGON_SERVICE

  static IntPtr Open() {
    LsaAttributes a = new LsaAttributes();
    a.Length = Marshal.SizeOf(typeof(LsaAttributes));
    IntPtr p;
    uint s = LsaOpenPolicy(IntPtr.Zero, ref a, PolicyAccess, out p);
    if (s != 0) { throw new Win32Exception(LsaNtStatusToWinError(s)); }
    return p;
  }
  // Rights: the account rights the SID holds.
  public static string[] Rights(byte[] sid) {
    IntPtr p = Open();
    try {
      IntPtr buf;
      uint n;
      uint s = LsaEnumerateAccountRights(p, sid, out buf, out n);
      if (s == NoRights) { return new string[0]; }
      if (s != 0) { throw new Win32Exception(LsaNtStatusToWinError(s)); }
      try {
        List<string> list = new List<string>();
        int size = Marshal.SizeOf(typeof(LsaString));
        for (int i = 0; i < n; i++) {
          LsaString u = (LsaString)Marshal.PtrToStructure(new IntPtr(buf.ToInt64() + i * size), typeof(LsaString));
          list.Add(Marshal.PtrToStringUni(u.Buffer, u.Length / 2));
        }
        return list.ToArray();
      } finally {
        LsaFreeMemory(buf);
      }
    } finally {
      LsaClose(p);
    }
  }
  // Grant: one right added to the SID.
  public static void Grant(byte[] sid, string right) {
    IntPtr p = Open();
    LsaString[] r = new LsaString[1];
    r[0].Buffer = Marshal.StringToHGlobalUni(right);
    r[0].Length = (ushort)(right.Length * 2);
    r[0].MaximumLength = (ushort)(right.Length * 2 + 2);
    try {
      uint s = LsaAddAccountRights(p, sid, r, 1);
      if (s != 0) { throw new Win32Exception(LsaNtStatusToWinError(s)); }
    } finally {
      Marshal.FreeHGlobal(r[0].Buffer);
      LsaClose(p);
    }
  }
  // ServiceLogon: 0 when the local account logs on as a service with this password, else the Windows error.
  public static int ServiceLogon(string user, string password) {
    IntPtr t;
    if (!LogonUserW(user, ".", password, ServiceLogonType, 0, out t)) { return Marshal.GetLastWin32Error(); }
    CloseHandle(t);
    return 0;
  }
}
'@
}

switch ($Action) {
  'account' {
    Need-Admin
    if (-not (Test-AccountName $Account)) { 'BAD-ARGS -Account'; exit 2 }
    $pw = Take-Secret $SecretEnv
    try {
      Add-AccountApi
      $sec = ConvertTo-SecureString -String $pw -AsPlainText -Force
      $did = 'ok'
      $u = Get-LocalUser -Name $Account -ErrorAction SilentlyContinue
      if (-not $u) {
        $u = New-LocalUser -Name $Account -Password $sec -PasswordNeverExpires -UserMayNotChangePassword -AccountNeverExpires -Description 'nova-fleet: an admin GitHub Actions runner'
        $did = 'created'
      }
      $sid = New-Object Security.Principal.SecurityIdentifier($u.SID.Value)
      $bin = New-Object byte[] $sid.BinaryLength
      $sid.GetBinaryForm($bin, 0)
      $have = @([NovaAccount]::Rights($bin))
      $added = @()
      foreach ($r in 'SeServiceLogonRight', 'SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight') {
        if ($have -notcontains $r) { [NovaAccount]::Grant($bin, $r); $added += "+$r" }
      }
      $rights = if ($added.Count -gt 0) { $added -join ',' } else { 'ok' }
      $admin = 'ok'
      try { Add-LocalGroupMember -SID 'S-1-5-32-544' -Member $u -ErrorAction Stop; $admin = 'added' }
      catch { if ("$($_.FullyQualifiedErrorId)" -notlike 'MemberExists*') { throw } }
      # an existing account is held to the stored password and the flags a new one gets; the logon check runs after
      # SeServiceLogonRight is granted, so a wrong password is the only way it fails on an enabled account
      if ($did -eq 'ok' -and (-not $u.Enabled -or $u.PasswordExpires -or $u.UserMayChangePassword -or [NovaAccount]::ServiceLogon($Account, $pw) -ne 0)) {
        Set-LocalUser -SID $u.SID -Password $sec -PasswordNeverExpires $true -UserMayChangePassword $false -AccountNeverExpires
        Enable-LocalUser -SID $u.SID
        $did = 'updated'
      }
      $sec.Dispose()
    } finally {
      $pw = $null
    }
    "ACCOUNT $Account account=$did rights=$rights admin=$admin"
  }
  'logon' {
    Need-Admin
    if (-not $Dir -or -not (Test-AccountName $Account)) { 'BAD-ARGS -Dir -Account'; exit 2 }
    $pw = Take-Secret $SecretEnv
    try {
      $s = Service-Of $Dir
      if (-not $s) { "NO-SERVICE $Dir"; exit 7 }
      $sid = (Get-LocalUser -Name $Account -ErrorAction Stop).SID.Value
      $full = [Security.AccessControl.FileSystemRights]::FullControl
      $both = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
      $acl = 'ok'
      $rules = @((Get-Acl -LiteralPath $Dir).GetAccessRules($true, $false, [Security.Principal.SecurityIdentifier]) | Where-Object {
          $_.IdentityReference.Value -eq $sid -and "$($_.AccessControlType)" -eq 'Allow' -and ($_.FileSystemRights -band $full) -eq $full -and $_.InheritanceFlags -eq $both })
      if ($rules.Count -eq 0) {
        & icacls.exe $Dir /grant "*${sid}:(OI)(CI)F" /C /Q | Out-Null
        if ($LASTEXITCODE -ne 0) { "ACL-FAILED $Dir rc=$LASTEXITCODE"; exit $LASTEXITCODE }
        $acl = 'set'
      }
      $want = ".\$Account"
      $did = 'ok'
      if ($Relogon -or @($want, "$env:COMPUTERNAME\$Account") -notcontains "$($s.StartName)") {
        $r = Invoke-CimMethod -InputObject $s -MethodName Change -Arguments @{ StartName = $want; StartPassword = $pw }
        if ($r.ReturnValue -ne 0) { "LOGON-FAILED $Dir rc=$($r.ReturnValue)"; exit 8 }
        if ($s.State -eq 'Running') { Restart-Service -Name $s.Name -Force }
        $did = 'set'
      }
    } finally {
      $pw = $null
    }
    "LOGON $Dir $did account=$want acl=$acl service=$($s.Name)"
  }
  'install' {
    Need-Admin
    if (-not $Dir -or -not $Cache -or -not $Version -or $Sha -notmatch '^[0-9a-f]{64}$') { 'BAD-PIN'; exit 2 }
    New-Item -ItemType Directory -Force -Path $Cache | Out-Null
    $zip = Join-Path $Cache "actions-runner-win-x64-$Version.zip"
    if (-not (Test-Path $zip) -or (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower() -ne $Sha) {
      $part = "$zip.part"
      Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/actions/runner/releases/download/v$Version/actions-runner-win-x64-$Version.zip" -OutFile $part
      $got = (Get-FileHash $part -Algorithm SHA256).Hash.ToLower()
      if ($got -ne $Sha) { Remove-Item -Force $part; "SHA-MISMATCH want $Sha got $got"; exit 5 }
      Move-Item -Force $part $zip
      "DOWNLOADED $zip"
    }
    if (-not (Test-Path (Join-Path $Dir 'config.cmd')) -and @(Get-ChildItem -LiteralPath $Dir -Force -ErrorAction SilentlyContinue).Count -gt 0) {
      "PARTIAL $Dir -> $(Move-Aside $Dir (Join-Path (Split-Path $Dir) "retired-$(Get-Date -Format yyyyMMdd)"))"
    }
    if (-not (Test-Path (Join-Path $Dir 'config.cmd'))) {
      New-Item -ItemType Directory -Force -Path $Dir | Out-Null
      Add-Type -AssemblyName System.IO.Compression.FileSystem
      [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Dir)
      "EXTRACTED $Dir"
    }
    # each runner's own Go build cache and dotnet install dir, never shared; DOTNET_INSTALL_DIR because NETWORK SERVICE
    # cannot write C:\Program Files\dotnet, where actions/setup-dotnet installs by default
    $own = [ordered]@{ GOCACHE = "$Dir\_cache\go-build"; DOTNET_INSTALL_DIR = "$Dir\_cache\dotnet"; DOTNET_CLI_TELEMETRY_OPTOUT = '1' }
    $pathText = ($PathList -split ';' | Where-Object { $_ }) -join ';'
    if ($pathText) { $own.Insert(0, 'PATH', $pathText) }
    $envFile = Join-Path $Dir '.env'
    $lines = @(); if (Test-Path $envFile) { $lines = @(Get-Content $envFile) }
    $kept = @($lines | Where-Object { $_ -notmatch '^([A-Z_]+)=' -or -not $own.Contains($Matches[1]) })
    $new = @($own.Keys | ForEach-Object { "$_=$($own[$_])" }) + $kept
    $changed = $false
    if (($new -join "`n") -ne ($lines -join "`n")) {
      [IO.File]::WriteAllLines($envFile, [string[]]$new, (New-Object System.Text.UTF8Encoding $false)); $changed = $true
    }
    $pathFile = Join-Path $Dir '.path'
    if ($pathText -and (-not (Test-Path $pathFile) -or (Get-Content -Raw $pathFile).TrimEnd() -ne $pathText)) {
      [IO.File]::WriteAllText($pathFile, "$pathText`n"); $changed = $true
    }
    if ($changed) { "ENV-CHANGED $Dir" }
  }
  'register' {
    Need-Admin
    if (Test-Path (Join-Path $Dir '.runner')) {
      $to = Join-Path $Dir "retired-$(Get-Date -Format yyyyMMdd)"
      New-Item -ItemType Directory -Force -Path $to | Out-Null
      foreach ($f in '.runner', '.credentials', '.credentials_rsaparams', '.service') {
        $p = Join-Path $Dir $f
        if (Test-Path -LiteralPath $p) { Move-Item -Force -LiteralPath $p -Destination (Join-Path $to $f) }
      }
      $s = Service-Of $Dir
      if ($s) { Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue; & sc.exe delete $s.Name | Out-Null }
      "REPAIR $Dir -> $to"
    }
    # A directory made through WSL carries an ACL that is not in canonical form, and config.cmd's service step cannot
    # change it ("This access control list is not in canonical form"): put it back to the inherited ACL first.
    & icacls.exe $Dir /reset /T /C /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { "ACL-RESET-FAILED $Dir rc=$LASTEXITCODE"; exit $LASTEXITCODE }
    $rc = Invoke-Config @('--unattended', '--url', $Url, '--name', $Name, '--labels', $Labels, '--work', '_work', '--replace', '--runasservice')
    if ($rc -ne 0) { "REGISTER-FAILED $Dir rc=$rc"; exit $rc }
    "REGISTERED $Name $Dir"
  }
  'ensure' {
    Need-Admin
    $s = Service-Of $Dir
    if (-not $s) { "NO-SERVICE $Dir"; exit 0 }
    $did = 'ok'
    if ($s.StartMode -ne 'Auto') { Set-Service -Name $s.Name -StartupType Automatic; $did = 'started' }
    if ($s.State -ne 'Running') { Start-Service -Name $s.Name; $did = 'started' }
    "SERVICE $Dir $did $($s.Name)"
  }
  'keepalive' {
    Need-Admin
    if ($MachineId -notmatch '^[0-9a-f]{32}$' -or $User -notmatch '^[a-z_][a-z0-9_-]*$') { 'BAD-ARGS -MachineId -User'; exit 2 }
    # The running distros (wsl.exe writes UTF-16) and the one whose /etc/machine-id is this machine's: never a stopped
    # one, which reading its files would start.
    $enc = [Console]::OutputEncoding
    [Console]::OutputEncoding = [Text.Encoding]::Unicode
    try { $running = @(& wsl.exe --list --running --quiet | ForEach-Object { "$_".Trim() } | Where-Object { $_ }) } finally { [Console]::OutputEncoding = $enc }
    $distro = $running | Where-Object {
      try { (Get-Content -LiteralPath "\\wsl.localhost\$_\etc\machine-id" -TotalCount 1 -ErrorAction Stop).Trim() -eq $MachineId } catch { $false }
    } | Select-Object -First 1
    if (-not $distro -or $distro -notmatch '^[A-Za-z0-9._-]+$') { "NO-DISTRO machine-id=$MachineId running=$(@($running) -join ',')"; exit 6 }
    $name = "WSL2 keepalive ($distro)"
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $exe = 'C:\Windows\System32\conhost.exe'
    $arg = "--headless C:\Windows\System32\wsl.exe -d $distro -u $User --exec /bin/sleep infinity"
    $xml = @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Set by nova-fleet windows-runners: holds WSL ($distro) running from boot, with nobody logged in, and starts it again each minute it is not running.</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>$sid</UserId>
      <LogonType>S4U</LogonType>
    </Principal>
  </Principals>
  <Settings>
    <Enabled>true</Enabled>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Triggers>
    <BootTrigger>
      <Repetition>
        <Interval>PT1M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
    </BootTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>$exe</Command>
      <Arguments>$arg</Arguments>
    </Exec>
  </Actions>
</Task>
"@
    # What the task does, read from its exported definition; an element the export leaves out has the scheduler's default.
    function Keepalive-Of([xml]$x) {
      $s = $x.Task.Settings
      $d = { param($v, $def) if ($null -eq $v -or "$v" -eq '') { $def } else { "$v" } }
      @(
        "user=$($x.Task.Principals.Principal.UserId)", "logon=$($x.Task.Principals.Principal.LogonType)",
        "triggers=$(@($x.Task.Triggers.ChildNodes | ForEach-Object { $_.LocalName }) -join ',')",
        "every=$($x.Task.Triggers.BootTrigger.Repetition.Interval)",
        "exec=$(@($x.Task.Actions.Exec | ForEach-Object { "$($_.Command) $($_.Arguments)" }) -join ';')",
        "enabled=$(& $d $s.Enabled 'true')", "multiple=$(& $d $s.MultipleInstancesPolicy 'IgnoreNew')",
        "limit=$(& $d $s.ExecutionTimeLimit 'PT72H')", "battery=$(& $d $s.DisallowStartIfOnBatteries 'true')/$(& $d $s.StopIfGoingOnBatteries 'true')"
      ) -join ' '
    }
    $did = @()
    $have = ''
    if (Get-ScheduledTask -TaskPath '\' -TaskName $name -ErrorAction SilentlyContinue) {
      $have = Keepalive-Of ([xml](Export-ScheduledTask -TaskPath '\' -TaskName $name))
    }
    if ($have -ne (Keepalive-Of ([xml]$xml))) {
      Register-ScheduledTask -TaskPath '\' -TaskName $name -Xml $xml -Force | Out-Null
      $did += 'set'
    }
    if ((Get-ScheduledTask -TaskPath '\' -TaskName $name).State -ne 'Running') {
      Start-ScheduledTask -TaskPath '\' -TaskName $name
      $did += 'started'
    }
    if ($did.Count -eq 0) { $did = @('ok') }
    "KEEPALIVE $distro $($did -join ',') task=$name"
  }
  'pagefile' {
    Need-Admin
    if ($SizeMb -lt 1024) { 'BAD-ARGS -SizeMb'; exit 2 }
    $path = 'C:\pagefile.sys'
    $cs = Get-CimInstance Win32_ComputerSystem
    $set = Get-CimInstance Win32_PageFileSetting | Where-Object { $_.Name -eq $path }
    $did = 'ok'
    if ($cs.AutomaticManagedPagefile) {
      Set-CimInstance -InputObject $cs -Property @{ AutomaticManagedPagefile = $false } | Out-Null
      $did = 'set'
    }
    $set = Get-CimInstance Win32_PageFileSetting | Where-Object { $_.Name -eq $path }
    if (-not $set) {
      $set = New-CimInstance -ClassName Win32_PageFileSetting -Property @{ Name = $path }
      $did = 'set'
    }
    if ($set.InitialSize -ne $SizeMb -or $set.MaximumSize -ne $SizeMb) {
      Set-CimInstance -InputObject $set -Property @{ InitialSize = [uint32]$SizeMb; MaximumSize = [uint32]$SizeMb } | Out-Null
      $did = 'set'
    }
    $use = Get-CimInstance Win32_PageFileUsage | Where-Object { $_.Name -eq $path }
    $active = if ($use) { [int]$use.AllocatedBaseSize } else { 0 }
    $owed = if ($active -eq $SizeMb) { 'no' } else { 'owed' }
    "PAGEFILE $did size_mb=$SizeMb active_mb=$active restart=$owed"
    $os = Get-CimInstance Win32_OperatingSystem
    "COMMIT limit_gb={0:N1} free_gb={1:N1}" -f ($os.TotalVirtualMemorySize / 1MB), ($os.FreeVirtualMemory / 1MB)
  }
}
