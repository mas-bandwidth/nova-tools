//go:build darwin

// The unit-tier twin of reap_darwin_functional_test.go: the same rule, asked of the
// process table instead of the reap, so it needs no wait on the wall clock and runs in
// the default build that CI runs (the security read of 2026-10-06 found the functional
// test red at head and behind a tag CI never sets).
package main

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheBareFormKeepsTheCallersGroup: rule 12. The tool is started in a group of the
// test's making, the way a supervisor starts a job, with stdin a pipe (the shape the caps
// card once gave a group of its own). The wrapped command forks a background sleep and
// names both pids on stdout; both must be in the caller's group, or the caller's group
// kill at its deadline misses them.
func TestTheBareFormKeepsTheCallersGroup(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	j := newJob(t)
	bin := toolBinary(t)
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--",
		"/bin/sh", "-c", "sleep 300 & echo $$ $!; read x")
	cmd.Env = j.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	pgid := cmd.Process.Pid
	// The caller's reap, which is also the cleanup: everything this test started is in
	// the group it made, or the assertion below has already failed.
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "the wrapped command named no pids: %v", err)
	f := strings.Fields(line)
	require.Len(t, f, 2, "the wrapped command's line: %q", line)
	for _, s := range f {
		pid, err := strconv.Atoi(s)
		require.NoError(t, err, "pid %q", s)
		got, err := syscall.Getpgid(pid)
		require.NoError(t, err, "pid %d is gone before it was asked about: %v", pid, err)
		require.Equal(t, pgid, got, "pid %d of the walled tree is in group %d, not the caller's %d: the caller's group kill misses it", pid, got, pgid)
	}
}
