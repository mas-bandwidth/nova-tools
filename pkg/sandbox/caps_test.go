package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSizedCapsFollowTheMachine: the fleet's shapes, each to the caps it is sized to.
func TestSizedCapsFollowTheMachine(t *testing.T) {
	t.Parallel()
	const kib, gib = int64(1) << 10, int64(1) << 30
	for _, c := range []struct {
		name  string
		m     Machine
		procs int
		mem   int64
	}{
		// The fleet as read on 2026-10-17: nproc, /proc/meminfo MemTotal (kB) and the
		// member's user.slice memory.max and user-1000(1001).slice pids.max, or darwin
		// hw.logicalcpu and hw.memsize. amd-1 was unreachable; it is amd-2's shape.
		{"hetzner1: 16 threads, 62 GiB, slice 52.6 GiB: the 8 GiB floor", Machine{16, 64902488 * kib, 56489934848, 166883}, 1024, 8 * gib},
		{"hetzner2: 16 threads, 125 GiB, slice 106 GiB", Machine{16, 130981968 * kib, 114006425600, 337242}, 1024, 130981968 * kib / 8},
		{"hulk: 64 threads, 126 GiB, slice 107 GiB", Machine{64, 131855232 * kib, 114765594624, 339458}, 4096, 131855232 * kib / 8},
		{"captain: 64 threads, 55 GiB, slice 46.7 GiB", Machine{64, 57572912 * kib, 50110398464, 148401}, 4096, 8 * gib},
		{"amd-2: 16 threads, 31 GiB, slice 26 GiB: the 8 GiB floor", Machine{16, 32074456 * kib, 27916238848, 72429}, 1024, 8 * gib},
		{"amd-3: 16 threads, 31 GiB, slice 26 GiB: the 8 GiB floor", Machine{16, 32068372 * kib, 27910995968, 82498}, 1024, 8 * gib},
		{"vision: 64 threads, 121 GiB, no slice limit", Machine{64, 127396116 * kib, 0, 328261}, 4096, 127396116 * kib / 8},
		{"mini-m5-1: 18 threads, 64 GiB, no cgroup", Machine{18, 64 * gib, 0, 0}, 1152, 8 * gib},
		{"studio: 32 threads, 512 GiB, no cgroup", Machine{32, 512 * gib, 0, 0}, 2048, 64 * gib},
		{"1 thread: the 512 process floor", Machine{1, 64 * gib, 0, 0}, 512, 8 * gib},
		{"a slice TasksMax under 4 x 4096 caps the processes", Machine{64, 128 * gib, 0, 8000}, 2000, 16 * gib},
		{"a slice MemoryMax under the share wins above the floor", Machine{64, 256 * gib, 80 * gib, 0}, 4096, 20 * gib},
		{"8 threads, 8 GiB: never above half the machine", Machine{8, 8 * gib, 0, 0}, 512, 4 * gib},
		{"64 threads, 12 GiB: half the machine wins", Machine{64, 12 * gib, 0, 0}, 4096, 6 * gib},
		{"memory unread: the default memory cap", Machine{16, 0, 0, 0}, 1024, DefaultMaxMem},
		{"memory unread on a large machine: still the default", Machine{64, 0, 0, 0}, 4096, DefaultMaxMem},
	} {
		procs, mem := SizedCaps(c.m)
		assert.Equal(t, c.procs, procs, "%s: processes", c.name)
		assert.Equal(t, c.mem, mem, "%s: memory", c.name)
	}
}

// TestMachineCapsAreSizedCapsHere: this machine's caps are SizedCaps of what it reports,
// and both are positive whatever it reports.
func TestMachineCapsAreSizedCapsHere(t *testing.T) {
	t.Parallel()
	procs, mem := MachineCaps()
	assert.Positive(t, procs)
	assert.Positive(t, mem)
	if ram := machineRAM(); ram > 0 {
		assert.LessOrEqual(t, mem, ram/2, "never above half the machine's memory")
	}
}
