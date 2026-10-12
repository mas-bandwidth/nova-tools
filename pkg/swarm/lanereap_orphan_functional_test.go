//go:build functional && (linux || darwin)

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReapOrphanLanesEndsAnEarlierMembersLaneAndNothingElse: a lane an earlier member m1 left
// under slots (its leader in a session of its own, a sleeper in that session with its
// environment emptied, one that left the session still marked) is ended whole; processes of no
// lane, a lane of a member of another name under the same slots, a lane of m1 under another
// slots directory, and a lane m1 runs now are untouched. The functional tier: real processes,
// real grace (lanereap_functional_test.go); the selection itself is pinned in the unit tier
// (TestOrphanProcsKeepsToTheMembersLanes).
func TestReapOrphanLanesEndsAnEarlierMembersLaneAndNothingElse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	slots := filepath.Join(dir, "slots")
	mark := filepath.Join(slots, "a-1.w1.g1.e1")
	in, emptied, out, ready := filepath.Join(dir, "in.pid"), filepath.Join(dir, "emptied.pid"), filepath.Join(dir, "out.pid"), filepath.Join(dir, "ready")
	want := 3
	script := "sleep 600 >/dev/null 2>&1 & echo $! > '" + in + "'\n" +
		"env -i sleep 600 >/dev/null 2>&1 & echo $! > '" + emptied + "'\n"
	if esc := reapEscape(); esc != "" {
		script += esc + " >/dev/null 2>&1 & echo $! > '" + out + "'\n"
		want++
	}
	script += ": > '" + ready + "'\nwait\n"
	clean := func(extra ...string) []string {
		var env []string
		for _, kv := range os.Environ() {
			if n, _, _ := strings.Cut(kv, "="); n != LaneEnv && n != LaneOwnerEnv {
				env = append(env, kv)
			}
		}
		return append(env, extra...)
	}
	leader := exec.Command("/bin/sh", "-c", script)
	leader.Env = clean(LaneEnv+"="+mark, LaneOwnerEnv+"=m1")
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
	plain := startStranger(t, clean())
	otherName := startStranger(t, clean(LaneEnv+"="+filepath.Join(slots, "a-2.w1.g1.e1"), LaneOwnerEnv+"=m2"))
	otherSlots := startStranger(t, clean(LaneEnv+"="+filepath.Join(dir, "other", "a-3.w1.g1.e1"), LaneOwnerEnv+"=m1"))
	runningNow := filepath.Join(slots, "a-4.w1.g1.e1")
	owned := startStranger(t, clean(LaneEnv+"="+runningNow, LaneOwnerEnv+"=m1"))
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 10*time.Second, 10*time.Millisecond)
	pids = append(pids, reapPID(t, in), reapPID(t, emptied))
	if want == 4 {
		pids = append(pids, reapPID(t, out))
	}
	lanes, found, survived := ReapOrphanLanes(slots, "m1", func(m string) bool { return m == runningNow }, TerminateGrace)
	require.Equal(t, 1, lanes, "one lane of m1 not running now")
	require.Equal(t, want, found, "the leader and each sleeper of the lane, the emptied one by its session")
	require.Zero(t, survived)
	require.False(t, reapLive(leader.Process.Pid), "the orphaned lane's leader outlived the reap")
	for _, pid := range pids {
		require.False(t, reapLive(pid), "the orphaned lane's process %d outlived the reap", pid)
	}
	require.True(t, reapLive(plain.Process.Pid), "a process of no lane is never touched")
	require.True(t, reapLive(otherName.Process.Pid), "a lane of a member of another name is never touched")
	require.True(t, reapLive(otherSlots.Process.Pid), "a lane under another slots directory is never touched")
	require.True(t, reapLive(owned.Process.Pid), "a lane the member runs now is never touched")
}
