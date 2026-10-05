//go:build slow && unix

// Unix process-group tests behind the `slow` build tag, with slow_test.go's tier: the unit
// tier's package budget is 2 s (internal/ci/slow-tests_allowlist.txt, its header), and each
// test here waits a terminate/kill grace or a signal out. They run nightly, whole.

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// SLOW: 5.0 s on vision (go test -count=1 -json), the harness's pipe holder ended after subproc.WaitDelay and the group's grace.
// TestNativeEndsTheGroupAHarnessLeftHoldingItsPipes pins native's end of a harness that
// exited 0 and left a grandchild holding its pipes: native ends within subproc.WaitDelay
// plus the group's grace (a terminate, swarm.TerminateGrace, a kill), the NATIVE line says
// rc=0 and names the group it ended as survivors=<pgid>:reaped, and no process of the
// group is alive after it. On the tip before this change the sleeper outlived native and
// the line said rc=-1 (exec.ErrWaitDelay read as a kill).
func TestNativeEndsTheGroupAHarnessLeftHoldingItsPipes(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "sleeper.pid")
	bin := pipeHolderScript(t, "the harness said this", pidFile)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
	var stdout, stderr bytes.Buffer
	start := time.Now()
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", "pipe-holder", "--card", cardPath,
		"--slot", slot, "--root", root, "--deadline", "60s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	took := time.Since(start)
	require.Equal(t, 0, rc, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	line := stdout.String()
	require.Regexp(t, regexp.MustCompile(`\bNATIVE \S+ .*\brc=0\b`), line, "a harness that exited 0 is rc=0 whatever its pipes did")
	require.Regexp(t, regexp.MustCompile(`\bsurvivors=\d+:reaped\b`), line, "the line names the group native ended")
	require.Less(t, took, subproc.WaitDelay+2*swarm.TerminateGrace+10*time.Second, "native waits WaitDelay and the group's grace, never the sleeper")
	pid := sleeperPID(pidFile)
	require.Positive(t, pid, "the harness recorded its sleeper")
	require.False(t, pidAlive(pid), "the sleeper, a process of the harness's group, outlived native")
}

// SLOW: 1.2 s on vision (go test -count=1 -json), a TERM-ignoring sleeper reaped through the wrapper group.
// TestAToolTimeoutReapsOnlyTheWrapperGroup pins the shell shim's group-reaping wrapper:
// when a tool timeout signals the wrapper pid, the wrapper must carry its own process
// group -- the real shell and everything it started -- and not only the pid the timeout
// hit, while leaving a bystander in a different session untouched (SPEC-SWARM.md rule 9,
// "A job is one blocking process group, reported once"; nova-tools #1814, the shell
// wrapper).
func TestAToolTimeoutReapsOnlyTheWrapperGroup(t *testing.T) {
	t.Parallel()
	dir, _, err := writeNativeShellShims(t.TempDir())
	require.NoError(t, err)
	shim := filepath.Join(dir, "sh")
	marker := filepath.Join(t.TempDir(), "sleeper.pid")
	// The sleeper ignores TERM. Only a group signal stops it. The shell stays
	// the parent so today's exec of the real shell is the pid the timeout hits.
	script := "trap '' TERM INT; sleep 30 & echo $! > " + shQuote(marker) + "; wait"
	cmd := exec.Command(shim, "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		signalGroup(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	bystander := exec.Command("/bin/sh", "-c", "sleep 30")
	bystander.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, bystander.Start())
	t.Cleanup(func() {
		signalGroup(bystander.Process.Pid)
		_ = bystander.Wait()
	})

	var sleeper int
	require.Eventually(t, func() bool {
		sleeper = readPID(marker)
		return sleeper > 0 && alive(sleeper)
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	require.Eventually(t, func() bool { return !alive(sleeper) }, 5*time.Second, 20*time.Millisecond)
	require.True(t, alive(bystander.Process.Pid), "a group this launch did not start was signaled")
}
