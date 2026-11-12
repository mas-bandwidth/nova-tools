//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// windows_runners_functional_test.go runs the windows-runners play with ansible-playbook on localhost against a fake
// powershell.exe and a fake gh (shell scripts in a temp dir), so the play's own Jinja and flow decide: which runners are
// whole and which are half registrations to repair. Nothing reaches Windows or GitHub.

// fakePowershell answers the role's calls from files: reg (dirs holding .runner), svc (dirs with a service), and
// gh-<path> (GitHub's list per target, "<name> <status> <busy>"). register logs REGISTER <name> repair=<yes|no>.
const fakePowershell = `#!/bin/sh
d=$(dirname "$0")/..
log=$d/calls.log
arg() { k=$1; shift; while [ $# -gt 0 ]; do [ "$1" = "$k" ] && { printf '%s' "$2"; return; }; shift; done; }
case "$*" in
*-File*) printf 'CALL %s\n' "$(arg -File "$@")" >>"$log" ;;
esac
case "$*" in
*-NoLogo*-EncodedCommand*)
	printf 'STAGECHECK\n' >>"$log"
	if [ -f "$d/stageopen" ] && [ "$(grep -c '^STAGECHECK$' "$log")" -ge "$(cat "$d/stageopen")" ]; then
		printf 'STAGE-OPEN C:\\ sid=S-1-5-32-545: nothing was run\r\n'; exit 14
	fi
	mkdir -p "$d/stage"; exit 0 ;;
*-EncodedCommand*)
	[ -f "$d/reg" ] && while IFS= read -r l; do printf 'REG %s\r\n' "$l"; done <"$d/reg"
	[ -f "$d/svc" ] && while IFS= read -r l; do printf 'SVC %s\r\n' "$l"; done <"$d/svc"
	exit 0 ;;
*"-Action register"*)
	IFS= read -r tok
	dir=$(arg -Dir "$@"); name=$(arg -Name "$@"); url=$(arg -Url "$@")
	repair=no
	grep -qxF "$dir" "$d/reg" 2>/dev/null && { repair=yes; printf 'REPAIR %s\r\n' "$dir"; }
	printf 'REGISTER %s repair=%s toklen=%s\n' "$name" "$repair" "${#tok}" >>"$log"
	grep -qxF "$dir" "$d/reg" 2>/dev/null || printf '%s\n' "$dir" >>"$d/reg"
	grep -qxF "$dir" "$d/svc" 2>/dev/null || printf '%s\n' "$dir" >>"$d/svc"
	p=$(printf '%s' "$url" | sed 's|^https://github.com/||')
	case $p in */*) f=$d/gh-repos-$(printf '%s' "$p" | tr / -) ;; *) f=$d/gh-orgs-$p ;; esac
	grep -q "^$name " "$f" 2>/dev/null || printf '%s online false\n' "$name" >>"$f"
	printf 'REGISTERED %s\r\n' "$name"; exit 0 ;;
*"-Action keepalive"*)
	printf 'KEEPALIVE machine=%s user=%s\n' "$(arg -MachineId "$@")" "$(arg -User "$@")" >>"$log"
	[ -f "$d/nodistro" ] && { printf 'NO-DISTRO machine-id=%s running=-\r\n' "$(arg -MachineId "$@")"; exit 6; }
	printf 'KEEPALIVE distro-a set,started task=WSL2 keepalive (distro-a)\r\n'; exit 0 ;;
*"-Action install"*|*"-Action ensure"*) printf 'OK\r\n'; exit 0 ;;
*" apply "*) printf 'CI-RULES x hooks=ok restart=no\r\n'; exit 0 ;;
esac
printf 'UNKNOWN %s\n' "$*"; exit 9
`

const fakeGh = `#!/bin/sh
d=$(dirname "$0")/..
case "$*" in
*registration-token*) echo TOKENTOKENTOKENTOKEN12345; exit 0 ;;
esac
p=$(printf '%s' "$2" | sed 's|/actions/runners$||')
case $p in repos/*) f=$d/gh-repos-$(printf '%s' "${p#repos/}" | tr / -) ;; *) f=$d/gh-orgs-${p#orgs/} ;; esac
[ -f "$f" ] && cat "$f"
exit 0
`

const (
	rn1  = `C:\actions-runner\mas-bandwidth-rocketnet-1`
	rn2  = `C:\actions-runner\mas-bandwidth-rocketnet-2`
	rn3  = `C:\actions-runner\mas-bandwidth-rocketnet-3`
	rgd1 = `C:\actions-runner\org-robotgamedevelopers-1`
	rgd2 = `C:\actions-runner\org-robotgamedevelopers-2`
)

var allDirs = []string{rn1, rn2, rn3, rgd1, rgd2}

const captainRecord = `
      nova_windows_runners:
        - {url: https://github.com/mas-bandwidth/rocketnet, count: 3, labels: [rocketnet-windows]}
        - {url: https://github.com/robotgamedevelopers, count: 2, labels: [fleet]}`

type winRig struct{ dir string }

func newWinRig(t *testing.T, record string, files map[string]string) winRig {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	r := winRig{t.TempDir()}
	for name, body := range map[string]string{"bin/powershell.exe": fakePowershell, "bin/gh": fakeGh} {
		r.write(t, name, body, 0o755)
	}
	r.write(t, "interop", "", 0o644)
	r.write(t, "inv.yml", "all:\n  hosts:\n    captain:\n      ansible_connection: local\n"+
		"      ansible_python_interpreter: \"{{ ansible_playbook_python }}\""+record+"\n  children:\n    bench: {hosts: {captain: {}}}\n", 0o644)
	r.write(t, "vars.yml", "github_runner_wsl_interop: "+r.dir+"/interop\ngithub_runner_wsl_powershell: "+r.dir+"/bin/powershell.exe\n"+
		"github_runner_wsl_stage_unix: "+r.dir+"/stage\ngithub_runner_gh: "+r.dir+"/bin/gh\n"+
		"github_runner_wsl_machine_id_file: "+r.dir+"/machine-id\n", 0o644)
	r.write(t, "machine-id", "0123456789abcdef0123456789abcdef\n", 0o644)
	for name, body := range files {
		r.write(t, name, body, 0o644)
	}
	return r
}

func (r winRig) write(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// play runs windows-runners.yml from this directory with the rig's inventory; it returns ansible's output and the
// fakes' call log.
func (r winRig) play(t *testing.T, extra ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	wd, _ := os.Getwd() // ignored: the test runs in fleet/
	args := append([]string{"-i", filepath.Join(r.dir, "inv.yml"), filepath.Join(wd, "windows-runners.yml"), "-e", "@" + filepath.Join(r.dir, "vars.yml")}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", args...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_STDOUT_CALLBACK=default")
	out, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(filepath.Join(r.dir, "calls.log")) // ignored: no call is no log
	return string(out), string(log), err
}

func lines(s ...string) string { return strings.Join(s, "\n") + "\n" }

func ghLine(names ...string) string {
	var out []string
	for _, n := range names {
		out = append(out, n+" online false")
	}
	return lines(out...)
}

var (
	ghRocketnet = ghLine("captain-win-mas-bandwidth-rocketnet-1", "captain-win-mas-bandwidth-rocketnet-2", "captain-win-mas-bandwidth-rocketnet-3")
	ghRgd       = ghLine("captain-win-org-robotgamedevelopers-1", "captain-win-org-robotgamedevelopers-2")
)

func TestWindowsRunnersPlayRepairsHalfRegistrations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string // the REGISTER lines the run must log, in order
	}{
		{".runner there, GitHub lacks it: repaired", map[string]string{"reg": lines(allDirs...), "svc": lines(allDirs...),
			"gh-repos-mas-bandwidth-rocketnet": ghLine("captain-win-mas-bandwidth-rocketnet-2", "captain-win-mas-bandwidth-rocketnet-3"),
			"gh-orgs-robotgamedevelopers":      ghRgd},
			[]string{"REGISTER captain-win-mas-bandwidth-rocketnet-1 repair=yes"}},
		{".runner there, no service: repaired", map[string]string{"reg": lines(allDirs...), "svc": lines(rn1, rn2, rn3, rgd1),
			"gh-repos-mas-bandwidth-rocketnet": ghRocketnet, "gh-orgs-robotgamedevelopers": ghRgd},
			[]string{"REGISTER captain-win-org-robotgamedevelopers-2 repair=yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newWinRig(t, captainRecord, tc.files)
			out, log, err := r.play(t)
			if err != nil {
				t.Fatalf("play failed: %v\n%s", err, out)
			}
			var got []string
			for _, l := range strings.Split(log, "\n") {
				if strings.HasPrefix(l, "REGISTER ") {
					got = append(got, l[:strings.Index(l, " toklen=")])
					if !strings.HasSuffix(l, " toklen=25") {
						t.Errorf("the token did not reach register on stdin: %s", l)
					}
				}
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("registrations:\n got %q\nwant %q", got, tc.want)
			}
			if strings.Contains(log, "TOKENTOKEN") {
				t.Error("the token reached an argument of a fake")
			}
			if !strings.Contains(out, "GITHUB-RUNNERS host=captain windows=5 registered=5 online=5") {
				t.Errorf("no whole receipt:\n%s", out)
			}
		})
	}
}

// TestFunctionalWSLKeepalive: the play holds every WSL machine running from boot, whether or not its record names
// windows_runners: the keepalive verb gets this distro's machine-id and the login, the receipt names the distro, a
// machine whose distro cannot be found fails the run, and --check changes nothing and calls nothing on Windows.
func TestFunctionalWSLKeepalive(t *testing.T) {
	t.Parallel()
	const keepalive = "KEEPALIVE machine=0123456789abcdef0123456789abcdef user="
	for _, tc := range []struct {
		name, record string
		files        map[string]string
		check, fail  bool
	}{
		{"no windows_runners", "", nil, false, false},
		{"with windows_runners", captainRecord, map[string]string{"reg": lines(allDirs...), "svc": lines(allDirs...),
			"gh-repos-mas-bandwidth-rocketnet": ghRocketnet, "gh-orgs-robotgamedevelopers": ghRgd}, false, false},
		{"distro not found", "", map[string]string{"nodistro": ""}, false, true},
		{"check", "", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newWinRig(t, tc.record, tc.files)
			var extra []string
			if tc.check {
				extra = append(extra, "--check")
			}
			out, log, err := r.play(t, extra...)
			if tc.fail {
				if err == nil || !strings.Contains(out, "NO-DISTRO") {
					t.Fatalf("a distro that cannot be found must fail the run with NO-DISTRO:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("play failed: %v\n%s", err, out)
			}
			if tc.check {
				if strings.Contains(log, "KEEPALIVE") || !strings.Contains(out, "KEEPALIVE-PLAN captain") {
					t.Errorf("--check must print the plan and call nothing:\nlog %q\n%s", log, out)
				}
				return
			}
			if !strings.Contains(log, keepalive) {
				t.Errorf("the keepalive verb was not called with the machine-id and the login:\n%q", log)
			}
			if !strings.Contains(out, "KEEPALIVE captain distro-a set,started") {
				t.Errorf("no keepalive receipt:\n%s", out)
			}
		})
	}
}

// TestFunctionalStageCheckGuardsEveryElevatedCall: the play stages the scripts it runs elevated and proves the stage
// (files/stage-check.ps1) right before each elevated call: every CALL in the fake's log comes straight after a
// STAGECHECK and the scripts are staged; and whichever check refuses (STAGE-OPEN; the fake refuses from its nth on),
// the run fails and nothing elevated runs after it.
func TestFunctionalStageCheckGuardsEveryElevatedCall(t *testing.T) {
	t.Parallel()
	files := map[string]string{"reg": lines(allDirs...), "svc": lines(allDirs...),
		"gh-repos-mas-bandwidth-rocketnet": ghRocketnet, "gh-orgs-robotgamedevelopers": ghRgd}
	r := newWinRig(t, captainRecord, files)
	out, log, err := r.play(t)
	if err != nil {
		t.Fatalf("play failed: %v\n%s", err, out)
	}
	prev, calls := "", 0
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "CALL ") {
			calls++
			if prev != "STAGECHECK" {
				t.Errorf("%q ran without the stage proved right before it (after %q)", l, prev)
			}
		}
		if l != "" && !strings.HasPrefix(l, "REGISTER ") && !strings.HasPrefix(l, "KEEPALIVE ") {
			prev = l
		}
	}
	if calls < 3 {
		t.Errorf("only %d elevated calls logged:\n%s", calls, log)
	}
	for _, f := range []string{"runner-windows.ps1", "nova-ci-rules.ps1", "job-started.ps1", "job-completed.ps1"} {
		if _, err := os.Stat(filepath.Join(r.dir, "stage", f)); err != nil {
			t.Errorf("%s was not staged: %v", f, err)
		}
	}
	// the make, the proof after staging, the first call's guard and the last call's guard (each run costs a play; the
	// guard on every call is TestStageGuardsEveryElevatedCall's)
	checks := strings.Count(log, "STAGECHECK\n")
	if checks < 4 {
		t.Fatalf("only %d checks:\n%s", checks, log)
	}
	for _, n := range []int{1, 2, 3, checks} {
		t.Run(fmt.Sprintf("a refusal at check %d of %d", n, checks), func(t *testing.T) {
			t.Parallel()
			r := newWinRig(t, captainRecord, files)
			r.write(t, "stageopen", fmt.Sprintf("%d\n", n), 0o644)
			out, log, err := r.play(t)
			if err == nil || !strings.Contains(out, "STAGE-OPEN") {
				t.Fatalf("a stage refusal must fail the run and say STAGE-OPEN:\n%s", out)
			}
			seen := 0
			for _, l := range strings.Split(log, "\n") {
				if l == "STAGECHECK" {
					seen++
				}
				if strings.HasPrefix(l, "CALL ") && seen >= n {
					t.Errorf("%q ran after check %d refused:\n%s", l, n, log)
				}
			}
			_, err = os.Stat(filepath.Join(r.dir, "stage", "runner-windows.ps1"))
			if n == 1 && err == nil {
				t.Error("a script was staged into a stage the first check refused")
			}
		})
	}
}
