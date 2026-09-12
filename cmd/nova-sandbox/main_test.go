package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Every test here runs the REAL thing on this Mac: a real sandbox-exec, a real profile
// generated from profiles/darwin.sb.tmpl, a real wrapped command. On another platform
// each one skips BY NAME rather than silently, because a green from a suite that ran
// nothing reads exactly like a green from one that ran (test on multiple platforms).
func needDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: the darwin body needs sandbox-exec, which is macOS only", runtime.GOOS)
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("skipped: sandbox-exec is not on this machine")
	}
}

// job is one worker's shape: a write set with its data home, a read set, and a secret
// directory in NEITHER list — the thing the wall exists to keep unreadable.
type job struct{ base, write, read, home, secret, outside string }

func newJob(t *testing.T) job {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	j := job{base: base,
		write:   filepath.Join(base, "w"),
		read:    filepath.Join(base, "r"),
		outside: filepath.Join(base, "outside"),
	}
	j.home = filepath.Join(j.write, "home")
	secretDir := filepath.Join(base, "secret")
	j.secret = filepath.Join(secretDir, "env")
	for _, d := range []string{j.write, j.read, j.home, secretDir, j.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(j.secret, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return j
}

func (j job) env(extra ...string) []string {
	return append([]string{
		"HOME=" + j.home,
		"PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
	}, extra...)
}

// tool runs nova-sandbox in process with the given argv and environment.
func (j job) tool(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, nil, &out, &errb, env)
	return code, out.String(), errb.String()
}

// shell is the command the tests wrap, and its flag: /bin/sh -c on unix, cmd.exe /c on
// windows. The tests that RUN a script inside the wall are darwin's; the ones that assert a
// REFUSAL run everywhere, and on windows a hard-coded /bin/sh made them pass on
// "/bin/sh is on no PATH entry" — a green about the wrong refusal.
func (j job) shell(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return []string{"/bin/sh", "-c"}
	}
	for _, candidate := range []string{os.Getenv("COMSPEC"), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")} {
		if candidate == "" {
			continue
		}
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return []string{candidate, "/c"}
		}
	}
	t.Skip("skipped: this windows machine has no cmd.exe, and rule 5 resolves the command before any policy")
	return nil
}

// noopScript and touchScript are the two scripts these tests need, in the shell of the
// platform: one that does nothing and one that would create a file. The second is what makes
// "the command did NOT run" an assertion rather than a hope.
func noopScript() string {
	if runtime.GOOS == "windows" {
		return "exit /b 0"
	}
	return "true"
}

func touchScript(path string) string {
	if runtime.GOOS == "windows" {
		return `type nul > "` + path + `"`
	}
	return "touch '" + path + "'"
}

// wrapped runs one shell script INSIDE the wall with this job's lists.
func (j job) wrapped(t *testing.T, script string, extraEnv ...string) (int, string, string) {
	t.Helper()
	args := []string{"--read", j.read, "--write", j.write, "--"}
	args = append(args, j.shell(t)...)
	args = append(args, script)
	return j.tool(t, j.env(extraEnv...), args...)
}

// Rule 1 and rule 12: a wrapped command runs, the OK line names the wall, and the exit
// status is the command's.
func TestWrappedCommandRunsAndTheLineNamesTheWall(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	code, _, errOut := j.wrapped(t, "true")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "SANDBOX OK backend=sandbox-exec") ||
		!strings.Contains(errOut, "net=nopromise") || !strings.Contains(errOut, "cmd=sh") {
		t.Fatalf("the OK line is not the grammar the spec fixes: %q", errOut)
	}
	if strings.Contains(errOut, "-c") {
		t.Fatal("the OK line printed an argument; arguments carry task text")
	}
}

// Rule 3, and the reason the tool exists: the named secret is unreadable INSIDE the wall
// and readable outside it in the same test — a denial that was never possible is not a
// wall. The same for a planted ~/.ssh key.
func TestTheNamedSecretIsUnreadableInsideTheWall(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	if b, err := os.ReadFile(j.secret); err != nil || !strings.Contains(string(b), "not-a-real-key") {
		t.Fatalf("control: the secret is not readable outside the wall: %v", err)
	}
	if code, _, _ := j.wrapped(t, "cat '"+j.secret+"' > /dev/null"); code == 0 {
		t.Fatal("the secret was readable inside the wall")
	}
	ssh := filepath.Join(j.base, "fakehome", ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(ssh, "id_test")
	if err := os.WriteFile(key, []byte("PRIVATE KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := j.wrapped(t, "cat '"+key+"' > /dev/null"); code == 0 {
		t.Fatal("a private key outside both lists was readable inside the wall")
	}
	if _, err := os.ReadFile(key); err != nil {
		t.Fatalf("control: the key is not readable outside the wall: %v", err)
	}
}

// The three escapes read 4 of PR #70 tried, each attempted from INSIDE the wall, and
// each must fail closed.
func TestTheThreeEscapesFailClosed(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	for _, tc := range []struct{ name, script string }{
		{"hardlink_the_secret_in", "ln '" + j.secret + "' '" + j.write + "/hard' && cat '" + j.write + "/hard'"},
		{"symlink_out_and_write", "ln -s '" + j.outside + "' '" + j.write + "/lnk' && echo x > '" + j.write + "/lnk/f'"},
		{"symlink_to_the_secret_and_read", "ln -s '" + j.secret + "' '" + j.write + "/s' && cat '" + j.write + "/s'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, _ := j.wrapped(t, tc.script)
			if code == 0 {
				t.Fatalf("%s SUCCEEDED inside the wall", tc.name)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(j.outside, "f")); err == nil {
		t.Fatal("a write through a symlink landed outside the wall")
	}
}

// The wall stands and the job still runs: the first second of a real job, by absolute
// path, inside the wall.
func TestTheFirstSecondOfARealJob(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	git := realGit(t)
	for _, tc := range []struct{ name, script string }{
		{"write_inside", "echo nova > '" + j.write + "/out.txt' && test \"$(cat '" + j.write + "/out.txt')\" = nova"},
		{"mkdir_p_absolute", "mkdir -p '" + j.write + "/a/b/c' && test -d '" + j.write + "/a/b/c'"},
		{"git_init_absolute", "'" + git + "' init -q '" + j.write + "/g' && test -d '" + j.write + "/g/.git'"},
		{"home_config_write", "cd '" + j.write + "/g' && '" + git + "' config --global user.name nova-test && test -f '" + j.home + "/.gitconfig'"},
		{"cat_etc_hosts", "cat /etc/hosts > /dev/null"},
		{"sh_c_true", "/bin/sh -c true"},
		{"child_kill", "sleep 5 & kill $!"},
		{"tmpdir_is_inside", "test -n \"$TMPDIR\" && echo x > \"$TMPDIR/t\" && test -f '" + j.write + "/.nova-sandbox-tmp/t'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, errOut := j.wrapped(t, tc.script); code != 0 {
				t.Fatalf("%s failed inside the wall: exit %d; %s", tc.name, code, errOut)
			}
		})
	}
	t.Run("write_outside_is_denied", func(t *testing.T) {
		if code, _, _ := j.wrapped(t, ": > '"+j.outside+"/probe'"); code == 0 {
			t.Fatal("a write outside every named path succeeded")
		}
		if err := os.WriteFile(filepath.Join(j.outside, "control"), []byte("x"), 0o600); err != nil {
			t.Fatalf("control: the outside path is unwritable anyway, so the denial proves nothing: %v", err)
		}
	})
}

// This build's fix to the spec: (allow network*) reaches every unix-domain socket, so
// the SSH agent socket was connectable from inside the wall. A socket created outside
// the wall must not be connectable from inside it, and SSH_AUTH_SOCK must be gone.
func TestTheAgentSocketIsUnreachable(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	// The socket lives in a SHORT directory of its own, outside every named list: a
	// unix socket path is capped near 104 bytes on macOS and t.TempDir()'s is longer,
	// which would skip the one test this build's network fix exists for.
	sockDir, err := os.MkdirTemp("", "nova-agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "agent.sock")
	if len(sock) > 100 {
		t.Skipf("skipped: %d-byte socket path is over the AF_UNIX limit on this machine", len(sock))
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("control: the socket could not be created outside the wall: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if c, err := net.Dial("unix", sock); err != nil {
		t.Fatalf("control: the socket is not connectable outside the wall: %v", err)
	} else {
		c.Close()
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("skipped: nc is not on this machine, and it is how the wrapped process dials")
	}
	env := j.env("SSH_AUTH_SOCK="+sock, "SSH_AGENT_PID=1")
	code, _, errOut := j.tool(t, env, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "nc -U '"+sock+"' -w 1 </dev/null")
	if code == 0 {
		t.Fatal("a unix socket outside the wall was connectable from inside it")
	}
	if !strings.Contains(errOut, "SANDBOX NOTE dropped") || !strings.Contains(errOut, "SSH_AUTH_SOCK") {
		t.Fatalf("the dropped agent variables were not named before the command started: %q", errOut)
	}
	if code, _, _ := j.tool(t, env, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "test -z \"$SSH_AUTH_SOCK\" && test -z \"$SSH_AGENT_PID\""); code != 0 {
		t.Fatal("SSH_AUTH_SOCK reached the child's environment")
	}
}

// With no key readable and no agent reachable, a push out of the job fails. The remote
// is a local path outside every named list, because no test here touches the network.
func TestGitPushOutOfTheJobFails(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	git := realGit(t)
	remote := filepath.Join(j.outside, "remote.git")
	mustRun(t, git, "init", "-q", "--bare", remote)
	repo := filepath.Join(j.write, "repo")
	mustRun(t, git, "init", "-q", repo)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, git, "-C", repo, "add", "-A")
	mustRun(t, git, "-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed")
	mustRun(t, git, "-C", repo, "remote", "add", "origin", remote)
	code, _, _ := j.wrapped(t, "cd '"+repo+"' && '"+git+"' push -q origin HEAD:refs/heads/main")
	if code == 0 {
		t.Fatal("a push out of the job succeeded; the remote is outside every named path")
	}
	out := mustOutput(t, git, "-C", remote, "for-each-ref", "--format=%(refname)")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("the push landed: %q", out)
	}
}

// Rule 12's exit grammar: the child's status is the tool's, and a death by signal N is
// 128+N.
func TestExitStatusPassesThrough(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	if code, _, _ := j.wrapped(t, "exit 3"); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if code, _, _ := j.wrapped(t, "kill -9 $$"); code != 137 {
		t.Fatalf("exit %d, want 137 for a SIGKILL death", code)
	}
	// A command that itself exits 125 gives 125 with NO refusal line: the number alone
	// cannot tell the tool's NO from the command's, and the line is how a caller can.
	code, _, errOut := j.wrapped(t, "exit 125")
	if code != 125 || strings.Contains(errOut, "SANDBOX REFUSED") {
		t.Fatalf("a command's own 125 was confused with the tool's: exit %d, stderr %q", code, errOut)
	}
}

// The end-to-end job: a real command under a read set that EXCLUDES the secret
// directory, proving in one run that the work runs and the secret does not.
func TestEndToEndTheWorkRunsAndTheSecretDoesNot(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	command := "/bin/sh"
	args := []string{"-c", "true"}
	if oc, err := exec.LookPath("opencode"); err == nil {
		command, args = oc, []string{"--version"}
	}
	argv := append([]string{"--read", j.read, "--write", j.write, "--", command}, args...)
	if code, _, errOut := j.tool(t, j.env(), argv...); code != 0 {
		t.Fatalf("%s did not run inside the wall: exit %d; %s", command, code, errOut)
	}
	if code, _, _ := j.wrapped(t, "cat '"+j.secret+"'"); code == 0 {
		t.Fatal("the same wall that ran the work also handed over the secret")
	}
}

// Rule 10: five checks under the real policy, and the probe proves the wall before the
// work runs.
func TestProbeProvesTheWall(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	code, out, errOut := j.tool(t, j.env(), "probe", "--read", j.read, "--write", j.write, "--secret", j.secret)
	if code != 0 {
		t.Fatalf("probe exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"PROBE STEP name=write_outside_control expect=allow got=allow",
		"PROBE STEP name=write_outside expect=deny got=deny",
		"PROBE STEP name=read_secret expect=deny got=deny",
		"PROBE STEP name=write_inside expect=allow got=allow",
		"PROBE STEP name=read_root expect=allow got=allow",
		"PROBE OK backend=sandbox-exec",
		"steps=5 passed=5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the probe did not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not-a-real-key") || strings.Contains(errOut, "not-a-real-key") {
		t.Fatal("the probe printed the secret's contents")
	}
	// Rule 6: a --secret inside a named list is a misconfiguration, not a failed probe.
	inside := filepath.Join(j.read, "env")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = j.tool(t, j.env(), "probe", "--read", j.read, "--write", j.write, "--secret", inside)
	if code != 2 || !strings.Contains(errOut, "reason=secret_inside_allow") {
		t.Fatalf("a --secret inside --read was exit %d: %s", code, errOut)
	}
}

// Rule 4 and the refusal grammar, through the binary's own argv.
func TestRefusalsThroughTheArgv(t *testing.T) {
	j := newJob(t)
	// The command is this platform's shell, not /bin/sh: every case below is about a FLAG,
	// and a command that resolves on no PATH entry would answer them with its own refusal.
	nop := append(append([]string{}, j.shell(t)...), noopScript())
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"no_write", "reason=bad_write", append([]string{"--read", j.read, "--"}, nop...)},
		{"no_dashdash", "reason=no_command", []string{"--write", j.write, nop[0]}},
		{"home_outside", "reason=home_outside", append([]string{"--write", j.write, "--"}, nop...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := j.env()
			if tc.name == "home_outside" {
				env = []string{"HOME=" + j.base, "PATH=/usr/bin:/bin"}
			}
			code, _, errOut := j.tool(t, env, tc.args...)
			if code != 125 || !strings.Contains(errOut, tc.want) {
				t.Fatalf("exit %d, stderr %q; want 125 and %s", code, errOut, tc.want)
			}
		})
	}
}

// check is a question, not an attempt, and version is the build.
func TestCheckAndVersion(t *testing.T) {
	j := newJob(t)
	code, out, _ := j.tool(t, j.env(), "check")
	if code != 0 || !strings.HasPrefix(out, "CHECK OK backend=") {
		t.Fatalf("check exit %d: %q", code, out)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(out, "backend=sandbox-exec") {
		t.Fatalf("check did not name the backend on darwin: %q", out)
	}
	if runtime.GOOS != "darwin" && !strings.Contains(out, "backend=none") {
		t.Fatalf("check named a backend on %s, where this build has none: %q", runtime.GOOS, out)
	}
	code, out, _ = j.tool(t, j.env(), "version")
	if code != 0 || !strings.Contains(out, "tool=nova-sandbox") {
		t.Fatalf("version exit %d: %q", code, out)
	}
}

// The usage banner carries the --read remedy, which is where it has to live: on linux
// the tool is gone by the time the command dies, so it cannot say so after the fact.
func TestUsageCarriesTheReadRemedy(t *testing.T) {
	j := newJob(t)
	code, out, _ := j.tool(t, j.env(), "help")
	if code != 0 || !strings.Contains(out, readRemedy) {
		t.Fatalf("the usage banner does not carry the --read remedy: %q", out)
	}
	if !strings.Contains(out, "example:") {
		t.Fatal("the usage banner has no runnable example: block")
	}
}

// Rule 1 on every platform whose body is not built: the refusal names the platform and
// the command does NOT run.
func TestUnbuiltPlatformsRefuse(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("skipped on darwin: the darwin body is built, and its refusals are tested above")
	}
	j := newJob(t)
	marker := filepath.Join(j.write, "ran")
	code, _, errOut := j.wrapped(t, touchScript(marker))
	if code != 125 || !strings.Contains(errOut, "reason=no_sandbox") || !strings.Contains(errOut, runtime.GOOS) {
		t.Fatalf("exit %d, stderr %q; want 125, reason=no_sandbox and the platform named", code, errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran on a platform with no wall")
	}
}

// realGit is the git a caller would use: /usr/bin/git on a Mac is an Xcode shim that
// reads /var/db/xcode_select_link, which no root grants, so it fails inside the wall.
func realGit(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/opt/local/bin/git"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	t.Skip("skipped: no git outside /usr/bin on this machine, and the Xcode shim cannot run inside the wall")
	return ""
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
}

func mustOutput(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return string(out)
}

// profiles/darwin-check.sh, run against the profile THIS TOOL generates rather than the
// one the script fills for itself. One text, filled two ways: if the generator and the
// script ever disagree, this is where it shows, and it shows as a named check rather
// than as a job that dies in its first second.
func TestTheCheckScriptPassesAgainstTheToolsProfile(t *testing.T) {
	needDarwin(t)
	root := repoRoot(t)
	script := filepath.Join(root, "profiles", "darwin-check.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("skipped: %s is not in this checkout", script)
	}
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the tool: %v\n%s", err, out)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	// Test 16 is absolute: no test reaches outside t.TempDir() or touches the network.
	// The script's own scratch lives beside it, in the repo working tree, and its two DNS
	// checks curl a third-party host — right for the operator run and for the mac CI job,
	// where the spec's work list puts them, and wrong for a Go test on a machine with an
	// egress policy, where they would go red for a reason that is not about the wall.
	cmd.Env = append(os.Environ(),
		"NOVA_SANDBOX_FILL="+bin,
		"NOVA_CHECK_SCRATCH="+filepath.Join(t.TempDir(), "check"),
		"NOVA_CHECK_NO_NETWORK=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("darwin-check.sh against the tool's generated profile failed: %v\n%s", err, out)
	}
	// The count is the SCRIPT's, and no number here or in the spec states it: a test that
	// named one would go red every time a check was added. What is asserted is that the
	// script ran a real suite and that none of it failed.
	if n := strings.Count(string(out), "CHECK OK name="); n < 20 {
		t.Fatalf("only %d checks passed; the script's own count is higher than that:\n%s", n, out)
	}
	if n := strings.Count(string(out), "CHECK SKIP name="); n != 2 {
		t.Fatalf("want the two DNS checks skipped under NOVA_CHECK_NO_NETWORK, got %d SKIP lines:\n%s", n, out)
	}
	if strings.Contains(string(out), "CHECK FAIL") {
		t.Fatalf("a check failed against the tool's profile:\n%s", out)
	}
}

// repoRoot walks up from this package to the module root, so the test can find the
// script without a guessed path (SPEC.md: no guessed paths).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above this package")
		}
		dir = parent
	}
}

// TestMain is the half of rule 10's re-exec that lives in the test binary: the probe
// re-executes os.Executable() with an internal verb, and under `go test` os.Executable()
// is THIS binary. Dispatching probe-step here makes the test binary the tool for that one
// verb, so the probe under test is the real re-exec and not a stub of it.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "probe-step" {
		os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	}
	os.Exit(m.Run())
}

// Rule 10, revision 9: "the child is the same binary with an internal verb, never a
// shell", and read_root "reads the first byte of the probe's own executable
// (os.Executable())". Before this test the probe wrapped `sh -c <script>` and read_root
// read /bin/sh, which lies under the FIXED root /bin — so the one check written to
// exercise the run-time root "the directory of the resolved command" exercised a root
// that is in the profile verbatim. This is the test the spec said would decide which.
func TestProbeReExecsTheToolAndNeverAShell(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	code, out, errOut := j.tool(t, j.env(), "probe", "--read", j.read, "--write", j.write, "--secret", j.secret)
	if code != 0 {
		t.Fatalf("probe exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	want := "PROBE STEP name=read_root expect=allow got=allow path=" + self
	if !strings.Contains(out, want) {
		t.Fatalf("read_root did not read the probe's own executable.\nwant a line %q\ngot:\n%s", want, out)
	}
	// No step stands on a shell. /bin and /usr/bin are fixed roots, so a read under one
	// of them proves nothing about the root the generator computes at run time.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "PROBE STEP ") {
			continue
		}
		for _, shell := range []string{"path=/bin/sh", "path=/bin/dash", "path=/bin/bash", "path=/usr/bin/sh"} {
			if strings.Contains(line, shell) {
				t.Fatalf("the probe still stands on a shell: %s", line)
			}
		}
	}
}

// The internal verb itself: it does the open, the write and the one-byte read in Go, so
// no probe step is ever a shell string and no path the caller handed the tool is ever
// re-parsed (rule 12: never through a shell).
func TestProbeStepIsTheInternalVerb(t *testing.T) {
	j := newJob(t)
	target := filepath.Join(j.write, "step")
	if code, _, errOut := j.tool(t, j.env(), "probe-step", "write_inside", target); code != 0 {
		t.Fatalf("probe-step write_inside exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("write_inside left its file behind; the step writes and removes")
	}
	if code, _, _ := j.tool(t, j.env(), "probe-step", "read_root", filepath.Join(j.base, "no-such-file")); code == 0 {
		t.Fatal("read_root reported success on a file that is not there")
	}
	if code, _, _ := j.tool(t, j.env(), "probe-step", "read_secret", j.secret); code != 0 {
		t.Fatal("read_secret could not open a file that is readable outside the wall")
	}
	code, _, errOut := j.tool(t, j.env(), "probe-step", "not_a_step", target)
	if code != 2 || !strings.Contains(errOut, "is not a probe step") {
		t.Fatalf("an unknown step was accepted: exit %d, stderr %q", code, errOut)
	}
}

// The falsifying read's B2, verbatim: a --secret path holding a quote and a semicolon was
// concatenated into the probe's shell script, so the injected command RAN inside the wall
// and read_secret's verdict flipped from deny to allow. Two halves, and both must hold:
// the path never reaches an interpreter, and rule 5 resolves --secret like every other
// path the caller hands the tool, so a path that does not exist is a refusal rather than
// a silent pass (the reader's m7).
func TestSecretPathIsNeverInterpreted(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	injected := filepath.Join(j.write, "INJECTED")
	evil := "/etc/hosts' ; : > '" + injected
	code, out, errOut := j.tool(t, j.env(), "probe", "--read", j.read, "--write", j.write, "--secret", evil)
	if _, err := os.Stat(injected); err == nil {
		t.Fatalf("a --secret path injected a command that ran inside the wall: %s exists\nstdout: %s", injected, out)
	}
	if strings.Contains(out, "name=read_secret expect=deny got=allow") {
		t.Fatalf("the injected command flipped read_secret's verdict:\n%s", out)
	}
	if code == 0 {
		t.Fatalf("a --secret that names no file was a probe PASS: exit %d\n%s\n%s", code, out, errOut)
	}
	// A --secret that simply does not exist is the same refusal, and it is rule 5's.
	code, _, errOut = j.tool(t, j.env(), "probe", "--read", j.read, "--write", j.write,
		"--secret", filepath.Join(j.base, "secret", "NO-SUCH-FILE"))
	if code == 0 {
		t.Fatalf("a misspelled --secret was a probe PASS: %s", errOut)
	}
}

// Rule 9's NOTE line, which no test pinned: it is printed before the command starts,
// whenever the scrub removed anything, naming EXACTLY what was dropped — and never
// otherwise. A NOTE that names a variable the child still has is a false statement about
// the wall, which is the silent-sandbox failure in reverse.
func TestTheNoteNamesExactlyWhatWasDropped(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	env := j.env("SSH_AUTH_SOCK=/private/tmp/a.sock", "GPG_AGENT_INFO=/private/tmp/g:1:1",
		"AI_AGENT=rowan", "CLAUDE_AGENT_SDK_VERSION=1.2.3", "FOO_TOKEN=keep-me")
	code, _, errOut := j.tool(t, env, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "true")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := "SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies"
	if !strings.Contains(errOut, want) {
		t.Fatalf("the NOTE line is not the grammar rule 9 fixes.\nwant: %s\ngot:  %s", want, errOut)
	}
	for _, kept := range []string{"AI_AGENT", "CLAUDE_AGENT_SDK_VERSION", "FOO_TOKEN"} {
		if strings.Contains(errOut, kept) {
			t.Fatalf("the NOTE claims to have dropped %s, which rule 9 passes through: %s", kept, errOut)
		}
	}
	// And never otherwise: nothing dropped, no NOTE.
	if _, _, errOut := j.tool(t, j.env(), "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "true"); strings.Contains(errOut, "SANDBOX NOTE") {
		t.Fatalf("a NOTE was printed with nothing dropped: %s", errOut)
	}
}

// Rule 12: "every file it opens is CLOEXEC and only 0, 1 and 2 are passed". The observable
// is cheap and the spec names it: a wrapped listing of /dev/fd. The second half is the
// measured hazard the same rule states — /dev/fd/N re-opens a descriptor the caller held,
// so the tool must hand the child none of its own.
func TestOnlyStdinStdoutStderrArePassedToTheChild(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	code, out, errOut := j.tool(t, j.env(), "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "ls /dev/fd")
	if code != 0 {
		t.Fatalf("ls /dev/fd inside the wall: exit %d; %s", code, errOut)
	}
	// The control is the SAME listing outside the wall: `ls` opens the directory it is
	// listing, so a bare count would assert the shape of ls rather than the shape of the
	// wrap. What the rule claims is that the tool adds none of its own, and the two
	// listings being equal is exactly that claim.
	control, err := exec.Command("/bin/sh", "-c", "ls /dev/fd").Output()
	if err != nil {
		t.Fatalf("control: ls /dev/fd outside the wall: %v", err)
	}
	if strings.Fields(out) == nil || strings.Join(strings.Fields(out), " ") != strings.Join(strings.Fields(string(control)), " ") {
		t.Fatalf("the child's descriptors differ from the same command's outside the wall:\ninside:  %q\noutside: %q", out, control)
	}
	for _, fd := range []string{"0", "1", "2"} {
		if !strings.Contains(out, fd) {
			t.Fatalf("the child is missing descriptor %s: %q", fd, out)
		}
	}
	// A descriptor THIS process holds onto the secret does not reach the child.
	f, err := os.Open(j.secret)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, out, _ = j.tool(t, j.env(), "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "cat /dev/fd/"+strconv.Itoa(int(f.Fd()))+" 2>/dev/null; exit 0")
	if strings.Contains(out, "not-a-real-key") {
		t.Fatalf("a descriptor the caller held reached the child: %q", out)
	}
}

// Test 15: the `policy` verb prints the generated policy and runs NOTHING, twice
// identically, and there is no flag by which a caller hands the tool a profile of its own.
// It is also the verb the spec's reader command now uses, so a reader who pastes that
// command gets what this test asserts.
func TestPolicyVerbPrintsAndRunsNothing(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	marker := filepath.Join(j.write, "ran")
	code, out, errOut := j.tool(t, j.env(), "policy", "--read", j.read, "--write", j.write)
	if code != 0 {
		t.Fatalf("policy exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "(version 1)") || !strings.Contains(out, "(deny default)") {
		t.Fatalf("the printed policy is not a profile:\n%s", out)
	}
	if !strings.Contains(errOut, "POLICY OK backend=sandbox-exec") {
		t.Fatalf("the POLICY OK line is not the grammar the spec fixes: %q", errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the policy verb ran something")
	}
	_, again, _ := j.tool(t, j.env(), "policy", "--read", j.read, "--write", j.write)
	if again != out {
		t.Fatal("the same lists printed two different policies")
	}
	// Rule 15: the tool never accepts a caller-supplied profile file, and the way a
	// reader can tell is that no such flag exists.
	for _, flag := range []string{"--profile", "--policy-file", "-f", "--print-policy"} {
		code, _, errOut := j.tool(t, j.env(), "policy", "--write", j.write, flag, "x")
		if code == 0 {
			t.Fatalf("%s was accepted by the policy verb", flag)
		}
		if !strings.Contains(errOut, "is not a flag this tool has") {
			t.Fatalf("%s was refused for the wrong reason: %s", flag, errOut)
		}
	}
}

// Test 10's two unexecuted branches: the named outside path must be OUTSIDE every list,
// and it must be writable by this user anyway, or the probe cannot answer its question
// and says so at exit 2 rather than reporting a check that failed.
func TestProbeRefusesWhenItCannotAnswerTheQuestion(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	// The outside path is <parent of the first --write>/.nova-sandbox-probe-<pid>. A
	// --read that covers that parent puts it INSIDE a named list. The nest keeps the
	// secret out of that --read, so the refusal under test is the one that fires.
	nest := filepath.Join(j.base, "nest")
	nestWrite := filepath.Join(nest, "w")
	if err := os.MkdirAll(filepath.Join(nestWrite, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	nestEnv := []string{"HOME=" + filepath.Join(nestWrite, "home"), "PATH=/opt/homebrew/bin:/usr/bin:/bin"}
	code, _, errOut := j.tool(t, nestEnv, "probe", "--read", nest, "--write", nestWrite, "--secret", j.secret)
	if code != 2 || !strings.Contains(errOut, "reason=probe_outside_inside") {
		t.Fatalf("exit %d, stderr %q; want 2 and reason=probe_outside_inside", code, errOut)
	}
	// And a parent this user cannot write to: a deny there proves nothing, because it was
	// never possible.
	ro := filepath.Join(j.base, "ro")
	if err := os.MkdirAll(filepath.Join(ro, "w", "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	env := []string{"HOME=" + filepath.Join(ro, "w", "home"), "PATH=/opt/homebrew/bin:/usr/bin:/bin"}
	code, _, errOut = j.tool(t, env, "probe", "--write", filepath.Join(ro, "w"), "--secret", j.secret)
	if code != 2 || !strings.Contains(errOut, "reason=probe_outside_unwritable") {
		t.Fatalf("exit %d, stderr %q; want 2 and reason=probe_outside_unwritable", code, errOut)
	}
}

// toolBinary builds nova-sandbox once for a test that needs a REAL process, not run() in
// this one: a process group is a property of a process, and the tests above that call
// run() in process share the test binary's group.
func toolBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the tool: %v\n%s", err, out)
	}
	return bin
}

// The wall's child stays in the CALLER's process group, and the caller owns pgid and
// reaping. A swarm supervisor puts each job in its own group and reaps that group when
// the job's deadline passes (SPEC-SWARM rule 11); if the tool put its child in a group of
// its own, a command that forked a background child left that child outside the group the
// supervisor kills — the reaper reported survivors=0 while a process was still running,
// which is the silent failure the rule exists to prevent.
//
// The test models exactly that: the tool is started in a group of the TEST's making, the
// wrapped command forks a background sleep and exits, and the test kills the group it
// made. Red with Setpgid on the tool's child; green without it.
func TestAForkedChildIsReapedWithTheCallersGroup(t *testing.T) {
	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	pidFile := filepath.Join(j.write, "bg.pid")
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "sleep 300 & echo $! > '"+pidFile+"'; exit 0")
	cmd.Env = j.env()
	// The caller's own group, the way a supervisor starts a job.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		t.Fatalf("the wrapped command did not run: %v", err)
	}
	pgid := cmd.Process.Pid
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the wrapped command wrote no background pid: %v", err)
	}
	bg, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || bg <= 0 {
		t.Fatalf("background pid %q: %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(bg, syscall.SIGKILL) })
	if err := syscall.Kill(bg, 0); err != nil {
		t.Fatalf("control: the background child was already gone before the reap: %v", err)
	}
	// The reap: the caller kills the group IT made, which is the only group it knows.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(bg, 0); err != nil {
			return // reaped
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d survived the caller's group kill (pgid %d): the tool put its child in a group of its own, outside the one the caller reaps", bg, pgid)
}
