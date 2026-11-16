# wsl-watchdog.ps1: the Windows-side self-heal watchdog, unattended host settings, and out-of-band Windows OpenSSH for a
# WSL machine (tasks/wsl-watchdog.yml, the windows-runners play; fleet/README.md, "WSL self-heal watchdog and Windows
# OpenSSH"). Staged to C:\nova-stage (tasks/wsl-stage.yml) and proved before each elevated call.
#
#   install  -MachineId -User [-WatchdogDir] [-WinSshPort] [-WslSshPort] [-Threshold] [-Backoff] [-BusyBackoff]
#            [-BusyWaitLimit] [-MaxReboots] [-WindowSeconds]
#            stdin carries zero or more authorized SSH public key lines for the control node. Creates -WatchdogDir
#            (C:\nova-watchdog) sealed to SYSTEM and Administrators, grants SeBatchLogonRight on the WSL owner SID,
#            writes config.json, registers the NT AUTHORITY\SYSTEM scheduled task "WSL2 watchdog (<distro>)" repeating
#            every minute with ExecutionTimeLimit PT5M, disables AC standby and hibernate, sets CrashControl AutoReboot=1
#            and WER DontShowUI=1, sets Tailscale --unattended when Tailscale is installed, and configures Windows
#            OpenSSH sshd on -WinSshPort (2222) with key-only auth, host private keys and administrators_authorized_keys
#            sealed to SYSTEM and Administrators, and a Windows Firewall rule scoped to the tailnet
#            (100.64.0.0/10,fd7a:115c:a1e0::/48):
#            WATCHDOG <distro> ok|set task=<name> limit=PT5M dir=<dir>
#            UNATTENDED ok|set sleep=off crash_autoreboot=1 wer_noui=1 tailscale_unattended=yes|absent
#            WINSSH ok|set port=<port> auth=key-only keys=<n> firewall=tailscale service=running admin=<win-user>
#   tick     [-WatchdogDir] [-Dry]
#            one 1-minute tick of cmd/nova-fleet/tla/WslWatch.tla as NT AUTHORITY\SYSTEM: starts any stopped automatic
#            actions.runner.* or sshd service, probes WSL2 (keepalive Running + wsl.exe --exec /bin/true under the WSL
#            owners elevated or S4U token with user profile loaded + WSL sshd TCP banner) and Windows Tailscale with
#            millisecond timeouts, advances Step, and runs rung 1 (WSL2 / Tailscale restart) or rung 2 (rate-bounded
#            shutdown.exe /r /t 5 /f). Logs capped lines to <WatchdogDir>\watchdog.log.
param(
  [Parameter(Mandatory = $true)][ValidateSet('install', 'tick')][string]$Action,
  [string]$MachineId = '',
  [string]$User = '',
  [string]$WatchdogDir = 'C:\nova-watchdog',
  [int]$WinSshPort = 2222,
  [int]$WslSshPort = 22,
  [int]$Threshold = 2,
  [int]$Backoff = 2,
  [int]$BusyBackoff = 2,
  [int]$BusyWaitLimit = 2,
  [int]$MaxReboots = 2,
  [int]$WindowSeconds = 21600,
  [switch]$Dry
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Add-WatchdogApi {
  if ('NovaWslWatch' -as [type]) { return }
  Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;

// WatchState: the watch record w of cmd/nova-fleet/tla/WslWatch.tla.
public sealed class WatchState {
  public int Misses;
  public int Rung;
  public int Wait;
  public int BusyWaits;
  public int Reboots;
  public int Outage;
  public bool Alerted;
  public bool Halted;

  public WatchState Clone() {
    WatchState c = new WatchState();
    c.Misses = Misses;
    c.Rung = Rung;
    c.Wait = Wait;
    c.BusyWaits = BusyWaits;
    c.Reboots = Reboots;
    c.Outage = Outage;
    c.Alerted = Alerted;
    c.Halted = Halted;
    return c;
  }
}

// TailObs: what the watch observes of Windows Tailscale and the coordination server.
public sealed class TailObs {
  public string Backend;
  public bool Self;
  public bool Peers;
  public string Control;

  public TailObs(string backend, bool selfNode, bool peers, string control) {
    Backend = backend ?? "none";
    Self = selfNode;
    Peers = peers;
    Control = control ?? "silent";
  }
}

public static class NovaWslWatch {
  // Classify: turns TailObs into "ok", "local" or "remote", matching WslWatch.tla Classify.
  public static string Classify(TailObs o) {
    if (o.Backend == "stopped") { return "ok"; }
    if (o.Backend == "login") { return "remote"; }
    if (o.Backend == "running" && o.Peers) { return o.Self ? "ok" : "remote"; }
    if (o.Backend == "running" && o.Self) { return "remote"; }
    if (o.Control == "answers") { return "local"; }
    return "remote";
  }

  // Protected: an operator hold always defers the reboot; a Runner.Worker defers only while WSL2 is up (when WSL2 is
  // down, Runner.Worker on a WSL machine may be wedged waiting on dead WSL interop).
  public static bool Protected(bool wsl, bool worker, bool hold) {
    return hold || (wsl && worker);
  }

  public static bool Rung1Wsl(bool wsl) { return !wsl; }
  public static bool Rung1Tail(string tail) { return tail != "ok"; }

  // Step: one tick of WslWatch.tla Step.
  public static WatchState Step(WatchState s, bool wsl, string tail, bool worker, bool hold,
      int threshold, int backoffStep, int busyBackoff, int maxReboots, int busyWaitLimit) {
    WatchState n = s.Clone();
    bool prot = Protected(wsl, worker, hold);
    if (wsl && tail == "ok") {
      n.Misses = 0;
      n.Rung = 0;
      n.Wait = 0;
      n.BusyWaits = 0;
      n.Outage = 0;
      n.Alerted = false;
      n.Halted = false;
      return n;
    }
    if (s.Halted) { return n; }
    if (s.Misses < threshold) {
      n.Misses = s.Misses + 1;
      return n;
    }
    if (s.Wait > 0) {
      n.Wait = s.Wait - 1;
      return n;
    }
    if (s.Rung == 0) {
      n.Rung = 1;
      n.Wait = backoffStep * 1;
      return n;
    }
    if (wsl && tail == "remote") {
      n.Alerted = true;
      return n;
    }
    if (s.Reboots >= maxReboots || s.Outage >= maxReboots) {
      n.Alerted = true;
      n.Halted = true;
      return n;
    }
    if (prot) {
      int bw = s.BusyWaits + 1;
      if (bw > busyWaitLimit) { bw = busyWaitLimit; }
      n.BusyWaits = bw;
      n.Wait = busyBackoff;
      n.Alerted = s.Alerted || (s.BusyWaits + 1 >= busyWaitLimit);
      return n;
    }
    n.Misses = 0;
    n.Rung = 0;
    n.Wait = 0;
    n.BusyWaits = 0;
    n.Reboots = s.Reboots + 1;
    n.Outage = s.Outage + 1;
    return n;
  }

  // ResolveStateBootId: uses kernelBootId when positive; on a registry read error (<= 0), preserves any valid bootId
  // already recorded in rawState so a transient read failure never mimics a reboot and wipes Misses or Rung.
  public static long ResolveStateBootId(string rawState, long kernelBootId) {
    if (kernelBootId > 0) { return kernelBootId; }
    if (!string.IsNullOrEmpty(rawState)) {
      string[] f = rawState.Trim().Split(new char[] { ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
      long saved;
      if (f.Length >= 1 && long.TryParse(f[0], NumberStyles.None, CultureInfo.InvariantCulture, out saved) && saved > 0) {
        return saved;
      }
    }
    return 1;
  }

  // ParseState: reads "bootId misses rung wait busyWaits outage halted"; when bootId differs from currentBootId (an OS
  // reboot happened), volatile counters reset to 0 while outage and halted persist.
  public static WatchState ParseState(string raw, long currentBootId, out bool corrupt) {
    corrupt = false;
    WatchState s = new WatchState();
    if (string.IsNullOrEmpty(raw)) { return s; }
    string[] f = raw.Trim().Split(new char[] { ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
    long bootId;
    int misses, rung, wait, bw, outage, halted;
    if (f.Length != 7 ||
        !long.TryParse(f[0], NumberStyles.None, CultureInfo.InvariantCulture, out bootId) ||
        !int.TryParse(f[1], NumberStyles.None, CultureInfo.InvariantCulture, out misses) ||
        !int.TryParse(f[2], NumberStyles.None, CultureInfo.InvariantCulture, out rung) ||
        !int.TryParse(f[3], NumberStyles.None, CultureInfo.InvariantCulture, out wait) ||
        !int.TryParse(f[4], NumberStyles.None, CultureInfo.InvariantCulture, out bw) ||
        !int.TryParse(f[5], NumberStyles.None, CultureInfo.InvariantCulture, out outage) ||
        !int.TryParse(f[6], NumberStyles.None, CultureInfo.InvariantCulture, out halted) ||
        (halted != 0 && halted != 1) || rung > 1) {
      corrupt = true;
      return s;
    }
    if (bootId == currentBootId) {
      s.Misses = misses;
      s.Rung = rung;
      s.Wait = wait;
      s.BusyWaits = bw;
    }
    s.Outage = outage;
    s.Halted = halted == 1;
    return s;
  }

  public static string FormatState(WatchState s, long bootId) {
    return string.Format(CultureInfo.InvariantCulture, "{0} {1} {2} {3} {4} {5} {6}",
      bootId, s.Misses, s.Rung, s.Wait, s.BusyWaits, s.Outage, s.Halted ? 1 : 0);
  }

  public static long[] PruneWindow(string[] lines, long now, long windowSec) {
    List<long> kept = new List<long>();
    if (lines == null) { return kept.ToArray(); }
    foreach (string l in lines) {
      long t;
      if (long.TryParse((l ?? "").Trim(), NumberStyles.Integer, CultureInfo.InvariantCulture, out t)) {
        long age = now - t;
        if (age >= 0 && age < windowSec) { kept.Add(t); }
      }
    }
    return kept.ToArray();
  }

  public static int RebootsInWindow(string[] lines, long now, long windowSec) {
    return PruneWindow(lines, now, windowSec).Length;
  }

  // NormalizeFirewallAddr: maps Windows Firewall netmask representations back to canonical CIDR strings for idempotency.
  public static string NormalizeFirewallAddr(string raw) {
    string s = (raw ?? "").Trim().ToLowerInvariant();
    if (s == "100.64.0.0/255.192.0.0") { return "100.64.0.0/10"; }
    if (s.StartsWith("fd7a:115c:a1e0::/ffff:ffff:ffff:", StringComparison.Ordinal)) { return "fd7a:115c:a1e0::/48"; }
    return s;
  }

  public static void AppendLog(string path, int maxBytes, string line) {
    FileInfo fi = new FileInfo(path);
    int add = Encoding.UTF8.GetByteCount(line) + Environment.NewLine.Length;
    if (fi.Exists && fi.Length > 0 && fi.Length + add > maxBytes) {
      string prev = path + ".1";
      if (File.Exists(prev)) { File.Delete(prev); }
      File.Move(path, prev);
    }
    File.AppendAllText(path, line + Environment.NewLine, new UTF8Encoding(false));
  }

  // RunBounded: runs exe with args under a hard millisecond ceiling; returns -124 and kills the process on timeout.
  public static int RunBounded(string exe, string args, int timeoutMs, Encoding outputEncoding, out string stdout, out string stderr) {
    StringBuilder outBuf = new StringBuilder();
    StringBuilder errBuf = new StringBuilder();
    ProcessStartInfo psi = new ProcessStartInfo(exe, args);
    psi.UseShellExecute = false;
    psi.CreateNoWindow = true;
    psi.RedirectStandardOutput = true;
    psi.RedirectStandardError = true;
    if (outputEncoding != null) {
      psi.StandardOutputEncoding = outputEncoding;
      psi.StandardErrorEncoding = outputEncoding;
    }
    using (Process p = new Process()) {
      p.StartInfo = psi;
      p.OutputDataReceived += delegate(object sender, DataReceivedEventArgs e) {
        if (e.Data != null) { lock (outBuf) { outBuf.AppendLine(e.Data); } }
      };
      p.ErrorDataReceived += delegate(object sender, DataReceivedEventArgs e) {
        if (e.Data != null) { lock (errBuf) { errBuf.AppendLine(e.Data); } }
      };
      p.Start();
      p.BeginOutputReadLine();
      p.BeginErrorReadLine();
      if (!p.WaitForExit(timeoutMs)) {
        KillTree(p);
        stdout = outBuf.ToString();
        stderr = errBuf.ToString();
        return -124;
      }
      p.WaitForExit();
      stdout = outBuf.ToString();
      stderr = errBuf.ToString();
      return p.ExitCode;
    }
  }

  static void KillTree(Process p) {
    try {
      if (Environment.OSVersion.Platform == PlatformID.Win32NT) {
        ProcessStartInfo tk = new ProcessStartInfo("C:\\Windows\\System32\\taskkill.exe", "/T /F /PID " + p.Id);
        tk.UseShellExecute = false;
        tk.CreateNoWindow = true;
        using (Process kp = Process.Start(tk)) {
          if (kp != null) { kp.WaitForExit(3000); }
        }
      }
    } catch { }
    try { p.Kill(); } catch { }
  }

  // ProbeSshBanner: connects TCP within timeoutMs and checks for an "SSH-" banner.
  public static bool ProbeSshBanner(string host, int port, int timeoutMs) {
    try {
      using (TcpClient c = new TcpClient()) {
        IAsyncResult ar = c.BeginConnect(host, port, null, null);
        if (!ar.AsyncWaitHandle.WaitOne(timeoutMs)) { return false; }
        c.EndConnect(ar);
        c.ReceiveTimeout = timeoutMs;
        c.SendTimeout = timeoutMs;
        NetworkStream ns = c.GetStream();
        ns.ReadTimeout = timeoutMs;
        byte[] buf = new byte[64];
        int n = ns.Read(buf, 0, buf.Length);
        if (n < 4) { return false; }
        string s = Encoding.ASCII.GetString(buf, 0, n);
        return s.StartsWith("SSH-", StringComparison.Ordinal);
      }
    } catch {
      return false;
    }
  }

  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct StartupInfo {
    public int cb;
    public string lpReserved, lpDesktop, lpTitle;
    public int dwX, dwY, dwXSize, dwYSize, dwXCountChars, dwYCountChars, dwFillAttribute, dwFlags;
    public short wShowWindow, cbReserved2;
    public IntPtr lpReserved2, hStdInput, hStdOutput, hStdError;
  }
  [StructLayout(LayoutKind.Sequential)]
  struct ProcessInformation {
    public IntPtr hProcess, hThread;
    public int dwProcessId, dwThreadId;
  }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct ProcessEntry32 {
    public uint dwSize;
    public uint cntUsage;
    public uint th32ProcessID;
    public IntPtr th32DefaultHeapID;
    public uint th32ModuleID;
    public uint cntThreads;
    public uint th32ParentProcessID;
    public int pcPriClassBase;
    public uint dwFlags;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)]
    public string szExeFile;
  }
  [StructLayout(LayoutKind.Sequential)]
  struct Luid { public uint Low; public int High; }
  [StructLayout(LayoutKind.Sequential)]
  struct TokenPriv1 { public uint Count; public uint LuidLow; public int LuidHigh; public uint Attr; }
  [StructLayout(LayoutKind.Sequential)]
  struct LsaUnicodeString { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
  [StructLayout(LayoutKind.Sequential)]
  struct LsaAnsiString { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
  [StructLayout(LayoutKind.Sequential)]
  struct LsaAttributes {
    public int Length;
    public IntPtr RootDirectory, ObjectName;
    public uint Attributes;
    public IntPtr SecurityDescriptor, SecurityQualityOfService;
  }
  [StructLayout(LayoutKind.Sequential)]
  struct MsvS4uLogon {
    public int MessageType;
    public uint Flags;
    public LsaUnicodeString UserPrincipalName;
    public LsaUnicodeString DomainName;
  }
  [StructLayout(LayoutKind.Sequential)]
  struct TokenSource {
    [MarshalAs(UnmanagedType.ByValArray, SizeConst = 8)]
    public byte[] SourceName;
    public uint SourceIdLow;
    public int SourceIdHigh;
  }
  [StructLayout(LayoutKind.Sequential)]
  struct QuotaLimits {
    public IntPtr PagedPool, NonPagedPool, MinWorkingSet, MaxWorkingSet, Pagefile;
    public long TimeLimit;
  }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct ProfileInfo {
    public int dwSize;
    public uint dwFlags;
    public string lpUserName, lpProfilePath, lpDefaultPath, lpServerName, lpPolicyPath;
    public IntPtr hProfile;
  }

  [DllImport("kernel32.dll")] static extern IntPtr GetCurrentProcess();
  [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr CreateToolhelp32Snapshot(uint flags, uint pid);
  [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool Process32FirstW(IntPtr snap, ref ProcessEntry32 pe);
  [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool Process32NextW(IntPtr snap, ref ProcessEntry32 pe);
  [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr OpenProcess(uint access, bool inherit, int pid);
  [DllImport("kernel32.dll", SetLastError = true)] static extern uint WaitForSingleObject(IntPtr handle, uint ms);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool GetExitCodeProcess(IntPtr proc, out uint code);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool TerminateProcess(IntPtr proc, uint code);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr mem);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);

  [DllImport("advapi32.dll", SetLastError = true)] static extern bool OpenProcessToken(IntPtr proc, uint access, out IntPtr token);
  [DllImport("advapi32.dll", SetLastError = true)] static extern bool GetTokenInformation(IntPtr token, int infoClass, IntPtr info, int len, out int needed);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool ConvertSidToStringSidW(IntPtr sid, out IntPtr str);
  [DllImport("advapi32.dll", SetLastError = true)] static extern bool DuplicateTokenEx(IntPtr existing, uint access, IntPtr attr, int imp, int type, out IntPtr dup);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool LookupPrivilegeValueW(string sys, string name, out Luid luid);
  [DllImport("advapi32.dll", SetLastError = true)] static extern bool AdjustTokenPrivileges(IntPtr token, bool disableAll, ref TokenPriv1 newState, int bufLen, IntPtr prev, IntPtr retLen);
  [DllImport("advapi32.dll", SetLastError = true)] static extern bool AllocateLocallyUniqueId(out Luid luid);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern bool CreateProcessAsUserW(IntPtr token, string app, StringBuilder cmdLine, IntPtr pAttr, IntPtr tAttr,
    bool inherit, uint flags, IntPtr env, string cwd, ref StartupInfo si, out ProcessInformation pi);

  [DllImport("advapi32.dll")] static extern uint LsaOpenPolicy(IntPtr system, ref LsaAttributes attr, uint access, out IntPtr policy);
  [DllImport("advapi32.dll")] static extern uint LsaEnumerateAccountRights(IntPtr policy, byte[] sid, out IntPtr rights, out uint count);
  [DllImport("advapi32.dll")] static extern uint LsaAddAccountRights(IntPtr policy, byte[] sid, LsaUnicodeString[] rights, uint count);
  [DllImport("advapi32.dll")] static extern uint LsaFreeMemory(IntPtr buffer);
  [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr policy);
  [DllImport("advapi32.dll")] static extern int LsaNtStatusToWinError(uint status);

  [DllImport("secur32.dll")] static extern uint LsaRegisterLogonProcess(ref LsaAnsiString name, out IntPtr lsa, out IntPtr mode);
  [DllImport("secur32.dll")] static extern uint LsaConnectUntrusted(out IntPtr lsa);
  [DllImport("secur32.dll")] static extern uint LsaLookupAuthenticationPackage(IntPtr lsa, ref LsaAnsiString pkg, out uint authPkg);
  [DllImport("secur32.dll")]
  static extern uint LsaLogonUser(IntPtr lsa, ref LsaAnsiString origin, int logonType, uint authPkg,
    IntPtr authInfo, uint authInfoLen, IntPtr localGroups, ref TokenSource source,
    out IntPtr profileBuf, out uint profileBufLen, out Luid logonId, out IntPtr token, out QuotaLimits quotas, out uint subStatus);
  [DllImport("secur32.dll")] static extern uint LsaFreeReturnBuffer(IntPtr buffer);
  [DllImport("secur32.dll")] static extern uint LsaDeregisterLogonProcess(IntPtr lsa);

  [DllImport("userenv.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool LoadUserProfileW(IntPtr token, ref ProfileInfo pi);
  [DllImport("userenv.dll", SetLastError = true)] static extern bool UnloadUserProfile(IntPtr token, IntPtr profile);
  [DllImport("userenv.dll", SetLastError = true)] static extern bool CreateEnvironmentBlock(out IntPtr env, IntPtr token, bool inherit);
  [DllImport("userenv.dll", SetLastError = true)] static extern bool DestroyEnvironmentBlock(IntPtr env);

  const uint ProcessQueryLimited = 0x1000;
  const uint TokenQuery = 0x0008;
  const uint TokenAdjustPriv = 0x0020;
  const uint TokenDupAccess = 0x0002 | 0x0008 | 0x0001 | 0x0080 | 0x0100;
  const uint TokenAllAccess = 0xF01FF;
  const uint CreateNoWindow = 0x08000000;
  const uint CreateUnicodeEnv = 0x00000400;
  const uint WaitTimeout = 0x00000102;
  const uint PolicyAccess = 0x00000810;
  const uint StatusObjectNameNotFound = 0xC0000034;

  // EnsureBatchLogonRight: grants SeBatchLogonRight on sid when absent; returns true if newly granted.
  public static bool EnsureBatchLogonRight(byte[] sid) {
    LsaAttributes a = new LsaAttributes();
    a.Length = Marshal.SizeOf(typeof(LsaAttributes));
    IntPtr pol;
    uint st = LsaOpenPolicy(IntPtr.Zero, ref a, PolicyAccess, out pol);
    if (st != 0) { throw new Win32Exception(LsaNtStatusToWinError(st)); }
    try {
      IntPtr buf;
      uint cnt;
      st = LsaEnumerateAccountRights(pol, sid, out buf, out cnt);
      bool has = false;
      if (st == 0) {
        try {
          int sz = Marshal.SizeOf(typeof(LsaUnicodeString));
          for (int i = 0; i < cnt; i++) {
            LsaUnicodeString u = (LsaUnicodeString)Marshal.PtrToStructure(new IntPtr(buf.ToInt64() + i * sz), typeof(LsaUnicodeString));
            string r = Marshal.PtrToStringUni(u.Buffer, u.Length / 2);
            if (r == "SeBatchLogonRight") { has = true; break; }
          }
        } finally {
          LsaFreeMemory(buf);
        }
      } else if (st != StatusObjectNameNotFound) {
        throw new Win32Exception(LsaNtStatusToWinError(st));
      }
      if (has) { return false; }
      LsaUnicodeString[] add = new LsaUnicodeString[1];
      const string right = "SeBatchLogonRight";
      add[0].Buffer = Marshal.StringToHGlobalUni(right);
      add[0].Length = (ushort)(right.Length * 2);
      add[0].MaximumLength = (ushort)(right.Length * 2 + 2);
      try {
        st = LsaAddAccountRights(pol, sid, add, 1);
        if (st != 0) { throw new Win32Exception(LsaNtStatusToWinError(st)); }
      } finally {
        Marshal.FreeHGlobal(add[0].Buffer);
      }
      return true;
    } finally {
      LsaClose(pol);
    }
  }

  static void EnableProcessPrivileges() {
    IntPtr hTok;
    if (!OpenProcessToken(GetCurrentProcess(), TokenAdjustPriv | TokenQuery, out hTok)) { return; }
    try {
      string[] privs = new string[] {
        "SeTcbPrivilege", "SeAssignPrimaryTokenPrivilege", "SeIncreaseQuotaPrivilege",
        "SeRestorePrivilege", "SeBackupPrivilege"
      };
      foreach (string name in privs) {
        Luid luid;
        if (LookupPrivilegeValueW(null, name, out luid)) {
          TokenPriv1 tp = new TokenPriv1();
          tp.Count = 1;
          tp.LuidLow = luid.Low;
          tp.LuidHigh = luid.High;
          tp.Attr = 0x00000002u;
          AdjustTokenPrivileges(hTok, false, ref tp, 0, IntPtr.Zero, IntPtr.Zero);
        }
      }
    } finally {
      CloseHandle(hTok);
    }
  }

  static string GetTokenSid(IntPtr hTok) {
    int needed = 0;
    GetTokenInformation(hTok, 1, IntPtr.Zero, 0, out needed);
    if (needed <= 0) { return ""; }
    IntPtr buf = Marshal.AllocHGlobal(needed);
    try {
      if (!GetTokenInformation(hTok, 1, buf, needed, out needed)) { return ""; }
      IntPtr sidPtr = Marshal.ReadIntPtr(buf);
      IntPtr strPtr;
      if (!ConvertSidToStringSidW(sidPtr, out strPtr)) { return ""; }
      try {
        return Marshal.PtrToStringUni(strPtr) ?? "";
      } finally {
        LocalFree(strPtr);
      }
    } finally {
      Marshal.FreeHGlobal(buf);
    }
  }

  // PromotePrimaryToken: unwraps TokenLinkedToken when UAC filtered (TokenElevationTypeLimited = 3) and duplicates to a
  // Primary token usable by CreateProcessAsUserW.
  static IntPtr PromotePrimaryToken(IntPtr hTok) {
    IntPtr src = hTok;
    IntPtr linked = IntPtr.Zero;
    IntPtr buf4 = Marshal.AllocHGlobal(IntPtr.Size);
    try {
      int needed;
      if (GetTokenInformation(hTok, 18, buf4, 4, out needed) && Marshal.ReadInt32(buf4) == 3) {
        if (GetTokenInformation(hTok, 19, buf4, IntPtr.Size, out needed)) {
          linked = Marshal.ReadIntPtr(buf4);
          if (linked != IntPtr.Zero) { src = linked; }
        }
      }
    } finally {
      Marshal.FreeHGlobal(buf4);
    }
    try {
      IntPtr hPrimary;
      if (DuplicateTokenEx(src, TokenAllAccess, IntPtr.Zero, 2, 1, out hPrimary)) {
        return hPrimary;
      }
      if (src != hTok) {
        if (DuplicateTokenEx(hTok, TokenAllAccess, IntPtr.Zero, 2, 1, out hPrimary)) {
          return hPrimary;
        }
      }
      return IntPtr.Zero;
    } finally {
      if (linked != IntPtr.Zero) { CloseHandle(linked); }
    }
  }

  // FindDonorPid: scans kernel process snapshot in-process (no WMI, no PEB reads) for a process owned by ownerSid,
  // preferring explorer.exe/sihost.exe when logged in and falling back to wsl.exe/conhost.exe.
  public static int FindDonorPid(string ownerSid) {
    if (string.IsNullOrEmpty(ownerSid)) { return 0; }
    IntPtr snap = CreateToolhelp32Snapshot(2u, 0u);
    if (snap == IntPtr.Zero || snap == new IntPtr(-1)) { return 0; }
    try {
      ProcessEntry32 pe = new ProcessEntry32();
      pe.dwSize = (uint)Marshal.SizeOf(typeof(ProcessEntry32));
      if (!Process32FirstW(snap, ref pe)) { return 0; }
      int fallback = 0;
      do {
        string exe = (pe.szExeFile ?? "").ToLowerInvariant();
        bool interactive = (exe == "explorer.exe" || exe == "sihost.exe");
        bool headless = (exe == "wsl.exe" || exe == "conhost.exe");
        if (!interactive && !headless) { continue; }
        IntPtr hp = OpenProcess(ProcessQueryLimited, false, (int)pe.th32ProcessID);
        if (hp == IntPtr.Zero) { continue; }
        try {
          IntPtr ht;
          if (OpenProcessToken(hp, TokenQuery, out ht)) {
            try {
              if (string.Equals(GetTokenSid(ht), ownerSid, StringComparison.OrdinalIgnoreCase)) {
                if (interactive) { return (int)pe.th32ProcessID; }
                if (fallback == 0) { fallback = (int)pe.th32ProcessID; }
              }
            } finally {
              CloseHandle(ht);
            }
          }
        } finally {
          CloseHandle(hp);
        }
      } while (Process32NextW(snap, ref pe));
      return fallback;
    } finally {
      CloseHandle(snap);
    }
  }

  // BuildS4uBuffer: packs MSV1_0_S4U_LOGON and its UTF-16 user/domain strings into one contiguous unmanaged buffer, as
  // LsaLogonUser requires. Caller frees with Marshal.FreeHGlobal.
  public static IntPtr BuildS4uBuffer(string user, string domain, out int totalBytes) {
    byte[] uBytes = Encoding.Unicode.GetBytes(user ?? "");
    byte[] dBytes = Encoding.Unicode.GetBytes(string.IsNullOrEmpty(domain) ? "." : domain);
    int hdrSize = Marshal.SizeOf(typeof(MsvS4uLogon));
    totalBytes = hdrSize + uBytes.Length + 2 + dBytes.Length + 2;
    IntPtr buf = Marshal.AllocHGlobal(totalBytes);
    for (int i = 0; i < totalBytes; i++) { Marshal.WriteByte(buf, i, 0); }
    IntPtr uPtr = new IntPtr(buf.ToInt64() + hdrSize);
    IntPtr dPtr = new IntPtr(buf.ToInt64() + hdrSize + uBytes.Length + 2);
    if (uBytes.Length > 0) { Marshal.Copy(uBytes, 0, uPtr, uBytes.Length); }
    if (dBytes.Length > 0) { Marshal.Copy(dBytes, 0, dPtr, dBytes.Length); }
    MsvS4uLogon hdr = new MsvS4uLogon();
    hdr.MessageType = 12;
    hdr.Flags = 0;
    hdr.UserPrincipalName.Length = (ushort)uBytes.Length;
    hdr.UserPrincipalName.MaximumLength = (ushort)(uBytes.Length + 2);
    hdr.UserPrincipalName.Buffer = uPtr;
    hdr.DomainName.Length = (ushort)dBytes.Length;
    hdr.DomainName.MaximumLength = (ushort)(dBytes.Length + 2);
    hdr.DomainName.Buffer = dPtr;
    Marshal.StructureToPtr(hdr, buf, false);
    return buf;
  }

  static IntPtr TryS4uDomain(IntPtr lsa, uint authPkg, string user, string domain) {
    int total;
    IntPtr authBuf = BuildS4uBuffer(user, domain, out total);
    IntPtr originPtr = Marshal.StringToHGlobalAnsi("NovaWatch");
    try {
      LsaAnsiString origin = new LsaAnsiString();
      origin.Buffer = originPtr;
      origin.Length = 9;
      origin.MaximumLength = 10;
      TokenSource src = new TokenSource();
      src.SourceName = Encoding.ASCII.GetBytes("NovaWslW");
      Luid srcLuid;
      if (AllocateLocallyUniqueId(out srcLuid)) {
        src.SourceIdLow = srcLuid.Low;
        src.SourceIdHigh = srcLuid.High;
      }
      IntPtr profBuf = IntPtr.Zero;
      uint profLen = 0;
      Luid logonId;
      IntPtr hTok = IntPtr.Zero;
      QuotaLimits quotas;
      uint subStatus;
      uint st = LsaLogonUser(lsa, ref origin, 4, authPkg, authBuf, (uint)total, IntPtr.Zero,
        ref src, out profBuf, out profLen, out logonId, out hTok, out quotas, out subStatus);
      if (profBuf != IntPtr.Zero) { LsaFreeReturnBuffer(profBuf); }
      if (st != 0 || hTok == IntPtr.Zero) { return IntPtr.Zero; }
      try {
        IntPtr promoted = PromotePrimaryToken(hTok);
        if (promoted != IntPtr.Zero) {
          CloseHandle(hTok);
          return promoted;
        }
        return hTok;
      } catch {
        CloseHandle(hTok);
        return IntPtr.Zero;
      }
    } finally {
      Marshal.FreeHGlobal(originPtr);
      Marshal.FreeHGlobal(authBuf);
    }
  }

  static IntPtr OpenS4uPrimaryToken(string winUser, string winDomain) {
    if (string.IsNullOrEmpty(winUser)) { return IntPtr.Zero; }
    IntPtr procNamePtr = Marshal.StringToHGlobalAnsi("NovaWslWatch");
    IntPtr pkgNamePtr = Marshal.StringToHGlobalAnsi("MICROSOFT_AUTHENTICATION_PACKAGE_V1_0");
    IntPtr lsa = IntPtr.Zero;
    try {
      LsaAnsiString procName = new LsaAnsiString();
      procName.Buffer = procNamePtr;
      procName.Length = 12;
      procName.MaximumLength = 13;
      IntPtr secMode;
      uint st = LsaRegisterLogonProcess(ref procName, out lsa, out secMode);
      if (st != 0 || lsa == IntPtr.Zero) {
        st = LsaConnectUntrusted(out lsa);
        if (st != 0 || lsa == IntPtr.Zero) { return IntPtr.Zero; }
      }
      LsaAnsiString pkgName = new LsaAnsiString();
      pkgName.Buffer = pkgNamePtr;
      pkgName.Length = 37;
      pkgName.MaximumLength = 38;
      uint authPkg;
      if (LsaLookupAuthenticationPackage(lsa, ref pkgName, out authPkg) != 0) { return IntPtr.Zero; }
      if (!string.IsNullOrEmpty(winDomain) && winDomain != ".") {
        IntPtr t1 = TryS4uDomain(lsa, authPkg, winUser, winDomain);
        if (t1 != IntPtr.Zero) { return t1; }
      }
      return TryS4uDomain(lsa, authPkg, winUser, ".");
    } finally {
      if (lsa != IntPtr.Zero) { LsaDeregisterLogonProcess(lsa); }
      Marshal.FreeHGlobal(pkgNamePtr);
      Marshal.FreeHGlobal(procNamePtr);
    }
  }

  static IntPtr OpenDonorPrimaryToken(int donorPid, string ownerSid) {
    if (donorPid <= 0) { return IntPtr.Zero; }
    IntPtr hProc = OpenProcess(ProcessQueryLimited, false, donorPid);
    if (hProc == IntPtr.Zero) { return IntPtr.Zero; }
    try {
      IntPtr hTok;
      if (!OpenProcessToken(hProc, TokenDupAccess, out hTok)) { return IntPtr.Zero; }
      try {
        if (!string.IsNullOrEmpty(ownerSid) && !string.Equals(GetTokenSid(hTok), ownerSid, StringComparison.OrdinalIgnoreCase)) {
          return IntPtr.Zero;
        }
        return PromotePrimaryToken(hTok);
      } finally {
        CloseHandle(hTok);
      }
    } finally {
      CloseHandle(hProc);
    }
  }

  // RunAsOwner: runs cmdLine as the WSL owner (using donorPids elevated token when running, or an LSA S4U Batch logon
  // token at cold boot when zero processes of ownerSid exist), mounting the owners registry hive via LoadUserProfileW so
  // HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss is reachable with nobody logged in; -124 on timeout, -1 on error.
  public static int RunAsOwner(int donorPid, string ownerSid, string winUser, string winDomain, string cmdLine, int timeoutMs) {
    EnableProcessPrivileges();
    IntPtr hPrimary = OpenDonorPrimaryToken(donorPid, ownerSid);
    if (hPrimary == IntPtr.Zero) {
      hPrimary = OpenS4uPrimaryToken(winUser, winDomain);
    }
    if (hPrimary == IntPtr.Zero) { return -1; }
    try {
      ProfileInfo prof = new ProfileInfo();
      bool profLoaded = false;
      if (!string.IsNullOrEmpty(winUser)) {
        prof.dwSize = Marshal.SizeOf(typeof(ProfileInfo));
        prof.dwFlags = 1u;
        prof.lpUserName = winUser;
        profLoaded = LoadUserProfileW(hPrimary, ref prof);
      }
      IntPtr env = IntPtr.Zero;
      CreateEnvironmentBlock(out env, hPrimary, false);
      try {
        StartupInfo si = new StartupInfo();
        si.cb = Marshal.SizeOf(typeof(StartupInfo));
        ProcessInformation pi;
        StringBuilder sb = new StringBuilder(cmdLine);
        uint flags = CreateNoWindow | (env != IntPtr.Zero ? CreateUnicodeEnv : 0u);
        if (!CreateProcessAsUserW(hPrimary, null, sb, IntPtr.Zero, IntPtr.Zero, false, flags, env, "C:\\Windows\\System32", ref si, out pi)) {
          return -1;
        }
        try {
          uint w = WaitForSingleObject(pi.hProcess, (uint)timeoutMs);
          if (w == WaitTimeout) {
            TerminateProcess(pi.hProcess, 124);
            return -124;
          }
          uint code;
          if (!GetExitCodeProcess(pi.hProcess, out code)) { return -1; }
          return (int)code;
        } finally {
          CloseHandle(pi.hThread);
          CloseHandle(pi.hProcess);
        }
      } finally {
        if (env != IntPtr.Zero) { DestroyEnvironmentBlock(env); }
        if (profLoaded && prof.hProfile != IntPtr.Zero) { UnloadUserProfile(hPrimary, prof.hProfile); }
      }
    } finally {
      CloseHandle(hPrimary);
    }
  }
}
'@
}

function Need-Admin {
  $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { 'NEEDS-ELEVATION'; exit 3 }
}

function Get-WslExe {
  $pf = 'C:\Program Files\WSL\wsl.exe'
  if (Test-Path -LiteralPath $pf -PathType Leaf) { return $pf }
  return 'C:\Windows\System32\wsl.exe'
}

$Sealers = @('S-1-5-18', 'S-1-5-32-544')

function Seal-WatchdogDir([string]$p) {
  if (-not (Test-Path -LiteralPath $p)) {
    $ds = New-Object Security.AccessControl.DirectorySecurity
    $ds.SetAccessRuleProtection($true, $false)
    $ds.SetOwner((New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'))
    foreach ($s in $Sealers) {
      $ds.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier $s),
        'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
    }
    [void][IO.Directory]::CreateDirectory($p, $ds)
  }
  $acl = (Get-Item -LiteralPath $p -Force).GetAccessControl()
  $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
  if ($Sealers -notcontains $owner -or -not $acl.AreAccessRulesProtected) {
    "PLANTED $p owner=$owner inherited=$(-not $acl.AreAccessRulesProtected): delete it in an elevated cmd (rmdir /s /q `"$p`") and run again"
    exit 11
  }
  foreach ($r in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
    if ($Sealers -notcontains $r.IdentityReference.Value) {
      "PLANTED $p ace=$($r.IdentityReference.Value): delete it in an elevated cmd (rmdir /s /q `"$p`") and run again"
      exit 11
    }
  }
}

function Seal-StrictFile([string]$p) {
  $acl = Get-Acl -LiteralPath $p
  $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
  $aces = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
  $aclOk = ($Sealers -contains $owner) -and $acl.AreAccessRulesProtected -and ($aces.Count -eq 2) -and
    (@($aces | Where-Object { $Sealers -notcontains $_.IdentityReference.Value }).Count -eq 0)
  if (-not $aclOk) {
    $fs = New-Object Security.AccessControl.FileSecurity
    $fs.SetAccessRuleProtection($true, $false)
    $fs.SetOwner((New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'))
    foreach ($s in $Sealers) {
      $fs.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier $s),
        'FullControl', 'None', 'None', 'Allow')))
    }
    Set-Acl -LiteralPath $p -AclObject $fs
    return $true
  }
  return $false
}

function Seal-AdminKeysFile([string]$p, [string[]]$lines) {
  $dir = Split-Path $p
  if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
  $changed = $false
  $have = @()
  if (Test-Path -LiteralPath $p -PathType Leaf) {
    $have = @(Get-Content -LiteralPath $p | ForEach-Object { "$_".Trim() } | Where-Object { $_ })
  }
  $merged = New-Object System.Collections.Generic.List[string]
  foreach ($l in ($have + $lines)) {
    $t = "$l".Trim()
    if ($t -and -not $merged.Contains($t)) { $merged.Add($t) }
  }
  $wantText = if ($merged.Count -gt 0) { ($merged.ToArray() -join "`n") + "`n" } else { '' }
  $curText = if (Test-Path -LiteralPath $p -PathType Leaf) { [IO.File]::ReadAllText($p) } else { $null }
  if ($curText -cne $wantText) {
    [IO.File]::WriteAllText($p, $wantText, (New-Object System.Text.UTF8Encoding $false))
    $changed = $true
  }
  if (Seal-StrictFile $p) { $changed = $true }
  return @{ Changed = $changed; Count = $merged.Count }
}

function Get-KernelBootId([string]$rawState) {
  $kBoot = 0L
  try {
    $v = (Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters' -Name BootId -ErrorAction Stop).BootId
    if ($null -ne $v -and [long]$v -gt 0) { $kBoot = [long]$v }
  } catch { $kBoot = 0L }
  return [NovaWslWatch]::ResolveStateBootId($rawState, $kBoot)
}

function Invoke-Bounded([string]$exe, [string]$argText, [int]$ms, [Text.Encoding]$enc = $null) {
  $so = ''; $se = ''
  $rc = [NovaWslWatch]::RunBounded($exe, $argText, $ms, $enc, [ref]$so, [ref]$se)
  return @{ Rc = $rc; Stdout = $so; Stderr = $se }
}

function Read-AcZero([string]$sub, [string]$setting) {
  $r = Invoke-Bounded 'C:\Windows\System32\powercfg.exe' "/query SCHEME_CURRENT $sub $setting" 10000
  if ($r.Rc -ne 0) { return $true }
  return ($r.Stdout -match 'Current AC Power Setting Index:\s*0x00000000')
}

Add-WatchdogApi

switch ($Action) {
  'install' {
    Need-Admin
    if ($MachineId -notmatch '^[0-9a-f]{32}$' -or $User -notmatch '^[a-z_][a-z0-9_-]*$') { 'BAD-ARGS -MachineId -User'; exit 2 }
    if ($WinSshPort -lt 1 -or $WinSshPort -gt 65535 -or $WinSshPort -eq $WslSshPort) { 'BAD-ARGS -WinSshPort'; exit 2 }

    $wslExe = Get-WslExe
    $list = Invoke-Bounded $wslExe '--list --running --quiet' 15000 ([Text.Encoding]::Unicode)
    if ($list.Rc -ne 0) { "WSL-LIST-FAILED rc=$($list.Rc)"; exit 6 }
    $running = @($list.Stdout -split '\r?\n' | ForEach-Object { ("$_" -replace '\0', '').Trim() } | Where-Object { $_ })
    $distro = $running | Where-Object {
      try { (Get-Content -LiteralPath "\\wsl.localhost\$_\etc\machine-id" -TotalCount 1 -ErrorAction Stop).Trim() -eq $MachineId } catch { $false }
    } | Select-Object -First 1
    if (-not $distro -or $distro -notmatch '^[A-Za-z0-9._-]+$') { "NO-DISTRO machine-id=$MachineId running=$(@($running) -join ',')"; exit 6 }

    Seal-WatchdogDir $WatchdogDir
    New-Item -ItemType Directory -Force -Path (Join-Path $WatchdogDir 'hold.d') | Out-Null
    $idObj = [Security.Principal.WindowsIdentity]::GetCurrent()
    $sidObj = $idObj.User
    $ownerSid = $sidObj.Value
    $ntName = try { $sidObj.Translate([Security.Principal.NTAccount]).Value } catch { "$($idObj.Name)" }
    $winDomain = '.'
    $winUser = $ntName
    if ($ntName -match '^([^\\]+)\\(.+)$') { $winDomain = $Matches[1]; $winUser = $Matches[2] }
    $sidBytes = New-Object byte[] $sidObj.BinaryLength
    $sidObj.GetBinaryForm($sidBytes, 0)
    $wdDid = 'ok'
    if ([NovaWslWatch]::EnsureBatchLogonRight($sidBytes)) { $wdDid = 'set' }

    $cfgObj = [ordered]@{
      distro = $distro; user = $User; ownerSid = $ownerSid
      winDomain = $winDomain; winUser = $winUser
      wslSshPort = $WslSshPort; winSshPort = $WinSshPort
      threshold = $Threshold; backoff = $Backoff
      busyBackoff = $BusyBackoff; busyWaitLimit = $BusyWaitLimit
      maxReboots = $MaxReboots; windowSeconds = $WindowSeconds
    }
    $cfgPath = Join-Path $WatchdogDir 'config.json'
    $cfgJson = ($cfgObj | ConvertTo-Json -Compress) + "`n"
    if (-not (Test-Path -LiteralPath $cfgPath) -or [IO.File]::ReadAllText($cfgPath) -cne $cfgJson) {
      [IO.File]::WriteAllText($cfgPath, $cfgJson, (New-Object System.Text.UTF8Encoding $false))
      $wdDid = 'set'
    }

    $taskName = "WSL2 watchdog ($distro)"
    $psExe = 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe'
    $scriptPath = $MyInvocation.MyCommand.Path
    $taskArg = "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$scriptPath`" -Action tick -WatchdogDir `"$WatchdogDir`""
    $xml = @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Set by nova-fleet windows-runners: probes WSL ($distro) and Windows Tailscale each minute with bounded timeouts; restarts WSL or reboots Windows within budget.</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-18</UserId>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <Enabled>true</Enabled>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT5M</ExecutionTimeLimit>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
  </Settings>
  <Triggers>
    <BootTrigger>
      <Repetition>
        <Interval>PT1M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd></Repetition>
    </BootTrigger>
    <TimeTrigger>
      <StartBoundary>2026-01-01T00:00:00Z</StartBoundary>
      <Repetition>
        <Interval>PT1M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd></Repetition>
    </TimeTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>$psExe</Command>
      <Arguments>$taskArg</Arguments>
    </Exec>
  </Actions>
</Task>
"@
    function Watchdog-Of([xml]$x) {
      $s = $x.Task.Settings
      $p = $x.Task.Principals.Principal
      $d = { param($v, $def) if ($null -eq $v -or "$v" -eq '') { $def } else { "$v" } }
      $uid = "$($p.UserId)".Trim()
      if ($uid -in @('S-1-5-18', 'SYSTEM', 'NT AUTHORITY\SYSTEM')) { $uid = 'S-1-5-18' }
      $lt = "$($p.LogonType)".Trim()
      if ($lt -eq 'ServiceAccount') { $lt = '' }
      @(
        "user=$uid", "logon=$lt", "level=$(& $d $p.RunLevel 'LeastPrivilege')",
        "triggers=$(@($x.Task.Triggers.ChildNodes | ForEach-Object { $_.LocalName }) -join ',')",
        "exec=$(@($x.Task.Actions.Exec | ForEach-Object { "$($_.Command) $($_.Arguments)" }) -join ';')",
        "enabled=$(& $d $s.Enabled 'true')", "multiple=$(& $d $s.MultipleInstancesPolicy 'IgnoreNew')",
        "limit=$(& $d $s.ExecutionTimeLimit 'PT72H')", "battery=$(& $d $s.DisallowStartIfOnBatteries 'true')/$(& $d $s.StopIfGoingOnBatteries 'true')"
      ) -join ' '
    }
    $haveTask = ''
    if (Get-ScheduledTask -TaskPath '\' -TaskName $taskName -ErrorAction SilentlyContinue) {
      $haveTask = Watchdog-Of ([xml](Export-ScheduledTask -TaskPath '\' -TaskName $taskName))
    }
    if ($haveTask -ne (Watchdog-Of ([xml]$xml))) {
      Register-ScheduledTask -TaskPath '\' -TaskName $taskName -Xml $xml -Force | Out-Null
      $wdDid = 'set'
    }
    "WATCHDOG $distro $wdDid task=$taskName limit=PT5M dir=$WatchdogDir"

    $unDid = 'ok'
    if (-not (Read-AcZero 'SUB_SLEEP' 'STANDBYIDLE')) {
      [void](Invoke-Bounded 'C:\Windows\System32\powercfg.exe' '/change standby-timeout-ac 0' 10000)
      $unDid = 'set'
    }
    if (-not (Read-AcZero 'SUB_SLEEP' 'HIBERNATEIDLE')) {
      [void](Invoke-Bounded 'C:\Windows\System32\powercfg.exe' '/change hibernate-timeout-ac 0' 10000)
      $unDid = 'set'
    }
    $ccKey = 'HKLM:\SYSTEM\CurrentControlSet\Control\CrashControl'
    if ((Get-ItemProperty -LiteralPath $ccKey -Name AutoReboot -ErrorAction SilentlyContinue).AutoReboot -ne 1) {
      New-ItemProperty -LiteralPath $ccKey -Name AutoReboot -PropertyType DWord -Value 1 -Force | Out-Null
      $unDid = 'set'
    }
    $werKey = 'HKLM:\SOFTWARE\Microsoft\Windows\Windows Error Reporting'
    if (-not (Test-Path -LiteralPath $werKey)) { New-Item -Path $werKey -Force | Out-Null }
    if ((Get-ItemProperty -LiteralPath $werKey -Name DontShowUI -ErrorAction SilentlyContinue).DontShowUI -ne 1) {
      New-ItemProperty -LiteralPath $werKey -Name DontShowUI -PropertyType DWord -Value 1 -Force | Out-Null
      $unDid = 'set'
    }
    $tsExe = 'C:\Program Files\Tailscale\tailscale.exe'
    $tsUnattended = 'absent'
    if (Test-Path -LiteralPath $tsExe -PathType Leaf) {
      $prefs = Invoke-Bounded $tsExe 'debug prefs' 10000
      $forceDaemon = $false
      if ($prefs.Rc -eq 0 -and $prefs.Stdout.Trim().StartsWith('{')) {
        try { $forceDaemon = [bool](($prefs.Stdout | ConvertFrom-Json).ForceDaemon) } catch { $forceDaemon = $false }
      }
      if (-not $forceDaemon) {
        $setTs = Invoke-Bounded $tsExe 'set --unattended' 15000
        if ($setTs.Rc -ne 0) { "TAILSCALE-UNATTENDED-FAILED rc=$($setTs.Rc)"; exit 7 }
        $unDid = 'set'
      }
      $tsUnattended = 'yes'
    }
    "UNATTENDED $unDid sleep=off crash_autoreboot=1 wer_noui=1 tailscale_unattended=$tsUnattended"

    $keyRe = '^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) AAAA[A-Za-z0-9+/=]+( [ -~]*)?$'
    $inKeys = @([Console]::In.ReadToEnd() -split '\r?\n' | ForEach-Object { "$_".Trim() } | Where-Object { $_ })
    foreach ($k in $inKeys) {
      if ($k -notmatch $keyRe) { "BAD-SSH-KEY $($k.Substring(0, [Math]::Min(32, $k.Length)))"; exit 2 }
    }
    $sshDid = 'ok'
    $sshdExe = 'C:\Windows\System32\OpenSSH\sshd.exe'
    if (-not (Test-Path -LiteralPath $sshdExe -PathType Leaf)) {
      try {
        Add-WindowsCapability -Online -Name 'OpenSSH.Server~~~~0.0.1.0' -ErrorAction Stop | Out-Null
      } catch {
        "WINSSH-INSTALL-FAILED $($_.Exception.Message -replace '\s+', ' ')"
        exit 8
      }
      if (-not (Test-Path -LiteralPath $sshdExe -PathType Leaf)) {
        'WINSSH-INSTALL-FAILED sshd.exe missing after Add-WindowsCapability'
        exit 8
      }
      $sshDid = 'set'
    }
    $sshProgramData = 'C:\ProgramData\ssh'
    if (-not (Test-Path -LiteralPath $sshProgramData)) { New-Item -ItemType Directory -Force -Path $sshProgramData | Out-Null }
    if (-not (Test-Path -LiteralPath (Join-Path $sshProgramData 'ssh_host_ed25519_key') -PathType Leaf)) {
      $kg = Invoke-Bounded 'C:\Windows\System32\OpenSSH\ssh-keygen.exe' '-A' 15000
      if ($kg.Rc -ne 0) { "WINSSH-KEYGEN-FAILED rc=$($kg.Rc)"; exit 8 }
      $sshDid = 'set'
    }
    foreach ($hk in @(Get-ChildItem -LiteralPath $sshProgramData -Filter 'ssh_host_*_key' -File -ErrorAction SilentlyContinue)) {
      if ($hk.Extension -ne '.pub' -and (Seal-StrictFile $hk.FullName)) { $sshDid = 'set' }
    }
    $akRes = Seal-AdminKeysFile (Join-Path $sshProgramData 'administrators_authorized_keys') $inKeys
    if ($akRes.Changed) { $sshDid = 'set' }
    if ($akRes.Count -eq 0) { 'WINSSH-NO-KEYS administrators_authorized_keys would be empty'; exit 8 }

    $cfgFile = Join-Path $sshProgramData 'sshd_config'
    $wantCfg = @(
      "# managed by nova-fleet windows-runners (out-of-band Windows OpenSSH on Tailscale)",
      "Port $WinSshPort",
      "PubkeyAuthentication yes",
      "PasswordAuthentication no",
      "PermitEmptyPasswords no",
      "AuthorizedKeysFile .ssh/authorized_keys",
      "Subsystem sftp sftp-server.exe",
      "Match Group administrators",
      "       AuthorizedKeysFile __PROGRAMDATA__/ssh/administrators_authorized_keys"
    ) -join "`n"
    $wantCfg += "`n"
    $curCfg = if (Test-Path -LiteralPath $cfgFile -PathType Leaf) { [IO.File]::ReadAllText($cfgFile) } else { '' }
    $restartSshd = $false
    if ($curCfg -cne $wantCfg) {
      $tmpCfg = Join-Path $WatchdogDir 'sshd_config.candidate'
      [IO.File]::WriteAllText($tmpCfg, $wantCfg, (New-Object System.Text.UTF8Encoding $false))
      $chk = Invoke-Bounded $sshdExe "-t -f `"$tmpCfg`"" 10000
      Remove-Item -LiteralPath $tmpCfg -Force -ErrorAction SilentlyContinue
      if ($chk.Rc -ne 0) { "WINSSH-CONFIG-INVALID rc=$($chk.Rc) $($chk.Stderr.Trim())"; exit 8 }
      [IO.File]::WriteAllText($cfgFile, $wantCfg, (New-Object System.Text.UTF8Encoding $false))
      $sshDid = 'set'
      $restartSshd = $true
    }

    $fwName = 'nova-fleet-winssh-tailscale'
    $tailCidrs = @('100.64.0.0/10', 'fd7a:115c:a1e0::/48')
    $fw = Get-NetFirewallRule -Name $fwName -ErrorAction SilentlyContinue
    $fwOk = $false
    if ($fw) {
      $pf = $fw | Get-NetFirewallPortFilter
      $af = $fw | Get-NetFirewallAddressFilter
      $gotAddr = @($af.RemoteAddress | ForEach-Object { [NovaWslWatch]::NormalizeFirewallAddr("$_") } | Sort-Object -Unique) -join ','
      $wantAddr = @($tailCidrs | ForEach-Object { [NovaWslWatch]::NormalizeFirewallAddr("$_") } | Sort-Object -Unique) -join ','
      $fwOk = ($fw.Enabled -eq 'True') -and ($fw.Direction -eq 'Inbound') -and ($fw.Action -eq 'Allow') -and
        ($pf.Protocol -eq 'TCP') -and ("$($pf.LocalPort)" -eq "$WinSshPort") -and ($gotAddr -eq $wantAddr)
    }
    if (-not $fwOk) {
      if ($fw) { Remove-NetFirewallRule -Name $fwName }
      New-NetFirewallRule -Name $fwName -DisplayName 'nova-fleet Windows OpenSSH (Tailscale only)' `
        -Direction Inbound -Action Allow -Protocol TCP -LocalPort $WinSshPort -RemoteAddress $tailCidrs -Profile Any -Enabled True | Out-Null
      $sshDid = 'set'
    }
    $stockFw = Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue
    if ($stockFw -and $stockFw.Enabled -eq 'True') {
      Disable-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' | Out-Null
      $sshDid = 'set'
    }

    $sshdSvc = Get-CimInstance Win32_Service -Filter "Name = 'sshd'" -OperationTimeoutSec 10
    if (-not $sshdSvc) { 'WINSSH-NO-SERVICE sshd'; exit 8 }
    $delayed = (Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Services\sshd' -Name DelayedAutostart -ErrorAction SilentlyContinue).DelayedAutostart
    if ($sshdSvc.StartMode -ne 'Auto' -or $delayed -eq 1) {
      [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' 'config sshd start= auto' 10000)
      $sshDid = 'set'
    }
    if ($restartSshd -and $sshdSvc.State -eq 'Running') {
      Restart-Service -Name sshd -Force
    } elseif ($sshdSvc.State -ne 'Running') {
      Start-Service -Name sshd
      $sshDid = 'set'
    }
    if (-not [NovaWslWatch]::ProbeSshBanner('127.0.0.1', $WinSshPort, 5000)) {
      "WINSSH-NOT-LISTENING port=$WinSshPort"
      exit 8
    }
    "WINSSH $sshDid port=$WinSshPort auth=key-only keys=$($akRes.Count) firewall=tailscale service=running admin=$winUser"
  }

  'tick' {
    $cfgPath = Join-Path $WatchdogDir 'config.json'
    if (-not (Test-Path -LiteralPath $cfgPath -PathType Leaf)) { exit 0 }
    $cfg = Get-Content -Raw -LiteralPath $cfgPath | ConvertFrom-Json
    $logFile = Join-Path $WatchdogDir 'watchdog.log'
    $stateFile = Join-Path $WatchdogDir 'state'
    $rebootsFile = Join-Path $WatchdogDir 'reboots'
    $alertFile = Join-Path $WatchdogDir 'ALERT'
    $holdDir = Join-Path $WatchdogDir 'hold.d'
    $logMax = 262144
    function Write-WatchLog([string]$msg) {
      $ts = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
      [NovaWslWatch]::AppendLog($logFile, $logMax, "$ts WSLWATCH $($cfg.distro) $msg")
    }

    # Flush negative Windows DNS cache when github.com fails resolution, then ensure automatic sshd and
    # actions.runner.* services are running with an active Runner.Listener.exe process.
    try {
      [void][Net.Dns]::GetHostAddresses('github.com')
    } catch {
      if (-not $Dry) { [void](Invoke-Bounded 'C:\Windows\System32\ipconfig.exe' '/flushdns' 5000) }
      Write-WatchLog 'DNS flushed negative cache (github.com lookup failed)'
    }
    try {
      $svcs = @(Get-CimInstance Win32_Service -OperationTimeoutSec 10 | Where-Object {
        ($_.Name -eq 'sshd' -or $_.Name -like 'actions.runner.*') -and $_.StartMode -eq 'Auto'
      })
      $listeners = @()
      try {
        $listeners = @(Get-CimInstance Win32_Process -Filter "Name = 'Runner.Listener.exe'" -Property ProcessId,ExecutablePath -OperationTimeoutSec 8 |
          ForEach-Object { "$($_.ExecutablePath)".ToLowerInvariant() })
      } catch { $listeners = $null }
      $anyWorker = (@([Diagnostics.Process]::GetProcessesByName('Runner.Worker')).Count -gt 0)
      foreach ($sv in $svcs) {
        if ($sv.State -eq 'Stopped') {
          if (-not $Dry) { [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' "start `"$($sv.Name)`"" 10000) }
          Write-WatchLog "SERVICE started=$($sv.Name)"
        } elseif ($sv.Name -like 'actions.runner.*' -and $sv.State -eq 'Running' -and $null -ne $listeners -and -not $anyWorker) {
          $rdir = Split-Path (Split-Path ($sv.PathName.Trim('"')))
          $wantExe = (Join-Path $rdir 'bin\Runner.Listener.exe').ToLowerInvariant()
          if ($listeners -notcontains $wantExe) {
            if (-not $Dry) {
              [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' "stop `"$($sv.Name)`"" 10000)
              [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' "start `"$($sv.Name)`"" 10000)
            }
            Write-WatchLog "SERVICE restarted=$($sv.Name) (no Runner.Listener.exe)"
          }
        }
      }
    } catch { }

    $wslExe = Get-WslExe
    $kaName = "WSL2 keepalive ($($cfg.distro))"
    $kaTask = "\$kaName"
    $winUser = if ($cfg.PSObject.Properties['winUser']) { "$($cfg.winUser)" } else { '' }
    $winDomain = if ($cfg.PSObject.Properties['winDomain']) { "$($cfg.winDomain)" } else { '.' }
    $kaState = try { "$((Get-ScheduledTask -TaskPath '\' -TaskName $kaName -ErrorAction SilentlyContinue).State)" } catch { '' }
    $donorPid = [NovaWslWatch]::FindDonorPid("$($cfg.ownerSid)")
    if (($kaState -ne 'Running' -or $donorPid -eq 0) -and -not $Dry) {
      [void](Invoke-Bounded 'C:\Windows\System32\schtasks.exe' "/Run /TN `"$kaTask`"" 5000)
      Start-Sleep -Seconds 3
      $kaState = try { "$((Get-ScheduledTask -TaskPath '\' -TaskName $kaName -ErrorAction SilentlyContinue).State)" } catch { '' }
      $donorPid = [NovaWslWatch]::FindDonorPid("$($cfg.ownerSid)")
    }
    $kaOk = ($kaState -eq 'Running')
    $cmd = "`"$wslExe`" -d $($cfg.distro) -u $($cfg.user) --exec /bin/true"
    $execRc = [NovaWslWatch]::RunAsOwner($donorPid, "$($cfg.ownerSid)", $winUser, $winDomain, $cmd, 10000)
    $execOk = ($execRc -eq 0)
    $sshOk = [NovaWslWatch]::ProbeSshBanner('127.0.0.1', [int]$cfg.wslSshPort, 5000)
    $wslOk = $kaOk -and $execOk -and $sshOk
    $wslWhy = "keepalive=$kaState donor=$donorPid exec=$execOk(rc=$execRc) sshd=$sshOk"

    $tsExe = 'C:\Program Files\Tailscale\tailscale.exe'
    $obs = New-Object TailObs('running', $true, $true, 'answers')
    $tailWhy = 'absent'
    if (Test-Path -LiteralPath $tsExe -PathType Leaf) {
      $backend = 'none'; $selfOn = $false; $peersAns = $false; $ctrl = 'silent'
      $st = Invoke-Bounded $tsExe 'status --json' 10000
      $peerIps = @()
      if ($st.Rc -eq 0 -and $st.Stdout.Trim().StartsWith('{')) {
        try {
          $j = $st.Stdout | ConvertFrom-Json
          $bs = "$($j.BackendState)"
          switch ($bs) {
            'Running' { $backend = 'running' }
            'Stopped' { $backend = 'stopped' }
            'NeedsLogin' { $backend = 'login' }
            'NeedsMachineAuth' { $backend = 'login' }
            '' { $backend = 'none' }
            default { $backend = 'other' }
          }
          if ($j.Self -and $j.Self.Online) { $selfOn = $true }
          if ($j.Peer) {
            foreach ($prop in $j.Peer.PSObject.Properties) {
              $pv = $prop.Value
              if ($pv.Online -and $pv.TailscaleIPs -and $pv.TailscaleIPs.Count -gt 0) {
                $peerIps += "$($pv.TailscaleIPs[0])"
              }
            }
          }
        } catch { $backend = 'none' }
      }
      if ($backend -eq 'running') {
        if ($peerIps.Count -eq 0) {
          $peersAns = $true
        } else {
          $tried = 0
          foreach ($ip in ($peerIps | Sort-Object)) {
            $tried++; if ($tried -gt 3) { break }
            $pg = Invoke-Bounded $tsExe "ping -c 1 --timeout 3s --until-direct=false $ip" 5000
            if ($pg.Rc -eq 0) { $peersAns = $true; break }
          }
        }
      }
      if ($backend -notin @('stopped', 'login') -and -not ($backend -eq 'running' -and $peersAns)) {
        $ctrlUrl = 'https://controlplane.tailscale.com'
        $dp = Invoke-Bounded $tsExe 'debug prefs' 5000
        if ($dp.Rc -eq 0 -and $dp.Stdout.Trim().StartsWith('{')) {
          try {
            $u = "$(($dp.Stdout | ConvertFrom-Json).ControlURL)"
            if ($u) { $ctrlUrl = $u }
          } catch { }
        }
        try {
          $req = [Net.HttpWebRequest]::Create($ctrlUrl)
          $req.Method = 'HEAD'
          $req.Timeout = 5000
          $req.ReadWriteTimeout = 5000
          $req.AllowAutoRedirect = $false
          try {
            $resp = [Net.HttpWebResponse]$req.GetResponse()
            $code = [int]$resp.StatusCode
            $resp.Close()
          } catch [Net.WebException] {
            if ($_.Exception.Response) {
              $code = [int]([Net.HttpWebResponse]$_.Exception.Response).StatusCode
              $_.Exception.Response.Close()
            } else { $code = 0 }
          }
          if ($code -ge 200 -and $code -lt 500) { $ctrl = 'answers' }
          elseif ($code -ge 500 -and $code -lt 600) { $ctrl = 'error' }
        } catch { $ctrl = 'silent' }
      } else {
        $ctrl = 'answers'
      }
      $obs = New-Object TailObs($backend, $selfOn, $peersAns, $ctrl)
      $tailWhy = "backend=$backend self=$selfOn peers=$peersAns control=$ctrl"
    }
    $tail = [NovaWslWatch]::Classify($obs)

    $worker = (@([Diagnostics.Process]::GetProcessesByName('Runner.Worker')).Count -gt 0)
    $hold = (Test-Path -LiteralPath $holdDir) -and (@(Get-ChildItem -LiteralPath $holdDir -Force -ErrorAction SilentlyContinue).Count -gt 0)

    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $rawState = if (Test-Path -LiteralPath $stateFile -PathType Leaf) { [IO.File]::ReadAllText($stateFile) } else { '' }
    $bootId = Get-KernelBootId $rawState
    $corrupt = $false
    $cur = [NovaWslWatch]::ParseState($rawState, $bootId, [ref]$corrupt)
    if ($corrupt) { Write-WatchLog 'STATE reset corrupt state file' }
    if ($cur.Halted -and -not (Test-Path -LiteralPath $alertFile)) {
      Write-WatchLog 'RESUMED alert file removed'
      $cur.Outage = 0; $cur.Halted = $false
    }
    $rebLines = if (Test-Path -LiteralPath $rebootsFile -PathType Leaf) { @([IO.File]::ReadAllLines($rebootsFile)) } else { @() }
    $keptRebs = [NovaWslWatch]::PruneWindow($rebLines, $now, [long]$cfg.windowSeconds)
    $cur.Reboots = $keptRebs.Length
    $cur.Alerted = Test-Path -LiteralPath $alertFile

    $next = [NovaWslWatch]::Step($cur, $wslOk, $tail, $worker, $hold,
      [int]$cfg.threshold, [int]$cfg.backoff, [int]$cfg.busyBackoff, [int]$cfg.maxReboots, [int]$cfg.busyWaitLimit)

    function Save-WatchState([WatchState]$st, [long[]]$rebs) {
      [IO.File]::WriteAllText($stateFile, [NovaWslWatch]::FormatState($st, $bootId) + "`n", (New-Object System.Text.UTF8Encoding $false))
      $rText = if ($rebs.Length -gt 0) { (($rebs | ForEach-Object { "$_" }) -join "`n") + "`n" } else { '' }
      [IO.File]::WriteAllText($rebootsFile, $rText, (New-Object System.Text.UTF8Encoding $false))
    }
    function Set-Alert([string]$kind, [string]$detail) {
      $old = ''
      if (Test-Path -LiteralPath $alertFile -PathType Leaf) {
        $first = (Get-Content -LiteralPath $alertFile -TotalCount 1 -ErrorAction SilentlyContinue)
        if ($first) { $old = ($first -split '\s+')[0] }
      }
      if ($old -ne $kind) { Write-WatchLog "ALERT kind=$kind $detail" }
      [IO.File]::WriteAllText($alertFile, "$kind $([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')) $detail`n", (New-Object System.Text.UTF8Encoding $false))
    }

    if ($wslOk -and $tail -eq 'ok') {
      if ($cur.Rung -ne 0 -or $cur.Misses -ge [int]$cfg.threshold -or $cur.Alerted) {
        Write-WatchLog "RECOVERED misses=$($cur.Misses) rung=$($cur.Rung) busywaits=$($cur.BusyWaits) outage=$($cur.Outage)"
      }
      Remove-Item -LiteralPath $alertFile -Force -ErrorAction SilentlyContinue
      Save-WatchState $next $keptRebs
      exit 0
    }

    if ($next.Misses -gt $cur.Misses -and $next.Misses -eq [int]$cfg.threshold) {
      Write-WatchLog "MISS threshold=$($next.Misses) wsl=$wslOk ($wslWhy) tail=$tail ($tailWhy)"
    }

    if ($next.Rung -eq 1 -and $cur.Rung -eq 0) {
      Save-WatchState $next $keptRebs
      if ([NovaWslWatch]::Rung1Wsl($wslOk)) {
        Write-WatchLog "RUNG 1 restart-wsl ($wslWhy)"
        if (-not $Dry) {
          # Shut down WSL before ending the keepalive task so donorPid (if keepalive owns it) is still alive, with S4U fallback if donorPid is 0.
          [void][NovaWslWatch]::RunAsOwner($donorPid, "$($cfg.ownerSid)", $winUser, $winDomain, "`"$wslExe`" --shutdown", 15000)
          [void](Invoke-Bounded 'C:\Windows\System32\schtasks.exe' "/End /TN `"$kaTask`"" 5000)
          foreach ($pn in 'wsl', 'wslhost', 'wslrelay') {
            foreach ($pr in @([Diagnostics.Process]::GetProcessesByName($pn))) {
              try { $pr.Kill() } catch { }
            }
          }
          [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' 'stop WslService' 10000)
          foreach ($pr in @([Diagnostics.Process]::GetProcessesByName('wslservice'))) {
            try { $pr.Kill() } catch { }
          }
          [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' 'start WslService' 10000)
          [void](Invoke-Bounded 'C:\Windows\System32\schtasks.exe' "/Run /TN `"$kaTask`"" 5000)
        }
      }
      if ([NovaWslWatch]::Rung1Tail($tail)) {
        Write-WatchLog "RUNG 1 restart-tailscale ($tailWhy)"
        if (-not $Dry) {
          [void](Invoke-Bounded 'C:\Windows\System32\ipconfig.exe' '/flushdns' 5000)
          [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' 'stop Tailscale' 10000)
          [void](Invoke-Bounded 'C:\Windows\System32\sc.exe' 'start Tailscale' 10000)
        }
      }
      exit 0
    }

    if ($next.Halted -and -not $cur.Halted) {
      Set-Alert 'halt' "reboot budget spent ($($cur.Reboots) in $($cfg.windowSeconds)s, $($cur.Outage) in this outage, max $($cfg.maxReboots))"
    } elseif ($wslOk -and $tail -eq 'remote' -and $next.Alerted) {
      Set-Alert 'remote' "tailnet fault is remote ($tailWhy)"
    } elseif ($cur.Misses -ge [int]$cfg.threshold -and $cur.Wait -eq 0 -and $cur.Rung -eq 1 -and [NovaWslWatch]::Protected($wslOk, $worker, $hold)) {
      Write-WatchLog "DEFER reboot for protected work ($($next.BusyWaits)/$($cfg.busyWaitLimit) worker=$worker hold=$hold)"
      if ($next.Alerted) { Set-Alert 'busy' "reboot deferred $($cfg.busyWaitLimit) times (worker=$worker hold=$hold)" }
    } elseif ($next.Reboots -gt $cur.Reboots) {
      $newRebs = @($keptRebs) + @([long]$now)
      Save-WatchState $next ([long[]]$newRebs)
      Write-WatchLog "RUNG 2 reboot ($($next.Reboots)/$($cfg.maxReboots) in window, $($next.Outage)/$($cfg.maxReboots) in outage; wsl=$wslOk tail=$tail)"
      if (-not $Dry) {
        [void](Invoke-Bounded 'C:\Windows\System32\shutdown.exe' '/r /t 5 /f /c "nova-fleet wsl-watchdog: rung 2 reboot"' 10000)
      }
      exit 0
    }

    Save-WatchState $next $keptRebs
  }
}
