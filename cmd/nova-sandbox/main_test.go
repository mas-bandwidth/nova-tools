package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

// wrapped runs one /bin/sh script INSIDE the wall with this job's lists.
func (j job) wrapped(t *testing.T, script string, extraEnv ...string) (int, string, string) {
	t.Helper()
	return j.tool(t, j.env(extraEnv...),
		"--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", script)
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
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"no_write", "reason=bad_write", []string{"--read", j.read, "--", "/bin/sh", "-c", "true"}},
		{"no_dashdash", "reason=no_command", []string{"--write", j.write, "/bin/sh"}},
		{"home_outside", "reason=home_outside", []string{"--write", j.write, "--", "/bin/sh", "-c", "true"}},
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
	code, _, errOut := j.wrapped(t, "touch '"+marker+"'")
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
