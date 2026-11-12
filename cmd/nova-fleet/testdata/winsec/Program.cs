// Program.cs: the admin runner's security decisions (NovaTree in runner-windows.ps1, NovaCiAdmin in nova-ci-rules.ps1)
// run on fake ACLs. TestFunctionalWindowsSecurityDecisions compiles the C# out of both scripts beside this file at C# 5
// and runs it with one argument, an empty directory for the zip cases; every failed case prints a FAIL line, and the
// exit code is the number of them.
using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Compression;

// FakeTree: paths with '\' separators; a folder's children are the paths one level below it; unlistable names folders
// whose Children throws.
sealed class FakeTree : INovaTree {
  public readonly Dictionary<string, NovaFacts> Facts = new Dictionary<string, NovaFacts>(StringComparer.OrdinalIgnoreCase);
  public readonly HashSet<string> Unlistable = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
  public NovaFacts Read(string p) {
    NovaFacts f;
    if (Facts.TryGetValue(p, out f)) { return f; }
    NovaFacts m = new NovaFacts();
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
}

static class Program {
  const string Sys = "S-1-5-18", Adm = "S-1-5-32-544", NS = "S-1-5-20", AU = "S-1-5-11", Users = "S-1-5-32-545", Acct = "S-1-5-21-1-2-3-1001";
  static readonly string[] Sealers = { Sys, Adm };
  static readonly string[] Openers = { NS, "S-1-5-6", "S-1-2-0", AU, Users, "S-1-1-0" };
  static int fails;

  static void Want(string name, string got, string want) {
    bool ok = want == null ? got == null : (got != null && got.Contains(want));
    if (!ok) {
      Console.WriteLine("FAIL " + name + ": got " + (got ?? "null") + ", want " + (want ?? "null"));
      fails++;
    }
  }
  static NovaAce Ace(string sid, long rights, bool inherited, bool inheritOnly, bool toFiles) {
    NovaAce a = new NovaAce();
    a.Sid = sid;
    a.Allow = true;
    a.Rights = rights;
    a.Inherited = inherited;
    a.InheritOnly = inheritOnly;
    a.ToFiles = toFiles;
    return a;
  }
  static NovaFacts Folder(string owner, bool prot, params NovaAce[] aces) {
    NovaFacts f = new NovaFacts();
    f.Folder = true;
    f.Owner = owner;
    f.Protected = prot;
    f.Aces = aces;
    return f;
  }
  static NovaFacts FileFacts(string owner, params NovaAce[] aces) {
    NovaFacts f = Folder(owner, false, aces);
    f.Folder = false;
    return f;
  }
  static NovaFacts Unreadable(bool folder, bool systemHidden) {
    NovaFacts f = new NovaFacts();
    f.Folder = folder;
    f.SystemHidden = systemHidden;
    f.Error = "UnauthorizedAccessException";
    return f;
  }
  // a sealed folder below a sealed root: its ACEs inherited, owned by Administrators
  static NovaFacts Inside(bool folder) {
    NovaFacts f = Folder(Adm, false, Ace(Sys, 0x1f01ff, true, false, true), Ace(Adm, 0x1f01ff, true, false, true));
    f.Folder = folder;
    return f;
  }
  // C:\ as Windows ships it: Administrators and SYSTEM full; Users read; Authenticated Users make folders (AD) here and
  // modify what they make (inherit only); hidden system files the page file and DumpStack.log.tmp.
  static FakeTree Drive() {
    FakeTree t = new FakeTree();
    t.Facts["C:\\"] = Folder("S-1-5-80-956008885", true, Ace(Adm, 0x1f01ff, false, false, true), Ace(Sys, 0x1f01ff, false, false, true),
      Ace(Users, 0x1200a9, false, false, true), Ace(AU, 0x4, false, false, false), Ace(AU, 0x1301bf, false, true, true));
    t.Facts["C:\\Windows"] = Folder("S-1-5-80-956008885", true, Ace(Adm, 0x1f01ff, false, false, true));
    t.Facts["C:\\pagefile.sys"] = Unreadable(false, true);
    NovaFacts dump = FileFacts(Sys, Ace(AU, 0x1301bf, true, false, false));
    dump.SystemHidden = true;
    t.Facts["C:\\DumpStack.log.tmp"] = dump;
    t.Facts["C:\\System Volume Information"] = Unreadable(true, true);
    t.Facts["C:\\nova-admin-runners"] = Folder(Adm, true, Ace(Sys, 0x1f01ff, false, false, true), Ace(Adm, 0x1f01ff, false, false, true));
    return t;
  }
  // a sealed admin runner directory with a bin folder and a file in it
  static FakeTree Runner() {
    FakeTree t = Drive();
    t.Facts["C:\\nova-admin-runners\\r"] = Folder(Adm, true, Ace(Sys, 0x1f01ff, false, false, true), Ace(Adm, 0x1f01ff, false, false, true), Ace(Acct, 0x1f01ff, false, false, true));
    t.Facts["C:\\nova-admin-runners\\r\\bin"] = Inside(true);
    t.Facts["C:\\nova-admin-runners\\r\\bin\\a.dll"] = Inside(false);
    return t;
  }

  static void Trees() {
    string[] runner = { Sys, Adm, Acct };
    string dir = "C:\\nova-admin-runners\\r";
    Want("Open sealed", NovaTree.Open(Runner(), dir, runner, true), null);
    FakeTree t = Runner();
    t.Facts[dir + "\\bin\\a.dll"].Aces = new NovaAce[] { Ace(NS, 0x2, false, false, false) };
    Want("Open deep finds an explicit ACE below", NovaTree.Open(t, dir, runner, true), "a.dll ace=" + NS);
    Want("Open shallow reads the top only", NovaTree.Open(t, dir, runner, false), null);
    t = Runner();
    t.Facts[dir + "\\bin\\a.dll"].Owner = NS;
    Want("Open deep finds an owner below", NovaTree.Open(t, dir, runner, true), "owner=" + NS);
    t = Runner();
    t.Facts[dir].Protected = false;
    Want("Open top inherits", NovaTree.Open(t, dir, runner, false), "inherited=yes");
    t = Runner();
    t.Facts[dir].Aces = new NovaAce[] { Ace(Users, 0x1200a9, false, false, true) };
    Want("Open top grants another", NovaTree.Open(t, dir, runner, false), "ace=" + Users);
    t = Runner();
    t.Facts[dir + "\\bin"].Reparse = true;
    t.Facts[dir + "\\bin\\a.dll"].Owner = NS;
    Want("Open does not follow a reparse point", NovaTree.Open(t, dir, runner, true), null);
    t = Runner();
    t.Unlistable.Add(dir + "\\bin");
    Want("Open refuses a folder it cannot list", NovaTree.Open(t, dir, runner, true), "bin unreadable=");
    Want("Open refuses a missing top", NovaTree.Open(t, "C:\\nope", runner, true), "missing=yes");

    // Writer: the here flag (an inherit-only ACE counts only for what files made there inherit) and the rights masks
    NovaFacts io = Folder(Adm, false, Ace(Users, 0x2, false, true, true));
    Want("Writer inherit-only to files, files", NovaTree.Writer(io, Openers, NovaTree.AnyWrite, true), "sid=" + Users);
    Want("Writer inherit-only to files, not files", NovaTree.Writer(io, Openers, NovaTree.AnyWrite, false), null);
    NovaFacts sub = Folder(Adm, false, Ace(Users, 0x2, false, true, false));
    Want("Writer inherit-only to folders, files", NovaTree.Writer(sub, Openers, NovaTree.AnyWrite, true), null);
    NovaFacts ad = Folder(Adm, false, Ace(AU, 0x4, false, false, false));
    Want("Writer make-folder is no file write", NovaTree.Writer(ad, Openers, NovaTree.FileWrite, false), null);
    Want("Writer make-folder is a write", NovaTree.Writer(ad, Openers, NovaTree.AnyWrite, false), "sid=" + AU);
    NovaFacts deny = Folder(Adm, false, Ace(Users, 0x2, false, false, false));
    deny.Aces[0].Allow = false;
    Want("Writer a deny ACE", NovaTree.Writer(deny, Openers, NovaTree.AnyWrite, false), null);
    Want("Writer another SID", NovaTree.Writer(Folder(Adm, false, Ace(Acct, 0x2, false, false, false)), Openers, NovaTree.AnyWrite, false), null);
    Want("Writer an opener owns it", NovaTree.Writer(Folder("S-1-5-6", false), Openers, NovaTree.AnyWrite, false), "owner=S-1-5-6");
    Want("Writer LOCAL", NovaTree.Writer(Folder(Adm, false, Ace("S-1-2-0", 0x40000000, false, false, false)), Openers, NovaTree.AnyWrite, false), "sid=S-1-2-0");
  }

  static FakeTree PathTree() {
    FakeTree t = Drive();
    t.Facts["C:\\Go"] = Folder(Adm, true, Ace(Adm, 0x1f01ff, false, false, true), Ace(AU, 0x1200a9, false, false, true));
    t.Facts["C:\\Go\\bin"] = Folder(Adm, false, Ace(Adm, 0x1f01ff, true, false, true), Ace(AU, 0x1200a9, true, false, true));
    t.Facts["C:\\Go\\bin\\go.exe"] = FileFacts(Adm, Ace(Adm, 0x1f01ff, true, false, false), Ace(AU, 0x1200a9, true, false, false));
    return t;
  }

  static void Paths() {
    string[] go = { "C:\\Go\\bin" };
    Want("OpenPath closed", NovaTree.OpenPath(PathTree(), go, Openers), null);
    FakeTree t = PathTree();
    t.Facts["C:\\Go\\bin"].Aces = new NovaAce[] { Ace(NS, 0x2, false, false, false) };
    Want("OpenPath folder writable", NovaTree.OpenPath(t, go, Openers), "sid=" + NS + " at=C:\\Go\\bin");
    t = PathTree();
    t.Facts["C:\\Go\\bin\\go.exe"].Owner = NS;
    Want("OpenPath file owned", NovaTree.OpenPath(t, go, Openers), "file=C:\\Go\\bin\\go.exe owner=" + NS);
    t = PathTree();
    t.Facts["C:\\Go\\bin\\go.exe"].Aces = new NovaAce[] { Ace(Users, 0x1301bf, true, false, false) };
    Want("OpenPath file writable", NovaTree.OpenPath(t, go, Openers), "go.exe sid=" + Users);
    t = PathTree();
    t.Facts["C:\\Go\\bin\\x.dll"] = Unreadable(false, false);
    Want("OpenPath file unreadable", NovaTree.OpenPath(t, go, Openers), "x.dll unreadable=");
    t = PathTree();
    t.Facts["C:\\Go\\bin\\pagefile.sys"] = Unreadable(false, true);
    Want("OpenPath hidden system file unreadable", NovaTree.OpenPath(t, go, Openers), null);
    t = PathTree();
    t.Facts["C:\\Go"].Owner = NS;
    Want("OpenPath parent owned", NovaTree.OpenPath(t, go, Openers), "owner=" + NS + " at=C:\\Go");
    t = PathTree();
    t.Facts["C:\\Go"].Aces = new NovaAce[] { Ace(Users, 0x40, false, false, true) };
    Want("OpenPath parent delete-child", NovaTree.OpenPath(t, go, Openers), "sid=" + Users + " at=C:\\Go");
    t = PathTree();
    t.Facts["C:\\ProgramData"] = Folder(Sys, true, Ace(Users, 0x116, false, false, false), Ace(Adm, 0x1f01ff, false, false, true));
    Want("OpenPath missing, nearest above writable", NovaTree.OpenPath(t, new string[] { "C:\\ProgramData\\nova\\ccache\\msvc" }, Openers),
      "msvc sid=" + Users + " at=C:\\ProgramData");
    Want("OpenPath missing under the drive", NovaTree.OpenPath(PathTree(), new string[] { "C:\\tools" }, Openers), "C:\\tools sid=" + AU + " at=C:\\");
  }

  static void Aboves() {
    string root = "C:\\nova-admin-runners";
    Want("Above the drive as shipped", NovaTree.Above(Drive(), root, Openers), null);
    FakeTree t = Drive();
    t.Facts["C:\\"].Aces = new NovaAce[] { Ace(Users, 0x2, false, false, false) };
    Want("Above the drive takes files", NovaTree.Above(t, root, Openers), "C:\\ sid=" + Users);
    t = Drive();
    t.Facts["C:\\"].Aces = new NovaAce[] { Ace(Users, 0x40, false, false, false) };
    Want("Above the drive deletes in it", NovaTree.Above(t, root, Openers), "C:\\ sid=" + Users);
    t = Drive();
    t.Facts["C:\\.cargo"] = Folder(NS, false, Ace(AU, 0x1301bf, true, false, true));
    Want("Above a folder a job made", NovaTree.Above(t, root, Openers), "C:\\.cargo owner=" + NS);
    t = Drive();
    t.Facts["C:\\Directory.Build.targets"] = FileFacts(Adm, Ace(AU, 0x1301bf, true, false, false));
    Want("Above a file a job may change", NovaTree.Above(t, root, Openers), "Directory.Build.targets sid=" + AU);
    t = Drive();
    t.Facts["C:\\hid"] = Unreadable(true, false);
    Want("Above a folder whose owner cannot be read", NovaTree.Above(t, root, Openers), "C:\\hid unreadable=");
    t = Drive();
    t.Unlistable.Add("C:\\");
    Want("Above a drive it cannot list", NovaTree.Above(t, root, Openers), "C:\\ unreadable=");
  }

  static void Zip(string path, params string[] names) {
    using (ZipArchive z = ZipFile.Open(path, ZipArchiveMode.Create)) {
      foreach (string n in names) {
        ZipArchiveEntry e = z.CreateEntry(n);
        if (n.EndsWith("/")) { continue; }
        using (StreamWriter w = new StreamWriter(e.Open())) { w.Write("content of " + n); }
      }
    }
  }

  static void Zips(string tmp) {
    string zip = System.IO.Path.Combine(tmp, "runner.zip");
    string dir = System.IO.Path.Combine(tmp, "r");
    Zip(zip, "bin/", "bin/a.dll", "externals/x/y.txt", "config.cmd");
    NovaTree.Extract(zip, dir);
    string[] closed = { "bin", "externals" };
    Want("Verify fresh", NovaTree.Verify(zip, dir, closed), null);
    System.IO.File.WriteAllText(System.IO.Path.Combine(dir, "_work.txt"), "a job's file outside bin and externals");
    Want("Verify a file outside the closed folders", NovaTree.Verify(zip, dir, closed), null);
    string extra = System.IO.Path.Combine(dir, "bin", "planted.dll");
    System.IO.File.WriteAllText(extra, "x");
    Want("Verify extra", NovaTree.Verify(zip, dir, closed), "planted.dll extra");
    System.IO.File.Delete(extra);
    string a = System.IO.Path.Combine(dir, "bin", "a.dll");
    System.IO.File.WriteAllText(a, "changed");
    Want("Verify changed", NovaTree.Verify(zip, dir, closed), "a.dll changed");
    System.IO.File.Delete(a);
    Want("Verify missing", NovaTree.Verify(zip, dir, closed), "a.dll missing");

    string bad = System.IO.Path.Combine(tmp, "bad.zip");
    Zip(bad, "ok.txt", "../outside.txt");
    string got = null;
    try {
      NovaTree.Extract(bad, System.IO.Path.Combine(tmp, "b"));
    } catch (IOException e) {
      got = e.Message;
    }
    Want("Extract refuses an entry outside", got, "outside the directory: ../outside.txt");
    Want("Extract wrote nothing outside", System.IO.File.Exists(System.IO.Path.Combine(tmp, "outside.txt")) ? "written" : null, null);
  }

  static void Hook() {
    string want = "mas-bandwidth/rocketnet/.github/workflows/appverif.yml@refs/heads/main";
    Want("Refuse the workflow, push to main", NovaCiAdmin.Refuse("push", "refs/heads/main", want, want), null);
    Want("Refuse schedule", NovaCiAdmin.Refuse("schedule", "refs/heads/main", want, want), null);
    Want("Refuse workflow_dispatch", NovaCiAdmin.Refuse("workflow_dispatch", "refs/heads/main", want, want), null);
    Want("Refuse owner case", NovaCiAdmin.Refuse("push", "refs/heads/main", "Mas-Bandwidth/RocketNet/.github/workflows/appverif.yml@refs/heads/main", want), null);
    Want("Refuse pull_request", NovaCiAdmin.Refuse("pull_request", "refs/heads/main", want, want), "not pull_request");
    Want("Refuse pull_request_target", NovaCiAdmin.Refuse("pull_request_target", "refs/heads/main", want, want), "not pull_request_target");
    Want("Refuse another branch", NovaCiAdmin.Refuse("push", "refs/heads/main2", want, want), "not refs/heads/main2");
    Want("Refuse a tag", NovaCiAdmin.Refuse("push", "refs/tags/v1", want, want), "not refs/tags/v1");
    Want("Refuse another workflow", NovaCiAdmin.Refuse("push", "refs/heads/main", "mas-bandwidth/rocketnet/.github/workflows/ci.yml@refs/heads/main", want), "not mas-bandwidth/rocketnet/.github/workflows/ci.yml");
    Want("Refuse the workflow at another ref", NovaCiAdmin.Refuse("workflow_dispatch", "refs/heads/main", "mas-bandwidth/rocketnet/.github/workflows/appverif.yml@refs/heads/dev", want), "not ");
    Want("Refuse another repository", NovaCiAdmin.Refuse("push", "refs/heads/main", "mas-bandwidth/other/.github/workflows/appverif.yml@refs/heads/main", want), "not ");
    Want("Refuse path case", NovaCiAdmin.Refuse("push", "refs/heads/main", "mas-bandwidth/rocketnet/.github/workflows/AppVerif.yml@refs/heads/main", want), "not ");
    Want("Refuse no workflow ref", NovaCiAdmin.Refuse("push", "refs/heads/main", "", want), "not ");
    Want("Refuse no allowed workflow", NovaCiAdmin.Refuse("push", "refs/heads/main", want, ""), "no allowed workflow");
    string[] paths = { "C:\\Windows", "C:\\System Volume Information", "C:\\.cargo" };
    Want("Planted none", NovaCiAdmin.Planted(new string[] { paths[0], paths[1] }, new string[] { "S-1-5-80-1", "" }, new bool[] { false, true }), null);
    Want("Planted by a job", NovaCiAdmin.Planted(paths, new string[] { "S-1-5-80-1", "", NS }, new bool[] { false, true, false }), "C:\\.cargo owner=" + NS);
    Want("Planted unreadable", NovaCiAdmin.Planted(new string[] { "C:\\x" }, new string[] { "" }, new bool[] { false }), "C:\\x owner=unreadable");
  }

  static int Main(string[] args) {
    Trees();
    Paths();
    Aboves();
    Zips(args[0]);
    Hook();
    Console.WriteLine(fails == 0 ? "ok" : fails + " failed");
    return fails;
  }
}
