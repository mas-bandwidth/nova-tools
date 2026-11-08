//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// windows_runners_functional_test.go runs the windows-runners play with ansible-playbook on localhost against a fake
// powershell.exe and a fake gh (shell scripts in a temp dir), so the play's own Jinja and flow decide: which runners are
// whole, which are half registrations to repair, and when v1's retirement is refused. Nothing reaches Windows or GitHub.

// fakePowershell answers the role's calls from files: reg (dirs holding .runner), svc (dirs with a service), and
// gh-<path> (GitHub's list per target, "<name> <status> <busy>"). register logs REGISTER <name> repair=<yes|no>.
const fakePowershell = `#!/bin/sh
d=$(dirname "$0")/..
log=$d/calls.log
arg() { k=$1; shift; while [ $# -gt 0 ]; do [ "$1" = "$k" ] && { printf '%s' "$2"; return; }; shift; done; }
case "$*" in
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
*"-Action retire"*) IFS= read -r tok; printf 'RETIRE %s\n' "$(arg -Dir "$@")" >>"$log"; printf 'RETIRED x -> y\r\n'; exit 0 ;;
*"-Action install"*|*"-Action ensure"*|*"-Action aside"*|*"-Action untask"*) printf 'OK\r\n'; exit 0 ;;
*" apply "*) printf 'CI-RULES x hooks=ok restart=no\r\n'; exit 0 ;;
esac
printf 'UNKNOWN %s\n' "$*"; exit 9
`

const fakeGh = `#!/bin/sh
d=$(dirname "$0")/..
case "$*" in
*registration-token*|*remove-token*) echo TOKENTOKENTOKENTOKEN12345; exit 0 ;;
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
		"github_runner_wsl_stage_unix: "+r.dir+"/stage\ngithub_runner_wsl_ci_stage_unix: "+r.dir+"/cistage\ngithub_runner_gh: "+r.dir+"/bin/gh\n", 0o644)
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

func TestWindowsRunnersPlayRetireRefusals(t *testing.T) {
	t.Parallel()
	whole := map[string]string{"reg": lines(allDirs...), "svc": lines(allDirs...),
		"gh-repos-mas-bandwidth-rocketnet": ghRocketnet, "gh-orgs-robotgamedevelopers": ghRgd}
	rocketnetOnly := `
      nova_windows_runners:
        - {url: https://github.com/mas-bandwidth/rocketnet, count: 3, labels: [rocketnet-windows]}`
	busy := map[string]string{}
	for k, v := range whole {
		busy[k] = v
	}
	busy["gh-orgs-robotgamedevelopers"] = ghRgd + "captain-rgd-1 online false\ncaptain-rgd-2 online true\n"
	for _, tc := range []struct {
		name, record string
		files        map[string]string
		check        bool
		want         string
	}{
		{"a v1 target the record does not name", rocketnetOnly, map[string]string{"reg": lines(rn1, rn2, rn3), "svc": lines(rn1, rn2, rn3),
			"gh-repos-mas-bandwidth-rocketnet": ghRocketnet}, true,
			"refused: v2 does not cover https://github.com/robotgamedevelopers (not in the record)"},
		{"a v1 runner is busy", captainRecord, busy, false, "refused: busy captain-rgd-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newWinRig(t, tc.record, tc.files)
			args := []string{"-e", `{"nova_windows_retire_v1": true}`}
			if tc.check {
				args = append(args, "--check")
			}
			out, log, err := r.play(t, args...)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Errorf("want a failed run saying %q, err %v:\n%s", tc.want, err, out)
			}
			if strings.Contains(log, "RETIRE ") {
				t.Errorf("a v1 runner was retired despite the refusal:\n%s", log)
			}
		})
	}
}
