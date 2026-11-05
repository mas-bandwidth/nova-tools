//go:build functional

package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

// bin builds nova-sandbox once for the package's functional tests.
func bin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-sandbox-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binary = filepath.Join(dir, "nova-sandbox")
		out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
		if err != nil {
			buildErr = &buildError{string(out)}
		}
	})
	require.NoError(t, buildErr)
	return binary
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }

// sandbox runs nova-sandbox with args from dir; returns exit code, stdout, stderr.
func sandbox(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin(t), args...)
	cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}, dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// scratch is a resolved directory with job/ (the write) and out/ beside it.
func scratch(t *testing.T) (job, out string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	job, out = filepath.Join(root, "job"), filepath.Join(root, "out")
	require.NoError(t, os.Mkdir(job, 0o700))
	require.NoError(t, os.Mkdir(out, 0o700))
	return
}

func TestCheckAndProbe(t *testing.T) {
	t.Parallel()
	code, out, _ := sandbox(t, os.TempDir(), "check")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "CHECK OK backend=")
	code, out, _ = sandbox(t, os.TempDir(), "probe")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "PROBE OK")
	assert.Contains(t, out, "name=write_outside expect=deny got=deny")
	assert.Contains(t, out, "name=read_named expect=allow got=allow")
	assert.Contains(t, out, "name=unix_deny expect=deny got=deny")
	assert.Contains(t, out, "name=unix_allow expect=allow got=allow")
}

func TestWriteInsideAllowedOutsideRefused(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	code, _, stderr := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(job, "ok"))
	require.Equal(t, 0, code, stderr)
	assert.FileExists(t, filepath.Join(job, "ok"))

	code, _, _ = sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(out, "bad"))
	assert.NotEqual(t, 0, code)
	assert.NoFileExists(t, filepath.Join(out, "bad"), "the kernel refused the write")
}

func TestReadOutsideRefusedUnlessNamed(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	secret := filepath.Join(out, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o600))
	code, stdout, _ := sandbox(t, job, "--write", job, "--", "/bin/cat", secret)
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", "/bin/cat", secret)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "s", stdout)
	// --read is not --write
	code, _, _ = sandbox(t, job, "--write", job, "--read", out, "--", "/bin/sh", "-c", "echo x > "+filepath.Join(out, "w"))
	assert.NotEqual(t, 0, code)
}

func TestExitStatusPassesThrough(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	code, _, _ := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "exit 7")
	assert.Equal(t, 7, code)
	code, _, stderr := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "kill -9 $$")
	assert.Equal(t, -1, code, "killed by a signal, so the shell parent sees it; "+stderr)
}

func TestRefusalsExit125And127(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	code, _, stderr := sandbox(t, job, "--write", "relative", "--", "/bin/true")
	assert.Equal(t, 125, code)
	assert.Contains(t, stderr, "SANDBOX REFUSED reason=bad_write")
	script := filepath.Join(out, "x.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ran\n"), 0o755))
	code, _, stderr = sandbox(t, job, "--write", job, "--", script)
	assert.Equal(t, 125, code)
	assert.Contains(t, stderr, "reason=bad_read", "a command outside every grant is refused before the wall")
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", script)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "ran\n", stdout)
	code, _, _ = sandbox(t, job, "--write", job, "--", "no-such-command-here")
	assert.Equal(t, 127, code)
	nox := filepath.Join(job, "noexec")
	require.NoError(t, os.WriteFile(nox, []byte("x"), 0o600))
	code, _, stderr = sandbox(t, job, "--write", job, "--", nox)
	assert.Equal(t, 126, code, "a file that is not executable is 126: "+stderr)
}

func TestEnvironmentAndDirectoryPassThrough(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	code, stdout, stderr := sandbox(t, job, "--write", job, "--", "/bin/sh", "-c", "pwd; echo $TMPDIR; mktemp")
	require.Equal(t, 0, code, stderr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{job, job}, lines[:2], "the caller's cwd and TMPDIR are the command's")
	assert.True(t, strings.HasPrefix(lines[2], job+"/"), "a temp file lands in the TMPDIR the caller pointed inside a --write: "+lines[2])
}

func TestArgv0IsVerbatim(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	// A symlink to /bin/sh invoked by the link's name: `sh -c` with no operand sets $0
	// to argv[0], so the command sees the name it was invoked as, not the resolved path.
	// (A shebang script cannot show this: the kernel sets $0 to the script file.)
	link := filepath.Join(out, "mysh")
	require.NoError(t, os.Symlink("/bin/sh", link))
	code, stdout, stderr := sandbox(t, job, "--write", job, "--read", out, "--", link, "-c", "echo $0")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, link+"\n", stdout, "argv[0] is the caller's, not the resolved /bin/sh target")
}

func TestUnixSocketIsFencedByDefault(t *testing.T) {
	t.Parallel()
	if _, out, _ := sandbox(t, os.TempDir(), "check"); strings.Contains(out, "seccomp UNAVAILABLE") {
		t.Skip("no seccomp on this kernel")
	}
	job, out := scratch(t)
	sock := filepath.Join(out, "s")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	self := bin(t)
	read := []string{"--read", filepath.Dir(self)}
	args := func(extra ...string) []string {
		return append(append(append([]string{}, read...), extra...), "--write", job, "--", self, "dial", sock)
	}
	code, _, stderr := sandbox(t, job, args()...)
	assert.NotEqual(t, 0, code, "AF_UNIX is refused, so the connect cannot be made: "+stderr)
	code, _, stderr = sandbox(t, job, args("--unix")...)
	assert.Equal(t, 0, code, "--unix reopens AF_UNIX: "+stderr)
}

// TestSystemdRunEscapeIsClosed reproduces the reported escape: systemd-run --user hands
// the command off to a service that runs outside the wall. The hand-off travels over a
// unix socket, which the default fence refuses, so the command cannot start the service.
func TestSystemdRunEscapeIsClosed(t *testing.T) {
	t.Parallel()
	sr, err := exec.LookPath("systemd-run")
	if err != nil {
		t.Skip("no systemd-run on this host")
	}
	if out, err := exec.Command(sr, "--user", "--wait", "--pipe", "/bin/true").CombinedOutput(); err != nil {
		t.Skip("systemd --user is not available here: " + string(out))
	}
	job, out := scratch(t)
	secret := filepath.Join(out, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("leak"), 0o600))
	code, stdout, _ := sandbox(t, job, "--read", filepath.Dir(sr), "--write", job, "--net-deny", "--",
		sr, "--user", "--wait", "--pipe", "/bin/cat", secret)
	assert.NotEqual(t, 0, code, "the hand-off socket is refused, so the service never runs")
	assert.NotContains(t, stdout, "leak", "the file outside the wall was not read")
}

func TestKeepFDsLeavesAnInheritedDescriptorOpen(t *testing.T) {
	t.Parallel()
	job, out := scratch(t)
	f, err := os.Create(filepath.Join(out, "inherited"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	_, err = f.WriteString("data")
	require.NoError(t, err)
	// Pass the open descriptor to the child as fd 3 and have it read from /dev/fd/3.
	run := func(extra ...string) (int, string) {
		cmd := exec.Command(bin(t), append(append([]string{}, extra...), "--write", job, "--", "/bin/sh", "-c", "cat <&3 2>/dev/null; true")...)
		cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin", "HOME=" + job}, job
		rf, _ := os.Open(f.Name())
		defer rf.Close()
		cmd.ExtraFiles = []*os.File{rf} // becomes fd 3 in the child
		var b strings.Builder
		cmd.Stdout = &b
		_ = cmd.Run()
		return 0, b.String()
	}
	_, withClose := run()
	assert.NotContains(t, withClose, "data", "by default the inherited fd 3 is closed before exec")
	_, withKeep := run("--keep-fds")
	assert.Contains(t, withKeep, "data", "--keep-fds leaves it open")
}

func TestNetDenyRefusesAConnect(t *testing.T) {
	t.Parallel()
	job, _ := scratch(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	dial := []string{"--read", filepath.Dir(bin(t)), "--write", job}
	args := func(extra ...string) []string {
		return append(append(append([]string{}, dial...), extra...), "--", bin(t), "dial", l.Addr().String())
	}
	code, _, stderr := sandbox(t, job, args()...)
	require.Equal(t, 0, code, "without --net-deny the connect works: "+stderr)
	code, _, stderr = sandbox(t, job, args("--net-deny")...)
	if code == 125 {
		t.Skip("--net-deny is not enforceable on this machine: " + stderr)
	}
	assert.Equal(t, 1, code, "the kernel refused the connect")
	code, _, stderr = sandbox(t, job, args("--net-deny", "--net-allow", l.Addr().String())...)
	assert.Equal(t, 0, code, "--net-allow opens the named loopback port back up: "+stderr)
}
