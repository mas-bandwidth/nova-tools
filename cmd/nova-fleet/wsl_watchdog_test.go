package main

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestWSLKeepaliveGrantsBatchLogonAndPrefersProgramFilesWsl: an S4U scheduled task at cold boot (nobody signed in) needs
// SeBatchLogonRight on its principal and the machine-wide C:\Program Files\WSL\wsl.exe binary rather than a per-user stub;
// its wsl.exe --list probe is bounded by WaitForExit, and ensure clears DelayedAutostart on runner services.
func TestWSLKeepaliveGrantsBatchLogonAndPrefersProgramFilesWsl(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`C:\Program Files\WSL\wsl.exe`,
		`ReadToEndAsync()`,
		`WaitForExit(`,
		`SeBatchLogonRight`,
		`<RunLevel>HighestAvailable</RunLevel>`,
		`DelayedAutostart`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("runner-windows.ps1 lacks %q", want)
		}
	}
	if strings.Contains(s, "& wsl.exe") {
		t.Error("runner-windows.ps1 runs an unbounded & wsl.exe")
	}
	if strings.Contains(s, "StandardOutput.ReadToEnd()") {
		t.Error("runner-windows.ps1 calls synchronous StandardOutput.ReadToEnd() before WaitForExit")
	}
}

// TestWSLCIRulesProcessQueryIsBounded: job-started.ps1 runs before a job's 2-minute watchdog is armed, so its WMI
// process queries never read every process's PEB CommandLine for a name lookup and always carry -OperationTimeoutSec.
func TestWSLCIRulesProcessQueryIsBounded(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/ci-rules/nova-ci-rules.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`-Property ProcessId,ParentProcessId,Name,CreationDate`,
		`-OperationTimeoutSec`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("nova-ci-rules.ps1 lacks %q", want)
		}
	}
}

// TestWSLWatchdogAndHostReliability: every WSL machine gets the SYSTEM self-heal watchdog (PT5M ceiling, never PT0S),
// unattended Windows power/crash/Tailscale settings, and out-of-band Windows OpenSSH on port 2222 restricted to the
// tailnet.
func TestWSLWatchdogAndHostReliability(t *testing.T) {
	t.Parallel()
	playBytes, err := fs.ReadFile(tree, "windows-runners.yml")
	if err != nil {
		t.Fatal(err)
	}
	play := string(playBytes)
	i := strings.Index(play, "tasks_from: wsl-watchdog.yml")
	if i < 0 {
		t.Fatal("windows-runners.yml does not include wsl-watchdog.yml")
	}
	if j := strings.Index(play[i:], "\n    - name:"); (j >= 0 && strings.Contains(play[i:i+j], "nova_windows_runners")) ||
		(j < 0 && strings.Contains(play[i:], "nova_windows_runners")) {
		t.Error("wsl-watchdog.yml must run on every WSL machine, not only those with windows_runners")
	}
	taskBytes, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-watchdog.yml")
	if err != nil {
		t.Fatal(err)
	}
	task := string(taskBytes)
	for _, want := range []string{"WATCHDOG-PLAN", "WINSSH-PLAN", "-Action install", "-WinSshPort", "ansible_pipelining"} {
		if !strings.Contains(task, want) {
			t.Errorf("wsl-watchdog.yml lacks %q", want)
		}
	}
	psBytes, err := fs.ReadFile(tree, roleDir+"/files/wsl-watchdog.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := string(psBytes)
	for _, want := range []string{
		"S-1-5-18",
		"<ExecutionTimeLimit>PT5M</ExecutionTimeLimit>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<Interval>PT1M</Interval>",
		`C:\nova-watchdog`,
		"WaitForExit(",
		"standby-timeout-ac",
		"hibernate-timeout-ac",
		"AutoReboot",
		"DontShowUI",
		"--unattended",
		"PasswordAuthentication no",
		"administrators_authorized_keys",
		"100.64.0.0",
		"fd7a:115c:a1e0::/48",
		"AuthorizedKeysFile .ssh/authorized_keys",
		"Seal-StrictFile",
		"NormalizeFirewallAddr",
		"ResolveStateBootId",
		"LoadUserProfileW",
		"LsaLogonUser",
		"PrefetchParameters",
		"shutdown.exe",
		"Runner.Listener.exe",
		"/flushdns",
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("wsl-watchdog.ps1 lacks %q", want)
		}
	}
	if strings.Contains(ps, "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>") {
		t.Error("wsl-watchdog.ps1 uses PT0S ExecutionTimeLimit; a hung OS call would wedge IgnoreNew forever")
	}
	if strings.Contains(ps, "& wsl.exe") {
		t.Error("wsl-watchdog.ps1 runs an unbounded & wsl.exe")
	}
	if strings.Contains(ps, "<LogonType>ServiceAccount</LogonType>") {
		t.Error("wsl-watchdog.ps1 emits <LogonType>ServiceAccount</LogonType>, which violates Task Scheduler task.xsd (0x8004131A)")
	}
	if strings.Contains(ps, "GetOwnerSid") {
		t.Error("wsl-watchdog.ps1 uses WMI GetOwnerSid instead of in-process kernel snapshot")
	}
	for _, name := range []string{"captain", "robotgamedevelopers", "actions-runner-r", "nova-wsl-keepalive"} {
		if strings.Contains(strings.ToLower(ps), name) {
			t.Errorf("wsl-watchdog.ps1 ships deployment-specific name %q", name)
		}
	}
}

// TestTailnetPolicyAllowsControlOutOfBandWindowsSSH: tag:control may reach tcp:2222 on tag:nova and tag:runner for
// out-of-band Windows OpenSSH, while neither tag:nova nor tag:runner gets tcp:2222 to peers or to tag:control.
func TestTailnetPolicyAllowsControlOutOfBandWindowsSSH(t *testing.T) {
	t.Parallel()
	has := func(list any, v string) bool { l, _ := list.([]any); return slices.Contains(l, any(v)) }
	p := fleetPolicy()
	controlToWin := false
	for _, r := range p["grants"].([]any) {
		g := r.(map[string]any)
		if has(g["src"], "tag:control") && has(g["dst"], "tag:nova") && has(g["dst"], "tag:runner") && has(g["ip"], "tcp:2222") {
			controlToWin = true
		}
		if (has(g["src"], "tag:nova") || has(g["src"], "tag:runner")) && has(g["ip"], "tcp:2222") {
			t.Errorf("grant %s opens tcp:2222 to a worker machine tag", canon(g))
		}
	}
	if !controlToWin {
		t.Error("tailscale-acl.json does not grant tag:control tcp:2222 on tag:nova and tag:runner")
	}
}
