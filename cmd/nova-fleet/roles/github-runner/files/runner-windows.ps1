# runner-windows.ps1: a WSL machine's Windows-side GitHub Actions runners, driven by the github-runner role's
# tasks/wsl-windows.yml through WSL interop (powershell.exe run from the machine's WSL login, which acts with the Windows
# account that started WSL, elevated). The role stages this file to C:\ProgramData\nova-runner-stage and calls one verb
# at a time; every line it prints starts with a word the role matches. fleet/README.md, "Windows runners of a WSL machine".
#
#   install   -Dir -Cache -Version -Sha [-PathList] [-Account]
#             the pinned runner zip in -Cache (its sha256 checked before it is
#             used), unpacked into -Dir where config.cmd is missing (a dir with files but no config.cmd, an
#             interrupted unpack, is moved aside first: PARTIAL <dir> -> <where>), and the .env lines the role owns (PATH, GOCACHE,
#             DOTNET_INSTALL_DIR, DOTNET_CLI_TELEMETRY_OPTOUT) set in place, every other line (the CI-rules hooks) kept:
#             DOWNLOADED, EXTRACTED, ENV-CHANGED <dir> when something moved.
#             With -Account (an admin runner, whose -Dir is <admin root>\<name>): the admin root and -Dir are created
#             sealed (Seal-Dir: owned by Administrators, nothing inherited, full control for SYSTEM, Administrators and,
#             on -Dir, the account), and one that was there unsealed is refused, never changed: PLANTED (exit 11); the
#             whole tree under -Dir is proved sealed ([NovaTree]::Open), else ACL-OPEN (exit 10). The zip is copied into
#             -Dir (.nova-runner.zip), that copy hashed, and extracted from that same copy (overwriting; a new pin
#             replaces bin and externals while the runner is idle: UPGRADE-OWED <dir> busy while a job runs); on every
#             run the copy holds its recorded hash and every file it lists is that file, with nothing else in bin or
#             externals, else TAMPERED (exit 11). Every PATH entry (the machine PATH when -PathList is empty) is checked
#             writable by none of NETWORK SERVICE, Authenticated Users, Users and Everyone, else PATH-WRITABLE (exit 12).
#             Its .env holds only the role's lines, CCACHE_DISABLE=1 among them, and the CI-rules hooks in -Dir.
#   register  -Dir -Url -Name -Labels [-Account]      stdin carries ONE registration token; it reaches config.cmd as
#             ACTIONS_RUNNER_INPUT_TOKEN, never an argument, a file or a line of output. Registers the runner as a
#             Windows service (config.cmd --runasservice --replace: NT AUTHORITY\NETWORK SERVICE, automatic start):
#             REGISTERED. With -Account, registered only (--disableupdate, no service and no change to -Dir's ACL, so
#             NETWORK SERVICE is never granted anything there): logon makes the service. The role calls it only for a
#             runner that is not whole (no .runner, no service, or not in
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
#             environment as it is read; never an argument, a cmdlet parameter, a file or a line of output. The account is
#             created with the description $AccountMarker (password never expires, the user cannot change it), or, when
#             it carries that marker, its password set where the stored one does not log it on as a service; a user of
#             that name without the marker is refused, nothing changed: ACCOUNT-NOT-OURS (exit 9). Put in Administrators
#             by SID (S-1-5-32-544); granted SeServiceLogonRight and denied interactive and remote-interactive logon
#             (SeDenyInteractiveLogonRight, SeDenyRemoteInteractiveLogonRight) through the LSA:
#             ACCOUNT <account> account=created|updated|ok rights=ok|+<right>,... admin=added|ok
#   logon     -Dir -Url -Name -Account -SecretEnv [-Relogon]
#             the admin root and the whole tree under -Dir proved sealed first ([NovaTree]::Open; nothing is
#             repaired): an owner other than SYSTEM, Administrators or the account, an explicit ACE below -Dir, or on the
#             two directories an ACE for anyone else or inheritance on, is ACL-OPEN (exit 10). Then the runner's service
#             logs on as .\<account> with the password from the environment as above: made (CreateService, the name
#             config.cmd gives, written to .service) where there is none, else set (ChangeServiceConfig, on every
#             call). The service is restarted while idle when -Relogon (the password was set), its logon was another
#             account, or its listener runs as another user:
#             LOGON <dir> ok|set|created account=.\<account> restart=no|done|busy acl=protected service=<name>
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
# The description every account the account action makes carries; a user without it is never changed.
$AccountMarker = 'nova-fleet: windows admin runner'
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
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern IntPtr OpenSCManagerW(string machine, string database, uint access);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern IntPtr OpenServiceW(IntPtr manager, string name, uint access);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern bool ChangeServiceConfigW(IntPtr service, uint type, uint start, uint error, string path, string group,
    IntPtr tag, string dependencies, string account, string password, string display);
  [DllImport("advapi32.dll")] static extern bool CloseServiceHandle(IntPtr handle);
  const uint NoChange = 0xFFFFFFFF;       // SERVICE_NO_CHANGE
  const uint ManagerConnect = 0x0001;     // SC_MANAGER_CONNECT
  const uint ServiceChangeConfig = 0x0002; // SERVICE_CHANGE_CONFIG
  // SetServiceLogon: the service logs on as account with password from its next start; 0, else the Windows error. A
  // method call, so the password is never a cmdlet parameter that module logging would record.
  public static int SetServiceLogon(string service, string account, string password) {
    IntPtr m = OpenSCManagerW(null, null, ManagerConnect);
    if (m == IntPtr.Zero) { return Marshal.GetLastWin32Error(); }
    try {
      IntPtr s = OpenServiceW(m, service, ServiceChangeConfig);
      if (s == IntPtr.Zero) { return Marshal.GetLastWin32Error(); }
      try {
        if (!ChangeServiceConfigW(s, NoChange, NoChange, NoChange, null, null, IntPtr.Zero, null, account, password, null)) {
          return Marshal.GetLastWin32Error();
        }
        return 0;
      } finally {
        CloseServiceHandle(s);
      }
    } finally {
      CloseServiceHandle(m);
    }
  }
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern IntPtr CreateServiceW(IntPtr manager, string name, string display, uint access, uint type, uint start,
    uint error, string path, string group, IntPtr tag, string dependencies, string account, string password);
  const uint ManagerCreate = 0x0002;      // SC_MANAGER_CREATE_SERVICE
  const uint OwnProcess = 0x00000010;     // SERVICE_WIN32_OWN_PROCESS
  const uint AutoStart = 0x00000002;      // SERVICE_AUTO_START
  const uint ErrorNormal = 0x00000001;    // SERVICE_ERROR_NORMAL
  // CreateService: a service that starts at boot and logs on as account with password; 0, else the Windows error.
  public static int CreateService(string name, string display, string path, string account, string password) {
    IntPtr m = OpenSCManagerW(null, null, ManagerConnect | ManagerCreate);
    if (m == IntPtr.Zero) { return Marshal.GetLastWin32Error(); }
    try {
      IntPtr s = CreateServiceW(m, name, display, ServiceChangeConfig, OwnProcess, AutoStart, ErrorNormal, path, null,
        IntPtr.Zero, null, account, password);
      if (s == IntPtr.Zero) { return Marshal.GetLastWin32Error(); }
      CloseServiceHandle(s);
      return 0;
    } finally {
      CloseServiceHandle(m);
    }
  }
}
'@
}
# Add-TreeApi: an admin runner's directory, walked and checked in-process (thousands of files under _work), and the
# pinned zip extracted and compared file by file.
function Add-TreeApi {
  if ('NovaTree' -as [type]) { return }
  Add-Type -ReferencedAssemblies 'System.IO.Compression', 'System.IO.Compression.FileSystem' -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Compression;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
public static class NovaTree {
  // Open: null when dir is sealed, else "<path> <why>" for the first place that is not. dir itself: inheritance off,
  // its owner and every ACE's SID in allowed. With deep, everything under it too, walked without following a reparse
  // point: its owner in allowed, inheritance on and no explicit ACE (so it holds exactly what dir gives). A place that
  // cannot be read is not sealed.
  public static string Open(string dir, string[] allowed, bool deep) {
    DirectoryInfo d = new DirectoryInfo(dir);
    string why = Check(d, allowed, true);
    if (why != null || !deep) { return why; }
    Stack<DirectoryInfo> todo = new Stack<DirectoryInfo>();
    todo.Push(d);
    while (todo.Count > 0) {
      DirectoryInfo cur = todo.Pop();
      FileSystemInfo[] items;
      try {
        items = cur.GetFileSystemInfos();
      } catch (Exception e) {
        return cur.FullName + " unreadable=" + e.GetType().Name;
      }
      foreach (FileSystemInfo i in items) {
        why = Check(i, allowed, false);
        if (why != null) { return why; }
        DirectoryInfo sub = i as DirectoryInfo;
        if (sub != null && (i.Attributes & FileAttributes.ReparsePoint) == 0) { todo.Push(sub); }
      }
    }
    return null;
  }
  static string Check(FileSystemInfo i, string[] allowed, bool top) {
    string at = i.FullName;
    FileSystemSecurity s;
    try {
      DirectoryInfo d = i as DirectoryInfo;
      s = d != null ? (FileSystemSecurity)d.GetAccessControl() : ((FileInfo)i).GetAccessControl();
    } catch (Exception e) {
      return at + " unreadable=" + e.GetType().Name;
    }
    string owner = s.GetOwner(typeof(SecurityIdentifier)).Value;
    if (Array.IndexOf(allowed, owner) < 0) { return at + " owner=" + owner; }
    if (top != s.AreAccessRulesProtected) { return at + (top ? " inherited=yes" : " protected=yes"); }
    foreach (FileSystemAccessRule r in s.GetAccessRules(true, true, typeof(SecurityIdentifier))) {
      string sid = r.IdentityReference.Value;
      if (top ? Array.IndexOf(allowed, sid) < 0 : !r.IsInherited) { return at + " ace=" + sid; }
    }
    return null;
  }
  // Extract: every entry of zip written under dir, overwriting; an entry naming a place outside dir is refused.
  public static void Extract(string zip, string dir) {
    string root = Path.GetFullPath(dir).TrimEnd('\\') + "\\";
    using (ZipArchive z = ZipFile.OpenRead(zip)) {
      foreach (ZipArchiveEntry e in z.Entries) {
        string p = Path.GetFullPath(Path.Combine(root, e.FullName.Replace('/', '\\')));
        if (!p.StartsWith(root, StringComparison.OrdinalIgnoreCase)) { throw new IOException("zip entry outside the directory: " + e.FullName); }
        if (e.FullName.EndsWith("/")) {
          Directory.CreateDirectory(p);
          continue;
        }
        Directory.CreateDirectory(Path.GetDirectoryName(p));
        e.ExtractToFile(p, true);
      }
    }
  }
  // Verify: null when every file zip lists is under dir with the same sha256 and the directories closed hold no file it
  // does not list; else "<path> missing|changed|extra".
  public static string Verify(string zip, string dir, string[] closed) {
    HashSet<string> listed = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
    using (SHA256 h = SHA256.Create())
    using (ZipArchive z = ZipFile.OpenRead(zip)) {
      foreach (ZipArchiveEntry e in z.Entries) {
        if (e.FullName.EndsWith("/")) { continue; }
        string rel = e.FullName.Replace('/', '\\');
        listed.Add(rel);
        string p = Path.Combine(dir, rel);
        if (!File.Exists(p)) { return p + " missing"; }
        byte[] want, got;
        using (Stream a = e.Open()) { want = h.ComputeHash(a); }
        using (FileStream b = new FileStream(p, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete)) { got = h.ComputeHash(b); }
        if (Convert.ToBase64String(want) != Convert.ToBase64String(got)) { return p + " changed"; }
      }
    }
    foreach (string c in closed) {
      string cd = Path.Combine(dir, c);
      if (!Directory.Exists(cd)) { continue; }
      foreach (string f in Directory.EnumerateFiles(cd, "*", SearchOption.AllDirectories)) {
        if (!listed.Contains(f.Substring(dir.TrimEnd('\\').Length + 1))) { return f + " extra"; }
      }
    }
    return null;
  }
}
'@
}
# The SIDs an admin runner's directories may hold: SYSTEM and Administrators (and, on -Dir, the account). The ones no
# admin runner's PATH may be writable by: NETWORK SERVICE (every ordinary runner), Authenticated Users, Users, Everyone.
$Sealers = @('S-1-5-18', 'S-1-5-32-544')
$Openers = @('S-1-5-20', 'S-1-5-11', 'S-1-5-32-545', 'S-1-1-0')
# Seal-Dir PATH SIDS: PATH created sealed where it is missing (in one call, so it is never there unsealed: owned by
# Administrators, nothing inherited, full control for SYSTEM, Administrators and SIDS); then, made here or found,
# checked sealed. One that is not was there before this play sealed it (anyone could have put a file in it): PLANTED,
# exit 11, nothing changed.
function Seal-Dir([string]$p, [string[]]$sids) {
  $allowed = $Sealers + $sids
  if (-not (Test-Path -LiteralPath $p)) {
    $ds = New-Object Security.AccessControl.DirectorySecurity
    $ds.SetAccessRuleProtection($true, $false)
    $ds.SetOwner((New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'))
    foreach ($s in $allowed) {
      $ds.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier $s),
        'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
    }
    [void][IO.Directory]::CreateDirectory($p, $ds)
  }
  $why = [NovaTree]::Open($p, $allowed, $false)
  if ($why) { "PLANTED ${why}: it was not made sealed by this play; delete it and run the play again: Remove-Item -LiteralPath '$p' -Recurse -Force"; exit 11 }
}
# Find-OpenPath DIRS: the first DIR (or, where it is missing, the nearest folder above it, where it could be made) that
# NETWORK SERVICE, Authenticated Users, Users or Everyone may write, as "<dir> sid=<sid> at=<folder>"; else nothing.
# An ACE counts where it applies to the folder itself or to the files in it.
function Find-OpenPath([string[]]$dirs) {
  $write = 0x2 -bor 0x4 -bor 0x40 -bor 0x10000 -bor 0x40000 -bor 0x80000 -bor 0x10000000 -bor 0x40000000
  foreach ($d in $dirs) {
    $p = $d
    while ($p -and -not (Test-Path -LiteralPath $p)) { $p = Split-Path $p }
    if (-not $p) { continue }
    foreach ($r in (Get-Acl -LiteralPath $p).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
      $here = ([int]$r.PropagationFlags -band 2) -eq 0 -or ([int]$r.InheritanceFlags -band 2) -ne 0
      if ("$($r.AccessControlType)" -eq 'Allow' -and $Openers -contains $r.IdentityReference.Value -and
          ([int64]$r.FileSystemRights -band $write) -ne 0 -and $here) {
        return "$d sid=$($r.IdentityReference.Value) at=$p"
      }
    }
  }
}
# Test-Busy DIR: the runner in DIR is running a job (its listener has a Runner.Worker child).
function Test-Busy([string]$d) {
  $exe = Join-Path $d 'bin\Runner.Listener.exe'
  $l = Get-CimInstance Win32_Process -Filter "Name = 'Runner.Listener.exe'" | Where-Object { $_.ExecutablePath -eq $exe } | Select-Object -First 1
  return [bool]($l -and (Get-CimInstance Win32_Process -Filter "ParentProcessId = $($l.ProcessId) AND Name = 'Runner.Worker.exe'"))
}
# Get-OurUser NAME: the local user an admin runner runs as, which must carry $AccountMarker.
function Get-OurUser([string]$n) {
  $u = Get-LocalUser -Name $n -ErrorAction Stop
  if ("$($u.Description)" -cne $AccountMarker) { "ACCOUNT-NOT-OURS ${n}: no description '$AccountMarker'; nothing was changed"; exit 9 }
  return $u
}

switch ($Action) {
  'account' {
    Need-Admin
    if (-not (Test-AccountName $Account)) { 'BAD-ARGS -Account'; exit 2 }
    $pw = Take-Secret $SecretEnv
    try {
      $u = Get-LocalUser -Name $Account -ErrorAction SilentlyContinue
      # a user of that name that this play did not make (no marker) is never taken over
      if ($u -and "$($u.Description)" -cne $AccountMarker) {
        "ACCOUNT-NOT-OURS ${Account}: a local user of that name exists without the description '$AccountMarker'; nothing was changed; give the runner another account"
        exit 9
      }
      Add-AccountApi
      # built char by char through method calls, so the plain text is never a cmdlet parameter (module logging)
      $sec = New-Object Security.SecureString
      foreach ($c in $pw.ToCharArray()) { $sec.AppendChar($c) }
      $sec.MakeReadOnly()
      $did = 'ok'
      if (-not $u) {
        $u = New-LocalUser -Name $Account -Password $sec -PasswordNeverExpires -UserMayNotChangePassword -AccountNeverExpires -Description $AccountMarker
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
    if (-not $Dir -or -not $Url -or -not $Name -or -not (Test-AccountName $Account)) { 'BAD-ARGS -Dir -Url -Name -Account'; exit 2 }
    $pw = Take-Secret $SecretEnv
    try {
      $u = Get-OurUser $Account
      Add-AccountApi
      Add-TreeApi
      # The proof, before the service is given the account: the admin root holds only SYSTEM and Administrators, and
      # the runner's directory and everything under it hold what install sealed it with. Nothing is repaired: a
      # directory that is not sealed may hold a file a job on another runner put there, which this account would run.
      foreach ($why in [NovaTree]::Open((Split-Path $Dir), $Sealers, $false), [NovaTree]::Open($Dir, $Sealers + @($u.SID.Value), $true)) {
        if ($why) { "ACL-OPEN ${why}: stop its service, delete the runner's directory and run the play again"; exit 10 }
      }
      $want = ".\$Account"
      $s = Service-Of $Dir
      if (-not $s) {
        # config.cmd's name for a repository's service; RunnerService.exe starts the listener as the service's account
        $svc = "actions.runner.$($Url -replace '^https://[^/]+/', '' -replace '/', '-').$Name"
        $rc = [NovaAccount]::CreateService($svc, "GitHub Actions Runner ($svc)", "`"$Dir\bin\RunnerService.exe`"", $want, $pw)
        if ($rc -ne 0) { "LOGON-FAILED $Dir create=$svc rc=$rc"; exit 8 }
        [IO.File]::WriteAllText((Join-Path $Dir '.service'), $svc)
        & sc.exe failure $svc reset= 86400 actions= restart/60000/restart/60000/restart/60000 | Out-Null
        $s = Service-Of $Dir
        $did, $moved = 'created', $false
      } else {
        $moved = @($want, "$env:COMPUTERNAME\$Account") -notcontains "$($s.StartName)"
        # applied on every run, so a run cut off between the account step and this one never leaves the old password
        $rc = [NovaAccount]::SetServiceLogon($s.Name, $want, $pw)
        if ($rc -ne 0) { "LOGON-FAILED $Dir rc=$rc"; exit 8 }
        $did = if ($moved) { 'set' } else { 'ok' }
      }
    } finally {
      $pw = $null
    }
    # A restart is owed when the password was set anew, the service had another logon, or its listener runs as
    # someone else; it is taken only while no job runs (a busy runner keeps the old logon until the next run).
    $exe = Join-Path $Dir 'bin\Runner.Listener.exe'
    $listener = Get-CimInstance Win32_Process -Filter "Name = 'Runner.Listener.exe'" | Where-Object { $_.ExecutablePath -eq $exe } | Select-Object -First 1
    $stale = $false
    if ($listener) { $stale = "$((Invoke-CimMethod -InputObject $listener -MethodName GetOwner).User)" -ne $Account }
    $restart = 'no'
    if (($Relogon -or $moved -or $stale) -and $s.State -eq 'Running') {
      if (Test-Busy $Dir) { $restart = 'busy' }
      else { Restart-Service -Name $s.Name -Force; $restart = 'done' }
    }
    "LOGON $Dir $did account=$want restart=$restart acl=protected service=$($s.Name)"
  }
  'install' {
    Need-Admin
    if (-not $Dir -or -not $Cache -or -not $Version -or $Sha -notmatch '^[0-9a-f]{64}$') { 'BAD-PIN'; exit 2 }
    if ($Account -and -not (Test-AccountName $Account)) { 'BAD-ARGS -Account'; exit 2 }
    if ($Account) {
      # sealed before anything is put in it, and proved sealed before anything in it is trusted
      $u = Get-OurUser $Account
      Add-TreeApi
      Seal-Dir (Split-Path $Dir) @()
      Seal-Dir $Dir @($u.SID.Value)
      $why = [NovaTree]::Open($Dir, $Sealers + @($u.SID.Value), $true)
      if ($why) { "ACL-OPEN ${why}: stop its service, delete the runner's directory and run the play again"; exit 10 }
      $paths = @($PathList -split ';' | Where-Object { $_ })
      if ($paths.Count -eq 0) { $paths = @([Environment]::GetEnvironmentVariable('Path', 'Machine') -split ';' | Where-Object { $_ }) }
      $open = Find-OpenPath $paths
      if ($open) { "PATH-WRITABLE ${open}: a job on another runner could put a program there that this administrator's jobs run; take it out of github_runner_windows_path or close it"; exit 12 }
    }
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
    if ($Account) {
      # The zip copied into the sealed -Dir, that copy hashed, and extracted from that same copy: the cache is not
      # sealed, so the file hashed there could be swapped before it is read again. A new pin replaces bin and externals
      # (moved into retired-<yyyymmdd>) while the runner is idle; a busy one keeps the copy it has until a later run.
      $pinned, $pinnedSha = (Join-Path $Dir '.nova-runner.zip'), (Join-Path $Dir '.nova-runner.sha256')
      $have = if (Test-Path -LiteralPath $pinnedSha) { (Get-Content -Raw -LiteralPath $pinnedSha).Trim() } else { '' }
      $fresh = -not (Test-Path -LiteralPath (Join-Path $Dir 'config.cmd'))
      $s = Service-Of $Dir
      if (-not $fresh -and $have -ne $Sha -and $s -and $s.State -eq 'Running' -and (Test-Busy $Dir)) {
        "UPGRADE-OWED $Dir busy: it keeps the runner it has until a run finds it idle"
      } elseif ($fresh -or $have -ne $Sha) {
        if ($s -and $s.State -eq 'Running') { Stop-Service -Name $s.Name -Force }
        Copy-Item -Force -LiteralPath $zip -Destination $pinned
        $got = (Get-FileHash -LiteralPath $pinned -Algorithm SHA256).Hash.ToLower()
        if ($got -ne $Sha) { Remove-Item -Force -LiteralPath $pinned; "SHA-MISMATCH want $Sha got $got"; exit 5 }
        if (-not $fresh) {
          $to = Join-Path $Dir "retired-$(Get-Date -Format yyyyMMdd)"
          New-Item -ItemType Directory -Force -Path $to | Out-Null
          foreach ($f in 'bin', 'externals') {
            if (Test-Path -LiteralPath (Join-Path $Dir $f)) { Move-Item -LiteralPath (Join-Path $Dir $f) -Destination (Join-Path $to "$f.$([DateTime]::UtcNow.ToString('HHmmss'))") }
          }
        }
        [NovaTree]::Extract($pinned, $Dir)
        [IO.File]::WriteAllText($pinnedSha, $Sha)
        "EXTRACTED $Dir"
      }
      # every run: the copy is the one that was hashed, and every file it lists is that file, nothing more in bin or
      # externals (the runner never updates itself: register gave it --disableupdate)
      $rec = (Get-Content -Raw -LiteralPath $pinnedSha).Trim()
      $why = if ((Get-FileHash -LiteralPath $pinned -Algorithm SHA256).Hash.ToLower() -ne $rec) { "$pinned changed" } else { [NovaTree]::Verify($pinned, $Dir, @('bin', 'externals')) }
      if ($why) { "TAMPERED ${why}: stop its service, delete the runner's directory and run the play again"; exit 11 }
    } else {
      if (-not (Test-Path (Join-Path $Dir 'config.cmd')) -and @(Get-ChildItem -LiteralPath $Dir -Force -ErrorAction SilentlyContinue).Count -gt 0) {
        "PARTIAL $Dir -> $(Move-Aside $Dir (Join-Path (Split-Path $Dir) "retired-$(Get-Date -Format yyyyMMdd)"))"
      }
      if (-not (Test-Path (Join-Path $Dir 'config.cmd'))) {
        New-Item -ItemType Directory -Force -Path $Dir | Out-Null
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Dir)
        "EXTRACTED $Dir"
      }
    }
    # each runner's own Go build cache and dotnet install dir, never shared; DOTNET_INSTALL_DIR because NETWORK SERVICE
    # cannot write C:\Program Files\dotnet, where actions/setup-dotnet installs by default
    $own = [ordered]@{ GOCACHE = "$Dir\_cache\go-build"; DOTNET_INSTALL_DIR = "$Dir\_cache\dotnet"; DOTNET_CLI_TELEMETRY_OPTOUT = '1' }
    $pathText = ($PathList -split ';' | Where-Object { $_ }) -join ';'
    if ($pathText) { $own.Insert(0, 'PATH', $pathText) }
    if ($Account) {
      # an admin runner's .env is the role's whole: no shared ccache (every runner writes its cache, and a job here
      # would link what they put there), and the CI-rules hooks as nova-ci-rules apply writes them, so no other line
      # (a hook or a variable some earlier hand put there) reaches an administrator's jobs
      $own['CCACHE_DISABLE'] = '1'
      $own['ACTIONS_RUNNER_HOOK_JOB_STARTED'] = "$Dir\.nova-ci-rules\job-started.ps1"
      $own['ACTIONS_RUNNER_HOOK_JOB_COMPLETED'] = "$Dir\.nova-ci-rules\job-completed.ps1"
    }
    $envFile = Join-Path $Dir '.env'
    $lines = @(); if (Test-Path $envFile) { $lines = @(Get-Content $envFile) }
    $kept = @($lines | Where-Object { -not $Account -and ($_ -notmatch '^([A-Z_]+)=' -or -not $own.Contains($Matches[1])) })
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
    $a = @('--unattended', '--url', $Url, '--name', $Name, '--labels', $Labels, '--work', '_work', '--replace')
    if ($Account) {
      # An admin runner's directory is sealed (install) and stays so: --runasservice would grant NETWORK SERVICE full
      # control of it and start the listener as NETWORK SERVICE, so it is registered only, and logon makes the service.
      # --disableupdate keeps bin and externals the pinned zip's, which install checks on every run.
      $a += '--disableupdate'
    } else {
      # A directory made through WSL carries an ACL that is not in canonical form, and config.cmd's service step cannot
      # change it ("This access control list is not in canonical form"): put it back to the inherited ACL first.
      & icacls.exe $Dir /reset /T /C /Q | Out-Null
      if ($LASTEXITCODE -ne 0) { "ACL-RESET-FAILED $Dir rc=$LASTEXITCODE"; exit $LASTEXITCODE }
      $a += '--runasservice'
    }
    $rc = Invoke-Config $a
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
