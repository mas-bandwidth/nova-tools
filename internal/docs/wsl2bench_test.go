package docs

import (
	"os"
	"strings"
	"testing"
)

// wsl2bench_test.go holds tools/bench-wsl2.ps1 against the one-step bootstrap
// the Threadripper asked for (#1458) and against the fleet page that has to name
// it.
//
// The Windows box joins the fleet as a LINUX machine under WSL2 (Glenn,
// 2026-09-18: "drop the native windows CI runners. WSL only from now on."), so
// docs/BENCH-STANDARD-WINDOWS.md is parked: the host half of the standard --
// winget, the .wslconfig memory ceiling, the mirrored network, the startup task
// that brings sshd back with nobody logged in, the distro-side user, sudo,
// systemd and the Go SDK -- is provisioned by tools/bench-wsl2.ps1 and by
// nothing else. Before it there was no file at all, and the setup was counted
// by hand at about twenty hands from Glenn: winget, `wsl --install`, the
// interactive user prompt, .wslconfig, one admin paste, an apt + Go +
// git-identity paste inside the distro, `gh auth login`, `wsl --set-default`,
// the seat key twice, and four pushes past a hook.
//
// It reads the script as text and runs nothing; a Windows box is not on this
// host and the script is not executable here.

const (
	wsl2BootstrapPath = "../../tools/bench-wsl2.ps1"
	wsl2FleetSpecPath = "../../docs/spec-pulse/03-fleet.md"
)

func TestWSL2BootstrapScriptCarriesTheOneStepSetup(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(wsl2BootstrapPath)
	if err != nil {
		t.Fatalf("tools/bench-wsl2.ps1 is not in the tree: the WSL2 Windows setup is still about twenty hands (winget, wsl --install, the interactive user prompt, .wslconfig, the admin paste, the apt + Go paste inside the distro, wsl --set-default) because there is no single script to run once from an elevated PowerShell: %v", err)
	}
	script := string(raw)

	// The host half: Tailscale, the clock, the power setting that lets the box
	// wake, the firewall, the .wslconfig, and the startup task that restores
	// sshd after a reboot with nobody at the keyboard.
	for _, want := range []string{
		"winget install",
		"Tailscale.Tailscale",
		"tailscale up",
		"w32tm",
		"time.windows.com",
		"powercfg /h off",
		"Set-NetFirewallHyperVVMSetting",
		"-DefaultInboundAction Allow",
		".wslconfig",
		"memory=",
		"networkingMode=mirrored",
		"Register-ScheduledTask",
		"-AtStartup",
		"systemctl start ssh",
		// The distro half: the user, sudo, systemd, the packages, sshd, the Go
		// SDK under ~/sdk, and the git identity a commit needs.
		"wsl --install -d",
		"Ubuntu-24.04",
		"--no-launch",
		"useradd -m -s /bin/bash nova",
		"/etc/sudoers.d/nova",
		"NOPASSWD",
		"/etc/wsl.conf",
		"systemd=true",
		"default=nova",
		"/etc/environment",
		".local/bin",
		"openssh-server",
		"build-essential",
		"sbcl",
		"redis-tools",
		"systemctl enable --now ssh",
		"go.mod",
		"sdk",
		"Rowan Claude",
		"rowan@mas-bandwidth.com",
		// The public half of the fleet's keys is carried in the repo and read,
		// never written back.
		"authorized_keys",
		"fleet/authorized_keys",
		// The tail: the default distro, the CHECK grammar the fleet parses, and
		// a green exit only when sshd answers.
		"wsl --set-default",
		"CHECK`t",
		"sshd",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("tools/bench-wsl2.ps1 does not carry %q; the one-step bootstrap is missing a step the issue had to do by hand", want)
		}
	}

	// The auth key is the single approval and it is a secret: it may be a
	// parameter and it may reach tailscale, but it never reaches a printed line.
	for _, leak := range []string{"Write-Host $AuthKey", "Write-Output $AuthKey", "echo $AuthKey", "Write-Host $authKey"} {
		if strings.Contains(script, leak) {
			t.Errorf("tools/bench-wsl2.ps1 prints the Tailscale auth key (%q); it is the one secret at the one approval and belongs in no line", leak)
		}
	}

	// And the fleet page a person reads first has to name the script, or the
	// next WSL2 box is provisioned from the twenty hands again.
	spec, err := os.ReadFile(wsl2FleetSpecPath)
	if err != nil {
		t.Fatalf("%s: %v", wsl2FleetSpecPath, err)
	}
	if !strings.Contains(string(spec), "tools/bench-wsl2.ps1") {
		t.Errorf("%s does not name tools/bench-wsl2.ps1; the page that says the Threadripper joins as a Linux machine must name the one script that provisions it", wsl2FleetSpecPath)
	}
}
