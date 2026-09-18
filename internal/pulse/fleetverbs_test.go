package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The four bench scripts retired into fleet verbs (#1142, "everything sketched becomes a
// tool"): tools/bench-standard.sh is `fleet standard`, bench-mirror.sh is `fleet mirror`,
// ts-join-one.sh is `fleet join`, fleet-sleep.sh is `fleet sleep`.
//
// Every test here drives a fake ssh on the test's own PATH that runs the piped remote
// script through `bash -s`, with fake sudo, git, tailscale, systemctl and pgrep beside it,
// so no test reaches a machine, opens a socket, or runs the real tool. What each fake was
// asked to do is on disk in a log the test reads: that is how `fleet join` is held to the
// rule that the auth key never appears in an argv, anywhere.

// fleetVerbsFake is one test's fake world: the ssh program to pass as --ssh, the directory
// the fakes log their argv into, and the bin directory they live in.
type fleetVerbsFake struct {
	SSH  string
	Logs string
	Bin  string
}

func (f fleetVerbsFake) log(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.Logs, name))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(raw)
}

func writeFleetVerbsExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newFleetVerbsFake writes the fakes and puts them first on PATH. The remote script the
// verb builds is run by the fake ssh through `bash -s`, so the script sees this PATH and
// every remote tool it starts is one of these.
func newFleetVerbsFake(t *testing.T) fleetVerbsFake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh runs bash -s; the fleet verb tests are unix-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}

	// sudo: log the whole argv, drop its own flags, run the rest. A remote step that needs
	// root is therefore visible to the test AND still runs the fake tool behind it.
	writeFleetVerbsExe(t, filepath.Join(bin, "sudo"), "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >> '"+logs+"/sudo.log'\n"+
		"while [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\n"+
		"exec \"$@\"\n")
	// systemctl: log and succeed.
	writeFleetVerbsExe(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >> '"+logs+"/systemctl.log'\nexit 0\n")
	// pgrep: no busy runner unless a test says otherwise.
	writeFleetVerbsExe(t, filepath.Join(bin, "pgrep"), "#!/bin/sh\nexit 1\n")
	// git: log the argv, make a clone's target directory, answer rev-parse with a sha.
	writeFleetVerbsExe(t, filepath.Join(bin, "git"), "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >> '"+logs+"/git.log'\n"+
		"for a in \"$@\"; do last=\"$a\"; done\n"+
		"case \"$*\" in\n"+
		"  *clone*) mkdir -p \"$last\"; exit 0;;\n"+
		"  *rev-parse*) echo abc1234; exit 0;;\n"+
		// The identity the standard now checks: empty on all four Linux machines of the
		// fleet on 2026-09-18, and a card that commits without it fails after the work.
		"  *config*user.email*) echo rowan@mas-bandwidth.com; exit 0;;\n"+
		"  *config*user.name*) echo 'Rowan Claude'; exit 0;;\n"+
		"  *) exit 0;;\n"+
		"esac\n")
	// tailscale: log the argv, keep whatever came down stdin, answer `ip -4`.
	writeFleetVerbsExe(t, filepath.Join(bin, "tailscale"), "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >> '"+logs+"/tailscale.log'\n"+
		"case \"$1\" in\n"+
		"  up) cat >> '"+logs+"/tailscale.stdin'; echo 'Success.'; exit 0;;\n"+
		"  ip) echo 100.64.0.9; exit 0;;\n"+
		"esac\nexit 0\n")
	// ssh: log the argv, then run the piped script.
	ssh := filepath.Join(dir, "ssh")
	writeFleetVerbsExe(t, ssh, "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" >> '"+logs+"/ssh.log'\nexec bash -s\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fleetVerbsFake{SSH: ssh, Logs: logs, Bin: bin}
}

// fleetStandardHome builds a bench home that meets the standard: a Go SDK under ~/sdk, the
// safe-rm helper, one seat key, and nova-swarm reporting the wanted stamp.
func fleetStandardHome(t *testing.T, stamp string) string {
	t.Helper()
	home := t.TempDir()
	writeFleetVerbsExe(t, filepath.Join(home, "sdk", "go1.26.5", "bin", "go"),
		"#!/bin/sh\necho 'go version go1.26.5 linux/amd64'\n")
	writeFleetVerbsExe(t, filepath.Join(home, "sdk", "sbcl-2.5.9", "bin", "sbcl"),
		"#!/bin/sh\necho 'SBCL 2.5.9'\n")
	// The module cache: a toolchain root the wall grants read-without-execute, and one a
	// linux bench is DRIFTED on when it is missing, because a Go card reads a dependency's
	// sources out of it inside the wall.
	if err := os.MkdirAll(filepath.Join(home, "go", "pkg", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFleetVerbsExe(t, filepath.Join(home, ".local", "bin", "nova-swarm"),
		"#!/bin/sh\necho 'nova-swarm "+stamp+"'\n")
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "safe-rm.sh"), []byte("# helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seat := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(seat, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seat, "rowan.key"), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A Mac bench's runner .path: the real git's directory ahead of /usr/bin, and the Go
	// SDK ahead of both. The SDK entry is what `runner-path-go` reads -- space's sixteen
	// .path files carried the bare distro PATH on 2026-09-18, so every Go shard scheduled
	// there ran with no toolchain at all.
	runner := filepath.Join(home, "runner-nova-tools-1")
	if err := os.MkdirAll(runner, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkBin := filepath.Join(home, "sdk", "go1.26.5", "bin")
	if err := os.WriteFile(filepath.Join(runner, ".path"), []byte(sdkBin+":/usr/local/bin:/usr/bin:/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And the bench's own non-interactive PATH carries ~/.local/bin, which is what
	// `path-noninteractive` reads: Ubuntu's ~/.bashrc returns before any PATH line for a
	// non-interactive shell, so `ssh <bench> nova-merge version` answered `command not
	// found` on every Linux machine in the fleet while a login shell worked.
	t.Setenv("PATH", filepath.Join(home, ".local", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

func fleetVerbsBenches(t *testing.T, home string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fleet.tsv")
	body := "worker-1\tnova@worker1\t" + home + "\t-\nstudio\tglenn@studio\t/Users/glenn\t-\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fleet-standard-lists-the-checks: the Linux and darwin lists are data, they differ where
// the two standards differ, and what both benches must carry is in both.
func TestFleetStandardChecksAreDataPerOS(t *testing.T) {
	linux := FleetStandardChecks("linux", "go1.26.5", "abc123", 25)
	darwin := FleetStandardChecks("darwin", "go1.26.5", "abc123", 25)
	windows := FleetStandardChecks("windows", "go1.26.5", "abc123", 25)
	if len(linux) == 0 || len(darwin) == 0 || len(windows) == 0 {
		t.Fatalf("check lists are empty: linux=%d darwin=%d windows=%d", len(linux), len(darwin), len(windows))
	}
	names := func(list []StandardCheck) map[string]bool {
		m := map[string]bool{}
		for _, c := range list {
			m[c.Name] = true
		}
		return m
	}
	ln, dn, wn := names(linux), names(darwin), names(windows)
	for _, want := range []string{"go", "sbcl", "nova-stamp", "seat", "disk-free"} {
		if !ln[want] || !dn[want] {
			t.Errorf("check %q must be in both lists (linux=%v darwin=%v)", want, ln[want], dn[want])
		}
	}
	for _, want := range []string{"go", "nova-stamp", "seat", "disk-free"} {
		if !wn[want] {
			t.Errorf("check %q must be in windows list", want)
		}
	}
	if !ln["safe-rm"] {
		t.Errorf("the Linux standard checks the safe-rm helper; the list is %v", ln)
	}
	for _, want := range []string{"git-ahead-of-shim", "runner-path"} {
		if !dn[want] {
			t.Errorf("the Mac bench standard checks %q; the list is %v", want, dn)
		}
		if ln[want] || wn[want] {
			t.Errorf("%q is a Mac bench check and must not be in linux or windows lists", want)
		}
	}
	for _, want := range []string{"git", "no-wsl", "features", "runner-service", "wol"} {
		if !wn[want] {
			t.Errorf("check %q must be in windows list; list is %v", want, wn)
		}
		if ln[want] || dn[want] {
			t.Errorf("%q is a Windows check and must not be in linux or darwin lists", want)
		}
	}
	// THE TOOLCHAIN ROOTS the sandbox wall grants, which are this table's half of the one
	// list in internal/swarm/toolchain.go (internal/ci's class test fails when the halves
	// drift). Both benches carry the two home roots; the installed trees are the Mac's,
	// because a Mac's toolchains are on PATH rather than unpacked into a home and each one
	// finds its runtime beside the launcher that ran it -- without them the M2 Air got
	// `'go' binary is trimmed`, `Unable to locate a Java Runtime` and `Failed to resolve
	// full path of the current executable []` inside the bare wall (2026-09-18).
	for _, want := range []string{"toolchain-sdk", "toolchain-modcache"} {
		if !ln[want] || !dn[want] {
			t.Errorf("toolchain root check %q must be in both lists (linux=%v darwin=%v)", want, ln[want], dn[want])
		}
	}
	for _, want := range []string{"toolchain-brew-go", "toolchain-brew-sbcl", "toolchain-brew-openjdk", "toolchain-jvm", "toolchain-dotnet"} {
		if !dn[want] {
			t.Errorf("the Mac bench standard names the toolchain root check %q; the list is %v", want, dn)
		}
		if ln[want] {
			t.Errorf("%q is an installed Mac toolchain and must not be in the Linux list", want)
		}
	}
	// A LINUX ROOT IS DEMANDED AND A DARWIN ROOT IS REPORTED. The standard's own installer
	// puts a linux root there, so a bench missing one is drift and every Go card on it dies;
	// a Mac's trees are the machine's shape, and drifting on a Mac with no .NET would leave
	// every Mac bench permanently red while the wall simply skips the root.
	for _, c := range linux {
		if c.Root != "" && c.Match != MatchEquals {
			t.Errorf("the linux standard reports the toolchain root %s (match=%s) instead of demanding it", c.Root, c.Match)
		}
	}
	for _, c := range darwin {
		if c.Root != "" && c.Match != MatchNonempty {
			t.Errorf("the darwin standard demands the toolchain root %s (match=%s); a Mac's roots are reported", c.Root, c.Match)
		}
	}
}

// fleet standard prints one STANDARD line per check and a verdict, and a bench that meets
// the standard is exit 0.
func TestFleetStandardPrintsALinePerCheckAndAVerdict(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH, OS: "linux",
		Go: "go1.26.5", Want: "abc123", MinFreeGB: 0,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	checks := FleetStandardChecks("linux", "go1.26.5", "abc123", 0)
	if len(lines) != len(checks)+1 {
		t.Fatalf("got %d lines, want one per check (%d) and a verdict:\n%s", len(lines), len(checks), out.String())
	}
	for i, c := range checks {
		want := "STANDARD worker-1 " + c.Name + " OK "
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want a %q line", i, lines[i], want)
		}
	}
	verdict := lines[len(lines)-1]
	if verdict != "FLEET worker-1 STANDARD OK checks="+itoaTest(len(checks)) {
		t.Fatalf("verdict = %q", verdict)
	}
	if errb.Len() == 0 {
		t.Errorf("a verb that talks to a machine says what it is doing on stderr; stderr was empty")
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A bench missing one thing the standard demands is exit 2, and the DRIFT line names the
// check that failed -- not "something is wrong".
func TestFleetStandardNamesTheCheckThatDrifted(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	// The bins drift: nova-swarm reports another stamp.
	writeFleetVerbsExe(t, filepath.Join(home, ".local", "bin", "nova-swarm"),
		"#!/bin/sh\necho 'nova-swarm deadbee'\n")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH, OS: "linux",
		Go: "go1.26.5", Want: "abc123", MinFreeGB: 0,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "STANDARD worker-1 nova-stamp DRIFT ") {
		t.Fatalf("no DRIFT line naming nova-stamp:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FLEET worker-1 STANDARD DRIFT drift=1/") {
		t.Fatalf("no verdict counting the drift:\n%s", out.String())
	}
}

// fleet-refuses-studio: every admin verb refuses the studio bench by name, and no ssh runs.
func TestFleetStandardMirrorJoinSleepRefuseStudio(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	if code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "studio", SSH: fake.SSH, OS: "linux",
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("fleet standard studio exit = %d, want 2", code)
	}
	if code := FleetMirror(FleetMirrorInput{
		Benches: benches, Name: "studio", SSH: fake.SSH,
		Repo: "https://example.com/mas-bandwidth/nova-tools.git", Path: "/home/nova/nova-bench/mirror/nova-tools.git",
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("fleet mirror studio exit = %d, want 2", code)
	}
	if code := FleetJoin(FleetJoinInput{
		Benches: benches, Name: "studio", SSH: fake.SSH,
		Tailscale: "/usr/local/bin/tailscale", AuthKeyEnv: "NOVA_TEST_TS_KEY",
		Getenv:  func(string) string { return "tskey-auth-fake" },
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("fleet join studio exit = %d, want 2", code)
	}
	if code := FleetSleep(FleetSleepInput{
		Benches: benches, Name: "studio", SSH: fake.SSH,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("fleet sleep studio exit = %d, want 2", code)
	}
	if got := fake.log(t, "ssh.log"); got != "" {
		t.Fatalf("a refused bench was still reached over ssh: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" && !strings.HasPrefix(line, "FLEET REFUSED bench=studio") {
			t.Errorf("line = %q, want FLEET REFUSED bench=studio", line)
		}
	}
}

// fleet mirror creates the bare mirror when it is missing and fetches it when it is there;
// the line says which it was.
func TestFleetMirrorCreatesThenRefreshes(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)
	path := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")

	var out, errb bytes.Buffer
	code := FleetMirror(FleetMirrorInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Repo: "https://example.com/mas-bandwidth/nova-tools.git", Path: path,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FLEET worker-1 MIRROR ") || !strings.Contains(out.String(), " created ") {
		t.Fatalf("first run must say it created the mirror:\n%s", out.String())
	}
	git := fake.log(t, "git.log")
	if !strings.Contains(git, "clone") || !strings.Contains(git, "--mirror") {
		t.Fatalf("the first run must clone a bare mirror; git saw:\n%s", git)
	}

	out.Reset()
	errb.Reset()
	code = FleetMirror(FleetMirrorInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Repo: "https://example.com/mas-bandwidth/nova-tools.git", Path: path,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("second exit = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), " refreshed ") {
		t.Fatalf("second run must refresh the mirror it found:\n%s", out.String())
	}
	if !strings.Contains(fake.log(t, "git.log"), "fetch") {
		t.Fatalf("the second run must fetch, not clone again:\n%s", fake.log(t, "git.log"))
	}
}

// A mirror URL that is not an https remote, and a target that is not an absolute path, are
// refusals with a remedy -- before any ssh.
func TestFleetMirrorRefusesABadRepoOrPath(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	if code := FleetMirror(FleetMirrorInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Repo: "git@example.com:mas-bandwidth/nova-tools.git", Path: filepath.Join(home, "m.git"),
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("a non-https remote exit = %d, want 2", code)
	}
	if code := FleetMirror(FleetMirrorInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Repo: "https://example.com/mas-bandwidth/nova-tools.git", Path: "nova-bench/mirror/nova-tools.git",
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("a relative remote path exit = %d, want 2", code)
	}
	if got := fake.log(t, "ssh.log"); got != "" {
		t.Fatalf("a refused invocation still reached the bench: %q", got)
	}
	if n := strings.Count(strings.TrimSpace(errb.String()), "\n"); n != 1 {
		t.Fatalf("want one refusal line each, got:\n%s", errb.String())
	}
}

// The auth key reaches the bench, and it reaches it on stdin: it is in no argv, on neither
// stream, and in no log this test can read.
func TestFleetJoinKeepsTheAuthKeyOutOfEveryArgv(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)
	const key = "tskey-auth-kNOTINARGV-secret"

	var out, errb bytes.Buffer
	code := FleetJoin(FleetJoinInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Tailscale: filepath.Join(fake.Bin, "tailscale"), AuthKeyEnv: "NOVA_TEST_TS_KEY",
		Getenv:  func(name string) string { return map[string]string{"NOVA_TEST_TS_KEY": key}[name] },
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FLEET worker-1 JOINED ip=100.64.0.9") {
		t.Fatalf("no JOINED line with the bench's tailnet address:\n%s", out.String())
	}
	if got := fake.log(t, "tailscale.stdin"); !strings.Contains(got, key) {
		t.Fatalf("the auth key did not reach tailscale on stdin; stdin held %q", got)
	}
	for _, name := range []string{"tailscale.log", "sudo.log", "ssh.log"} {
		if strings.Contains(fake.log(t, name), key) {
			t.Fatalf("the auth key appeared in an argv (%s)", name)
		}
	}
	if strings.Contains(out.String(), key) || strings.Contains(errb.String(), key) {
		t.Fatalf("the auth key was printed")
	}
}

// An --authkey-env naming an empty or unset variable is a refusal with the remedy that
// names nova-secrets exec, and nothing is reached.
func TestFleetJoinRefusesAnEmptyAuthkeyEnv(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	code := FleetJoin(FleetJoinInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Tailscale: "/usr/local/bin/tailscale", AuthKeyEnv: "NOVA_TEST_TS_KEY",
		Getenv:  func(string) string { return "" },
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := fake.log(t, "ssh.log"); got != "" {
		t.Fatalf("a refused join still reached the bench: %q", got)
	}
	line := strings.TrimSpace(errb.String())
	if strings.Count(line, "\n") != 0 || !strings.Contains(line, "nova-secrets exec") {
		t.Fatalf("want one refusal line naming the remedy, got:\n%s", errb.String())
	}
}

// A tailscale path that is not absolute is refused before any ssh: the path is pasted into
// a remote command, and a guessed one is a bench running something else.
func TestFleetJoinRefusesARelativeTailscalePath(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	if code := FleetJoin(FleetJoinInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Tailscale: "tailscale", AuthKeyEnv: "NOVA_TEST_TS_KEY",
		Getenv:  func(string) string { return "tskey-auth-fake" },
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := fake.log(t, "ssh.log"); got != "" {
		t.Fatalf("a refused join still reached the bench: %q", got)
	}
}

// fleet sleep is the suspend the fleet-sleep.sh script did by hand: a bench holding a live
// job is BUSY and is never suspended.
func TestFleetSleepRefusesABenchWithALiveJob(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	jobs := filepath.Join(home, "rowan-swarm-root", "0", "jobs", "card-1")
	if err := os.MkdirAll(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	record := "{\"job\":\"card-1\",\"slot\":0,\"state\":\"launched\",\"pid\":" + itoaTest(os.Getpid()) + "}\n"
	if err := os.WriteFile(filepath.Join(jobs, "pid"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	code := FleetSleep(FleetSleepInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "FLEET worker-1 BUSY ") {
		t.Fatalf("line = %q, want a BUSY line", strings.TrimSpace(out.String()))
	}
	if strings.Contains(fake.log(t, "systemctl.log"), "suspend") {
		t.Fatalf("a busy bench was suspended:\n%s", fake.log(t, "systemctl.log"))
	}
}

// An idle bench is suspended, and the suspend goes through systemctl over ssh.
func TestFleetSleepSuspendsAnIdleBench(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	code := FleetSleep(FleetSleepInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "FLEET worker-1 SUSPENDED" {
		t.Fatalf("line = %q", got)
	}
	if !strings.Contains(fake.log(t, "systemctl.log"), "suspend") {
		t.Fatalf("the suspend did not go through systemctl:\n%s", fake.log(t, "systemctl.log"))
	}
}

// Every one of the four verbs refuses a bench the file does not carry, rather than guessing
// which machine was meant.
func TestFleetVerbsRefuseAnUnknownBench(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	var out, errb bytes.Buffer
	if code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "nowhere", SSH: fake.SSH, OS: "linux",
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("standard exit = %d, want 2", code)
	}
	if code := FleetSleep(FleetSleepInput{
		Benches: benches, Name: "nowhere", SSH: fake.SSH,
		Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
	}); code != 2 {
		t.Errorf("sleep exit = %d, want 2", code)
	}
	if got := fake.log(t, "ssh.log"); got != "" {
		t.Fatalf("an unknown bench was still reached over ssh: %q", got)
	}
}

// Windows checks have negative controls: missing powershell.exe, disabled features,
// stopped runner, wrong runner service account, and disabled WoL must report DRIFT,
// never false-green OK.
func TestFleetStandardWindowsChecksNegativeControls(t *testing.T) {
	fake := newFleetVerbsFake(t)
	home := fleetStandardHome(t, "abc123")
	benches := fleetVerbsBenches(t, home)

	// 1. Without powershell.exe on PATH, all three PowerShell-based checks must DRIFT.
	var out1, errb1 bytes.Buffer
	code1 := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH, OS: "windows",
		Go: "go1.26.5", Want: "abc123", MinFreeGB: 0,
		Timeout: 30 * time.Second, Stdout: &out1, Stderr: &errb1,
	})
	if code1 != 2 {
		t.Fatalf("missing powershell must fail with exit 2, got %d\nstdout:\n%s", code1, out1.String())
	}
	for _, check := range []string{"features", "runner-service", "wol"} {
		if strings.Contains(out1.String(), "STANDARD worker-1 "+check+" OK") {
			t.Errorf("missing powershell must not report false-green OK for %s:\n%s", check, out1.String())
		}
		if !strings.Contains(out1.String(), "STANDARD worker-1 "+check+" DRIFT") {
			t.Errorf("missing powershell must report DRIFT for %s:\n%s", check, out1.String())
		}
	}

	// 2. With fake powershell.exe returning negative controls (disabled feature, stopped service, disabled WoL),
	// they must also report DRIFT.
	fakePS := filepath.Join(fake.Bin, "powershell.exe")
	writeFleetVerbsExe(t, fakePS, `#!/bin/sh
cmd="$*"
case "$cmd" in
  *Microsoft-Hyper-V-All*)
    echo "disabled"
    ;;
  *actions.runner.*)
    echo "stopped"
    ;;
  *Wake*)
    echo "disabled"
    ;;
  *)
    echo "unknown"
    ;;
esac
`)

	var out2, errb2 bytes.Buffer
	code2 := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "worker-1", SSH: fake.SSH, OS: "windows",
		Go: "go1.26.5", Want: "abc123", MinFreeGB: 0,
		Timeout: 30 * time.Second, Stdout: &out2, Stderr: &errb2,
	})
	if code2 != 2 {
		t.Fatalf("negative controls must fail with exit 2, got %d\nstdout:\n%s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "STANDARD worker-1 features DRIFT want=contains:Containers got=disabled") {
		t.Errorf("features must report DRIFT with got=disabled:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "STANDARD worker-1 runner-service DRIFT want=contains:Running\\x20(nova) got=stopped") {
		t.Errorf("runner-service must report DRIFT with got=stopped:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "STANDARD worker-1 wol DRIFT want=equals:enabled got=disabled") {
		t.Errorf("wol must report DRIFT with got=disabled:\n%s", out2.String())
	}

	// 3. Verify single-quoted bash command generation does not expand $null or $_
	checks := FleetStandardChecks("windows", "go1.26.5", "abc123", 25)
	script := fleetStandardScript(home, checks)
	for _, c := range checks {
		if c.Name == "features" || c.Name == "runner-service" || c.Name == "wol" {
			if !strings.Contains(c.Probe, "powershell.exe -NoProfile -Command '") {
				t.Errorf("%s probe must wrap PowerShell command in single quotes to prevent outer Bash expansion: %s", c.Name, c.Probe)
			}
			if strings.Contains(c.Probe, "|| echo") {
				t.Errorf("%s probe must not have false-green fallback '|| echo': %s", c.Name, c.Probe)
			}
		}
	}
	// Check generated script preserves $_ for PowerShell
	if !strings.Contains(script, `$_`) {
		t.Errorf("generated bash script must preserve $_ for PowerShell without expansion:\n%s", script)
	}
}
