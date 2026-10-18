package sandbox

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMemTotalReadsTheKernelsKilobytes: MemTotal is kB, the answer bytes; no line is 0.
func TestMemTotalReadsTheKernelsKilobytes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(32617624)<<10, memTotal("MemTotal:       32617624 kB\nMemFree:  100 kB\n"))
	assert.Equal(t, int64(0), memTotal("MemFree:  100 kB\n"))
	assert.Equal(t, int64(0), memTotal("MemTotal: lots kB\n"))
}

// TestSliceLimitsAreTheTightestAncestor: the walk from the process's cgroup to the root
// keeps the least memory.max and pids.max, and "max" or a missing file is no limit.
func TestSliceLimitsAreTheTightestAncestor(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/sys/fs/cgroup/user.slice/user-1000.slice/session-7.scope/memory.max": "max\n",
		"/sys/fs/cgroup/user.slice/user-1000.slice/session-7.scope/pids.max":   "max\n",
		"/sys/fs/cgroup/user.slice/user-1000.slice/memory.max":                 "60000000000\n",
		"/sys/fs/cgroup/user.slice/user-1000.slice/pids.max":                   "166883\n",
		"/sys/fs/cgroup/user.slice/memory.max":                                 "56489934848\n",
		"/sys/fs/cgroup/user.slice/pids.max":                                   "max\n",
	}
	read := func(p string) (string, error) {
		if s, ok := files[p]; ok {
			return s, nil
		}
		return "", errors.New("no such file")
	}
	mem, tasks := sliceLimits("0::/user.slice/user-1000.slice/session-7.scope\n", read)
	assert.Equal(t, int64(56489934848), mem)
	assert.Equal(t, 166883, tasks)
	mem, tasks = sliceLimits("0::/\n", read)
	assert.Zero(t, mem)
	assert.Zero(t, tasks)
	mem, tasks = sliceLimits("1:name=systemd:/x\n", read)
	assert.Zero(t, mem, "no unified hierarchy: no limits")
	assert.Zero(t, tasks)
}

// TestNprocLimitLeavesRoomForEverySlot: the thread limit holds every slot of the bench at
// its cap, whatever the machine ran when the child started, and stays under the slice.
func TestNprocLimitLeavesRoomForEverySlot(t *testing.T) {
	t.Parallel()
	// hetzner2 quiet at the start (900 tasks), a 1024-process tree, slice TasksMax 337242.
	lim := nprocLimit(900, 1024, 337242)
	assert.Equal(t, uint64(900+SizedSliceTrees*NprocThreadsPerProc*1024), lim)
	// The old limit, now plus twice the cap, is what a growing bench ran out of.
	assert.Greater(t, lim, uint64(900+2*1024))
	// No slice visible: the same headroom.
	assert.Equal(t, uint64(500+SizedSliceTrees*NprocThreadsPerProc*512), nprocLimit(500, 512, 0))
	// A tight slice caps it, but never under now plus twice the cap.
	assert.Equal(t, uint64(10000), nprocLimit(1000, 1024, 10000))
	assert.Equal(t, uint64(9000+2*1024), nprocLimit(9000, 1024, 10000))
	// An absurd cap is held to nprocMaxProcs: no overflow, no wrap.
	assert.Equal(t, uint64(100+SizedSliceTrees*NprocThreadsPerProc*nprocMaxProcs), nprocLimit(100, 1<<62, 0))
}
