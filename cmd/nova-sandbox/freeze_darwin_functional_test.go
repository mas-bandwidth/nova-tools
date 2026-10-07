//go:build functional && darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveInGroupDarwin is the live processes of group pgid, from one ps(1).
func liveInGroupDarwin(t *testing.T, pgid int) []string {
	t.Helper()
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=,pgid=,stat=,command=").Output()
	require.NoError(t, err)
	var live []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != strconv.Itoa(pgid) || strings.HasPrefix(f[2], "Z") {
			continue
		}
		live = append(live, line)
	}
	return live
}

// TestARunawayIsFrozenBeforeItIsKilled: a loop that forks without pause is past the
// cap; a kill from one listing misses the children forked after it, which outlive
// their parent as orphans (the second read of 2026-10-06: 35-39 alive after "killed").
// The tree is stopped until no new or running member appears, then killed, and the
// tool says "killed" only when nothing of it is left: the test reads its own group the
// moment the tool has exited, with no wait.
func TestARunawayIsFrozenBeforeItIsKilled(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "i=0; while [ $i -lt 1500 ]; do sleep 60 & i=$((i+1)); done; wait")
	cmd.Env = j.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// stderr is a FILE: a pipe would be held open by any survivor, and the wait for it
	// would outlast the survivors' own sleep, hiding them.
	errPath := filepath.Join(t.TempDir(), "stderr")
	errFile, err := os.Create(errPath)
	require.NoError(t, err)
	defer errFile.Close()
	cmd.Stderr = errFile
	require.NoError(t, cmd.Start())
	pgid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	_ = cmd.Wait()
	live := liveInGroupDarwin(t, pgid)
	raw, _ := os.ReadFile(errPath)
	errb := bytes.NewBuffer(raw)
	assert.Equal(t, 137, cmd.ProcessState.ExitCode(), "stderr: %s", errb.String())
	assert.Contains(t, errb.String(), "the process tree was killed", "stderr: %s", errb.String())
	assert.Empty(t, live, "%d processes of the killed tree are still running", len(live))
}
