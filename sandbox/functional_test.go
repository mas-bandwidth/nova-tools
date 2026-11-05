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
