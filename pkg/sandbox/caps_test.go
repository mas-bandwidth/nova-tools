package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSizedCapsFollowTheMachine: the fleet's shapes, each to the caps it is sized to.
func TestSizedCapsFollowTheMachine(t *testing.T) {
	t.Parallel()
	const gib = int64(1) << 30
	for _, c := range []struct {
		name  string
		cpus  int
		ram   int64
		procs int
		mem   int64
	}{
		{"16 threads, 30 GiB (amd mini-PC)", 16, 30 * gib, 512, 8 * gib},
		{"16 threads, 61 GiB (hetzner AX42)", 16, 61 * gib, 512, 8 * gib},
		{"32 threads, 128 GiB (hetzner AX102)", 32, 128 * gib, 1024, 16 * gib},
		{"64 threads, 125 GiB (threadripper)", 64, 125 * gib, 1024, 16 * gib},
		{"64 threads, 48 GiB: large CPU, not large memory", 64, 48 * gib, 1024, 8 * gib},
		{"8 threads, 8 GiB: never above half the machine", 8, 8 * gib, 512, 4 * gib},
		{"64 threads, 12 GiB: half the machine wins", 64, 12 * gib, 1024, 6 * gib},
		{"memory unread: the default memory cap", 16, 0, 512, DefaultMaxMem},
		{"memory unread on a large machine: no large memory cap", 64, 0, 1024, DefaultMaxMem},
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
