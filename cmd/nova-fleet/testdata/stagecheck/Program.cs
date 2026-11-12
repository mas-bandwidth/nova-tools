// Program.cs: the stage check's decisions (NovaStage in files/stage-check.ps1) on a fake disk. TestFunctionalStageCheck
// compiles it with the script's own C# at C# 5; every failed case prints a FAIL line, and the exit code is their count.
using System;
using System.Collections.Generic;

// FakeDisk: paths with '\' separators; a folder's children are the paths one level below it.
sealed class FakeDisk : IStageDisk {
  public readonly Dictionary<string, StageFacts> Facts = new Dictionary<string, StageFacts>(StringComparer.OrdinalIgnoreCase);
  public readonly Dictionary<string, string> Shas = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
  public readonly HashSet<string> Unlistable = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
  public StageFacts Read(string p) {
    StageFacts f;
    if (Facts.TryGetValue(p, out f)) { return f; }
    StageFacts m = new StageFacts();
    m.Missing = true;
    return m;
  }
  public string[] Children(string p) {
    if (Unlistable.Contains(p)) { throw new UnauthorizedAccessException(p); }
    List<string> o = new List<string>();
    foreach (string k in Facts.Keys) {
      if (k.Length > 3 && string.Equals(Parent(k), p, StringComparison.OrdinalIgnoreCase)) { o.Add(k); }
    }
    o.Sort(StringComparer.Ordinal);
    return o.ToArray();
  }
  public string Parent(string p) {
    if (p.Length <= 3) { return null; }
    int i = p.TrimEnd('\\').LastIndexOf('\\');
    return i == 2 ? p.Substring(0, 3) : p.Substring(0, i);
  }
  public string Sha256(string p) { return Shas[p]; }
}

static class Program {
  const string Sys = "S-1-5-18", Adm = "S-1-5-32-544", NS = "S-1-5-20", AU = "S-1-5-11", Users = "S-1-5-32-545", Me = "S-1-5-21-1-2-3-500";
  const string TI = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464";
  const string Stage = "C:\\nova-stage";
  const string Sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
  static readonly string[] Owners = { Sys, Adm, Me };
  static readonly string[] Names = { "runner-windows.ps1", "nova-ci-rules.ps1" };
  static readonly string[] Shas = { Sha, Sha };
  static int fails;

  static void Want(string name, string got, string want) {
    bool ok = want == null ? got == null : (got != null && got.StartsWith(want, StringComparison.Ordinal));
    if (!ok) {
      Console.WriteLine("FAIL " + name + ": got " + (got ?? "null") + ", want " + (want ?? "null"));
      fails++;
    }
  }
  static StageAce Ace(string sid, long rights, bool inherited, bool inheritOnly) {
    StageAce a = new StageAce();
    a.Sid = sid;
    a.Allow = true;
    a.Rights = rights;
    a.Inherited = inherited;
    a.InheritOnly = inheritOnly;
    return a;
  }
  static StageFacts Facts(bool folder, string owner, bool prot, params StageAce[] aces) {
    StageFacts f = new StageFacts();
    f.Folder = folder;
    f.Owner = owner;
    f.Protected = prot;
    f.Aces = aces;
    return f;
  }
  // a drive as Windows ships it (Authenticated Users may make a folder, and modify what they make: inherit only),
  // the stage directory as the play makes it, and the two scripts staged in it
  static FakeDisk Good() {
    FakeDisk d = new FakeDisk();
    d.Facts["C:\\"] = Facts(true, TI, true, Ace(Adm, 0x1f01ff, false, false), Ace(Sys, 0x1f01ff, false, false),
      Ace(Users, 0x1200a9, false, false), Ace(AU, 0x4, false, false), Ace(AU, 0x1301bf, false, true));
    d.Facts[Stage] = Facts(true, Adm, true, Ace(Sys, 0x1f01ff, false, false), Ace(Adm, 0x1f01ff, false, false));
    foreach (string n in Names) {
      d.Facts[Stage + "\\" + n] = Facts(false, Me, false, Ace(Sys, 0x1f01ff, true, false), Ace(Adm, 0x1f01ff, true, false));
      d.Shas[Stage + "\\" + n] = Sha;
    }
    return d;
  }
  static string Check(FakeDisk d) { return NovaStage.Check(d, Stage, Owners, Names, Shas); }

  static StageFacts Drive(params StageAce[] aces) {
    return Facts(true, Adm, false, aces);
  }
  static string Opened(long rights, string sid) {
    FakeDisk d = Good();
    d.Facts["C:\\"].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, false, false), Ace(sid, rights, false, false) };
    return Check(d);
  }
  static void Above() {
    Want("good", Check(Good()), null);
    // captain's C:\ (icacls, 2026-11-12): Authenticated Users Modify on the drive itself, owner Administrators
    FakeDisk d = Good();
    d.Facts["C:\\"] = Drive(Ace("S-1-15-3-65536-1", 0x1200a9, true, false), Ace(Adm, 0x1f01ff, true, false), Ace(Adm, 0x1f01ff, true, true),
      Ace(Sys, 0x1f01ff, true, false), Ace(AU, 0x1301bf, true, false), Ace(AU, 0x1301bf, true, true), Ace(Users, 0x1200a9, true, false));
    Want("captain's drive: files beside the stage are no threat", Check(d), null);
    Want("a file added by Users", Opened(0x2, Users), null);
    Want("written by LOCAL (GENERIC_WRITE)", Opened(0x40000000, "S-1-2-0"), null);
    Want("a delete in it by Authenticated Users", Opened(0x40, AU), "STAGE-OPEN C:\\ sid=" + AU);
    Want("DELETE on the drive's root takes nothing", Opened(0x10000, Users), null);
    Want("permissions changed by SERVICE", Opened(0x40000, "S-1-5-6"), "STAGE-OPEN C:\\ sid=S-1-5-6");
    Want("owner changed by LOCAL", Opened(0x80000, "S-1-2-0"), "STAGE-OPEN C:\\ sid=S-1-2-0");
    Want("all Everyone's", Opened(0x10000000, "S-1-1-0"), "STAGE-OPEN C:\\ sid=S-1-1-0");
    Want("This Organization, not named anywhere", Opened(0x40, "S-1-5-15"), "STAGE-OPEN C:\\ sid=S-1-5-15");
    Want("a runner service's own SID", Opened(0x10000000, "S-1-5-80-1-2-3-4-5"), "STAGE-OPEN C:\\ sid=S-1-5-80-1-2-3-4-5");
    Want("TrustedInstaller", Opened(0x1f01ff, TI), null);
    Want("CREATOR OWNER and OWNER RIGHTS stand for the owner", Opened(0x1f01ff, "S-1-3-0") ?? Opened(0x1f01ff, "S-1-3-4"), null);
    d = Good();
    d.Facts["C:\\"].Owner = NS;
    Want("owned by NETWORK SERVICE", Check(d), "STAGE-OPEN C:\\ owner=" + NS);
    d = Good();
    d.Facts["C:\\"].Owner = Me;
    Want("owned by a user", Check(d), "STAGE-OPEN C:\\ owner=" + Me);
    d = Good();
    d.Facts["C:\\"].Owner = Sys;
    Want("owned by SYSTEM", Check(d), null);
    d = Good();
    d.Facts["C:\\"].Aces = new StageAce[] { Ace(Users, 0x40, false, false) };
    d.Facts["C:\\"].Aces[0].Allow = false;
    Want("a deny entry gives nothing", Check(d), null);
    d = Good();
    d.Facts["C:\\"].Aces = new StageAce[] { Ace(Users, 0x1f01ff, false, true) };
    Want("inherit-only: not on the drive itself", Check(d), null);
    d = Good();
    d.Facts["C:\\"].Error = "UnauthorizedAccessException";
    Want("unreadable", Check(d), "STAGE-OPEN C:\\ unreadable=UnauthorizedAccessException");
    // a stage two levels down: every folder up to the drive is read, not only the parent
    d = Good();
    string deep = "C:\\sealed\\nova-stage";
    d.Facts["C:\\sealed"] = Facts(true, Adm, true, Ace(Sys, 0x1f01ff, false, false), Ace(Adm, 0x1f01ff, false, false));
    d.Facts[deep] = d.Facts[Stage];
    Want("deeper, sealed above", NovaStage.Check(d, deep, Owners, new string[0], new string[0]), null);
    d.Facts["C:\\sealed"].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, false, false), Ace(Users, 0x10000, false, false) };
    Want("deeper, the folder above renamed by Users (DELETE)", NovaStage.Check(d, deep, Owners, new string[0], new string[0]), "STAGE-OPEN C:\\sealed sid=" + Users);
    d.Facts["C:\\sealed"].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, false, false) };
    d.Facts["C:\\"].Aces = new StageAce[] { Ace(Users, 0x40, false, false) };
    Want("deeper, the drive open", NovaStage.Check(d, deep, Owners, new string[0], new string[0]), "STAGE-OPEN C:\\ sid=" + Users);
    d.Facts.Remove("C:\\sealed");
    Want("deeper, a folder above missing", NovaStage.Check(d, deep, Owners, new string[0], new string[0]), "STAGE-OPEN C:\\sealed unreadable=missing");
  }

  static void Root() {
    FakeDisk d = Good();
    d.Facts.Remove(Stage + "\\runner-windows.ps1");
    d.Facts.Remove(Stage + "\\nova-ci-rules.ps1");
    d.Facts.Remove(Stage);
    Want("missing", Check(d), "STAGE-MISSING " + Stage);
    d = Good();
    d.Facts[Stage].Owner = NS;
    Want("made by NETWORK SERVICE", Check(d), "STAGE-PLANTED " + Stage + " owner=" + NS);
    d = Good();
    d.Facts[Stage].Owner = Me;
    Want("owned by a user, not Administrators", Check(d), "STAGE-PLANTED " + Stage + " owner=" + Me);
    d = Good();
    d.Facts[Stage].Protected = false;
    Want("inheriting", Check(d), "STAGE-PLANTED " + Stage + " inherited=yes");
    d = Good();
    d.Facts[Stage].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, false, false), Ace(Users, 0x1200a9, false, false) };
    Want("another account in its list", Check(d), "STAGE-PLANTED " + Stage + " ace=" + Users);
    d = Good();
    d.Facts[Stage].Reparse = true;
    Want("a link", Check(d), "STAGE-PLANTED " + Stage + " folder=no");
    d = Good();
    d.Facts[Stage].Folder = false;
    Want("a file", Check(d), "STAGE-PLANTED " + Stage + " folder=no");
    d = Good();
    d.Facts[Stage].Error = "UnauthorizedAccessException";
    Want("unreadable", Check(d), "STAGE-PLANTED " + Stage + " unreadable=");
  }

  static void Files() {
    string f = Stage + "\\runner-windows.ps1";
    FakeDisk d = Good();
    d.Facts[f].Owner = NS;
    Want("a script owned by NETWORK SERVICE", Check(d), "STAGE-PLANTED " + f + " owner=" + NS);
    d = Good();
    d.Facts[f].Owner = Adm;
    Want("a script owned by Administrators", Check(d), null);
    d = Good();
    d.Facts[f].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, true, false), Ace(Users, 0x2, false, false) };
    Want("a script with its own entry", Check(d), "STAGE-PLANTED " + f + " ace=" + Users);
    d = Good();
    d.Facts[f].Aces = new StageAce[] { Ace(Sys, 0x1f01ff, true, false), Ace(Users, 0x1301bf, true, false) };
    Want("a script moved in, its old inherited entries kept", Check(d), "STAGE-PLANTED " + f + " ace=" + Users);
    d = Good();
    d.Facts[f].Reparse = true;
    Want("a script that is a link", Check(d), "STAGE-PLANTED " + f + " file=no");
    d = Good();
    d.Facts[Stage + "\\sub"] = Facts(true, Adm, false, Ace(Sys, 0x1f01ff, true, false));
    Want("a folder in it", Check(d), "STAGE-PLANTED " + Stage + "\\sub file=no");
    d = Good();
    d.Facts[Stage + "\\extra.ps1"] = Facts(false, Adm, false, Ace(Sys, 0x1f01ff, true, false));
    Want("another sealed file in it", Check(d), null);
    d = Good();
    d.Facts[Stage + "\\x"] = Facts(false, NS, false);
    Want("another file planted in it", Check(d), "STAGE-PLANTED " + Stage + "\\x owner=" + NS);
    d = Good();
    d.Facts[f].Error = "IOException";
    Want("a script unreadable", Check(d), "STAGE-PLANTED " + f + " unreadable=");
    d = Good();
    d.Unlistable.Add(Stage);
    Want("unlistable", Check(d), "STAGE-PLANTED " + Stage + " unreadable=");
    d = Good();
    d.Facts.Remove(Stage + "\\nova-ci-rules.ps1");
    Want("a script missing", Check(d), "STAGE-CHANGED " + Stage + "\\nova-ci-rules.ps1 missing");
    d = Good();
    d.Shas[f] = "ff" + Sha.Substring(2);
    Want("a script changed", Check(d), "STAGE-CHANGED " + f + " sha256=ff");
    d = Good();
    d.Shas[f] = Sha.ToUpperInvariant();
    Want("a hash in capitals", Check(d), null);
    Want("no names: the directory only", NovaStage.Check(Good(), Stage, Owners, new string[0], new string[0]), null);
  }

  static int Main() {
    Above();
    Root();
    Files();
    Console.WriteLine(fails == 0 ? "ok" : fails + " failed");
    return fails;
  }
}
