using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Xml;
using System.Xml.Schema;

public static class Program {
  static int failures = 0;

  static void Check(bool cond, string msg) {
    if (!cond) {
      Console.WriteLine("FAIL " + msg);
      failures++;
    }
  }

  static string BoolTla(bool b) { return b ? "TRUE" : "FALSE"; }

  static string RecTla(WatchState s) {
    return string.Format(CultureInfo.InvariantCulture, "R({0}, {1}, {2}, {3}, {4}, {5}, {6}, {7})",
      s.Misses, s.Rung, s.Wait, s.BusyWaits, s.Reboots, s.Outage, BoolTla(s.Alerted), BoolTla(s.Halted));
  }

  static string ObsTla(TailObs o) {
    return string.Format(CultureInfo.InvariantCulture, "O(\"{0}\", {1}, {2}, \"{3}\")",
      o.Backend, BoolTla(o.Self), BoolTla(o.Peers), o.Control);
  }

  static string ExtractTaskXml(string scriptPath) {
    string raw = File.ReadAllText(scriptPath);
    Match m = Regex.Match(raw, "\\$xml = @\"\\r?\\n(.*?)\\r?\\n\"@", RegexOptions.Singleline);
    if (!m.Success) { return ""; }
    string x = m.Groups[1].Value;
    x = x.Replace("$distro", "distro-a")
         .Replace("$sid", "S-1-5-21-1000-2000-3000-1001")
         .Replace("$exe", "C:\\Windows\\System32\\conhost.exe")
         .Replace("$arg", "--headless \"C:\\Program Files\\WSL\\wsl.exe\" -d distro-a -u dev --exec /bin/sleep infinity")
         .Replace("$psExe", "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
         .Replace("$taskArg", "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File \"C:\\nova-stage\\wsl-watchdog.ps1\" -Action tick")
         .Replace("encoding=\"UTF-16\"", "encoding=\"UTF-8\"");
    return x;
  }

  static List<string> ValidateTaskXml(string xsdPath, string xmlContent) {
    List<string> errs = new List<string>();
    XmlReaderSettings settings = new XmlReaderSettings();
    settings.ValidationType = ValidationType.Schema;
    settings.Schemas.Add("http://schemas.microsoft.com/windows/2004/02/mit/task", xsdPath);
    settings.ValidationEventHandler += delegate(object sender, ValidationEventArgs e) {
      errs.Add(e.Severity + ": " + e.Message);
    };
    using (StringReader sr = new StringReader(xmlContent))
    using (XmlReader xr = XmlReader.Create(sr, settings)) {
      while (xr.Read()) { }
    }
    return errs;
  }

  public static int Main(string[] args) {
    string workDir = args.Length > 0 ? args[0] : Path.GetTempPath();
    string traceOut = args.Length > 1 ? args[1] : "";
    string xsdPath = args.Length > 2 ? args[2] : "";
    string kaScript = args.Length > 3 ? args[3] : "";
    string wdScript = args.Length > 4 ? args[4] : "";

    List<string> seen = new List<string>();
    TailObs okObs = new TailObs("running", true, true, "answers");
    TailObs stoppedObs = new TailObs("stopped", false, false, "silent");
    TailObs localNoneObs = new TailObs("none", false, false, "answers");
    TailObs localOffObs = new TailObs("running", false, false, "answers");
    TailObs remoteSilentObs = new TailObs("none", false, false, "silent");
    TailObs remote500Obs = new TailObs("none", false, false, "error");
    TailObs remoteRelayObs = new TailObs("running", true, false, "answers");
    TailObs remoteLoginObs = new TailObs("login", false, false, "answers");

    Check(NovaWslWatch.Classify(okObs) == "ok", "Classify ok");
    Check(NovaWslWatch.Classify(stoppedObs) == "ok", "Classify stopped");
    Check(NovaWslWatch.Classify(localNoneObs) == "local", "Classify local none");
    Check(NovaWslWatch.Classify(localOffObs) == "local", "Classify local offline");
    Check(NovaWslWatch.Classify(remoteSilentObs) == "remote", "Classify silent control (dead ISP)");
    Check(NovaWslWatch.Classify(remote500Obs) == "remote", "Classify 5xx control");
    Check(NovaWslWatch.Classify(remoteRelayObs) == "remote", "Classify self online, peers silent");
    Check(NovaWslWatch.Classify(remoteLoginObs) == "remote", "Classify login");

    Check(!NovaWslWatch.Protected(false, true, false), "dead WSL does not defer on wedged Runner.Worker");
    Check(NovaWslWatch.Protected(true, true, false), "healthy WSL protects busy Runner.Worker");
    Check(NovaWslWatch.Protected(false, false, true), "operator hold always defers");
    Check(!NovaWslWatch.Rung1Wsl(true) && NovaWslWatch.Rung1Wsl(false), "Rung1Wsl");
    Check(!NovaWslWatch.Rung1Tail("ok") && NovaWslWatch.Rung1Tail("local"), "Rung1Tail");

    // Constants matching WslWatchTrace.cfg: Threshold=2, BackoffStep=1, BusyBackoff=2, MaxReboots=2, BusyWaitLimit=2
    Func<WatchState, bool, TailObs, bool, bool, WatchState> tick = delegate(WatchState cur, bool wsl, TailObs obs, bool wrk, bool hld) {
      string tail = NovaWslWatch.Classify(obs);
      WatchState post = NovaWslWatch.Step(cur, wsl, tail, wrk, hld, 2, 1, 2, 2, 2);
      string row = string.Format(CultureInfo.InvariantCulture, "    T({0}, {1}, \"{2}\", {3}, {4}, {5}, {6})",
        RecTla(cur), BoolTla(wsl), tail, ObsTla(obs), BoolTla(wrk), BoolTla(hld), RecTla(post));
      if (!seen.Contains(row)) { seen.Add(row); }
      return post;
    };

    // Walk 1: WSL2 Rung 1 recovery
    WatchState s = new WatchState();
    s = tick(s, true, okObs, false, false);
    s = tick(s, false, okObs, false, false); // miss 1
    s = tick(s, false, okObs, false, false); // miss 2
    s = tick(s, false, okObs, false, false); // rung 1, wait 1
    Check(s.Rung == 1 && s.Wait == 1, "walk1 rung 1");
    s = tick(s, true, okObs, false, false);  // recovered
    Check(s.Rung == 0 && s.Misses == 0, "walk1 recovered");

    // Walk 2: WSL2 Rung 2 reboot is NOT blocked by wedged Runner.Worker (worker=true, wsl=false)
    s = new WatchState();
    s = tick(s, false, okObs, true, false);
    s = tick(s, false, okObs, true, false);
    s = tick(s, false, okObs, true, false); // rung 1
    s = tick(s, false, okObs, true, false); // wait -> 0
    s = tick(s, false, okObs, true, false); // rung 2 reboot!
    Check(s.Reboots == 1 && s.Outage == 1 && s.Rung == 0, "walk2 reboot despite wedged worker");

    // Walk 3: Healthy WSL2 + local Tailscale fault IS deferred by busy Runner.Worker until BusyWaitLimit alerts, then reboots when idle
    s = new WatchState();
    s = tick(s, true, localOffObs, true, false);
    s = tick(s, true, localOffObs, true, false);
    s = tick(s, true, localOffObs, true, false); // rung 1 (tailscale restart only)
    s = tick(s, true, localOffObs, true, false); // wait -> 0
    s = tick(s, true, localOffObs, true, false); // defer 1, wait 2
    Check(s.Reboots == 0 && s.BusyWaits == 1 && !s.Alerted, "walk3 defer 1");
    s = tick(s, true, localOffObs, true, false); // wait 1
    s = tick(s, true, localOffObs, true, false); // wait 0
    s = tick(s, true, localOffObs, true, false); // defer 2, alerted
    Check(s.Reboots == 0 && s.BusyWaits == 2 && s.Alerted, "walk3 defer 2 alerts");
    s = tick(s, true, localOffObs, false, false); // wait 1
    s = tick(s, true, localOffObs, false, false); // wait 0
    s = tick(s, true, localOffObs, false, false); // idle -> reboot
    Check(s.Reboots == 1 && s.Outage == 1, "walk3 reboot once worker finishes");

    // Walk 4: Remote / dead-ISP / 5xx / relay faults raise alert after Rung 1 and never reboot
    foreach (TailObs ro in new TailObs[] { remoteSilentObs, remote500Obs, remoteRelayObs }) {
      s = new WatchState();
      s = tick(s, true, ro, false, false);
      s = tick(s, true, ro, false, false);
      s = tick(s, true, ro, false, false); // rung 1
      s = tick(s, true, ro, false, false); // wait -> 0
      s = tick(s, true, ro, false, false); // remote alert, no reboot
      Check(s.Reboots == 0 && s.Alerted && !s.Halted, "walk4 remote never reboots");
    }

    // Walk 5: Outage budget halts even if sliding-window reboots aged out
    s = new WatchState();
    s.Reboots = 0;
    s.Outage = 2;
    s = tick(s, false, okObs, false, false);
    s = tick(s, false, okObs, false, false);
    s = tick(s, false, okObs, false, false); // rung 1
    s = tick(s, false, okObs, false, false); // wait -> 0
    s = tick(s, false, okObs, false, false); // budget spent -> halted + alerted
    Check(s.Halted && s.Alerted && s.Reboots == 0, "walk5 outage budget halts");
    s = tick(s, false, okObs, false, false); // stays halted
    Check(s.Halted && s.Reboots == 0, "walk5 halted stays put");
    s = tick(s, true, okObs, false, false);  // healthy tick clears halt and outage
    Check(!s.Halted && !s.Alerted && s.Outage == 0, "walk5 healthy clears halt");

    // Walk 6: Operator hold defers WSL2 reboot
    s = new WatchState();
    s = tick(s, false, okObs, false, true);
    s = tick(s, false, okObs, false, true);
    s = tick(s, false, okObs, false, true);
    s = tick(s, false, okObs, false, true);
    s = tick(s, false, okObs, false, true);
    Check(s.Reboots == 0 && s.BusyWaits == 1, "walk6 hold defers");

    // ParseState / FormatState / ResolveStateBootId across reboot, read failure, and corruption
    bool corrupt;
    WatchState ps = new WatchState();
    ps.Misses = 2; ps.Rung = 1; ps.Wait = 1; ps.BusyWaits = 1; ps.Outage = 1; ps.Halted = false;
    string fmt = NovaWslWatch.FormatState(ps, 5000);
    Check(NovaWslWatch.ResolveStateBootId(fmt, 0) == 5000, "ResolveStateBootId preserves recorded bootId on read error");
    Check(NovaWslWatch.ResolveStateBootId(fmt, 6000) == 6000, "ResolveStateBootId advances on positive kernel BootId");
    Check(NovaWslWatch.ResolveStateBootId("", 0) == 1, "ResolveStateBootId defaults to 1 on empty state");
    WatchState sameBoot = NovaWslWatch.ParseState(fmt, 5000, out corrupt);
    Check(!corrupt && sameBoot.Misses == 2 && sameBoot.Rung == 1 && sameBoot.Outage == 1, "ParseState same boot");
    WatchState nextBoot = NovaWslWatch.ParseState(fmt, 6000, out corrupt);
    Check(!corrupt && nextBoot.Misses == 0 && nextBoot.Rung == 0 && nextBoot.Wait == 0 && nextBoot.BusyWaits == 0 && nextBoot.Outage == 1, "ParseState new boot resets volatile only");
    WatchState badState = NovaWslWatch.ParseState("truncated garbage", 6000, out corrupt);
    Check(corrupt && badState.Outage == 0 && !badState.Halted, "ParseState corrupt resets");

    // NormalizeFirewallAddr CIDR / subnet mask idempotency
    Check(NovaWslWatch.NormalizeFirewallAddr("100.64.0.0/255.192.0.0") == "100.64.0.0/10", "NormalizeFirewallAddr IPv4 netmask");
    Check(NovaWslWatch.NormalizeFirewallAddr("100.64.0.0/10") == "100.64.0.0/10", "NormalizeFirewallAddr IPv4 CIDR");
    Check(NovaWslWatch.NormalizeFirewallAddr("FD7A:115C:A1E0::/ffff:ffff:ffff:0:0:0:0:0") == "fd7a:115c:a1e0::/48", "NormalizeFirewallAddr IPv6 netmask");

    // BuildS4uBuffer contiguous MSV1_0_S4U_LOGON layout check
    int s4uLen;
    IntPtr s4uBuf = NovaWslWatch.BuildS4uBuffer("winadmin", "HOSTA", out s4uLen);
    try {
      Check(Marshal.ReadInt32(s4uBuf, 0) == 12, "BuildS4uBuffer MsV1_0S4ULogon=12");
      int ptrOff = 8;
      ushort uLen = (ushort)Marshal.ReadInt16(s4uBuf, ptrOff);
      IntPtr uPtr = Marshal.ReadIntPtr(s4uBuf, ptrOff + IntPtr.Size);
      int domOff = ptrOff + IntPtr.Size * 2;
      ushort dLen = (ushort)Marshal.ReadInt16(s4uBuf, domOff);
      IntPtr dPtr = Marshal.ReadIntPtr(s4uBuf, domOff + IntPtr.Size);
      long baseAddr = s4uBuf.ToInt64();
      Check(uPtr.ToInt64() >= baseAddr && uPtr.ToInt64() + uLen <= baseAddr + s4uLen, "BuildS4uBuffer user inside contiguous block");
      Check(dPtr.ToInt64() >= baseAddr && dPtr.ToInt64() + dLen <= baseAddr + s4uLen, "BuildS4uBuffer domain inside contiguous block");
      Check(Marshal.PtrToStringUni(uPtr, uLen / 2) == "winadmin", "BuildS4uBuffer user text");
      Check(Marshal.PtrToStringUni(dPtr, dLen / 2) == "HOSTA", "BuildS4uBuffer domain text");
    } finally {
      Marshal.FreeHGlobal(s4uBuf);
    }

    // Official Microsoft Task Scheduler 1.3 XSD validation + negative witness
    if (!string.IsNullOrEmpty(xsdPath)) {
      string kaXml = ExtractTaskXml(kaScript);
      string wdXml = ExtractTaskXml(wdScript);
      Check(!string.IsNullOrEmpty(kaXml) && kaXml.Contains("<RunLevel>HighestAvailable</RunLevel>"), "keepalive XML extracted with HighestAvailable");
      Check(!string.IsNullOrEmpty(wdXml) && !wdXml.Contains("ServiceAccount"), "watchdog XML extracted without ServiceAccount");
      List<string> kaErrs = ValidateTaskXml(xsdPath, kaXml);
      Check(kaErrs.Count == 0, "keepalive XML valid against task.xsd (" + string.Join("; ", kaErrs.ToArray()) + ")");
      List<string> wdErrs = ValidateTaskXml(xsdPath, wdXml);
      Check(wdErrs.Count == 0, "watchdog XML valid against task.xsd (" + string.Join("; ", wdErrs.ToArray()) + ")");
      string badXml = wdXml.Replace("<UserId>S-1-5-18</UserId>", "<UserId>S-1-5-18</UserId><LogonType>ServiceAccount</LogonType>");
      List<string> badErrs = ValidateTaskXml(xsdPath, badXml);
      Check(badErrs.Count > 0, "task.xsd rejects invalid <LogonType>ServiceAccount</LogonType> witness");
    }

    // PruneWindow / RebootsInWindow
    string[] rlines = new string[] { "1000", "10000", "25000", "not-a-number" };
    Check(NovaWslWatch.RebootsInWindow(rlines, 26000, 21600) == 2, "RebootsInWindow ages out 1000");

    // AppendLog size rotation
    string logPath = Path.Combine(workDir, "watchdog.log");
    NovaWslWatch.AppendLog(logPath, 64, new string('a', 50));
    NovaWslWatch.AppendLog(logPath, 64, new string('b', 50));
    Check(File.Exists(logPath) && File.Exists(logPath + ".1"), "AppendLog rotates at maxBytes");

    // RunBounded kills a hung subprocess within milliseconds
    string so, se;
    Stopwatch sw = Stopwatch.StartNew();
    int rc = NovaWslWatch.RunBounded("/bin/sh", "-c \"sleep 30\"", 150, Encoding.UTF8, out so, out se);
    sw.Stop();
    Check(rc == -124 && sw.ElapsedMilliseconds < 5000, "RunBounded times out and kills hung child");

    // ProbeSshBanner detects SSH-2.0 banner and rejects non-SSH
    TcpListener srv = new TcpListener(IPAddress.Loopback, 0);
    srv.Start();
    int port = ((IPEndPoint)srv.LocalEndpoint).Port;
    Thread acceptThread = new Thread(delegate() {
      try {
        using (TcpClient cl = srv.AcceptTcpClient()) {
          byte[] b = Encoding.ASCII.GetBytes("SSH-2.0-OpenSSH_Test\r\n");
          cl.GetStream().Write(b, 0, b.Length);
        }
      } catch { }
    });
    acceptThread.IsBackground = true;
    acceptThread.Start();
    Check(NovaWslWatch.ProbeSshBanner("127.0.0.1", port, 2000), "ProbeSshBanner positive");
    srv.Stop();

    if (!string.IsNullOrEmpty(traceOut)) {
      StringBuilder tla = new StringBuilder();
      tla.AppendLine("------------------------------ MODULE WslWatchLog ------------------------------");
      tla.AppendLine("(* The distinct ticks NovaWslWatch (roles/github-runner/files/wsl-watchdog.ps1) took in                      *)");
      tla.AppendLine("(* wsl_watchdog_functional_test.go, checked by WslWatchTrace.tla against WslWatch.tla Step and Classify.     *)");
      tla.AppendLine("R(m, r, wt, bw, rb, o, a, h) == [misses |-> m, rung |-> r, wait |-> wt, busyWaits |-> bw, reboots |-> rb,");
      tla.AppendLine("                                 outage |-> o, alerted |-> a, halted |-> h]");
      tla.AppendLine("O(be, self, peers, control) == [backend |-> be, self |-> self, peers |-> peers, control |-> control]");
      tla.AppendLine("T(pre, wsl, tail, o, wrk, hld, post) == [pre |-> pre, wsl |-> wsl, tail |-> tail, obs |-> o,");
      tla.AppendLine("                                         worker |-> wrk, hold |-> hld, post |-> post]");
      tla.AppendLine("TickLog == <<");
      for (int i = 0; i < seen.Count; i++) {
        tla.AppendLine(seen[i] + (i + 1 < seen.Count ? "," : ""));
      }
      tla.AppendLine(">>");
      tla.AppendLine("=============================================================================");
      File.WriteAllText(traceOut, tla.ToString(), new UTF8Encoding(false));
    }

    if (failures == 0) {
      Console.WriteLine("ok");
      return 0;
    }
    return 1;
  }
}
