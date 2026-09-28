package swarm

import "testing"

// TestIssue2019Repro pins that a bench's share is derived from measured per-card
// memory with headroom (nova-tools#2019), not guessed by hand: the share is the
// total usable memory divided by the measured per-card figure scaled up, so a
// box priced at ~600 MB/card reports a share near its real card count instead of
// whatever number the operator wrote down.
func TestIssue2019Repro(t *testing.T) {
	t.Parallel()

	const (
		mib = int64(1024 * 1024)
		gib = 1024 * mib
	)
	for _, c := range []struct {
		name              string
		totalMem, perCard int64
		want              int
	}{
		{"measured 600 MB/card on 128 GiB", 128 * gib, 600 * mib, 109},
		{"headroom halves a naive 10 GiB / 1 GiB", 10 * gib, 1 * gib, 5},
		{"no memory measured", 0, 600 * mib, 0},
		{"no per-card figure measured", 128 * gib, 0, 0},
		{"negative inputs", -8 * gib, -600 * mib, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ShareFromMemory(c.totalMem, c.perCard)
			if got != c.want {
				t.Errorf("ShareFromMemory(%d,%d) = %d, want %d: the share must be derived from memory, never a guess", c.totalMem, c.perCard, got, c.want)
			}
		})
	}
}
