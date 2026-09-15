//go:build linux

// The linux wall, run for real on a linux machine: a real Landlock ruleset, a real
// wrapped command, a real denial from the kernel. These are the counterpart of the
// needDarwin tests above, and they exist because a green from a suite that ran nothing
// reads exactly like a green from one that ran.
//
// EVERY TEST HERE RUNS THE TOOL AS A SUBPROCESS, never in process through j.tool(). That
// is not a style choice: a Landlock domain CANNOT BE LIFTED, so a Run() called in the
// test binary would wall the test binary itself for the rest of the run and every later
// test in the package would fail against a wall it never asked for. The darwin tests can
// use j.tool() because sandbox-exec's policy lives around a child; linux's lives on the
// caller, so linux's tests need a caller of their own.
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
)

var (
	buildOnce sync.Once
	builtTool string
	buildErr  error
)

// walledTool builds the real nova-sandbox ONCE for this file and returns its path. It
// is not main_test.go's toolBinary, which rebuilds on every call into the calling test's
// t.TempDir(): these tests run the tool seven times over, and a binary that vanished
// when the test that built it ended could not be cached anyway.
func walledTool(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-sandbox-bin")
		if err != nil {
			buildErr = err
			return
		}
		builtTool = filepath.Join(dir, "nova-sandbox")
		out, err := exec.Command("go", "build", "-o", builtTool, ".").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("the tool under test could not be built: %v", buildErr)
	}
	return builtTool
}

// runTool runs nova-sandbox as a child and returns its status and streams.
func (j job) runTool(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(walledTool(t), args...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if err != nil {
		t.Fatalf("nova-sandbox did not run at all: %v", err)
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

// needLandlock skips BY NAME on a machine with no Landlock, so that a green here always
// means the kernel actually enforced something.
func needLandlock(t *testing.T) {
	t.Helper()
	j := newJob(t)
	code, out, _ := j.runTool(t, j.env(), "check")
	if code != 0 || !strings.Contains(out, "backend=landlock") {
		t.Skipf("skipped: this linux kernel reports no landlock, so there is no wall to test: %q", out)
	}
}

// The wall's whole reason: a worker that reads untrusted input all day cannot write
// outside the job it was given.
func TestLandlockWallRefusesWriteOutsideJob(t *testing.T) {
	needLandlock(t)
	j := newJob(t)
	outside := filepath.Join(j.outside, "escaped")

	// THE CONTROL FIRST, and it is why the denial below can be believed: a write that
	// was never possible is not a wall. Outside the tool, this user can create the file.
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatalf("the control write failed, so a denial inside the wall would prove nothing: %v", err)
	}
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := j.wall(t, "echo escaped > "+outside)
	if code == 0 {
		t.Fatalf("the wrapped command WROTE OUTSIDE THE JOB and exited 0: %s", errOut)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("the file outside the job exists: the wall did not hold")
	}
	// And the wall was announced, on the stream a log keeps.
	if !strings.Contains(errOut, "SANDBOX OK backend=landlock") {
		t.Errorf("the run did not announce the landlock wall: %q", errOut)
	}
}

// A wall that denies the work is broken: what --read names must be readable.
func TestLandlockWallAllowsReadPaths(t *testing.T) {
	needLandlock(t)
	j := newJob(t)
	const want = "the-read-set-is-readable"
	if err := os.WriteFile(filepath.Join(j.read, "input"), []byte(want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := j.wall(t, "cat "+filepath.Join(j.read, "input"))
	if code != 0 {
		t.Fatalf("reading a --read path inside the wall exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("the --read file did not come back: %q", out)
	}
	// The write set is writable in the same run, or the job cannot do its work.
	if code, _, errOut := j.wall(t, "echo ok > "+filepath.Join(j.write, "output")); code != 0 {
		t.Fatalf("writing inside the --write set exited %d: %s", code, errOut)
	}
	// And the roots table's two WRITABLE device files, which is a regression test and not
	// a nicety: the first cut of this body handed /dev/null the same access mask as a
	// directory, the kernel rejected that rule with EINVAL for carrying directory-only
	// rights on a non-directory, the tool skipped the error as "absent device", and every
	// `cmd > /dev/null` in every job was denied. Four wall tests passed over it, because
	// not one of them redirected anywhere.
	if code, _, errOut := j.wall(t, "echo hi > /dev/null"); code != 0 {
		t.Fatalf("redirecting to /dev/null inside the wall exited %d: %s; the roots table grants write on it", code, errOut)
	}
}

// The secret is in NEITHER list, and #69 is exactly this file: the bench's SSH key and
// the bench's gh token, which the worker holds today and must not be able to read.
//
// The NAME of this test says "hides" and the backend DENIES: Landlock has no mount
// namespace, so the secret's bytes are unreadable while its NAME can still appear in a
// listing of a readable parent. That is the darwin backend's behaviour too, and the
// spec's limits list says so. What is asserted here is what both backends promise: the
// CONTENTS do not come out.
func TestLandlockWallHidesSecret(t *testing.T) {
	needLandlock(t)
	j := newJob(t)
	secret, err := os.ReadFile(j.secret)
	if err != nil {
		t.Fatalf("the control read failed, so a denial inside the wall would prove nothing: %v", err)
	}
	code, out, errOut := j.wall(t, "cat "+j.secret)
	if code == 0 {
		t.Fatalf("the wrapped command READ THE SECRET and exited 0: %q", out)
	}
	if strings.Contains(out+errOut, strings.TrimSpace(string(secret))) {
		t.Fatal("the secret's contents came out of the wall")
	}
}

// Rule 7: --net-deny is an ENFORCED denial on this platform or it is a refusal, and at
// abi 4 and up it is enforced for TCP.
func TestLandlockWallBlocksNetworkWhenNotAllowed(t *testing.T) {
	needLandlock(t)
	j := newJob(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("skipped: no bash on this machine, and /dev/tcp is how a connect is made here without a second tool")
	}
	// A listener this test owns, so that a refused connect is the WALL and not an empty
	// port. The listener is outside the wall; the wrapped command is what is walled.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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
	if code == 0 {
		t.Fatalf("the wrapped command CONNECTED under --net-deny: %s", errOut)
	}
	if !strings.Contains(errOut, "net=denied") {
		t.Errorf("the run did not announce net=denied: %q", errOut)
	}
}

// check is a question, not an attempt, and on a linux machine with Landlock it names the
// backend and the discovered abi.
func TestCheckReportsLandlock(t *testing.T) {
	j := newJob(t)
	code, out, _ := j.runTool(t, j.env(), "check")
	if code != 0 {
		t.Fatalf("check exit %d: %q", code, out)
	}
	if !strings.HasPrefix(out, "CHECK OK backend=") {
		t.Fatalf("check did not print its line: %q", out)
	}
	// Landlock is in this kernel or it is not, and check must say which WITHOUT ever
	// naming a backend it cannot apply (rule 1).
	if !strings.Contains(out, "backend=landlock") {
		if !strings.Contains(out, "backend=none") {
			t.Fatalf("check named neither landlock nor none on linux: %q", out)
		}
		if !strings.Contains(out, "abi=-") {
			t.Errorf("check reported no backend but still an abi: %q", out)
		}
		t.Skipf("skipped the abi assertions: this kernel has no landlock: %q", out)
	}
	abi := fieldOf(out, "abi=")
	n, err := strconv.Atoi(abi)
	if err != nil || n < 1 {
		t.Fatalf("check named landlock but no usable abi: %q", out)
	}
	// Rule 7's two answers, and they must agree with the abi that was just printed.
	wantNet := "net=unenforceable"
	if n >= 4 {
		wantNet = "net=enforceable"
	}
	if !strings.Contains(out, wantNet) {
		t.Errorf("abi %d and %q disagree: %q", n, wantNet, out)
	}
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
