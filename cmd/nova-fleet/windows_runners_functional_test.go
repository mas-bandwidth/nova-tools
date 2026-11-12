//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	printf 'LABELS %s %s %s\n' "$name" "$dir" "$(arg -Labels "$@")" >>"$log"
	printf 'REGACCT %s account=%s\n' "$name" "$(arg -Account "$@")" >>"$log"
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
*"-Action account"*|*"-Action logon"*)
	n=$(arg -SecretEnv "$@"); eval "v=\${$n:-}"
	verb=ACCOUNT; case "$*" in *"-Action logon"*) verb=LOGON ;; esac
	relogon=no; case "$*" in *-Relogon*) relogon=yes ;; esac
	printf '%s acct=%s dir=%s wslenv=%s secret=%s relogon=%s argv=%s\n' "$verb" "$(arg -Account "$@")" "$(arg -Dir "$@")" "${WSLENV:-}" \
		"${v:+set}" "$relogon" "$*" >>"$log"
	[ -n "$v" ] || { printf 'NO-SECRET %s\r\n' "$n"; exit 4; }
	if [ $verb = ACCOUNT ]; then
		st=ok; [ -f "$d/account" ] && st=$(cat "$d/account")
		printf 'ACCOUNT %s account=%s rights=ok admin=ok\r\n' "$(arg -Account "$@")" "$st"
	else
		[ -f "$d/aclopen" ] && { printf 'ACL-OPEN %s ace=S-1-5-11 owner=S-1-5-32-544\r\n' "$(arg -Dir "$@")"; exit 10; }
		[ -f "$d/aclsilent" ] && { printf 'LOGON %s ok account=.\\%s restart=no service=s\r\n' "$(arg -Dir "$@")" "$(arg -Account "$@")"; exit 0; }
		printf 'LOGON %s set account=.\\%s restart=done acl=protected service=s\r\n' "$(arg -Dir "$@")" "$(arg -Account "$@")"
	fi
	exit 0 ;;
*"-Action install"*) printf 'INSTALL %s account=%s path=%s\n' "$(arg -Dir "$@")" "$(arg -Account "$@")" "$(arg -PathList "$@")" >>"$log"; printf 'OK\r\n'; exit 0 ;;
*"-Action ensure"*) printf 'OK\r\n'; exit 0 ;;
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

// fakeSecrets is the machine's nova-secrets: names lists the file names (the seat's names), and exec adds its one
// --only name with a stand-in value to the environment of the command after --, as the real one does.
const fakeSecrets = `#!/bin/sh
d=$(dirname "$0")/..
case "$1" in
names) printf 'NAMES %s\n' "$*" >>"$d/calls.log"; [ -f "$d/names" ] && cat "$d/names"; exit 0 ;;
exec)
	only=
	while [ $# -gt 0 ]; do case $1 in --only) only=$2; shift 2 ;; --) shift; break ;; *) shift ;; esac; done
	printf 'EXEC only=%s\n' "$only" >>"$d/calls.log"
	export "$only=SECRETVALUE-not-shown"
	exec "$@" ;;
esac
exit 9
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
	for name, body := range map[string]string{"bin/powershell.exe": fakePowershell, "bin/gh": fakeGh, "bin/nova-secrets": fakeSecrets} {
		r.write(t, name, body, 0o755)
	}
	r.write(t, "interop", "", 0o644)
	r.write(t, "inv.yml", "all:\n  hosts:\n    captain:\n      ansible_connection: local\n"+
		"      ansible_python_interpreter: \"{{ ansible_playbook_python }}\""+record+"\n  children:\n    bench: {hosts: {captain: {}}}\n", 0o644)
	r.write(t, "vars.yml", "github_runner_wsl_interop: "+r.dir+"/interop\ngithub_runner_wsl_powershell: "+r.dir+"/bin/powershell.exe\n"+
		"github_runner_wsl_stage_unix: "+r.dir+"/stage\ngithub_runner_wsl_ci_stage_unix: "+r.dir+"/cistage\ngithub_runner_gh: "+r.dir+"/bin/gh\n"+
		"github_runner_wsl_machine_id_file: "+r.dir+"/machine-id\ngithub_runner_nova_secrets: "+r.dir+"/bin/nova-secrets\n", 0o644)
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

const (
	adminSecret = "NOVA_WINRUNNER_VERIFIER_PASSWORD"
	adminDir    = `C:\ProgramData\nova-admin-runners\mas-bandwidth-rocketnet-verifier`
	adminName   = "captain-win-mas-bandwidth-rocketnet-verifier"
	adminRecord = captainRecord + `
      nova_windows_admin_runners:
        - {url: https://github.com/mas-bandwidth/rocketnet, account: verifier, labels: [verify-nightly], secret: ` + adminSecret + `}`
)

// logLines are the call log's lines that start with prefix.
func logLines(log, prefix string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// TestFunctionalWindowsAdminRunner: an admin runner beside the ordinary ones. Its account is set and its service
// switched to it with the password only in powershell's environment (WSLENV names it, no argument carries it, and no
// output shows it); it registers with only its entry's labels while the ordinary runners keep theirs, dirs and names;
// a password set anew re-applies the service logon; a seat without the password stops the run before anything changes,
// naming the commands that mint it; --check prints the plan and calls nothing on Windows.
func TestFunctionalWindowsAdminRunner(t *testing.T) {
	t.Parallel()
	whole := map[string]string{"reg": lines(append(allDirs, adminDir)...), "svc": lines(append(allDirs, adminDir)...),
		"gh-repos-mas-bandwidth-rocketnet": ghRocketnet + ghLine(adminName), "gh-orgs-robotgamedevelopers": ghRgd, "names": lines(adminSecret)}
	for _, tc := range []struct {
		name     string
		files    map[string]string
		check    bool
		register []string // the LABELS lines (name dir labels) the run must log, sorted
		relogon  string
	}{
		{"everything new", map[string]string{"names": lines("OTHER_NAME", adminSecret)}, false, []string{
			"LABELS captain-win-mas-bandwidth-rocketnet-1 " + rn1 + " captain,fleet,rocketnet-windows",
			"LABELS captain-win-mas-bandwidth-rocketnet-2 " + rn2 + " captain,fleet,rocketnet-windows",
			"LABELS captain-win-mas-bandwidth-rocketnet-3 " + rn3 + " captain,fleet,rocketnet-windows",
			"LABELS " + adminName + " " + adminDir + " verify-nightly",
			"LABELS captain-win-org-robotgamedevelopers-1 " + rgd1 + " captain,fleet",
			"LABELS captain-win-org-robotgamedevelopers-2 " + rgd2 + " captain,fleet",
		}, "no"},
		{"admin new beside whole ordinary runners", map[string]string{"reg": lines(allDirs...), "svc": lines(allDirs...),
			"gh-repos-mas-bandwidth-rocketnet": ghRocketnet, "gh-orgs-robotgamedevelopers": ghRgd, "names": lines(adminSecret)}, false,
			[]string{"LABELS " + adminName + " " + adminDir + " verify-nightly"}, "no"},
		{"all whole, password unchanged", whole, false, nil, "no"},
		{"all whole, password set anew", mergeFiles(whole, map[string]string{"account": "updated"}), false, nil, "yes"},
		{"check", whole, true, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newWinRig(t, adminRecord, tc.files)
			var extra []string
			if tc.check {
				extra = append(extra, "--check")
			}
			out, log, err := r.play(t, extra...)
			if err != nil {
				t.Fatalf("play failed: %v\n%s", err, out)
			}
			if strings.Contains(out, "SECRETVALUE") || strings.Contains(log, "SECRETVALUE") {
				t.Fatal("the password reached ansible's output or a fake's argument")
			}
			if len(logLines(log, "NAMES names --store ")) != 1 {
				t.Errorf("the seat's names were not read once:\n%s", log)
			}
			if tc.check {
				for _, p := range []string{"EXEC", "ACCOUNT", "LOGON", "INSTALL", "REGISTER"} {
					if len(logLines(log, p)) > 0 {
						t.Errorf("--check called %s:\n%s", p, log)
					}
				}
				if !strings.Contains(out, "RUNNER-PLAN captain windows=6 register=- repair=- admin="+adminName+"@verifier unsealed=- ") {
					t.Errorf("no plan naming the admin runner:\n%s", out)
				}
				return
			}
			got := logLines(log, "LABELS ")
			slices.Sort(got)
			if strings.Join(got, "\n") != strings.Join(tc.register, "\n") {
				t.Errorf("registrations:\n got %q\nwant %q", got, tc.register)
			}
			if n := len(logLines(log, "INSTALL ")); n != 6 {
				t.Errorf("%d installs; want the six runners", n)
			}
			want := map[string]string{
				"EXEC":    "EXEC only=" + adminSecret,
				"ACCOUNT": "ACCOUNT acct=verifier dir= wslenv=" + adminSecret + " secret=set relogon=no argv=",
				"LOGON":   "LOGON acct=verifier dir=" + adminDir + " wslenv=" + adminSecret + " secret=set relogon=" + tc.relogon + " argv=",
			}
			for p, w := range want {
				ls := logLines(log, p+" ")
				if p == "EXEC" && len(ls) != 2 || p != "EXEC" && len(ls) != 1 {
					t.Errorf("%s calls: %q", p, ls)
					continue
				}
				for _, l := range ls {
					if !strings.HasPrefix(l, w) {
						t.Errorf("%s call:\n got %s\nwant %s...", p, l, w)
					}
				}
			}
			if !strings.Contains(out, "ACCOUNT captain verifier account=") || !strings.Contains(out, "LOGON captain "+strings.ReplaceAll(adminDir, `\`, `\\`)+" set account=.\\\\verifier") {
				t.Errorf("no account or logon receipt:\n%s", out)
			}
			if !strings.Contains(out, "GITHUB-RUNNERS host=captain windows=6 registered=6 online=6") {
				t.Errorf("no whole receipt:\n%s", out)
			}
		})
	}
}

// TestFunctionalWindowsAdminRunnerInstallArgs: the admin runner alone is installed and registered with its account
// (sealed directory, no service from config.cmd), in the admin directory, with a job PATH that leaves out the shared
// ccache directory the ordinary runners keep; its logon names its url and runner name (the service it makes).
func TestFunctionalWindowsAdminRunnerInstallArgs(t *testing.T) {
	t.Parallel()
	r := newWinRig(t, adminRecord, map[string]string{"names": lines(adminSecret)})
	out, log, err := r.play(t, "-e", `{"github_runner_windows_path": ["C:\\Go\\bin", "C:\\ProgramData\\nova\\ccache\\msvc"]}`)
	if err != nil {
		t.Fatalf("play failed: %v\n%s", err, out)
	}
	installs := logLines(log, "INSTALL ")
	slices.Sort(installs)
	for _, w := range []string{
		"INSTALL " + adminDir + ` account=verifier path=C:\Go\bin`,
		"INSTALL " + rn1 + ` account= path=C:\Go\bin;C:\ProgramData\nova\ccache\msvc`,
	} {
		if !slices.Contains(installs, w) {
			t.Errorf("no install %q among:\n%s", w, strings.Join(installs, "\n"))
		}
	}
	if n := len(slices.DeleteFunc(slices.Clone(installs), func(l string) bool { return !strings.Contains(l, "account=verifier") })); n != 1 {
		t.Errorf("%d installs with the account; want the admin runner's alone", n)
	}
	regs := logLines(log, "REGACCT ")
	if !slices.Contains(regs, "REGACCT "+adminName+" account=verifier") || !slices.Contains(regs, "REGACCT captain-win-mas-bandwidth-rocketnet-1 account=") {
		t.Errorf("register passes -Account for the admin runner alone:\n%s", strings.Join(regs, "\n"))
	}
	logon := logLines(log, "LOGON ")
	if len(logon) != 1 || !strings.Contains(logon[0], "-Url https://github.com/mas-bandwidth/rocketnet -Name "+adminName+" -Account verifier") {
		t.Errorf("the logon step names the runner's url and name:\n%s", strings.Join(logon, "\n"))
	}
}

func mergeFiles(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// TestFunctionalWindowsAdminRunnerWithoutPassword: a seat without the admin runner's password stops the run before
// anything on Windows changes, and names the commands that mint the value on the control node, grant it and push it.
func TestFunctionalWindowsAdminRunnerWithoutPassword(t *testing.T) {
	t.Parallel()
	r := newWinRig(t, adminRecord, map[string]string{"names": lines("OTHER_NAME")})
	out, log, err := r.play(t, "-e", "nova_seal_store=/srv/store", "-e", "nova_seal_seat=control", "-e", "nova_seal_key=/k/control.key")
	if err == nil {
		t.Fatalf("a seat without the password must fail the run:\n%s", out)
	}
	for _, p := range []string{"EXEC", "ACCOUNT", "LOGON", "INSTALL", "REGISTER"} {
		if len(logLines(log, p)) > 0 {
			t.Errorf("the run called %s before stopping:\n%s", p, log)
		}
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"seat captain holds no " + adminSecret,
		"{ printf 'Nw1-'; openssl rand -hex 24; } | nova-secrets set " + adminSecret + " --store /srv/store --key /k/control.key --pins ",
		"nova-secrets grant " + adminSecret + " --to captain --pins ",
		"git -C /srv/store push && nova-fleet secrets-refresh -- -e nova_seal_store=/srv/store",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, out)
		}
	}
}

// TestFunctionalWindowsAdminRunnerLabelClash: an admin runner whose label another runner of the machine carries (the
// role's Windows labels, windows_runners' or runner_scopes'), or one GitHub gives runners, in any case, stops the run
// before anything on Windows changes, naming the clash.
func TestFunctionalWindowsAdminRunnerLabelClash(t *testing.T) {
	t.Parallel()
	for label, want := range map[string]string{
		"rocketnet-windows": "verifier label rocketnet-windows is windows_runners of https://github.com/mas-bandwidth/rocketnet",
		"Fleet":             "verifier label Fleet is ", // the role's and windows_runners' both
		"captain":           "verifier label captain is github_runner_wsl_labels",
		"tlc":               "verifier label tlc is runner_scopes of https://github.com/o",
		"X64":               "verifier label X64 is a label GitHub gives runners",
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			record := strings.Replace(adminRecord, "labels: [verify-nightly]", "labels: [verify-nightly, "+label+"]", 1) + `
      nova_runner_scopes:
        - {url: https://github.com/o, count: 1, labels: [tlc]}`
			r := newWinRig(t, record, map[string]string{"names": lines(adminSecret)})
			out, log, err := r.play(t)
			if err == nil {
				t.Fatalf("a shared label must fail the run:\n%s", out)
			}
			for _, p := range []string{"NAMES", "EXEC", "ACCOUNT", "LOGON", "INSTALL", "REGISTER"} {
				if len(logLines(log, p)) > 0 {
					t.Errorf("the run called %s before stopping:\n%s", p, log)
				}
			}
			if !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
				t.Errorf("the refusal does not name %q:\n%s", want, out)
			}
		})
	}
}

// TestFunctionalWindowsAdminRunnerDirectoryNotProtected: a logon that reports its directory open (ACL-OPEN), or that
// does not say acl=protected, fails the run.
func TestFunctionalWindowsAdminRunnerDirectoryNotProtected(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"aclopen", "aclsilent"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"reg": lines(append(allDirs, adminDir)...), "svc": lines(append(allDirs, adminDir)...),
				"gh-repos-mas-bandwidth-rocketnet": ghRocketnet + ghLine(adminName), "gh-orgs-robotgamedevelopers": ghRgd,
				"names": lines(adminSecret), flag: ""}
			r := newWinRig(t, adminRecord, files)
			out, _, err := r.play(t)
			if err == nil || !strings.Contains(out, "or its directory is not protected") {
				t.Fatalf("an admin runner's directory that is not protected must fail the run:\n%s", out)
			}
		})
	}
}
