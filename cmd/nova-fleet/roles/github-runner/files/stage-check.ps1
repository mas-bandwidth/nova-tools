# stage-check.ps1: the proof that the scripts the role runs elevated on a WSL machine's Windows side (runner-windows.ps1,
# nova-ci-rules.ps1 and its hooks) are the role's own and that no other account could have changed them. It is never
# staged: tasks/wsl-stage.yml hands it to powershell.exe as -EncodedCommand, after a first line that sets
#   $Make   create the stage directory sealed when it is missing (the first call of a run), else only check
#   $Stage  the stage directory (github_runner_wsl_stage_dir, C:\nova-stage)
#   $Names  the files that must be there, $Shas their sha256 (from the role's own files on the control node)
# and every elevated call runs only after it exits 0 (github_runner_win_stage_check). It changes nothing it finds and
# repairs nothing; one line, then exit 14, on a refusal:
#   STAGE-OPEN <path> ...     a folder above the stage directory lets NETWORK SERVICE, SERVICE, LOCAL, Authenticated
#                             Users, Users or Everyone make, change or delete a file in it, or is owned by one of them
#   STAGE-PLANTED <path> ...  the stage directory, or something in it, is not as this play makes it: owned by SYSTEM
#                             or Administrators, nothing inherited, only SYSTEM and Administrators in its list; and in
#                             it only files owned by those or by this account, with nothing but what they inherit
#   STAGE-CHANGED <path> ...  a file to run is missing or is not the role's (its sha256)
# On success it prints nothing, or with $Make, STAGE <dir> ok|made.
$ErrorActionPreference = 'Stop'
if (-not ('NovaStage' -as [type])) {
  Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
// StageAce: one ACE as the checks read it. InheritOnly: it does not apply to the place itself.
public sealed class StageAce {
  public string Sid;
  public bool Allow;
  public bool Inherited;
  public bool InheritOnly;
  public long Rights;
}
// StageFacts: what the checks read of one place. Error: it is there and its security could not be read.
public sealed class StageFacts {
  public bool Missing;
  public bool Folder;
  public bool Reparse;
  public string Error;
  public string Owner;
  public bool Protected;
  public StageAce[] Aces = new StageAce[0];
}
// IStageDisk: the file system as the checks see it (the tests give a fake one). Children throws when a folder cannot
// be listed; Parent is null above the drive.
public interface IStageDisk {
  StageFacts Read(string path);
  string[] Children(string path);
  string Parent(string path);
  string Sha256(string path);
}
public sealed class StageDisk : IStageDisk {
  public StageFacts Read(string p) {
    StageFacts f = new StageFacts();
    FileSystemInfo i = Directory.Exists(p) ? (FileSystemInfo)new DirectoryInfo(p) : new FileInfo(p);
    if (!i.Exists) {
      f.Missing = true;
      return f;
    }
    f.Folder = i is DirectoryInfo;
    f.Reparse = (i.Attributes & FileAttributes.ReparsePoint) != 0;
    try {
      FileSystemSecurity s = f.Folder ? (FileSystemSecurity)((DirectoryInfo)i).GetAccessControl() : ((FileInfo)i).GetAccessControl();
      f.Owner = s.GetOwner(typeof(SecurityIdentifier)).Value;
      f.Protected = s.AreAccessRulesProtected;
      List<StageAce> aces = new List<StageAce>();
      foreach (FileSystemAccessRule r in s.GetAccessRules(true, true, typeof(SecurityIdentifier))) {
        StageAce a = new StageAce();
        a.Sid = r.IdentityReference.Value;
        a.Allow = r.AccessControlType == AccessControlType.Allow;
        a.Inherited = r.IsInherited;
        a.InheritOnly = (r.PropagationFlags & PropagationFlags.InheritOnly) != 0;
        a.Rights = (long)(uint)(int)r.FileSystemRights;
        aces.Add(a);
      }
      f.Aces = aces.ToArray();
    } catch (Exception e) {
      f.Error = e.GetType().Name;
    }
    return f;
  }
  public string[] Children(string p) { return Directory.GetFileSystemEntries(p); }
  public string Parent(string p) {
    DirectoryInfo d = Directory.GetParent(p);
    return d == null ? null : d.FullName;
  }
  public string Sha256(string p) {
    using (SHA256 h = SHA256.Create())
    using (FileStream s = new FileStream(p, FileMode.Open, FileAccess.Read, FileShare.Read)) {
      return BitConverter.ToString(h.ComputeHash(s)).Replace("-", "").ToLowerInvariant();
    }
  }
}
public static class NovaStage {
  public static readonly string[] Sealers = { "S-1-5-18", "S-1-5-32-544" };
  // NETWORK SERVICE (every ordinary runner's jobs), SERVICE, LOCAL, Authenticated Users, Users, Everyone
  public static readonly string[] Openers = { "S-1-5-20", "S-1-5-6", "S-1-2-0", "S-1-5-11", "S-1-5-32-545", "S-1-1-0" };
  // The rights on a folder that let someone make, change or delete a file in it: FILE_WRITE_DATA (add a file),
  // DELETE_CHILD, DELETE, WRITE_DAC, WRITE_OWNER, GENERIC_ALL, GENERIC_WRITE. FILE_APPEND_DATA (add a folder) is not
  // among them: C:\ gives it to Authenticated Users, and a folder they make is beside the stage directory, not in it.
  public const long FileWrite = 0x2 | 0x40 | 0x10000 | 0x40000 | 0x80000 | 0x10000000 | 0x40000000;

  // Above: the first folder above stage, up to the drive, that one of Openers owns or may make, change or delete a file
  // in (an allow ACE that applies to the folder itself), or that cannot be read; else null.
  public static string Above(IStageDisk d, string stage) {
    for (string p = d.Parent(stage); p != null; p = d.Parent(p)) {
      StageFacts f = d.Read(p);
      if (f.Missing || f.Error != null) { return "STAGE-OPEN " + p + " unreadable=" + (f.Error ?? "missing"); }
      if (Array.IndexOf(Openers, f.Owner) >= 0) { return "STAGE-OPEN " + p + " owner=" + f.Owner; }
      foreach (StageAce a in f.Aces) {
        if (a.Allow && !a.InheritOnly && (a.Rights & FileWrite) != 0 && Array.IndexOf(Openers, a.Sid) >= 0) { return "STAGE-OPEN " + p + " sid=" + a.Sid; }
      }
    }
    return null;
  }
  // Root: null when stage is a folder (not a link) owned by SYSTEM or Administrators, inheriting nothing, with only
  // SYSTEM and Administrators in its list; "STAGE-MISSING" when it is not there; else why.
  public static string Root(IStageDisk d, string stage) {
    StageFacts f = d.Read(stage);
    if (f.Missing) { return "STAGE-MISSING " + stage; }
    if (f.Error != null) { return "STAGE-PLANTED " + stage + " unreadable=" + f.Error; }
    if (!f.Folder || f.Reparse) { return "STAGE-PLANTED " + stage + " folder=no"; }
    if (Array.IndexOf(Sealers, f.Owner) < 0) { return "STAGE-PLANTED " + stage + " owner=" + f.Owner; }
    if (!f.Protected) { return "STAGE-PLANTED " + stage + " inherited=yes"; }
    foreach (StageAce a in f.Aces) {
      if (Array.IndexOf(Sealers, a.Sid) < 0) { return "STAGE-PLANTED " + stage + " ace=" + a.Sid; }
    }
    return null;
  }
  // Files: null when everything in stage is a file (not a link) owned by one of owners with only inherited ACEs, and
  // each of names is there with the sha256 in shas; else why.
  public static string Files(IStageDisk d, string stage, string[] owners, string[] names, string[] shas) {
    string[] items;
    try {
      items = d.Children(stage);
    } catch (Exception e) {
      return "STAGE-PLANTED " + stage + " unreadable=" + e.GetType().Name;
    }
    foreach (string p in items) {
      StageFacts f = d.Read(p);
      if (f.Error != null) { return "STAGE-PLANTED " + p + " unreadable=" + f.Error; }
      if (f.Folder || f.Reparse) { return "STAGE-PLANTED " + p + " file=no"; }
      if (Array.IndexOf(owners, f.Owner) < 0) { return "STAGE-PLANTED " + p + " owner=" + f.Owner; }
      foreach (StageAce a in f.Aces) {
        if (!a.Inherited) { return "STAGE-PLANTED " + p + " ace=" + a.Sid; }
      }
    }
    for (int i = 0; i < names.Length; i++) {
      string p = stage.TrimEnd('\\') + "\\" + names[i];
      if (d.Read(p).Missing) { return "STAGE-CHANGED " + p + " missing"; }
      string got = d.Sha256(p);
      if (!string.Equals(got, shas[i], StringComparison.OrdinalIgnoreCase)) { return "STAGE-CHANGED " + p + " sha256=" + got; }
    }
    return null;
  }
  // Check: the folders above, then the directory, then what is in it; null when all hold.
  public static string Check(IStageDisk d, string stage, string[] owners, string[] names, string[] shas) {
    return Above(d, stage) ?? Root(d, stage) ?? Files(d, stage, owners, names, shas);
  }
}
'@
}
$disk = New-Object StageDisk
$owners = [string[]](@([NovaStage]::Sealers) + @([Security.Principal.WindowsIdentity]::GetCurrent().User.Value))
$did = 'ok'
if ($Make -and -not [NovaStage]::Above($disk, $Stage) -and -not (Test-Path -LiteralPath $Stage)) {
  # made sealed in the one call that creates it, so it is never there open
  $ds = New-Object Security.AccessControl.DirectorySecurity
  $ds.SetAccessRuleProtection($true, $false)
  $ds.SetOwner((New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'))
  foreach ($s in [NovaStage]::Sealers) {
    $ds.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier $s),
      'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
  }
  [void][IO.Directory]::CreateDirectory($Stage, $ds)
  $did = 'made'
}
$why = [NovaStage]::Check($disk, $Stage, $owners, [string[]]$Names, [string[]]$Shas)
if ($why) {
  if ($why -like 'STAGE-PLANTED *') { "${why}: it was not made by this play and is never used or changed; in an elevated PowerShell: Remove-Item -LiteralPath '$Stage' -Recurse -Force, then run the play again" }
  elseif ($why -like 'STAGE-OPEN *') { "${why}: a job on an ordinary runner could put a file there; nothing was run; close it (no write for those accounts) and run the play again" }
  else { "${why}: nothing was run; run the play again" }
  exit 14
}
if ($Make) { "STAGE $Stage $did" }
