package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox/darwincheck"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/profiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nonzero is a row's exit when all that is pinned is that the run failed: inside the wall
// that is the wall refusing, and its exact number is the command's.
const nonzero = -1

// exitIs is r.Exit(code), or require.NotEqual(0) for a nonzero row.
func exitIs(t *testing.T, r testkit.Ran, code int) testkit.Ran {
	t.Helper()
	if code == nonzero {
		require.NotEqual(t, 0, r.Code, r)
		return r
	}
	return r.Exit(code)
}

// The wall from inside, one script per row under this job's lists, on darwin.
//   - Rule 1 and rule 12: a wrapped command runs, the OK line names the wall and never an
//     argument (arguments carry task text), and the exit status is the command's.
//   - Rule 3, the reason the tool exists: the named secret, and a planted ~/.ssh key in
//     neither list, are unreadable INSIDE and readable outside in the same test; a denial
//     that was never possible is not a wall.
//   - The three escapes read 4 of PR #70 tried, each from inside, each failing closed.
//   - The first second of a real job, by absolute path: the wall stands and the job runs.
//   - With no key readable and no agent reachable a push out of the job fails; the remote
//     is a local path outside every list, because no test here touches the network.
func TestTheWallHoldsAndTheJobRuns(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	require.Contains(t, testkit.ReadFile(t, j.secret), "not-a-real-key", "control: the secret is not readable outside the wall")
	key := filepath.Join(j.base, "fakehome", ".ssh", "id_test")
	testkit.WriteFile(t, key, "PRIVATE KEY\n", 0o600)
	// Homebrew's git first, the path a developer shell takes; /usr/bin/git is an Xcode shim
	// that works inside the wall because the profile grants xcode_select_link (#1557).
	git := "/usr/bin/git"
	for _, p := range []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/opt/local/bin/git"} {
		if statErr(p) == nil {
			git = p
			break
		}
	}
	gitRun := func(args ...string) string {
		out, err := exec.Command(git, args...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	remote, repo := filepath.Join(j.outside, "remote.git"), filepath.Join(j.write, "repo")
	gitRun("init", "-q", "--bare", remote)
	gitRun("init", "-q", repo)
	testkit.WriteFile(t, filepath.Join(repo, "f.txt"), "x\n", 0o600)
	gitRun("-C", repo, "add", "-A")
	gitRun("-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed")
	gitRun("-C", repo, "remote", "add", "origin", remote)
	w := j.write
	for _, c := range []struct {
		name, script string
		exit         int
		err, notErr  []string
	}{
		{"wrapped true names the wall", "true", 0, []string{"SANDBOX OK backend=sandbox-exec", "net=nopromise", "cmd=sh"}, []string{"-c"}},
		{"exit status 3", "exit 3", 3, nil, nil},
		{"a SIGKILL death is 128+9", "kill -9 $$", 137, nil, nil},
		// The number alone cannot tell the tool's NO from the command's; the line is how a
		// caller can, so a command's own 125 has none.
		{"a command's own 125 has no refusal line", "exit 125", 125, nil, []string{"SANDBOX REFUSED"}},
		{"the secret is unreadable", "cat '" + j.secret + "' > /dev/null", nonzero, nil, nil},
		{"a key outside both lists is unreadable", "cat '" + key + "' > /dev/null", nonzero, nil, nil},
		{"escape hardlink_the_secret_in", "ln '" + j.secret + "' '" + w + "/hard' && cat '" + w + "/hard'", nonzero, nil, nil},
		{"escape symlink_out_and_write", "ln -s '" + j.outside + "' '" + w + "/lnk' && echo x > '" + w + "/lnk/f'", nonzero, nil, nil},
		{"escape symlink_to_the_secret_and_read", "ln -s '" + j.secret + "' '" + w + "/s' && cat '" + w + "/s'", nonzero, nil, nil},
		{"job write_inside", "echo nova > '" + w + "/out.txt' && test \"$(cat '" + w + "/out.txt')\" = nova", 0, nil, nil},
		{"job mkdir_p_absolute", "mkdir -p '" + w + "/a/b/c' && test -d '" + w + "/a/b/c'", 0, nil, nil},
		{"job git_init_absolute", "'" + git + "' init -q '" + w + "/g' && test -d '" + w + "/g/.git'", 0, nil, nil},
		{"job home_config_write", "cd '" + w + "/g' && '" + git + "' config --global user.name nova-test && test -f '" + j.home + "/.gitconfig'", 0, nil, nil},
		{"job cat_etc_hosts", "cat /etc/hosts > /dev/null", 0, nil, nil},
		{"job sh_c_true", "/bin/sh -c true", 0, nil, nil},
		{"job child_kill", "sleep 5 & kill $!", 0, nil, nil},
		{"job tmpdir_is_inside", "test -n \"$TMPDIR\" && echo x > \"$TMPDIR/t\" && test -f '" + w + "/.nova-sandbox-tmp/t'", 0, nil, nil},
		{"job write_outside_is_denied", ": > '" + j.outside + "/probe'", nonzero, nil, nil},
		{"git push out of the job fails", "cd '" + repo + "' && '" + git + "' push -q origin HEAD:refs/heads/main", nonzero, nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			exitIs(t, j.wrapped(t, c.script), c.exit).Err(c.err...).NotErr(c.notErr...)
		})
	}
	_, err := os.ReadFile(key)
	require.NoError(t, err, "control: the key is not readable outside the wall")
	require.Error(t, statErr(filepath.Join(j.outside, "f")), "a write through a symlink landed outside the wall")
	require.NoError(t, os.WriteFile(filepath.Join(j.outside, "control"), []byte("x"), 0o600), "control: the outside path is unwritable anyway, so the denial proves nothing")
	require.Empty(t, strings.TrimSpace(gitRun("-C", remote, "for-each-ref", "--format=%(refname)")), "the push landed")
	// End to end: a real command under a read set that EXCLUDES the secret's directory, and
	// the same wall refusing the secret.
	command, args := "/bin/sh", []string{"-c", "true"}
	if oc, err := exec.LookPath("opencode"); err == nil {
		command, args = oc, []string{"--version"}
	}
	j.run(t, append([]string{"--read", j.read, "--write", j.write, "--", command}, args...)...).Exit(0)
	require.NotEqual(t, 0, j.wrapped(t, "cat '"+j.secret+"'").Code, "the same wall that ran the work also handed over the secret")
}

// #1557: /usr/bin/c++ is an Xcode shim that reads /var/db/xcode_select_link. The profile
// granted /var as a literal on the symlink, not a subpath, so the shim died inside the wall
// with "unable to read data link" and a worker read that as "no compiler installed". A
// probe that compiles outside the wall must compile inside it, with no extra --read.
func TestCXXCompilesInsideTheWallOnDarwin(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	if statErr("/usr/bin/c++") != nil {
		t.Skip("skipped: /usr/bin/c++ is not on this machine")
	}
	j := newJob(t)
	src := filepath.Join(j.write, "probe.cpp")
	testkit.WriteFile(t, src, "#include <iostream>\nint main(){ std::cout << \"ok\\n\"; return 0; }\n")
	if out, err := exec.Command("/usr/bin/c++", "-o", filepath.Join(j.outside, "probe"), src).CombinedOutput(); err != nil {
		t.Skipf("skipped: /usr/bin/c++ does not compile outside the wall, so a denial inside it proves nothing: %s", out)
	}
	bin := filepath.Join(j.write, "probe")
	j.run(t, "--write", j.write, "--", "/usr/bin/c++", "-o", bin, src).Exit(0).NotErr("xcode_select_link")
	j.run(t, "--write", j.write, "--", bin).Exit(0).Out("ok")
}

// zsh switches large heredocs from a pipe to a temporary file, chosen on macOS from
// TMPPREFIX and not TMPDIR; an inherited outside prefix made a legitimate report write
// fail at the wall even though its destination was allowed.
func TestZshLargeHeredocKeepsItsTemporaryFileInsideTheWall(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	if statErr("/bin/zsh") != nil {
		t.Skip("/bin/zsh is not available on this macOS machine")
	}
	j := newJob(t)
	script := "test \"$TMPPREFIX\" = \"$TMPDIR/zsh\" || exit 1\ncat <<'NOVA_REPORT' > \"$TMPDIR/report\"\n" + strings.Repeat("x", 16000) + "\nNOVA_REPORT\ntest \"$(wc -c < \"$TMPDIR/report\")\" -eq 16001\n"
	j.runEnv(t, j.env("TMPPREFIX="+j.outside+"/zsh"), "--read", j.read, "--write", j.write, "--", "/bin/zsh", "-c", script).Exit(0)
	info, err := os.Stat(filepath.Join(j.write, ".nova-sandbox-tmp", "report"))
	require.NoError(t, err)
	require.Equal(t, int64(16001), info.Size(), "the report was not written inside the selected temp directory at its full size")
}

// This build's fix to the spec: (allow network*) reaches every unix-domain socket, so the
// SSH agent socket was connectable from inside the wall. A socket created outside the wall
// must not be connectable from inside it, and SSH_AUTH_SOCK must be gone.
//
// The control is deterministic under load (#2958). The earlier form bound with `nc -lU`
// and took "the socket file exists" as readiness, but bind(2) creates the file before
// listen(2), so a busy machine dialled in that window and the control failed. The listener
// is this test binary re-executed with an internal verb: it prints READY only after
// net.Listen has returned, serves connections in order and prints each client's line. The
// walled attempt sits between two unwalled controls on the same listener, so "nothing
// connected from inside the wall" is read from the accept order, not from a timeout.
func TestTheAgentSocketIsUnreachable(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("skipped: nc is not on this machine, and it is how a socket is dialled here")
	}
	// Test 16: no test reaches outside t.TempDir(), so the socket lives in this job's outside
	// directory, in NEITHER list, bound and dialled by RELATIVE name from that cwd as
	// tools/sandboxcheck does: sun_path is 104 bytes and a t.TempDir() path is longer, so an
	// absolute bind fails silently and this test would pass for the wrong reason.
	lines := startUnixListener(t, j.outside, "agent.sock")
	next := func(what string) string {
		t.Helper()
		select {
		case l, ok := <-lines:
			require.True(t, ok, "control: the listener exited before %s", what)
			return l
		case <-time.After(60 * time.Second):
			t.Fatalf("control: no %s from the listener in 60 s", what)
		}
		return ""
	}
	require.Equal(t, "READY", next("READY"), "control: no listener could be bound outside the wall")

	env := j.env("SSH_AUTH_SOCK="+filepath.Join(j.outside, "agent.sock"), "SSH_AGENT_PID=1")
	// The cwd inside the wall is the first --write, so the controls dial from there too: the
	// same relative name, a sun_path of 26 bytes, one tagged line per client.
	dial := func(tag string) string { return "printf '" + tag + "\\n' | nc -U ../outside/agent.sock -w 1" }
	control := func(tag string) {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", dial(tag))
		cmd.Dir, cmd.Env = j.write, env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "control: the socket is not connectable outside the wall (%s): %s", tag, out)
		got := next("ACCEPT " + tag)
		require.NotEqual(t, "ACCEPT walled", got, "a unix socket outside the wall was connectable from inside it: the listener accepted the walled client")
		require.Equal(t, "ACCEPT "+tag, got, "control: the listener's accept order")
	}

	control("control-before")
	r := j.runEnv(t, env, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", dial("walled"))
	require.NotEqual(t, 0, r.Code, "a unix socket outside the wall was connectable from inside it")
	// Had the walled client (now exited) connected, its line would come before this one.
	control("control-after")
	r.Err("SANDBOX NOTE dropped", "SSH_AUTH_SOCK")
	j.runEnv(t, env, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "test -z \"$SSH_AUTH_SOCK\" && test -z \"$SSH_AGENT_PID\"").Exit(0)
}

// unixListenerVerb is the test binary's internal verb for TestTheAgentSocketIsUnreachable's
// listener; TestMain dispatches it. It is a child process, not a goroutine, because the
// socket is bound by a relative name from its own cwd and a test must not chdir.
const unixListenerVerb = "test-unix-listener"

// unixListenerNameEnv carries the socket's relative name to the listener child. It is an
// environment field, not a positional argument: argv carries only the verb (law #2583).
const unixListenerNameEnv = "NOVA_SANDBOX_TEST_UNIX_NAME"

// startUnixListener runs the listener in dir and returns its stdout lines: READY once
// bound and listening (or LISTEN-ERROR), then ACCEPT <tag> per connection, in order.
func startUnixListener(t *testing.T, dir, name string) <-chan string {
	t.Helper()
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	cmd := exec.Command(selfExecutable(t), unixListenerVerb)
	cmd.Env = append(os.Environ(), unixListenerNameEnv+"="+name)
	cmd.Dir, cmd.Stdout = dir, pw
	require.NoError(t, cmd.Start(), "control: no listener could be started outside the wall")
	_ = pw.Close()
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		defer pr.Close()
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return lines
}

// serveUnixForTest is the listener's body: bind and listen by relative name, say READY,
// then accept one connection at a time and echo the first line each client sends.
func serveUnixForTest(name string) int {
	ln, err := net.Listen("unix", name)
	if err != nil {
		fmt.Printf("LISTEN-ERROR %v\n", err)
		return 1
	}
	fmt.Println("READY")
	for {
		c, err := ln.Accept()
		if err != nil {
			return 1
		}
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, _ := bufio.NewReader(c).ReadString('\n')
		fmt.Printf("ACCEPT %s\n", strings.TrimSpace(line))
		_ = c.Close()
	}
}

// Rule 10 on darwin: the probe proves the wall before the work runs, and every way it can
// be asked a question it cannot answer is a refusal rather than a verdict.
func TestProbeProvesTheWall(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	self := selfExecutable(t)
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	steps := []string{
		"PROBE STEP name=write_outside_control expect=allow got=allow",
		"PROBE STEP name=write_outside expect=deny got=deny",
		"PROBE STEP name=write_inside expect=allow got=allow",
		"PROBE STEP name=read_root expect=allow got=allow",
		"PROBE OK backend=sandbox-exec",
	}
	t.Run("five checks with a secret", func(t *testing.T) {
		r := j.run(t, "probe", "--read", j.read, "--write", j.write, "--secret", j.secret).Exit(0).Out(steps...)
		r.Out("PROBE STEP name=read_secret expect=deny got=deny", "steps=5 passed=5").NotOut("not-a-real-key").NotErr("not-a-real-key")
		// Revision 9: "the child is the same binary with an internal verb, never a shell", and
		// read_root reads the probe's own executable. When the probe wrapped `sh -c` read_root
		// read /bin/sh, under the FIXED root /bin, so the check meant for the run-time root
		// "the directory of the resolved command" exercised a root that is in the profile
		// verbatim. No step may stand on a shell for the same reason.
		r.Out("PROBE STEP name=read_root expect=allow got=allow path=" + self)
		for _, line := range strings.Split(r.Stdout, "\n") {
			if strings.HasPrefix(line, "PROBE STEP ") {
				for _, shell := range []string{"path=/bin/sh", "path=/bin/dash", "path=/bin/bash", "path=/usr/bin/sh"} {
					require.NotContains(t, line, shell, "the probe still stands on a shell")
				}
			}
		}
	})
	// Issue #881: a key delivered by nova-secrets exec is never a file, so a probe without one
	// is sound: the other four checks, and no read_secret step invented.
	t.Run("four checks without a secret", func(t *testing.T) {
		j.run(t, "probe", "--read", j.read, "--write", j.write).Exit(0).Out(steps...).Out("steps=4 passed=4").NotOut("read_secret")
	})
	t.Run("rule 6: a secret inside a list is a misconfiguration, not a failed probe", func(t *testing.T) {
		inside := filepath.Join(j.read, "env")
		testkit.WriteFile(t, inside, "x", 0o600)
		j.run(t, "probe", "--read", j.read, "--write", j.write, "--secret", inside).ExitErr(2, "reason=secret_inside_allow")
	})
	// The falsifying read's B2: a --secret holding a quote and a semicolon was concatenated
	// into the probe's shell script, so the injected command RAN inside the wall and
	// read_secret flipped to allow. The path never reaches an interpreter, and rule 5
	// resolves --secret like every path, so one that is not there is a refusal and not a
	// silent pass (the reader's m7).
	t.Run("the secret path is never interpreted", func(t *testing.T) {
		injected := filepath.Join(j.write, "INJECTED")
		r := j.run(t, "probe", "--read", j.read, "--write", j.write, "--secret", "/etc/hosts' ; : > '"+injected)
		require.Error(t, statErr(injected), "a --secret path injected a command that ran inside the wall")
		r.NotOut("name=read_secret expect=deny got=allow")
		require.NotEqual(t, 0, r.Code, "a --secret that names no file was a probe PASS: %s", r)
		r = j.run(t, "probe", "--read", j.read, "--write", j.write, "--secret", filepath.Join(j.base, "secret", "NO-SUCH-FILE"))
		require.NotEqual(t, 0, r.Code, "a misspelled --secret was a probe PASS: %s", r)
	})
	// Test 10's two unexecuted branches: the outside path (<parent of the first
	// --write>/.nova-sandbox-probe-<pid>) must be OUTSIDE every list and writable by this
	// user anyway, or the probe cannot answer and says so at exit 2.
	t.Run("it cannot answer the question", func(t *testing.T) {
		// A --read covering that parent puts it inside a list; the nest keeps the secret out
		// of that --read, so the refusal under test is the one that fires.
		nest := filepath.Join(j.base, "nest")
		require.NoError(t, os.MkdirAll(filepath.Join(nest, "w", "home"), 0o755))
		env := []string{"HOME=" + filepath.Join(nest, "w", "home"), "PATH=/opt/homebrew/bin:/usr/bin:/bin"}
		j.runEnv(t, env, "probe", "--read", nest, "--write", filepath.Join(nest, "w"), "--secret", j.secret).ExitErr(2, "reason=probe_outside_inside")
		// A parent this user cannot write to: a deny there was never possible.
		ro := filepath.Join(j.base, "ro")
		require.NoError(t, os.MkdirAll(filepath.Join(ro, "w", "home"), 0o755))
		require.NoError(t, os.Chmod(ro, 0o500))
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		env = []string{"HOME=" + filepath.Join(ro, "w", "home"), "PATH=/opt/homebrew/bin:/usr/bin:/bin"}
		j.runEnv(t, env, "probe", "--write", filepath.Join(ro, "w"), "--secret", j.secret).ExitErr(2, "reason=probe_outside_unwritable")
	})
}

// Rule 4 and the refusal grammar through the binary's own argv, on every platform, and
// what a refusal leaves behind: nothing.
func TestRefusalsThroughTheArgv(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	// The command is this platform's shell, not /bin/sh: every case is about a FLAG, and a
	// command on no PATH entry would answer with its own refusal.
	nop := append(append([]string{}, j.shell(t)...), noopScript())
	tmp := filepath.Join(j.write, ".nova-sandbox-tmp")
	missing := filepath.Join(j.base, "no-such-cache")
	notExec := filepath.Join(j.read, "data.txt")
	testkit.WriteFile(t, notExec, "x\n", 0o600)
	type row struct {
		name        string
		env, args   []string
		exit        int
		err         []string
		absent, dir string // a path the run must not create; a directory it must
	}
	rows := []row{
		{name: "no_write", args: append([]string{"--read", j.read, "--"}, nop...), exit: 125, err: []string{"reason=bad_write"}},
		{name: "no_dashdash", args: []string{"--write", j.write, nop[0]}, exit: 125, err: []string{"reason=no_command"}},
		{name: "home_outside", env: []string{"HOME=" + j.base, "PATH=/usr/bin:/bin"}, args: append([]string{"--write", j.write, "--"}, nop...), exit: 125, err: []string{"reason=home_outside"}},
		// Rule 16: "a refusal names the flag and the form it wants". parse() is shared by every
		// verb, so `nova-sandbox --write <dir> --secret /etc/hosts --max 3 -- true` exited 0
		// with probe's and check's flags silently dropped.
		{name: "flags of another verb", args: []string{"--write", j.write, "--secret", j.secret, "--max", "3", "--", "/bin/sh", "-c", "true"}, exit: 125,
			err: []string{"--secret is not a flag of the bare form", "--max is not a flag of the bare form", "probe"}},
		// Build "creates exactly one directory, rule 8's, and only when the rest of the input is
		// sound". It made the temp directory before resolving the command, so a refused
		// `--write <fresh> -- no-such-cmd` left .nova-sandbox-tmp behind.
		{name: "not_found creates nothing", args: []string{"--write", j.write, "--", "no-such-command-xyz"}, exit: 127, absent: tmp},
		// --read-noexec: a --read root carries EXECUTE on both bodies (landlock's read subset is
		// EXECUTE|READ_FILE|READ_DIR, the darwin profile grants process-exec*), so a tree the
		// job's user can write could be RUN from. Johnny's security read of #1364 stopped
		// ~/go/pkg/mod being granted that way, and until the flag existed Policy.ReadsNoExec was
		// unreachable from the argv. Rule 5 holds it like --read; rule 16 names it.
		{name: "read-noexec that is not there", args: []string{"--read-noexec", missing, "--write", j.write, "--", "/bin/sh", "-c", "true"}, exit: 125,
			err: []string{"reason=bad_read", "--read-noexec"}, absent: missing},
		{name: "read-noexec with no value", args: []string{"--write", j.write, "--read-noexec"}, exit: nonzero, err: []string{"--read-noexec wants a value"}},
	}
	// The executable BIT is rule 5's pre-flight and a unix idea: windows refuses a .txt later.
	if runtime.GOOS != "windows" {
		rows = append(rows, row{name: "not_executable creates nothing", args: []string{"--write", j.write, "--", notExec}, exit: 125, absent: tmp})
	}
	// A sound run needs a backend, so these are darwin's until the linux body is built. The
	// sound run gets the one directory, because rule 8 is why it exists. --acl goes the OTHER
	// way from rule 16: the verb table has it on the bare form on all three platforms,
	// accepted and ignored here, "so one caller has one script for three platforms".
	if runtime.GOOS == "darwin" {
		rows = append(rows,
			row{name: "a sound run still gets its temp directory", args: append([]string{"--read", j.read, "--write", j.write, "--"}, nop...), dir: tmp},
			row{name: "acl caller is accepted and ignored", args: []string{"--write", j.write, "--acl", "caller", "--", "/bin/sh", "-c", "true"}, err: []string{"SANDBOX NOTE --acl caller is accepted and ignored"}},
			row{name: "acl nonsense", args: []string{"--write", j.write, "--acl", "nonsense", "--", "/bin/sh", "-c", "true"}, exit: 125, err: []string{"--acl wants tool or caller"}})
	}
	for _, c := range rows {
		t.Run(c.name, func(t *testing.T) {
			env := c.env
			if env == nil {
				env = j.env()
			}
			exitIs(t, j.runEnv(t, env, c.args...), c.exit).Err(c.err...)
			if c.absent != "" {
				require.Error(t, statErr(c.absent), "a refused run created %s; a refusal makes nothing and no path is guessed", c.absent)
			}
			if c.dir != "" {
				fi, err := os.Stat(c.dir)
				require.NoError(t, err)
				require.True(t, fi.IsDir(), "the one directory this tool creates was not created for a sound run")
			}
		})
	}
}

// check is a question, not an attempt, and version is the build.
func TestCheckAndVersion(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	r := j.run(t, "check").Exit(0)
	require.True(t, strings.HasPrefix(r.Stdout, "CHECK OK backend="), r)
	switch runtime.GOOS {
	case "darwin":
		r.Out("backend=sandbox-exec")
	case "linux":
		// The abi and the net= field are asserted against each other in TestCheckReportsLandlock.
		require.True(t, strings.Contains(r.Stdout, "backend=landlock") || strings.Contains(r.Stdout, "backend=none"), "check named neither landlock nor none on linux: %s", r)
	default:
		r.Out("backend=none")
	}
	// `version` is SPEC.md's Conventions line, the four tokens every binary prints, then this
	// tool's two extras. Its old shape, `SANDBOX VERSION tool=... version=...`, no reader of
	// a version line could take apart (#1297).
	r = j.run(t, "version").Exit(0)
	f, ok := buildinfo.Parse(r.Stdout)
	require.True(t, ok, "version printed a line internal/buildinfo.Parse refuses: %s", r)
	require.Equal(t, "nova-sandbox", f.Tool, r)
	require.NotEmpty(t, f.Version, r)
	for _, x := range [][2]string{{"backend", sandbox.Backend}, {"platform", runtime.GOOS}} {
		v, have := f.Extra(x[0])
		require.True(t, have, "version does not carry %s=: %s", x[0], r)
		require.Equal(t, x[1], v, r)
	}
}

// The usage banner. It carries the --read remedy, which has to live there: on linux the
// tool is gone by the time the command dies, so it cannot say so after the fact. It names
// --read-noexec, or a caller cannot find it (ONBOARDING.md point 2). And PR 948 re-cut
// (#893): the linux read roots are not switchable, so it must not advertise a
// --no-system-reads the parser no longer has.
func TestUsageCarriesTheReadRemedy(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	r := j.run(t, "help").Exit(0).Out(readRemedy, "example:", "--read-noexec").NotOut("no-system-reads")
	// The pasted line runs. Emma pasted the probe example out of `nova-sandbox help` and it
	// exited 2 (`PROBE REFUSED reason=check: HOME /Users/glenn is outside every --write`)
	// because it set no HOME while rule 9 requires one inside a --write.
	probe := ""
	for _, block := range strings.Split(r.Stdout, "\n\n") {
		if strings.Contains(block, "nova-sandbox probe ") {
			probe = block
		}
	}
	require.NotEmpty(t, probe, "the banner has no probe example at all")
	require.Contains(t, probe, "HOME=", "the probe example omits HOME=, so a reader who pastes it is refused by rule 9")
	// ONBOARDING.md: an example's lines are RUN, not grepped; a grep for "HOME=" is green on
	// an example that exits 2 for any other reason (a --secret gone, a flag renamed, an
	// unwritable parent). So paste it as a reader does, with /path/to in this test's temp dir
	// (DeepSeek's read of #108 at ab880be, finding 2).
	t.Run("the probe example sets HOME and runs", func(t *testing.T) {
		needDarwin(t)
		home := ""
		var argv []string
		for _, line := range exampleCommands(t, probe, j.base) {
			switch {
			case strings.HasPrefix(line, "mkdir -p "):
				require.NoError(t, os.MkdirAll(strings.Trim(strings.TrimPrefix(line, "mkdir -p "), `"'`), 0o755))
			case strings.HasPrefix(line, "touch "):
				testkit.WriteFile(t, strings.Trim(strings.TrimPrefix(line, "touch "), `"'`), "", 0o600)
			case strings.Contains(line, "nova-sandbox probe "):
				fields := strings.Fields(line)
				for i, f := range fields {
					if strings.HasPrefix(f, "HOME=") {
						home = strings.Trim(strings.TrimPrefix(f, "HOME="), `"'`)
					}
					if f == "nova-sandbox" {
						argv = fields[i+1:]
					}
				}
				for i, a := range argv {
					argv[i] = strings.Trim(a, `"'`)
				}
			default:
				t.Fatalf("the probe example has a line this test cannot run: %q", line)
			}
		}
		require.NotEmpty(t, home, "the probe example is not a HOME= plus a nova-sandbox command:\n%s", probe)
		require.NotEmpty(t, argv, "the probe example is not a HOME= plus a nova-sandbox command:\n%s", probe)
		// The one thing a reader supplies that a temp dir cannot: the credential file, created
		// empty (the probe proves it CANNOT be read; rule 5 refuses one that is not there).
		for i, a := range argv {
			if a == "--secret" && i+1 < len(argv) {
				testkit.WriteFile(t, argv[i+1], "", 0o600)
			}
		}
		r := j.runEnv(t, []string{"HOME=" + home, "PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"}, argv...)
		require.NotEqual(t, 2, r.Code, "the pasted probe example could not run (exit 2); an example that exits 2 is a documentation defect:\n%s\n%s", probe, r)
		r.Exit(0).Out("PROBE OK")
	})
}

// Rule 1 on every platform whose body is not built: the refusal names the platform and
// the command does NOT run.
func TestUnbuiltPlatformsRefuse(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skipf("skipped on %s: this body IS built (darwin is sandbox-exec, linux is landlock), and its refusals are tested by its own file", runtime.GOOS)
	}
	j := newJob(t)
	marker := filepath.Join(j.write, "ran")
	j.wrapped(t, touchScript(marker)).ExitErr(125, "reason=no_sandbox").Err(runtime.GOOS)
	require.Error(t, statErr(marker), "the command ran on a platform with no wall")
}

// The darwin check (internal/sandbox/darwincheck, run by tools/sandboxcheck), run against
// the profile THIS TOOL generates rather than the one the check fills for itself. One text,
// filled two ways: if the generator and the check ever disagree, this is where it shows, as
// a named check rather than as a job that dies in its first second.
func TestTheCheckScriptPassesAgainstTheToolsProfile(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	needDarwin(t)
	// Test 16: no test reaches outside t.TempDir() or touches the network. The check's DNS
	// checks curl a third-party host, right for the operator run and the mac CI job and wrong
	// for a Go test on a machine with an egress policy.
	var out, errOut bytes.Buffer
	code := darwincheck.Run(darwincheck.Options{
		Template: profiles.DarwinTemplate, Scratch: filepath.Join(t.TempDir(), "check"),
		NoNetwork: true, Fill: toolBinary(t), Stdout: &out, Stderr: &errOut,
	}, darwincheck.OSSystem{})
	require.Equal(t, 0, code, "the darwin check against the tool's generated profile failed:\n%s%s", out.String(), errOut.String())
	// The count is the CHECK's: a number here would go red every time a check was added.
	// What is pinned is that a real suite ran and none of it failed.
	n := strings.Count(out.String(), "CHECK OK name=")
	require.GreaterOrEqual(t, n, 20, "only %d checks passed; the check's own count is higher than that:\n%s", n, out.String())
	require.Equal(t, 2, strings.Count(out.String(), "CHECK SKIP name="), "want the two DNS checks skipped under NoNetwork:\n%s", out.String())
	require.NotContains(t, out.String(), "CHECK FAIL", "a check failed against the tool's profile")
}

// #1557 HOLD: the darwin check fills the template two ways. The tool generator follows
// xcode_select_link into OptionalRoots; the hand filler did not, so a reader running the
// check without the generator got the link literal without the selected Xcode root, while
// cxx_compile required that root.
func TestDarwinCheckHandFillerGrantsTheSameXcodeRoot(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	var xcode []string
	for _, r := range sandbox.OptionalRoots("/bin/echo") {
		if strings.Contains(r, "Xcode.app") {
			xcode = append(xcode, r)
		}
	}
	if len(xcode) == 0 {
		t.Skip("skipped: xcode-select's developer dir is already a fixed root or absent on this machine")
	}
	j := newJob(t)
	p, bad := sandbox.Build(sandbox.Input{Reads: []string{j.read}, Writes: []string{j.write}, Home: j.home, Argv: []string{"/bin/echo"}})
	require.Empty(t, bad, "generated policy refused")
	generated, _, err := sandbox.DarwinProfile(p)
	require.NoError(t, err)
	var hand, errOut bytes.Buffer
	code := darwincheck.Run(darwincheck.Options{
		Template: profiles.DarwinTemplate, Scratch: filepath.Join(t.TempDir(), "check"),
		NoNetwork: true, DumpProfile: true, Stdout: &hand, Stderr: &errOut,
	}, darwincheck.OSSystem{})
	require.Equal(t, 0, code, "hand-filling the profile:\n%s%s", hand.String(), errOut.String())
	for _, r := range xcode {
		grant := `(allow file-read* (subpath "` + r + `"))`
		require.Contains(t, generated, grant, "the generated profile does not grant what OptionalRoots named: %v", xcode)
		assert.Contains(t, hand.String(), grant, "the hand-filled profile does not grant what the generated profile has; the two filler modes drifted")
	}
	if t.Failed() {
		t.Logf("hand-filled profile:\n%s", hand.String())
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
	if len(os.Args) == 2 && os.Args[1] == unixListenerVerb {
		os.Exit(serveUnixForTest(os.Getenv(unixListenerNameEnv)))
	}
	os.Exit(m.Run())
}

// probeChild is the internal verb run as a REAL CHILD of exe, with nonceVar in its
// environment and fd3 (or no fd 3 when nil) as its descriptor 3. It cannot be j.run: that
// runs the verb IN PROCESS, where os.Getppid() is `go test`'s and fd 3 is whatever the test
// binary holds, and the halves of the guard about the PROCESS need a process. exe is this
// binary for the probe's own child, a copy of it for a foreign parent.
func probeChild(t *testing.T, exe string, fd3 *os.File, nonceVar string) testkit.Main {
	return func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		cmd := exec.Command(exe, append([]string{"probe-step"}, args...)...)
		cmd.Env, cmd.Stdout, cmd.Stderr = append(os.Environ(), nonceVar), stdout, stderr
		if fd3 != nil {
			cmd.ExtraFiles = []*os.File{fd3}
		}
		err := cmd.Run()
		require.NotNil(t, cmd.ProcessState, "probe-step child never ran: %v", err)
		return cmd.ProcessState.ExitCode()
	}
}

// pipeFD3 is fd 3 as the probe's parent hands it: a pipe holding raw, its write end closed.
func pipeFD3(t *testing.T, raw []byte) *os.File {
	t.Helper()
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pr.Close() })
	_, err = pw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	return pr
}

// copyOfThisBinary is a second executable with the same bytes and a different path: the
// foreign parent, for which "the parent process is this binary" is false while every other
// half of the guard is true. A COPY, never a link: a hard link is this binary
// (internal/testbin.PlaceCopy).
func copyOfThisBinary(t *testing.T) string {
	t.Helper()
	name := "nova-sandbox-copy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	copied := filepath.Join(t.TempDir(), name)
	require.NoError(t, testbin.PlaceCopy(selfExecutable(t), copied))
	return copied
}

// The internal verb itself: it does the open, the write and the one-byte read in Go, so no
// probe step is ever a shell string and no path the caller handed the tool is re-parsed
// (rule 12: never through a shell).
func TestProbeStepIsTheInternalVerb(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	target := filepath.Join(j.write, "step")
	testkit.WriteFile(t, target, "MUST-SURVIVE\n", 0o600)
	survives := func() {
		t.Helper()
		require.Equal(t, "MUST-SURVIVE\n", testkit.ReadFile(t, target), "a refused step touched the file")
	}
	// The guard first, because it is the whole of this verb's safety: probe-step opens,
	// TRUNCATES and reads its path, and at 1922f9d nothing stood between an ordinary shell and
	// that O_TRUNC. Measured: `nova-sandbox probe-step write_outside <file>` emptied a file
	// outside every wall and exited 0.
	j.run(t, "probe-step", "write_outside", target).ExitErr(2, "probe_step_not_a_child")
	survives()
	// The second measurement, on 29646c1: the argv and the environment are BOTH the caller's
	// to set, so `NOVA_SANDBOX_PROBE_NONCE=<x> nova-sandbox probe-step <x> write_outside
	// <file>` agreed with itself and truncated the file. Refused in process and as a child.
	raw := []byte("0123456789abcdef")
	nonce := hex.EncodeToString(raw)
	env := probeNonceVar + "=" + nonce
	self := selfExecutable(t)
	step := []string{nonce, "write_outside", target}
	j.runEnv(t, j.env(env), append([]string{"probe-step"}, step...)...).ExitErr(2, "probe_step_not_a_child")
	probeChild(t, self, nil, env).Do(t, step...).ExitErr(2, "probe_step_not_a_child")
	survives()
	if runtime.GOOS == "windows" {
		// Everything below hands a child a descriptor, and exec.Cmd.ExtraFiles is unsupported
		// on windows: a test that never ran dressed as one that failed (measured: "probe-step
		// child never ran"). No windows body is built, so no probe there has a child at all.
		t.Skip("skipped on windows: exec.Cmd.ExtraFiles is unsupported there, and no windows sandbox body is built for a probe to have a child at all")
	}
	regularPath := filepath.Join(t.TempDir(), "fd3")
	testkit.WriteFile(t, regularPath, string(raw), 0o600)
	regular, err := os.Open(regularPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = regular.Close() })
	inside := filepath.Join(j.write, "inside")
	for _, c := range []struct {
		name, exe string
		fd3       *os.File
		args      []string
		exit      int
		err       string
	}{
		// Holding a descriptor is not holding the probe's secret: the value is what it is for.
		{"a pipe carrying the wrong value", self, pipeFD3(t, []byte("fedcba9876543210")), step, 2, "probe_step_not_a_child"},
		{"a short pipe: fewer bytes than the parent promised", self, pipeFD3(t, raw[:8]), step, 2, "probe_step_not_a_child"},
		// The parent writes exactly probeNonceLen and closes.
		{"a long pipe: more bytes than the parent promised", self, pipeFD3(t, append(append([]byte{}, raw...), 'x')), step, 2, "probe_step_not_a_child"},
		// The guard wants the parent's pipe, and a file is a thing a caller can make.
		{"a regular file on fd 3", self, regular, step, 2, "probe_step_not_a_child"},
		// Everything right but the parent: the half a caller who has learned the shape of the
		// guard cannot supply without already being the tool.
		{"a foreign parent", copyOfThisBinary(t), pipeFD3(t, raw), step, 2, "probe_step_not_a_child"},
		// With all the parent passes, the steps are themselves; the parent builds every step
		// path absolute.
		{"a relative step path", self, pipeFD3(t, raw), []string{nonce, "write_inside", "step"}, 2, "absolute"},
		{"write_inside", self, pipeFD3(t, raw), []string{nonce, "write_inside", inside}, 0, ""},
		{"read_root on a file that is not there", self, pipeFD3(t, raw), []string{nonce, "read_root", filepath.Join(j.base, "no-such-file")}, nonzero, ""},
		{"read_secret on a file readable outside the wall", self, pipeFD3(t, raw), []string{nonce, "read_secret", j.secret}, 0, ""},
		{"an unknown step", self, pipeFD3(t, raw), []string{nonce, "not_a_step", target}, 2, "is not a probe step"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := exitIs(t, probeChild(t, c.exe, c.fd3, env).Do(t, c.args...), c.exit)
			if c.err != "" {
				r.Err(c.err)
			}
		})
	}
	survives()
	require.Error(t, statErr(inside), "write_inside left its file behind; the step writes and removes")
	// A verb a caller must not run is not offered to one.
	require.NotContains(t, usage, probeStepVerbName, "probe-step is in the usage banner")
}

// The parent half of the guard is an IDENTITY test, here on its own with no process in it:
// every case is a file on disk and an answer that cannot move.
//
// It used to compare path STRINGS, the wrong question twice over: too weak (a name says
// nothing about which FILE wears it) and too brittle. The two paths come from syscalls that
// spell the same file differently (os.Executable() as passed to exec, the kernel's per-pid
// path resolved; measured on darwin, /tmp/x against /private/tmp/x), so the old form leaned
// on filepath.EvalSymlinks and SWALLOWED its error: one component it could not Lstat (a
// directory being removed, an interrupted call on a loaded machine, a step the wall denies)
// and it silently compared spellings, a guard that answers differently under load.
// device+inode is one stat each, is what "the same binary" means, and does not move.
func TestTheParentGuardComparesFilesAndNotNames(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: the probe's parent guard is not built there, and os.SameFile there is a different identity")
	}
	dir, elsewhere := t.TempDir(), t.TempDir()
	tool := filepath.Join(dir, "tool")
	require.NoError(t, testbin.WriteExecutable(tool, []byte("not really a tool\n"), 0o700))
	// The foreign parent: PlaceCopy, never Place, since a hard link would BE this binary.
	copied := filepath.Join(dir, "tool-copy")
	require.NoError(t, testbin.PlaceCopy(tool, copied))
	// A symlink and a hard link are the same file under another name.
	symlinked, hardLinked := filepath.Join(dir, "tool-symlink"), filepath.Join(dir, "tool-hardlink")
	require.NoError(t, os.Symlink(tool, symlinked))
	require.NoError(t, os.Link(tool, hardLinked))
	// Another spelling of its directory: what the string comparison got wrong whenever the
	// walk could not finish, and what made the legitimate probe's own child refusable.
	alias := filepath.Join(elsewhere, "alias")
	require.NoError(t, os.Symlink(dir, alias))
	missing := filepath.Join(dir, "no-such-tool")
	for _, c := range []struct {
		name, self, parent string
		want               bool
	}{
		{"the same path", tool, tool, true},
		{"a symlink to it", tool, symlinked, true},
		{"a hard link to it", tool, hardLinked, true},
		{"the same file spelled through another directory name", tool, filepath.Join(alias, "tool"), true},
		{"a byte-for-byte copy", tool, copied, false},
		{"a copy in the other direction", copied, tool, false},
		{"a parent that is not there", tool, missing, false},
		{"a self that is not there", missing, tool, false},
		{"neither is there", missing, missing, false},
	} {
		assert.Equal(t, c.want, sameImage(c.self, c.parent), "%s: sameImage(%q, %q)", c.name, c.self, c.parent)
	}
}

// fakeParent is a parent identity the test can MOVE between the guard's reads. It answers
// every os.Getppid() and every image read from a script and counts both, so that a guard
// which stopped asking after the first answer fails here rather than passing.
type fakeParent struct {
	pids   []int
	images []string
	errs   []error
	// before[i] runs just before image read i answers: how a case changes the world inside
	// the window the guard is asked about.
	before   []func()
	pidCalls int
	imgCalls int
}

func (f *fakeParent) getppid() int {
	i := f.pidCalls
	f.pidCalls++
	if i >= len(f.pids) {
		return f.pids[len(f.pids)-1]
	}
	return f.pids[i]
}

func (f *fakeParent) imageOf(int) (string, error) {
	i := f.imgCalls
	f.imgCalls++
	if i < len(f.before) && f.before[i] != nil {
		f.before[i]()
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}
	if i >= len(f.images) {
		return f.images[len(f.images)-1], nil
	}
	return f.images[i], nil
}

// The ordering of the parent half, syscalls taken out: the pid, the image, the pid AGAIN
// and the image AGAIN, and a refusal if anything moved between any two.
//
// The second image read is the one a pid cannot speak for: exec(2) replaces a process's
// image IN PLACE and keeps its pid, so a parent that is this binary at the first look can
// exec something else and still be the same number at the second.
//
// No clock, core or network: the world changes only where a case says, and the call counts
// make the ORDER the assertion; a guard that decided after the first answer reads the image
// once and fails here.
func TestTheParentGuardRereadsTheImageAndNotJustThePid(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: the probe's parent guard is not built there, and os.SameFile there is a different identity")
	}
	dir := t.TempDir()
	// self is a SECOND NAME for image's file, so a case can replace what lives at image
	// without touching self: that makes "the second read is a fresh stat" an assertion.
	image, self, other := filepath.Join(dir, "image"), filepath.Join(dir, "self"), filepath.Join(dir, "other")
	require.NoError(t, testbin.WriteExecutable(image, []byte("the tool\n"), 0o700))
	require.NoError(t, os.Link(image, self))
	require.NoError(t, testbin.WriteExecutable(other, []byte("the tool\n"), 0o700))
	missing := filepath.Join(dir, "no-such-image")
	failed := errors.New("proc_pidpath(7): no such process")
	// replaceImage is an exec in place with no exec in it: the same path, a different file.
	replaceImage := func() {
		if assert.NoError(t, os.Remove(image)) {
			assert.NoError(t, testbin.WriteExecutable(image, []byte("something else\n"), 0o700))
		}
	}
	const (
		notThis = "the parent process is not this binary"
		moved   = "the parent process changed the image it is running while the guard was reading it"
		unnamed = "the parent process cannot be named"
	)
	for _, c := range []struct {
		name               string
		parent             fakeParent
		want               string
		wantPids, wantImgs int
	}{
		{"nothing moved", fakeParent{pids: []int{7, 7}, images: []string{image, image}}, "", 2, 2},
		{"the parent is a copy from the first look", fakeParent{pids: []int{7, 7}, images: []string{other}}, notThis, 1, 1},
		{"the pid moved between the two looks", fakeParent{pids: []int{7, 9}, images: []string{image, image}}, "the parent process changed while the guard was reading it", 2, 1},
		// Only the image moves: exec in place, which the pid re-read alone could not see.
		{"the same pid exec'd a different path", fakeParent{pids: []int{7, 7}, images: []string{image, other}}, moved, 2, 2},
		// The same pid AND path, another file underneath: the second read must be a fresh stat.
		{"the same pid exec'd a different file at the same path", fakeParent{pids: []int{7, 7}, images: []string{image, image}, before: []func(){nil, replaceImage}}, moved, 2, 2},
		{"the first look cannot name the parent", fakeParent{pids: []int{7, 7}, images: []string{image}, errs: []error{failed}}, unnamed, 1, 1},
		// The parent went away between the looks: the second read's own witness.
		{"the second look cannot name the parent", fakeParent{pids: []int{7, 7}, images: []string{image}, errs: []error{nil, failed}}, unnamed, 2, 2},
		{"the second look is not an absolute path", fakeParent{pids: []int{7, 7}, images: []string{image, "image"}}, "the parent process is not named by an absolute path", 2, 2},
		{"the parent is a path that is not there", fakeParent{pids: []int{7, 7}, images: []string{missing}}, notThis, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Each case gets the file back as it was, because one of them replaces it.
			if err := os.Remove(image); !os.IsNotExist(err) {
				require.NoError(t, err)
			}
			require.NoError(t, os.Link(self, image))
			p := c.parent
			assert.Equal(t, c.want, parentIsThisImage(self, p.getppid, p.imageOf))
			assert.Equal(t, c.wantPids, p.pidCalls, "the guard's pid reads")
			assert.Equal(t, c.wantImgs, p.imgCalls, "the guard's image reads")
		})
	}
}

// TestProbeStepIsTheInternalVerb's foreign-parent refusal, made many times at once while
// every core is busy: a guard that fails OPEN under load is a security defect and not a
// flake, so the load belongs in the suite.
//
// No sleep, deadline or clock: the pool burns exactly as long as the children take (it is
// stopped by this test's cleanup, which runs after the parallel subtests finish), so the
// test asserts the same thing on a fast machine and a slow one.
func TestParentGuardRefusesACopiedParentUnderLoad(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: exec.Cmd.ExtraFiles is unsupported there, so no probe child can be given fd 3 at all")
	}
	j := newJob(t)
	// ONE copy, exec'd many times: a fresh copy per child would race its own write against
	// its own exec, and this test is about the guard rather than ETXTBSY.
	copied := copyOfThisBinary(t)
	raw := []byte("0123456789abcdef")
	nonce := hex.EncodeToString(raw)
	stop := make(chan struct{})
	var burning sync.WaitGroup
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		burning.Add(1)
		go func() {
			defer burning.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
	}
	t.Cleanup(func() {
		close(stop)
		burning.Wait()
	})
	for i := 0; i < 8; i++ {
		t.Run("child-"+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			// Each child's own file OUTSIDE any wall: an acceptance is a truncated file.
			target := filepath.Join(j.write, "under-load-"+strconv.Itoa(i))
			testkit.WriteFile(t, target, "MUST-SURVIVE\n", 0o600)
			probeChild(t, copied, pipeFD3(t, raw), probeNonceVar+"="+nonce).Do(t, nonce, "write_outside", target).ExitErr(2, "probe_step_not_a_child")
			require.Equal(t, "MUST-SURVIVE\n", testkit.ReadFile(t, target), "a step refused under load still touched the file")
		})
	}
}

// selfExecutable is this test binary's own path, which is the tool for the internal verb
// (see TestMain).
func selfExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	return self
}

// Rule 9's NOTE line: printed before the command starts whenever the scrub removed
// anything, naming EXACTLY what was dropped, and never otherwise. A NOTE that names a
// variable the child still has is a false statement about the wall.
func TestTheNoteNamesExactlyWhatWasDropped(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	env := j.env("SSH_AUTH_SOCK=/private/tmp/a.sock", "GPG_AGENT_INFO=/private/tmp/g:1:1",
		"AI_AGENT=rowan", "CLAUDE_AGENT_SDK_VERSION=1.2.3", "FOO_TOKEN=keep-me")
	j.runEnv(t, env, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "true").Exit(0).
		Err("SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies").
		NotErr("AI_AGENT", "CLAUDE_AGENT_SDK_VERSION", "FOO_TOKEN")
	j.run(t, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "true").NotErr("SANDBOX NOTE")
}

// Rule 12: "every file it opens is CLOEXEC and only 0, 1 and 2 are passed", observed as a
// wrapped listing of /dev/fd. Then the hazard the same rule states: /dev/fd/N re-opens a
// descriptor the caller held, so the tool must hand the child none of its own.
func TestOnlyStdinStdoutStderrArePassedToTheChild(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	r := j.run(t, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "ls /dev/fd").Exit(0).Out("0", "1", "2")
	// The control is the SAME listing outside the wall: ls opens the directory it lists, so a
	// bare count would pin the shape of ls. Equal listings are exactly "the tool adds none".
	control, err := exec.Command("/bin/sh", "-c", "ls /dev/fd").Output()
	require.NoError(t, err, "control: ls /dev/fd outside the wall")
	require.NotNil(t, strings.Fields(r.Stdout), r)
	require.Equal(t, strings.Join(strings.Fields(string(control)), " "), strings.Join(strings.Fields(r.Stdout), " "), "the child's descriptors differ from the same command's outside the wall")
	f, err := os.Open(j.secret)
	require.NoError(t, err)
	defer f.Close()
	j.run(t, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "cat /dev/fd/"+strconv.Itoa(int(f.Fd()))+" 2>/dev/null; exit 0").NotOut("not-a-real-key")
}

// Test 15: the `policy` verb prints the generated policy and runs NOTHING, twice
// identically, and no flag hands the tool a profile of the caller's own (rule 15: the way a
// reader can tell is that no such flag exists). It is the verb the spec's reader command
// uses, so a reader who pastes that command gets what this test asserts.
func TestPolicyVerbPrintsAndRunsNothing(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	r := j.run(t, "policy", "--read", j.read, "--write", j.write).Exit(0).Out("(version 1)", "(deny default)").Err("POLICY OK backend=sandbox-exec")
	require.Error(t, statErr(filepath.Join(j.write, "ran")), "the policy verb ran something")
	require.Equal(t, r.Stdout, j.run(t, "policy", "--read", j.read, "--write", j.write).Stdout, "the same lists printed two different policies")
	for _, flag := range []string{"--profile", "--policy-file", "-f", "--print-policy"} {
		exitIs(t, j.run(t, "policy", "--write", j.write, flag, "x"), nonzero).Err("unknown flag " + flag + "; run: nova-sandbox help policy")
	}
}

// toolBinary builds nova-sandbox for a test that needs a REAL process, not run() in this
// one: a process group is a property of a process, and in-process runs share the test
// binary's group. The module root is found by walking up (SPEC.md: no guessed paths).
func toolBinary(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	require.NoError(t, err)
	for statErr(filepath.Join(root, "go.mod")) != nil {
		require.NotEqual(t, root, filepath.Dir(root), "no go.mod above this package")
		root = filepath.Dir(root)
	}
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir, build.Env = root, goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoError(t, err, "building the tool: %s", out)
	return bin
}

// TESTS.md's header says every transcript line is compared with what the tool prints, but
// no test read the file, so its read_root line still said path=/bin/sh long after the probe
// stopped standing on a shell. This pins the one line that goes stale silently.
func TestTheTranscriptNamesTheToolsOwnBinary(t *testing.T) {
	t.Parallel()

	var line string
	for _, l := range strings.Split(testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md")), "\n") {
		if strings.Contains(l, "PROBE STEP name=read_root") {
			line = l
			break
		}
	}
	require.NotEmpty(t, line, "TESTS.md has no read_root transcript line to pin")
	require.True(t, strings.HasSuffix(line, "/nova-sandbox"), "the transcript's read_root does not name the tool's own binary: %q", line)
	for _, shell := range []string{"/bin/sh", "/bin/dash", "/bin/bash", "/usr/bin/sh"} {
		require.NotContains(t, line, shell, "the transcript's read_root stands on a shell")
	}
}

// probeRefusalReasons is the PROBE REFUSED reason set of docs/SPEC-SANDBOX.md's output
// grammar, copied verbatim: a token outside it is a tool and a spec that disagree, and the
// grammar is what a caller's parser stands on. TestProbeRefusalReasonsAreTheSpecsOwnSet in
// grammar_test.go holds the copy to the spec both ways; without it an edit to the spec's
// line left this green (#119).
var probeRefusalReasons = map[string]bool{
	"check": true, "secret_inside_allow": true, "probe_outside_inside": true,
	"probe_outside_unwritable": true, "no_sandbox": true, "net_unenforceable": true,
}

// Emma, dogfooding v0.12.0 (nova-tools #104, 2026-09-12): a bare `nova-sandbox probe`
// reported `--secret is required` and did not report the missing `--write` until a second
// run. A first run sequenced into as many runs as it had mistakes; a refusal reports EVERY
// independent problem at once (SPEC-SANDBOX's onboarding, SPEC-BOARD's rule 6). --secret
// is no longer required at all (issue #881): a key from nova-secrets exec is never a file.
func TestProbeNamesEveryMissingRequiredFlagAtOnce(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	t.Run("bare", func(t *testing.T) {
		r := j.run(t, "probe").Exit(2)
		assert.Contains(t, r.Stderr, "--write is required", r)
		assert.NotContains(t, r.Stderr, "--secret is required", r)
		assert.GreaterOrEqual(t, strings.Count(strings.TrimSpace(r.Stderr), "\n")+1, 1, "the refusal must be printed, one line: %s", r)
		// One refusal per problem, each inside the published grammar: a bare probe's missing
		// --write is reason=check naming bad_write, not reason=bad_write.
		for _, line := range strings.Split(strings.TrimSpace(r.Stderr), "\n") {
			reason, _, ok := strings.Cut(strings.TrimPrefix(line, "PROBE REFUSED reason="), ":")
			if assert.True(t, ok && strings.HasPrefix(line, "PROBE REFUSED reason="), "not a PROBE REFUSED line: %q", line) {
				assert.True(t, probeRefusalReasons[reason], "reason=%s is not in the PROBE REFUSED grammar of SPEC-SANDBOX.md", reason)
			}
		}
	})
	// The flags are independent: naming one must not swallow the other either way.
	t.Run("only --secret", func(t *testing.T) {
		r := j.run(t, "probe", "--secret", j.secret)
		assert.Equal(t, 2, r.Code, r)
		assert.Contains(t, r.Stderr, "--write is required", r)
	})
	t.Run("only --write", func(t *testing.T) {
		r := j.run(t, "probe", "--write", j.write)
		assert.False(t, r.Code == 2 && strings.Contains(r.Stderr, "--secret is required"), "a probe without --secret is refused for one: %s", r)
	})
	// A refusal raised before anything runs is `check` with its own token in the TEXT, as the
	// spec writes `PROBE REFUSED reason=check ... home_outside`. At ab880be this asserted only
	// the prefix and passed against main's main.go too (DeepSeek's read of #108, finding 3).
	t.Run("a --secret that does not exist is reason=check naming bad_read", func(t *testing.T) {
		r := j.run(t, "probe", "--write", j.write, "--secret", filepath.Join(j.base, "no-such-file"))
		assert.Equal(t, 2, r.Code, r)
		assert.Contains(t, r.Stderr, "PROBE REFUSED reason=check: ", r)
		assert.Contains(t, r.Stderr, "(bad_read)", "the refusal drops the bad_read token a reader greps for: %s", r)
		assert.NotContains(t, r.Stderr, "--write is required", "a run that named --write was told it had not: %s", r)
		for _, line := range strings.Split(r.Stderr, "\n") {
			if reason, _, _ := strings.Cut(strings.TrimPrefix(line, "PROBE REFUSED reason="), ":"); strings.HasPrefix(line, "PROBE REFUSED reason=") {
				assert.True(t, probeRefusalReasons[reason], "reason=%s is not in the PROBE REFUSED grammar of SPEC-SANDBOX.md", reason)
			}
		}
	})
}

// exampleCommands turns one banner example block into the lines a reader would type:
// continuations joined, indentation dropped, and every /path/to path pointed at a
// directory this test owns. The substitution is the only edit a reader makes.
func exampleCommands(t *testing.T, block, base string) []string {
	t.Helper()
	var lines []string
	joined := ""
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			joined += strings.TrimSpace(strings.TrimSuffix(line, "\\")) + " "
			continue
		}
		cmdLine := joined + line
		cmdLine = strings.ReplaceAll(cmdLine, `"$PWD/scratch`, filepath.Join(base, "scratch"))
		cmdLine = strings.ReplaceAll(cmdLine, `"$PWD"`, base)
		cmdLine = strings.ReplaceAll(cmdLine, `$PWD`, base)
		cmdLine = strings.ReplaceAll(cmdLine, "/path/to", base)
		lines = append(lines, cmdLine)
		joined = ""
	}
	require.Empty(t, joined, "the example block ends in a continuation:\n%s", block)
	return lines
}

// Rule 6 THROUGH THE BINARY, with a spelling a person can type (#145). The check for a
// --secret inside a named path compared strings, so a secret spelled in another case than
// its --read passed and the probe reported a pass; on APFS and NTFS that is ONE FILE inside
// the read set. The refusal runs before any wall is built, so it is the same everywhere.
// THE FILESYSTEM DECIDES WHETHER THIS CAN RUN, NOT runtime.GOOS: APFS can be case-sensitive
// and a linux mount can fold, so the test asks for a file back in another case.
func TestASecretSpelledInAnotherCaseIsRefusedWhereTheFilesystemFolds(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	testkit.WriteFile(t, filepath.Join(j.base, "CaseProbe"), "x\n", 0o600)
	if statErr(filepath.Join(j.base, "caseprobe")) != nil {
		t.Skipf("the filesystem under %s is case-SENSITIVE: caseprobe is not CaseProbe, so a --secret spelled in another case is a different file here and the fold this test is about cannot happen", j.base)
	}
	// There is no <base>/R or <base>/W, so each write goes through the fold into a list.
	for _, list := range []string{"R", "W"} {
		folded := filepath.Join(j.base, list, "env")
		testkit.WriteFile(t, folded, "not-a-real-key\n", 0o600)
		r := j.run(t, "probe", "--read", j.read, "--write", j.write, "--secret", folded).ExitErr(2, "reason=secret_inside_allow")
		if list == "R" {
			assert.Contains(t, r.Stderr, "the secret is never inside either list", "the refusal does not quote the rule: %s", r)
		}
	}
	// A secret in NEITHER list is still not refused for being one: the repair widens no list.
	j.run(t, "policy", "--read", j.read, "--write", j.write, "--secret", j.secret).Exit(0)
}

// The wall's two read sets end to end on darwin: a script under --read-noexec is READABLE
// and NOT EXECUTABLE, and the OK line counts it, so a log says which grant a run had. The
// control, the same file under --read, runs: the denial is the grant, not a broken script.
func TestReadNoExecReadsAndRefusesToExecuteOnDarwin(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	cache := filepath.Join(j.base, "cache")
	script := filepath.Join(cache, "x.sh")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	require.NoError(t, testbin.WriteExecutable(script, []byte("#!/bin/sh\necho ran\n"), 0o755))
	args := []string{"--read", j.read, "--read-noexec", cache, "--write", j.write, "--", "/bin/sh", "-c"}
	r := j.run(t, append(args, "cat "+script)...).Exit(0).Out("echo ran")
	assert.Contains(t, r.Stderr, "read-noexec=1", "the SANDBOX OK line does not count the no-exec reads")
	require.NotEqual(t, 0, j.run(t, append(args, script)...).Code, "the script under --read-noexec EXECUTED inside the wall; readable is not executable")
	j.run(t, "--read", cache, "--write", j.write, "--", "/bin/sh", "-c", script).Exit(0)
}

// An unknown verb exits 2 and names itself, rather than falling into the bare wrap as a
// flag or a command.
func TestUnknownVerbRefused(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"bogus verb", []string{"bogus"}},
		{"unknown verb with args", []string{"some-other-verb", "--flag"}},
		{"positional command not treated as bare wrap", []string{"echo", "hello"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := j.run(t, c.args...).Exit(sandbox.ExitCannotRun)
			require.Empty(t, r.Stdout, r)
			require.Equal(t, `SANDBOX REFUSED reason=unknown_verb: unknown verb "`+c.args[0]+`"; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help`+"\n", r.Stderr)
		})
	}
}

// check parses its arguments: unknown flags (--max included) and positional arguments are
// refused naming the door, and -h/--help is the usage.
func TestCheckFlagParsing(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	refused := func(why string) string {
		return "CHECK REFUSED reason=bad_flag: " + why + "; run: nova-sandbox help check"
	}
	for _, c := range []struct {
		name          string
		args          []string
		exit          int
		stdout, errLn string
	}{
		{"bare check", []string{"check"}, 0, "CHECK OK", ""},
		{"check with max flag refused", []string{"check", "--max", "10"}, sandbox.ExitCannotRun, "", refused("unknown flag --max")},
		{"unrecognized double-dash flag", []string{"check", "--bogus"}, sandbox.ExitCannotRun, "", refused("unknown flag --bogus")},
		{"unrecognized single-dash flag", []string{"check", "-bogus"}, sandbox.ExitCannotRun, "", refused("unknown flag -bogus")},
		{"unexpected positional argument", []string{"check", "extra"}, sandbox.ExitCannotRun, "", refused("unexpected argument extra")},
		{"check -h", []string{"check", "-h"}, 0, "usage: nova-sandbox check", ""},
		{"check --help", []string{"check", "--help"}, 0, "usage: nova-sandbox check", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := j.run(t, c.args...).Exit(c.exit)
			assert.Contains(t, r.Stdout, c.stdout, r)
			assert.Contains(t, r.Stderr, c.errLn, r)
			if c.exit == sandbox.ExitCannotRun {
				assert.Contains(t, r.Stderr, "run: nova-sandbox help check", "no door: %s", r)
			}
		})
	}
}
