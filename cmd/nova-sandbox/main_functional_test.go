//go:build functional

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox/darwincheck"
	"github.com/mas-bandwidth/nova-tools/profiles"
	"github.com/stretchr/testify/require"
)

// The darwin check (pkg/sandbox/darwincheck, run by tools/sandboxcheck), run against
// the profile THIS TOOL generates rather than the one the check fills for itself. One text,
// filled two ways: if the generator and the check ever disagree, this is where it shows, and
// it shows as a named check rather than as a job that dies in its first second. It is behind
// the functional tag because it waits on the wall clock (measured over 5 s on the 2026-09-25
// PR run): the System it hands in is darwincheck.OSSystem, whose Sleep is time.Sleep, and the
// check's waitForSocket polls a live nc listener with it. A clock that does not wait finds no
// listener and fails the socket checks for a reason that is not the wall, so a fake clock
// changes the behavior being checked.
func TestTheCheckScriptPassesAgainstTheToolsProfile(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir = repoRoot(t)
	build.Env = goenv.Clean(os.Environ())
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "building the tool: %v\n%s", err, buildOut)
	// Test 16 is absolute: no test reaches outside t.TempDir() or touches the network.
	// The check's own scratch is handed to it, and its two DNS checks curl a third-party
	// host -- right for the operator run and for the mac CI job, where the spec's work list
	// puts them, and wrong for a Go test on a machine with an egress policy, where they
	// would go red for a reason that is not about the wall.
	var out, errOut bytes.Buffer
	code := darwincheck.Run(darwincheck.Options{
		Template:  profiles.DarwinTemplate,
		Scratch:   filepath.Join(t.TempDir(), "check"),
		NoNetwork: true,
		Fill:      bin,
		Stdout:    &out,
		Stderr:    &errOut,
	}, darwincheck.OSSystem{})
	require.Equal(t, 0, code, "the darwin check against the tool's generated profile failed: exit %d\n%s%s", code, out.String(), errOut.String())
	// The count is the CHECK's, and no number here or in the spec states it: a test that
	// named one would go red every time a check was added. What is asserted is that the
	// check ran a real suite and that none of it failed.
	n := strings.Count(out.String(), "CHECK OK name=")
	require.GreaterOrEqual(t, n, 20, "only %d checks passed; the check's own count is higher than that:\n%s", n, out.String())
	n = strings.Count(out.String(), "CHECK SKIP name=")
	require.Equal(t, 2, n, "want the two DNS checks skipped under NoNetwork, got %d SKIP lines:\n%s", n, out.String())
	require.NotContains(t, out.String(), "CHECK FAIL", "a check failed against the tool's profile:\n%s", out.String())
}

// This build's fix to the spec: (allow network*) reaches every unix-domain socket, so
// the SSH agent socket was connectable from inside the wall. A socket created outside
// the wall must not be connectable from inside it, and SSH_AUTH_SOCK must be gone.
//
// The control is deterministic under load (#2958). The earlier form bound with
// `nc -lU` and took "the socket file exists" as readiness, but bind(2) creates the
// file before listen(2), so a busy machine could dial in that window and the control
// failed with a refused connect. The listener is now this test binary re-executed with
// an internal verb: it prints READY only after net.Listen has returned (bind AND
// listen), serves every connection in order, and prints the line each client sent. The
// walled attempt sits between two unwalled controls on the same listener, so "nothing
// connected from inside the wall" is read from the accept order, not from a timeout.
func TestTheAgentSocketIsUnreachable(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("skipped: nc is not on this machine, and it is how a socket is dialled here")
	}
	// Test 16: no test reaches outside t.TempDir(). The socket therefore lives in this
	// job's own outside directory, which is under t.TempDir() and in NEITHER list — and
	// it is bound and dialled by RELATIVE name, with the process's cwd in that directory,
	// exactly as tools/sandboxcheck does it. sun_path is 104 bytes and the absolute
	// path of a t.TempDir() is longer, so an absolute bind fails silently and the one
	// test this build's network fix exists for would pass for the wrong reason.
	lines := startUnixListener(t, j.outside, "agent.sock")
	// The reads are received directly, with no wall-clock bound: a listener that
	// stops answers ok=false (the channel closes), and one that answers nothing
	// hangs until the package test's own -timeout.
	next := func(what string) string {
		t.Helper()
		l, ok := <-lines
		require.True(t, ok, "control: the listener exited before %s", what)
		return l
	}
	l := next("READY")
	require.Equal(t, "READY", l, "control: no listener could be bound outside the wall: %s", l)

	sock := filepath.Join(j.outside, "agent.sock")
	env := j.env("SSH_AUTH_SOCK="+sock, "SSH_AGENT_PID=1")
	// The cwd inside the wall is the first --write, so the controls dial from there too:
	// the same relative name, the same socket, a sun_path of 26 bytes. Each client sends
	// one tagged line, and the listener echoes the tag of every connection it accepts.
	dial := func(tag string) string { return "printf '" + tag + "\\n' | nc -U ../outside/agent.sock -w 1" }
	control := func(tag string) {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", dial(tag))
		cmd.Dir = j.write
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "control: the socket is not connectable outside the wall (%s): %v %s", tag, err, out)
		got := next("ACCEPT " + tag)
		require.NotEqual(t, "ACCEPT walled", got, "a unix socket outside the wall was connectable from inside it: the listener accepted the walled client")
		require.Equal(t, "ACCEPT "+tag, got, "control: the listener saw %q, want %q", got, "ACCEPT "+tag)
	}

	control("control-before")
	code, _, errOut := j.tool(t, env, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", dial("walled"))
	require.NotEqual(t, 0, code, "a unix socket outside the wall was connectable from inside it")
	// The listener serves in accept order, and the walled client has exited, so had it
	// connected its line would come before this control's.
	control("control-after")
	require.Contains(t, errOut, "SANDBOX NOTE dropped", "the dropped agent variables were not named before the command started: %q", errOut)
	require.Contains(t, errOut, "SSH_AUTH_SOCK", "the dropped agent variables were not named before the command started: %q", errOut)
	code, _, _ = j.tool(t, env, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "test -z \"$SSH_AUTH_SOCK\" && test -z \"$SSH_AGENT_PID\"")
	require.Equal(t, 0, code, "SSH_AUTH_SOCK reached the child's environment")
}

func init() { unixListener = serveUnixForTest }

// startUnixListener runs the listener in dir and returns its stdout lines: READY once
// bound and listening (or LISTEN-ERROR), then ACCEPT <tag> per connection, in order.
func startUnixListener(t *testing.T, dir, name string) <-chan string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	cmd := exec.Command(self, unixListenerVerb)
	cmd.Env = append(os.Environ(), unixListenerNameEnv+"="+name)
	cmd.Dir = dir
	cmd.Stdout = pw
	err = cmd.Start()
	require.NoError(t, err, "control: no listener could be started outside the wall: %v", err)
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
