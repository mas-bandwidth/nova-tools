//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/member"
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
