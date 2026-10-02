//go:build darwin

// The reaper case of test 12, in its own file because it is POSIX: Setpgid and
// syscall.Kill do not exist on windows, and a test file with no build tag broke the
// windows job at COMPILE time -- a green suite that never ran is the failure "test on
// multiple platforms" exists to catch, and so is a red one about the wrong thing.
package main

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/require"
)

// The wall's child stays in the CALLER's process group: a swarm supervisor reaps each
// job's group at its deadline (SPEC-SWARM rule 11), and a tool that gave its child a group
// of its own left a forked background child outside it, the reaper reporting survivors=0
// while a process still ran. The tool is started in a group of the TEST's making, the
// wrapped command forks a background sleep and exits, and the test kills the group it
// made. Red with Setpgid on the tool's child; green without it.
func TestAForkedChildIsReapedWithTheCallersGroup(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	pidFile := filepath.Join(j.write, "bg.pid")
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "sleep 300 & echo $! > '"+pidFile+"'; exit 0")
	cmd.Env = j.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the caller's own group, the way a supervisor starts a job
	require.NoError(t, cmd.Run(), "the wrapped command did not run")
	pgid := cmd.Process.Pid
	bg, err := strconv.Atoi(strings.TrimSpace(testkit.ReadFile(t, pidFile)))
	require.NoError(t, err)
	require.Positive(t, bg)
	t.Cleanup(func() { _ = syscall.Kill(bg, syscall.SIGKILL) })
	require.NoError(t, syscall.Kill(bg, 0), "control: the background child was already gone before the reap")
	// The reap: the caller kills the group IT made, which is the only group it knows.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 50; i++ {
		if err := syscall.Kill(bg, 0); err != nil {
			return // reaped
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d survived the caller's group kill (pgid %d): the tool put its child in a group of its own, outside the one the caller reaps", bg, pgid)
}
