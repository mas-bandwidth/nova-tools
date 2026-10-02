//go:build linux

// The linux wall, run for real on a linux machine: a real Landlock ruleset, a real
// wrapped command, a real denial from the kernel. These are the counterpart of the
// needDarwin tests above, and they exist because a green from a suite that ran nothing
// reads exactly like a green from one that ran.
//
// EVERY TEST HERE RUNS THE TOOL AS A SUBPROCESS, never in process. A Landlock domain
// CANNOT BE LIFTED, so a Run() in the test binary would wall the test binary itself and
// every later test in the package would fail against a wall it never asked for. The darwin
// tests run in process because sandbox-exec's policy lives around a child; linux's lives
// on the caller, so linux's tests need a caller of their own.
package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// walledTool builds the real nova-sandbox ONCE for this file: these tests run the tool
// many times over, and a binary in one test's t.TempDir() would vanish when that test
// ended.
var walledTool = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "nova-sandbox-bin")
	if err != nil {
		return "", err
	}
	tool := filepath.Join(dir, "nova-sandbox")
	build := exec.Command("go", "build", "-o", tool, ".")
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v: %s", err, out)
	}
	return tool, nil
})

// runTool runs nova-sandbox as a child and returns its status and streams.
func (j job) runTool(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	tool, err := walledTool()
	require.NoError(t, err, "the tool under test could not be built")
	cmd := exec.Command(tool, args...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "nova-sandbox did not run at all")
	}
	return code, out.String(), errb.String()
}

// wall runs one /bin/sh script inside the wall of a normal job policy.
func (j job) wall(t *testing.T, script string, extra ...string) (int, string, string) {
	t.Helper()
	args := append([]string{"--read", j.read, "--write", j.write}, extra...)
	args = append(args, "--", "/bin/sh", "-c", script)
	return j.runTool(t, j.env(), args...)
}

// landlockJob is a new job, skipping BY NAME on a machine with no Landlock, so that a
// green here always means the kernel actually enforced something.
func landlockJob(t *testing.T) job {
	t.Helper()
	j := newJob(t)
	code, out, _ := j.runTool(t, j.env(), "check")
	if code != 0 || !strings.Contains(out, "backend=landlock") {
		t.Skipf("skipped: this linux kernel reports no landlock, so there is no wall to test: %q", out)
	}
	return j
}

// The wall's whole reason: a worker that reads untrusted input all day cannot write
// outside the job it was given.
func TestLandlockWallRefusesWriteOutsideJob(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	outside := filepath.Join(j.outside, "escaped")

	// THE CONTROL FIRST, and it is why the denial below can be believed: a write that
	// was never possible is not a wall. Outside the tool, this user can create the file.
	testkit.WriteFile(t, outside, "x")
	require.NoError(t, os.Remove(outside))

	code, _, errOut := j.wall(t, "echo escaped > "+outside)
	require.NotEqual(t, 0, code, "the wrapped command WROTE OUTSIDE THE JOB and exited 0: %s", errOut)
	require.Error(t, statErr(outside), "the file outside the job exists: the wall did not hold")
	// And the wall was announced, on the stream a log keeps.
	assert.Contains(t, errOut, "SANDBOX OK backend=landlock")
}

// The clamp, end to end, on whatever kernel this machine has. An ABI above the tool's
// table is not a refusal: the wall is built at the table's maximum, the command runs, and
// the SANDBOX OK line carries the kernel's number on abi= and the wall's on used=. At or
// below the table the line carries NO used= field, or "clamped" would mean nothing. Run
// 35045469738: the hosted ubuntu-latest leg moved to landlock abi 7 and every walled run
// there was `SANDBOX REFUSED reason=landlock_abi_unknown` at exit 125; at abi 4 (the
// fleet's linux bench) this takes the other branch.
func TestLandlockWallClampsAnABIAboveTheTable(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	code, _, errOut := j.wall(t, "echo ran > "+filepath.Join(j.write, "output"))
	require.Equal(t, 0, code, "a clamped abi must still run: %s", errOut)
	used, clamped := sandbox.ClampedABI()
	if !clamped {
		assert.NotContains(t, errOut, " used=", "this kernel's abi is inside the table and the line still claims a clamp")
		return
	}
	// abi= is the kernel's, read off the tool's own `check` line rather than computed
	// here (the test asserts the two lines agree, so it must not be the one deciding);
	// used= is the wall's; both are on the one line a log keeps.
	code, out, checkErr := j.runTool(t, j.env(), "check")
	require.Equal(t, 0, code, checkErr)
	assert.Contains(t, errOut, "abi="+fieldOf(out, "abi=")+" used="+strconv.Itoa(used))
	// The clamp is said in a note before the command starts.
	for _, want := range []string{"SANDBOX NOTE", "clamped"} {
		assert.Contains(t, errOut, want)
	}
}

// A wall that denies the work is broken: what --read names must be readable.
func TestLandlockWallAllowsReadPaths(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	const want = "the-read-set-is-readable"
	testkit.WriteFile(t, filepath.Join(j.read, "input"), want+"\n")
	code, out, errOut := j.wall(t, "cat "+filepath.Join(j.read, "input"))
	require.Equal(t, 0, code, "reading a --read path inside the wall: %s", errOut)
	require.Contains(t, out, want)
	// The write set is writable in the same run, or the job cannot do its work.
	code, _, errOut = j.wall(t, "echo ok > "+filepath.Join(j.write, "output"))
	require.Equal(t, 0, code, "writing inside the --write set: %s", errOut)
	// And the roots table's WRITABLE device files, a regression: the first cut handed
	// /dev/null a directory's access mask, the kernel rejected that rule with EINVAL, the
	// tool skipped the error as "absent device", and every `cmd > /dev/null` in every job
	// was denied while four wall tests, none of which redirected anywhere, passed.
	code, _, errOut = j.wall(t, "echo hi > /dev/null")
	require.Equal(t, 0, code, "redirecting to /dev/null inside the wall: %s", errOut)
}

// The secret is in NEITHER list, and #69 is exactly this file: the bench's SSH key and gh
// token, which the worker holds today and must not be able to read. The name says "hides"
// and the backend DENIES: with no mount namespace the secret's NAME can still appear in a
// listing of a readable parent (darwin's too; the spec's limits list says so). What both
// backends promise, and what is asserted, is that the CONTENTS do not come out.
func TestLandlockWallHidesSecret(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	secret := testkit.ReadFile(t, j.secret) // the control: readable outside the wall
	code, out, errOut := j.wall(t, "cat "+j.secret)
	require.NotEqual(t, 0, code, "the wrapped command READ THE SECRET and exited 0: %q", out)
	require.NotContains(t, out+errOut, strings.TrimSpace(secret), "the secret's contents came out of the wall")
}

// Rule 7: --net-deny is an ENFORCED denial on this platform or it is a refusal, and at
// abi 4 and up it is enforced for TCP.
func TestLandlockWallBlocksNetworkWhenNotAllowed(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("skipped: no bash on this machine, and /dev/tcp is how a connect is made here without a second tool")
	}
	// A listener this test owns, so that a refused connect is the WALL and not an empty
	// port. The listener is outside the wall; the wrapped command is what is walled.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
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
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	dial := "exec 3<>/dev/tcp/127.0.0.1/" + port

	connect := func(extra ...string) (int, string) {
		args := append([]string{"--read", j.read, "--write", j.write}, extra...)
		args = append(args, "--", bash, "-c", dial)
		code, _, errOut := j.runTool(t, j.env(), args...)
		return code, errOut
	}
	// THE CONTROL: without --net-deny the same connect succeeds, so the denial below is
	// the wall and not a broken listener.
	if code, errOut := connect(); code != 0 {
		t.Skipf("skipped: the control connect failed (exit %d), so a denial would prove nothing: %s", code, errOut)
	}
	code, errOut := connect("--net-deny")
	require.NotEqual(t, 0, code, "the wrapped command CONNECTED under --net-deny: %s", errOut)
	assert.Contains(t, errOut, "net=denied")
}

// check is a question, not an attempt, and on a linux machine with Landlock it names the
// backend and the discovered abi.
func TestCheckReportsLandlock(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	code, out, _ := j.runTool(t, j.env(), "check")
	require.Equal(t, 0, code, out)
	require.True(t, strings.HasPrefix(out, "CHECK OK backend="), out)
	// Landlock is in this kernel or it is not, and check must say which WITHOUT ever
	// naming a backend it cannot apply (rule 1).
	if !strings.Contains(out, "backend=landlock") {
		require.Contains(t, out, "backend=none", "check named neither landlock nor none on linux")
		assert.Contains(t, out, "abi=-", "check reported no backend but still an abi")
		t.Skipf("skipped the abi assertions: this kernel has no landlock: %q", out)
	}
	abi := fieldOf(out, "abi=")
	n, err := strconv.Atoi(abi)
	require.NoError(t, err, "check named landlock but no usable abi: %q", out)
	require.GreaterOrEqual(t, n, 1, out)
	// Rule 7's two answers, and they must agree with the abi that was just printed.
	wantNet := "net=unenforceable"
	if n >= 4 {
		wantNet = "net=enforceable"
	}
	assert.Contains(t, out, wantNet, "abi %d and %q disagree: %q", n, wantNet, out)
}

// fieldOf pulls one key=value field out of a one-line status line.
func fieldOf(line, key string) string {
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// THE NO-EXEC READ SET ON A REAL KERNEL. landlock's read subset is
// EXECUTE|READ_FILE|READ_DIR, so `--read` grants execution of everything under a root and
// a module cache granted that way is a place a card can run code from. `--read-noexec`
// drops fsExecute, and this is the measurement: the same script is readable and is NOT
// executable, with a control proving it runs under `--read` on this same machine.
func TestLandlockReadNoExecReadsAndRefusesToExecute(t *testing.T) {
	t.Parallel()
	j := landlockJob(t)
	cache := filepath.Join(j.base, "cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	script := filepath.Join(cache, "x.sh")
	require.NoError(t, testbin.WriteExecutable(script, []byte("#!/bin/sh\necho ran\n"), 0o755))
	code, out, errOut := j.wall(t, "cat "+script, "--read-noexec", cache)
	require.Equal(t, 0, code, "the --read-noexec tree is not readable inside the wall: %s", errOut)
	require.Contains(t, out, "echo ran", "the --read-noexec tree is not readable inside the wall")
	assert.Contains(t, errOut, "read-noexec=1", "the SANDBOX OK line does not count the no-exec reads")
	code, _, _ = j.wall(t, script, "--read-noexec", cache)
	require.NotEqual(t, 0, code, "the script under --read-noexec EXECUTED inside the wall; readable is not executable")
	code, _, errOut = j.wall(t, script, "--read", cache)
	require.Equal(t, 0, code, "the control: the same script under --read did not run: %s", errOut)
}
