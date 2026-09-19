package pulse

// `fleet standard` is tools/bench-standard.sh and the Mac bench's provisioning standard,
// run as a verb over ssh (SPEC-PULSE ## Fleet: "fleet standard is the standard itself, run
// locally or over ssh, whose checks are the contract below"). It retires
// scripts/bench-standard.sh, which refused to run anywhere but ON a Linux bench and could
// say nothing at all about a Mac one.
//
// The checks are DATA, one table per operating system, so the standard is read rather than
// traced through a shell script: each check is a one-line probe and the answer it must
// give. The Go side is the only place a STANDARD line is formatted, and the only place a
// verdict is decided; the remote side prints `CHECK<TAB>name<TAB>value` and nothing else.
//
// One ssh child per bench, bounded by --timeout, and ssh comes from --ssh: a test puts a
// fake on PATH and no test reaches a machine.

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The matchers a check may ask for. They are strings rather than functions so the whole
// table is data a test (and a reader) can hold against the standard.
const (
	MatchEquals    = "equals"   // the value is exactly Want
	MatchContains  = "contains" // the value holds Want
	MatchNonempty  = "nonempty" // the value is not empty
	MatchNotEquals = "not"      // the value is anything but Want
	MatchAtLeast   = "at-least" // the value is a number >= Want
)

// StandardCheck is one line of the bench provisioning standard: what it is called, which
// benches it applies to, the one-line shell that reads the bench, and the answer the
// standard demands.
type StandardCheck struct {
	Name  string // the token the STANDARD line carries
	OS    string // "linux", "darwin", or "" for every bench
	Probe string // one line of POSIX shell printing the value on stdout
	Match string // one of the Match* constants
	Want  string // what Match compares against
	// Root, when set, is the TOOLCHAIN ROOT this check is the standard's own copy of, in the
	// slash form internal/swarm/toolchain.go declares it: HOME-relative (`sdk`) or absolute
	// (`/opt/homebrew/Cellar/go`). The wall grants exactly these paths, so the standard and
	// the wall are ONE LIST checked in both directions, per OS, by the class test in
	// internal/ci -- a root the wall grants that no bench provisions is a wall naming a path
	// that will not be there, and a root a bench has that the wall never names is the hurt of
	// 2026-09-18 returning. The KIND of each grant is the wall's decision and lives with the
	// wall, not here: a bench only has to HAVE a root.
	Root string
}

// FleetStandardChecks is the standard itself: the checks for one operating system, in the
// order they print. goWant is the Go toolchain the fleet is on, stamp is the nova bins'
// build stamp (empty means "report whatever this bench has"), and minFreeGB is the floor
// under HOME (launch refuses below 25 GB).
//
// The Linux list is tools/bench-standard.sh's toolchain half: the Go SDK, sbcl, the safe-rm
// helper every bench script sources, and the nova stamp. The darwin list is
// fleet/macos/provision-mac-bench.sh: the Go SDK and sbcl under ~/sdk, real git ahead of
// the Xcode shim, and each runner's .path carrying it (INSTALL-batman.md, 2026-09-18:
// /usr/bin/git is the shim and the sandbox cannot read /var/db/xcode_select_link).
//
// THE TOOLCHAIN ROOTS (the `toolchain-*` checks, Root set) are this table's half of the one
// list the sandbox wall grants -- internal/swarm/toolchain.go is the other half, and the
// class test in internal/ci fails when they drift apart in either direction, per OS. A LINUX
// root is DEMANDED, because the standard's own installer is what puts it there and a bench
// missing one kills every Go card on it. A DARWIN root is REPORTED and never drifted on: a
// Mac's toolchains are installed and on PATH rather than unpacked into a home, so which
// trees exist is the machine's shape -- a Mac with no .NET is a Mac with no .NET -- while
// that the bench has a working Go at all is what the `go` check above asserts. The wall
// skips an absent root either way, so the report is the wall's argv read in advance.
func FleetStandardChecks(goos, goWant, stamp string, minFreeGB int) []StandardCheck {
	if strings.TrimSpace(goWant) == "" {
		goWant = "go1.26.5"
	}
	stampMatch, stampWant := MatchNonempty, ""
	if strings.TrimSpace(stamp) != "" {
		stampMatch, stampWant = MatchContains, stamp
	}
	all := []StandardCheck{
		{
			Name: "go", OS: "linux", Match: MatchContains, Want: goWant,
			Probe: `for g in "$HOME"/sdk/go*/bin/go "$HOME/go/bin/go" "$(command -v go 2>/dev/null)"; do [ -x "$g" ] || continue; "$g" version; break; done`,
		},
		{
			Name: "go", OS: "darwin", Match: MatchContains, Want: goWant,
			Probe: `for g in "$HOME"/sdk/go*/bin/go; do [ -x "$g" ] || continue; "$g" version; break; done`,
		},
		{
			Name: "sbcl", OS: "linux", Match: MatchNonempty,
			Probe: `command -v sbcl 2>/dev/null || for s in "$HOME"/sdk/sbcl-*/bin/sbcl; do [ -x "$s" ] && echo "$s" && break; done`,
		},
		{
			Name: "sbcl", OS: "darwin", Match: MatchNonempty,
			Probe: `for s in "$HOME"/sdk/sbcl-*/bin/sbcl; do [ -x "$s" ] && echo "$s" && break; done`,
		},
		{
			Name: "safe-rm", OS: "linux", Match: MatchEquals, Want: "present",
			Probe: `[ -f "$HOME/.local/bin/safe-rm.sh" ] && echo present`,
		},
		{
			Name: "git-ahead-of-shim", OS: "darwin", Match: MatchNotEquals, Want: "/usr/bin/git",
			Probe: `command -v git 2>/dev/null`,
		},
		{
			Name: "runner-path", OS: "darwin", Match: MatchEquals, Want: "ok",
			Probe: `bad=""; for p in "$HOME"/runner-nova-tools-*/.path; do [ -f "$p" ] || continue; head -n 1 "$p" | grep -Eq '^/usr/bin(:|$)' && bad="$p"; done; [ -n "$bad" ] && echo "$bad" || echo ok`,
		},
		// The toolchain roots the sandbox wall grants a card, one check each.
		{
			Name: "toolchain-sdk", OS: "linux", Root: "sdk", Match: MatchEquals, Want: "present",
			Probe: `[ -d "$HOME/sdk" ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-modcache", OS: "linux", Root: "go/pkg/mod", Match: MatchEquals, Want: "present",
			Probe: `[ -d "$HOME/go/pkg/mod" ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-sdk", OS: "darwin", Root: "sdk", Match: MatchNonempty,
			Probe: `[ -d "$HOME/sdk" ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-modcache", OS: "darwin", Root: "go/pkg/mod", Match: MatchNonempty,
			Probe: `[ -d "$HOME/go/pkg/mod" ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-brew-go", OS: "darwin", Root: "/opt/homebrew/Cellar/go", Match: MatchNonempty,
			Probe: `case "$(readlink -f "$(command -v go 2>/dev/null)" 2>/dev/null)" in /opt/homebrew/Cellar/go/*) echo present;; *) echo absent;; esac`,
		},
		{
			Name: "toolchain-brew-sbcl", OS: "darwin", Root: "/opt/homebrew/Cellar/sbcl", Match: MatchNonempty,
			Probe: `case "$(readlink -f "$(command -v sbcl 2>/dev/null)" 2>/dev/null)" in /opt/homebrew/Cellar/sbcl/*) echo present;; *) echo absent;; esac`,
		},
		{
			Name: "toolchain-brew-openjdk", OS: "darwin", Root: "/opt/homebrew/opt/openjdk", Match: MatchNonempty,
			Probe: `[ -d /opt/homebrew/opt/openjdk ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-jvm", OS: "darwin", Root: "/Library/Java/JavaVirtualMachines", Match: MatchNonempty,
			Probe: `[ -d /Library/Java/JavaVirtualMachines ] && echo present || echo absent`,
		},
		{
			Name: "toolchain-dotnet", OS: "darwin", Root: "/usr/local/share/dotnet", Match: MatchNonempty,
			Probe: `[ -d /usr/local/share/dotnet ] && echo present || echo absent`,
		},
		{
			Name: "go", OS: "windows", Match: MatchContains, Want: goWant,
			Probe: `for g in "C:/sdk/go/bin/go.exe" "$HOME/sdk/go*/bin/go.exe" "$(command -v go 2>/dev/null)"; do [ -x "$g" ] || continue; "$g" version; break; done`,
		},
		{
			Name: "git", OS: "windows", Match: MatchContains, Want: "windows",
			Probe: `git version 2>/dev/null`,
		},
		{
			Name: "no-wsl", OS: "windows", Match: MatchEquals, Want: "ok",
			Probe: `[ -z "$WSL_DISTRO_NAME" ] && [ -z "$WSL_INTEROP" ] && echo ok`,
		},
		{
			Name: "features", OS: "windows", Match: MatchContains, Want: "Containers",
			Probe: `powershell.exe -NoProfile -Command '$h = (Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -ErrorAction SilentlyContinue).State; $c = (Get-WindowsOptionalFeature -Online -FeatureName Containers -ErrorAction SilentlyContinue).State; if ($h -eq "Enabled" -and $c -eq "Enabled") { Write-Output "Hyper-V+Containers" } else { Write-Output "disabled" }'`,
		},
		{
			Name: "runner-service", OS: "windows", Match: MatchContains, Want: "Running (nova)",
			Probe: `powershell.exe -NoProfile -Command '$s = Get-CimInstance Win32_Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -like "actions.runner.*" -and $_.State -eq "Running" -and ($_.StartName -match "(?i)(^|\\)nova$") }; if ($s) { Write-Output "Running (nova)" } else { Write-Output "stopped" }'`,
		},
		{
			Name: "wol", OS: "windows", Match: MatchEquals, Want: "enabled",
			Probe: `powershell.exe -NoProfile -Command '$w = Get-NetAdapterAdvancedProperty -DisplayName "*Wake*" -ErrorAction SilentlyContinue | Where-Object { $_.DisplayValue -match "Enabled" }; if ($w) { Write-Output "enabled" } else { Write-Output "disabled" }'`,
		},
		{
			Name: "nova-stamp", Match: stampMatch, Want: stampWant,
			Probe: `"$HOME/.local/bin/nova-swarm" version 2>/dev/null`,
		},
		{
			Name: "seat", Match: MatchEquals, Want: "1",
			Probe: `ls "$HOME"/.config/nova-secrets/*.key 2>/dev/null | wc -l | tr -d ' '`,
		},
		{
			Name: "disk-free", Match: MatchAtLeast, Want: strconv.Itoa(minFreeGB),
			Probe: `df -Pk "$HOME" 2>/dev/null | awk 'NR==2{printf "%d", $4/1048576}'`,
		},
		// The four the by-hand certification of 2026-09-18 added. Every one of them was
		// true of every Linux machine in the fleet that morning, and no check anywhere
		// could see any of them, because they are all faults of the NON-INTERACTIVE
		// environment -- the one an ssh, a card and a CI shard actually get -- and every
		// check we had ran in a login shell.
		{
			// Ubuntu's ~/.bashrc returns before any PATH line for a non-interactive shell
			// and ~/.profile is never read, so `ssh <bench> nova-merge version` answered
			// `command not found` on hulk, vision, space and mini while the same command
			// in a login shell worked.
			Name: "path-noninteractive", Match: MatchEquals, Want: "ok",
			Probe: `if printf '%s' ":$PATH:" | grep -Fq ":$HOME/.local/bin:"; then echo ok; else echo "$PATH"; fi`,
		},
		{
			// Eighteen `go install`-built nova-* binaries in ~/go/bin, answering v0.15.3,
			// AHEAD of the release in ~/.local/bin on the same PATH. This is also why
			// `release adopt` reported tools=0 skipped=21: it asked the binary in --bin,
			// which was current, while the shadow was what served PATH.
			Name: "gobin-shadow", Match: MatchEquals, Want: "0",
			Probe: `ls "$HOME"/go/bin/nova-* 2>/dev/null | wc -l | tr -d ' '`,
		},
		{
			// Empty on all four Linux machines. A card that commits in a clone with no
			// repo-local config fails at the commit, after the work is done.
			Name: "git-identity", Match: MatchNonempty,
			Probe: `git config --global --get user.email 2>/dev/null`,
		},
		{
			// A runner reads its PATH from the `.path` file beside it and from no shell at
			// all, so every fault there is invisible to every other check. space's sixteen
			// carried the bare distro PATH -- no go -- and batman's six led with
			// /usr/local/bin, where go is 1.25.5, below go.mod's line.
			Name: "runner-path-go", Match: MatchEquals, Want: "ok",
			Probe: `bad=""; for p in "$HOME"/runner-nova-tools-*/.path "$HOME"/actions-runner-*/.path; do [ -f "$p" ] || continue; PATH="$(head -n 1 "$p")" command -v go >/dev/null 2>&1 || bad="$bad $(basename "$(dirname "$p")")"; done; [ -n "$bad" ] && echo "no go on the .path of:$bad" || echo ok`,
		},
	}
	out := make([]StandardCheck, 0, len(all))
	for _, c := range all {
		if c.OS == "" || c.OS == goos {
			out = append(out, c)
		}
	}
	return out
}

// Met says whether one check's value meets the standard.
func (c StandardCheck) Met(value string) bool {
	v := strings.TrimSpace(value)
	switch c.Match {
	case MatchEquals:
		return v == c.Want
	case MatchContains:
		return strings.Contains(v, c.Want)
	case MatchNonempty:
		return v != ""
	case MatchNotEquals:
		return v != "" && v != c.Want
	case MatchAtLeast:
		got, err := strconv.Atoi(v)
		if err != nil {
			return false
		}
		want, err := strconv.Atoi(c.Want)
		if err != nil {
			return false
		}
		return got >= want
	}
	return false
}

// wants is the check's demand, as one token for the DRIFT line.
func (c StandardCheck) wants() string {
	if c.Match == MatchNonempty {
		return MatchNonempty
	}
	return c.Match + ":" + c.Want
}

// FleetStandardInput is everything `fleet standard` needs apart from flag parsing.
type FleetStandardInput struct {
	Benches   string // the fleet file: name<TAB>ssh target<TAB>home<TAB>mac
	Machines  string // the machines registry; a machine whose roles lack `bench` is refused
	Name      string // the one bench to check
	SSH       string // the ssh program; empty is "ssh"
	OS        string // "linux" or "darwin"; empty asks the bench with uname -s
	Go        string // the Go toolchain the fleet is on; empty is go1.26.5
	Want      string // the nova bins' stamp; empty reports what the bench has
	MinFreeGB int    // the floor on free space under HOME
	Timeout   time.Duration
	Max       int // at most this many lines; 0 is all
	Runner    FleetRunner
	Stdout    io.Writer
	Stderr    io.Writer
}

// FleetStandard runs the standard's checks on one bench and prints one
// `STANDARD <bench> <check> OK|DRIFT ...` line per check and one verdict line: exit 0 when
// every check is met, 2 when any drifted or the bench was refused, 3 when the bench could
// not be reached.
func FleetStandard(in FleetStandardInput) int {
	bench, code := fleetOneBench(in.Benches, in.Machines, in.Name, in.Stdout, in.Stderr, "standard")
	if code != 0 {
		return code
	}
	run := in.Runner
	if run == nil {
		run = SSHRunner{Program: in.SSH}
	}
	bound := fleetPowerTimeout(in.Timeout)

	goos := strings.TrimSpace(in.OS)
	if goos == "" {
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		out, err := run.Run(ctx, bench.SSH, `uname -s`)
		cancel()
		if err != nil {
			return fleetUnreachable(in.Stdout, bench.Name, fleetReason(out, err))
		}
		switch v := strings.ToLower(strings.TrimSpace(lastLine(out))); {
		case v == "linux":
			goos = "linux"
		case v == "darwin":
			goos = "darwin"
		case strings.HasPrefix(v, "mingw") || strings.HasPrefix(v, "msys") || strings.HasPrefix(v, "cygwin") || strings.HasPrefix(v, "windows"):
			goos = "windows"
		default:
			return fleetUnreachable(in.Stdout, bench.Name, "uname said "+oneline.Field(strings.TrimSpace(lastLine(out))))
		}
	}

	checks := FleetStandardChecks(goos, in.Go, in.Want, in.MinFreeGB)
	start := time.Now()
	fmt.Fprintf(in.Stderr, "STANDARD WALK bench=%s os=%s checks=%d\n", oneline.Field(bench.Name), oneline.Field(goos), len(checks))

	ctx, cancel := context.WithTimeout(context.Background(), bound)
	out, err := run.Run(ctx, bench.SSH, fleetStandardScript(bench.Home, checks))
	cancel()
	values, ok := fleetStandardValues(out)
	if err != nil && !ok {
		return fleetUnreachable(in.Stdout, bench.Name, fleetReason(out, err))
	}

	list := bounded.Capped(in.Stdout, in.Max, "STANDARD", "check",
		"run: nova-pulse fleet standard --benches <file> --bench "+bench.Name+" --max 0")
	drift := 0
	for _, c := range checks {
		value, answered := values[c.Name]
		if answered && c.Met(value) {
			list.Line(fmt.Sprintf("STANDARD %s %s OK got=%s", oneline.Field(bench.Name), c.Name, fleetValue(value)))
			continue
		}
		drift++
		list.Line(fmt.Sprintf("STANDARD %s %s DRIFT want=%s got=%s",
			oneline.Field(bench.Name), c.Name, oneline.Field(c.wants()), fleetValue(value)))
	}
	if drift == 0 {
		list.Line(fmt.Sprintf("FLEET %s STANDARD OK checks=%d", oneline.Field(bench.Name), len(checks)))
	} else {
		list.Line(fmt.Sprintf("FLEET %s STANDARD DRIFT drift=%d/%d", oneline.Field(bench.Name), drift, len(checks)))
	}
	list.More()
	fmt.Fprintf(in.Stderr, "STANDARD DONE bench=%s drift=%d/%d elapsed=%s\n",
		oneline.Field(bench.Name), drift, len(checks), time.Since(start).Round(time.Millisecond))
	if drift > 0 {
		return 2
	}
	return 0
}

// fleetValue renders a check's value as one token, "-" when the bench said nothing.
func fleetValue(v string) string {
	t := strings.TrimSpace(v)
	if t == "" {
		return "-"
	}
	return oneline.Field(oneline.Cap(t, 120))
}

// fleetStandardScript is the remote side: HOME set to the bench's home column, then one
// `CHECK<TAB>name<TAB>value` line per check. A probe that fails prints an empty value; the
// Go side decides what that means.
//
// NO `case` IN A PROBE. Every probe is spliced into `v=$( { <probe>; } ... )`, and bash 3.2
// -- which is the /bin/bash every Mac in this fleet has -- mis-parses the `)` that closes a
// case PATTERN as the `)` that closes the `$( )`. Bash 4 fixed it; macOS never shipped bash
// 4. The failure is silent and it is worse than a refusal: `bash -s` reads its script from
// the pipe in chunks, so everything before the bad line runs and prints, and every check
// AFTER it simply never happens. A darwin shard read six checks, four empty values and a
// clean exit. TestNoStandardProbeUsesCase keeps it from coming back.
func fleetStandardScript(home string, checks []StandardCheck) string {
	lines := []string{"HOME=" + fleetQuote(home), "export HOME", "LC_ALL=C", "export LC_ALL"}
	for _, c := range checks {
		lines = append(lines,
			`v=$( { `+c.Probe+`; } 2>/dev/null | head -n 1 | tr -d '\r' )`,
			`printf 'CHECK\t`+c.Name+`\t%s\n' "$v"`)
	}
	return strings.Join(lines, "\n")
}

// fleetStandardValues reads the CHECK lines back. It returns whether the bench answered
// with any at all, which is how an ssh that failed AFTER the script ran is told from one
// that never reached the bench.
func fleetStandardValues(out string) (map[string]string, bool) {
	values := map[string]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(raw, "\r"), "CHECK\t")
		if !ok {
			continue
		}
		name, value, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}
		values[name] = value
	}
	return values, len(values) > 0
}
