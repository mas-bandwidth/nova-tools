//go:build linux

package sandbox

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A table read cannot own another exec.Cmd's wait, even when that child is already
// a zombie. WNOWAIT makes the formerly intermittent stolen wait deterministic.
func TestProcRowsDoesNotReapOtherCommands(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() }) // ignored: the assertion below owns the first wait
	var info unix.Siginfo
	require.NoError(t, unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil))
	_, err := procRows()
	require.NoError(t, err)
	require.NoError(t, cmd.Wait(), "the table reader stole another command's exit status")
}

// The collector's ownership boundary is a fresh process, never the parallel test
// runner. The descendant is adopted before any table read, so observation of its
// original parent cannot be required. The primary command's wait remains its own.
func TestRunProcRowsCollectsUnseenAdoptedZombie(t *testing.T) {
	t.Parallel()
	const marker = "NOVA_TEST_SANDBOX_REAPER_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
		cmd.Env = append(os.Environ(), marker+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return
	}
	require.NoError(t, unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0))
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() }) // ignored: the fixture is finished
	parent := exec.Command("/bin/sh", "-c", "sleep 60 & echo $!")
	parent.Stdout = w
	require.NoError(t, parent.Start())
	require.NoError(t, w.Close())
	scan := bufio.NewScanner(r)
	require.True(t, scan.Scan(), "the fixture must name its own descendant: %v", scan.Err())
	pid, err := strconv.Atoi(scan.Text())
	require.NoError(t, err)
	t.Cleanup(func() {
		var ws unix.WaitStatus
		waited, err := unix.Wait4(pid, &ws, unix.WNOHANG, nil)
		if err == nil && waited == 0 {
			_ = unix.Kill(pid, unix.SIGKILL)    // ignored: the test's child may have exited meanwhile
			_, _ = unix.Wait4(pid, &ws, 0, nil) // ignored: cleanup has ended its own child
		}
	})
	require.NoError(t, parent.Wait())
	require.NoError(t, unix.Kill(pid, unix.SIGKILL))
	var info unix.Siginfo
	require.NoError(t, unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil))

	command := exec.Command("/bin/sh", "-c", "exit 0")
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Wait() }) // ignored: the assertion owns the first wait
	require.NoError(t, unix.Waitid(unix.P_PID, command.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil))
	rows, err := runProcRows(os.Getpid(), command.Process.Pid)
	require.NoError(t, err)
	var adopted bool
	for _, row := range rows {
		if row.pid == pid {
			adopted = row.zombie && row.ppid == os.Getpid()
		}
	}
	require.True(t, adopted, "the fixture must be an unseen adopted zombie")
	var ws unix.WaitStatus
	_, err = unix.Wait4(pid, &ws, unix.WNOHANG, nil)
	require.ErrorIs(t, err, unix.ECHILD, "Run's collector must reap its adopted descendant")
	require.NoError(t, command.Wait(), "the primary command alone owns its wait")
}
