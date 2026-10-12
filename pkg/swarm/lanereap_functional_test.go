//go:build functional && (linux || darwin)

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The functional tier of the lane reap (lanereap.go): these tests start real processes, signal
// them, and wait through real grace, which the unit tier never does (docs/TESTS.md, the
// owner's rule: a unit test uses no real time, no socket, no outside service and no real
// process). The judgments they exercise are proved in the unit tier on fakes
// (lanereap_unix_test.go, lanereap_orphan_unix_test.go).

// reapEscape is a shell command that runs `sleep 600` in a session of its own, the way a
// harness's tool shell leaves its parent's; "" where setsid (util-linux) is not installed.
func reapEscape() string {
	if _, err := exec.LookPath("setsid"); err == nil {
		return "setsid sleep 600"
	}
	return ""
}

func reapPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pid > 0
	}, 10*time.Second, 10*time.Millisecond)
	return pid
}

// reapLive is whether a pid names a live process this test may ask about (a zombie is not).
func reapLive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndex(string(b), ")"); i >= 0 {
			if f := strings.Fields(string(b)[i+1:]); len(f) > 0 && (f[0] == "Z" || f[0] == "X") {
				return false
			}
		}
	}
	return true
}

// startStranger starts `sleep 600` in a session of its own with env, a process of no lane.
func startStranger(t *testing.T, env []string) *exec.Cmd {
	t.Helper()
	c := exec.Command("sleep", "600")
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, c.Start())
	t.Cleanup(func() {
		_ = c.Process.Kill() // ignored: the stranger this test started
		_ = c.Wait()         // ignored: as above
	})
	return c
}

// TestReapLaneEndsItsSessionAndItsMarkAndNothingElse: a lane leader in a session of its own,
// with a sleeper in its session and one that left it, both carrying the lane's mark. The reap
// ends the three of them, says found=3 survived=0, and leaves alone a stranger in no session of
// the lane with no mark, and one whose mark only begins with the lane's.
func TestReapLaneEndsItsSessionAndItsMarkAndNothingElse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mark := filepath.Join(dir, "slot")
	in, out, ready := filepath.Join(dir, "in.pid"), filepath.Join(dir, "out.pid"), filepath.Join(dir, "ready")
	want := 2
	script := "sleep 600 >/dev/null 2>&1 & echo $! > '" + in + "'\n"
	if esc := reapEscape(); esc != "" {
		script += esc + " >/dev/null 2>&1 & echo $! > '" + out + "'\n"
		want++
	}
	script += ": > '" + ready + "'\nwait\n"
	leader := exec.Command("/bin/sh", "-c", script)
	leader.Env = append(os.Environ(), LaneEnv+"="+mark)
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, leader.Start())
	var pids []int
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL) // ignored: the sleepers this test started
		}
		_ = leader.Process.Kill() // ignored: as above
		_ = leader.Wait()         // ignored: as above
	})
	plain := startStranger(t, os.Environ())
	prefixed := startStranger(t, append(os.Environ(), LaneEnv+"="+mark+"-other"))
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 10*time.Second, 10*time.Millisecond)
	pids = append(pids, reapPID(t, in))
	if want == 3 {
		pids = append(pids, reapPID(t, out))
	}
	found, survived := ReapLane(leader.Process.Pid, mark, LaneStartStamp(leader.Process.Pid), TerminateGrace)
	require.Equal(t, want, found, "the leader and each sleeper of the lane")
	require.Zero(t, survived)
	for _, pid := range pids {
		require.False(t, reapLive(pid), "the lane's sleeper %d outlived the reap", pid)
	}
	require.True(t, reapLive(plain.Process.Pid), "a process of no lane is never touched")
	require.True(t, reapLive(prefixed.Process.Pid), "a mark is matched whole, never by its prefix")
}

// TestReapLaneOfAnEmptyLaneFindsNothing: a lane whose leader has ended and left nothing says 0.
func TestReapLaneOfAnEmptyLaneFindsNothing(t *testing.T) {
	t.Parallel()
	leader := exec.Command("/bin/sh", "-c", "exit 0")
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, leader.Run())
	found, survived := ReapLane(leader.Process.Pid, filepath.Join(t.TempDir(), "slot"), "departed", TerminateGrace)
	require.Zero(t, found)
	require.Zero(t, survived)
}

// A numeric session ID reused for another process, or a listed PID whose birth
// changed before signalling, gives no authority over the replacement process.
func TestLaneReapRefusesAReusedProcessIdentity(t *testing.T) {
	t.Parallel()
	stranger := startStranger(t, os.Environ())
	pid := stranger.Process.Pid
	mark := filepath.Join(t.TempDir(), "absent-lane")
	found, survived := ReapLane(pid, mark, "old-birth", TerminateGrace)
	require.Zero(t, found)
	require.Zero(t, survived)
	require.True(t, reapLive(pid))
	require.ErrorIs(t, signalLaneProcess(laneProcess{pid: pid, started: "old-birth"}, syscall.SIGTERM), os.ErrProcessDone)
	require.True(t, reapLive(pid), "the PID number now names a different process")
}
