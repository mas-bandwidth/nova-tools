//go:build unix && slow

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/require"
)

// A HARNESS THAT LEAVES A PIPE HOLDER (docs/SPEC-CARD-CONTRACT.md, the finish). The fake is
// a shell script: it forks a sleeper that inherits its stdout and stderr, records the
// sleeper's pid, says one line and exits 0, the shape of a harness that leaves a language
// server running.

// pipeHolderScript writes an executable script that forks `sleep 120` holding its stdout and
// stderr, writes the sleeper's pid to pidFile, prints say, and exits 0. Each test reaps the
// sleeper it started at its end, whatever the run did.
func pipeHolderScript(t *testing.T, say, pidFile string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe-holder")
	body := "#!/bin/sh\nsleep 120 &\necho $! > " + pidFile + "\necho '" + say + "'\nexit 0\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	t.Cleanup(func() {
		if pid := sleeperPID(pidFile); pid > 0 {
			// ignored: the sleeper this test started may already be gone, which is the point
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return path
}

// sleeperPID is the pid the script recorded, 0 when none.
func sleeperPID(pidFile string) int {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// pidAlive is whether a signal 0 reaches pid.
func pidAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

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

// TestNativeAnOrdinaryHarnessLeavesNoSurvivors pins the ordinary case as it was: a harness
// that leaves nothing in its group ends with no survivors= field and no group signal.
func TestNativeAnOrdinaryHarnessLeavesNoSurvivors(t *testing.T) {
	t.Parallel()
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: nativeHarness(t), model: "fake/fake-model", label: "ordinary",
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	require.Equal(t, 0, res.rc)
	require.Empty(t, res.survivors, "an ordinary harness leaves nothing to end")
}

// TestMemberSeesANativeThatExitedWhateverItsPipes pins the member's wait on native: native's
// output goes to a file in the slot, never a pipe back to the member, so a native that
// printed its line and exited is Done while a process it left still holds that output open
// (the bound is far under the sleeper's 120 s; in practice one member tick), and the finish
// is read from the log.
func TestMemberSeesANativeThatExitedWhateverItsPipes(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "sleeper.pid")
	self := pipeHolderScript(t, "NATIVE OK label=c1 rc=0 wall=1.00s harness=ok budget=-/1", pidFile)
	root := t.TempDir()
	r := &nativeRunner{self: self, harness: "h", model: "fake/fake-model", root: root, slots: filepath.Join(root, "slots"),
		resultsRoot: filepath.Join(root, "results"), deadline: time.Minute, tokens: "unmetered", stderr: &bytes.Buffer{}}
	require.NoError(t, os.MkdirAll(r.slots, 0o755))
	start := time.Now()
	c, err := r.Start(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1})
	require.NoError(t, err)
	require.Eventually(t, c.Done, 30*time.Second, 20*time.Millisecond, "the member waits on native's process, never on its pipes")
	require.Less(t, time.Since(start), 30*time.Second, "the sleeper holds the output for 120 s")
	require.True(t, pidAlive(sleeperPID(pidFile)), "the holder is still there: the wait did not depend on it")
	require.True(t, c.Result().Ran, "the NATIVE line native printed before it exited is the finish")
}
