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

// wall runs nova-sandbox with args and HOME set to home; returns exit code, stdout, stderr.
func wall(t *testing.T, home string, extraEnv []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin(t), args...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + home}, extraEnv...)
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

// scratch is a resolved directory with job/ (the write), home/ inside it, and out/ beside it.
func scratch(t *testing.T) (job, home, out string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	job, home, out = filepath.Join(root, "job"), filepath.Join(root, "job", "home"), filepath.Join(root, "out")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.Mkdir(out, 0o700))
	return
}

func TestCheckAndProbe(t *testing.T) {
	t.Parallel()
	code, out, _ := wall(t, os.TempDir(), nil, "check")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "CHECK OK backend=")
	code, out, _ = wall(t, os.TempDir(), nil, "probe")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "PROBE OK")
	assert.Contains(t, out, "name=write_outside expect=deny got=deny")
}

func TestWriteInsideAllowedOutsideRefused(t *testing.T) {
	t.Parallel()
	job, home, out := scratch(t)
	code, _, stderr := wall(t, home, nil, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(job, "ok"))
	require.Equal(t, 0, code, stderr)
	assert.FileExists(t, filepath.Join(job, "ok"))

	code, _, _ = wall(t, home, nil, "--write", job, "--", "/bin/sh", "-c", "echo hi > "+filepath.Join(out, "bad"))
	assert.NotEqual(t, 0, code)
	assert.NoFileExists(t, filepath.Join(out, "bad"), "the kernel refused the write")
}

func TestReadOutsideRefusedUnlessNamed(t *testing.T) {
	t.Parallel()
	job, home, out := scratch(t)
	secret := filepath.Join(out, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("s"), 0o600))
	code, stdout, _ := wall(t, home, nil, "--write", job, "--", "/bin/cat", secret)
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	code, stdout, stderr := wall(t, home, nil, "--write", job, "--read", out, "--", "/bin/cat", secret)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "s", stdout)
	// --read is not --write
	code, _, _ = wall(t, home, nil, "--write", job, "--read", out, "--", "/bin/sh", "-c", "echo x > "+filepath.Join(out, "w"))
	assert.NotEqual(t, 0, code)
}

func TestExitStatusPassesThrough(t *testing.T) {
	t.Parallel()
	job, home, _ := scratch(t)
	code, _, _ := wall(t, home, nil, "--write", job, "--", "/bin/sh", "-c", "exit 7")
	assert.Equal(t, 7, code)
	code, _, stderr := wall(t, home, nil, "--write", job, "--", "/bin/sh", "-c", "kill -9 $$")
	assert.Equal(t, -1, code, "killed by a signal, so the shell parent sees it; "+stderr)
}

func TestRefusalsExit125And127(t *testing.T) {
	t.Parallel()
	job, home, _ := scratch(t)
	code, _, stderr := wall(t, home, nil, "--write", "relative", "--", "/bin/true")
	assert.Equal(t, 125, code)
	assert.Contains(t, stderr, "SANDBOX REFUSED reason=bad_write")
	code, _, stderr = wall(t, "/nowhere", nil, "--write", job, "--", "/bin/true")
	assert.Equal(t, 125, code)
	assert.Contains(t, stderr, "reason=home_outside")
	code, _, _ = wall(t, home, nil, "--write", job, "--", "no-such-command-here")
	assert.Equal(t, 127, code)
	assert.NoDirExists(t, filepath.Join(filepath.Dir(job), "x"))
}

func TestCwdAndTmp(t *testing.T) {
	t.Parallel()
	job, home, _ := scratch(t)
	sub := filepath.Join(job, "sub")
	require.NoError(t, os.Mkdir(sub, 0o700))
	code, stdout, stderr := wall(t, home, nil, "--write", job, "--cwd", sub, "--", "/bin/sh", "-c", "pwd; echo $TMPDIR")
	require.Equal(t, 0, code, stderr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)
	assert.True(t, strings.HasSuffix(lines[0], "/sub"), lines[0])
	assert.Equal(t, filepath.Join(job, ".nova-sandbox-tmp"), lines[1])
	assert.DirExists(t, filepath.Join(job, ".nova-sandbox-tmp"))
}

func TestAgentSocketsAreRemovedFromTheEnvironment(t *testing.T) {
	t.Parallel()
	job, home, _ := scratch(t)
	code, stdout, stderr := wall(t, home, []string{"SSH_AUTH_SOCK=/tmp/agent", "KEEP=1"}, "--write", job, "--", "/usr/bin/env")
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, "SSH_AUTH_SOCK")
	assert.Contains(t, stdout, "KEEP=1")
}

func TestReadNoExecCannotBeExecuted(t *testing.T) {
	t.Parallel()
	job, home, out := scratch(t)
	script := filepath.Join(out, "tool.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ran$((1+1))\n"), 0o755))
	code, stdout, stderr := wall(t, home, nil, "--write", job, "--read", out, "--", script)
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "ran2\n", stdout, "under --read it runs")
	code, stdout, _ = wall(t, home, nil, "--write", job, "--read-noexec", out, "--", "/bin/sh", "-c", script+"; cat "+script)
	assert.NotContains(t, stdout, "ran2", "under --read-noexec it is readable and not executable")
	assert.Contains(t, stdout, "echo ran$((1+1))")
	_ = code
}

func TestNetDenyRefusesAConnect(t *testing.T) {
	t.Parallel()
	job, home, _ := scratch(t)
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
	code, _, stderr := wall(t, home, nil, args()...)
	require.Equal(t, 0, code, "without --net-deny the connect works: "+stderr)
	code, _, stderr = wall(t, home, nil, args("--net-deny")...)
	if code == 125 {
		t.Skip("--net-deny is not enforceable on this machine: " + stderr)
	}
	assert.Equal(t, 1, code, "the kernel refused the connect")
	code, _, stderr = wall(t, home, nil, args("--net-deny", "--net-allow", l.Addr().String())...)
	assert.Equal(t, 0, code, "--net-allow opens the named loopback port back up: "+stderr)
}
