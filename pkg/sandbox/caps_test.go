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
		cpus  int
		ram   int64
		procs int
		mem   int64
	}{
		// The fleet as read on 2026-10-17: nproc and /proc/meminfo MemTotal (kB), or
		// darwin hw.logicalcpu and hw.memsize. amd-1 was unreachable; it is amd-2's shape.
		{"hetzner1: 16 threads, 62 GiB", 16, 64902488 * kib, 1024, 64902488 * kib / 4},
		{"hetzner2: 16 threads, 125 GiB", 16, 130981968 * kib, 1024, 130981968 * kib / 4},
		{"hulk: 64 threads, 126 GiB", 64, 131855232 * kib, 4096, 131855232 * kib / 4},
		{"captain: 64 threads, 55 GiB", 64, 57572912 * kib, 4096, 57572912 * kib / 4},
		{"amd-2: 16 threads, 31 GiB: the 8 GiB floor", 16, 32074456 * kib, 1024, 8 * gib},
		{"amd-3: 16 threads, 31 GiB: the 8 GiB floor", 16, 32068372 * kib, 1024, 8 * gib},
		{"vision: 64 threads, 121 GiB", 64, 127396116 * kib, 4096, 127396116 * kib / 4},
		{"mini-m5-1: 18 threads, 64 GiB", 18, 64 * gib, 1152, 16 * gib},
		{"studio: 32 threads, 512 GiB", 32, 512 * gib, 2048, 128 * gib},
		{"1 thread: the 512 process floor", 1, 64 * gib, 512, 16 * gib},
		{"8 threads, 8 GiB: never above half the machine", 8, 8 * gib, 512, 4 * gib},
		{"64 threads, 12 GiB: half the machine wins", 64, 12 * gib, 4096, 6 * gib},
		{"memory unread: the default memory cap", 16, 0, 1024, DefaultMaxMem},
		{"memory unread on a large machine: still the default", 64, 0, 4096, DefaultMaxMem},
	} {
		procs, mem := SizedCaps(c.cpus, c.ram)
		assert.Equal(t, c.procs, procs, "%s: processes", c.name)
		assert.Equal(t, c.mem, mem, "%s: memory", c.name)
	}
}

// TestMachineCapsAreSizedCapsHere: this machine's caps are SizedCaps of what it reports,
// and both are positive whatever it reports.
func TestMachineCapsAreSizedCapsHere(t *testing.T) {
	t.Parallel()
	procs, mem := MachineCaps()
	assert.GreaterOrEqual(t, procs, SizedProcs)
	assert.Positive(t, mem)
	if ram := machineRAM(); ram > 0 {
		assert.LessOrEqual(t, mem, ram/2, "never above half the machine's memory")
	}
}
