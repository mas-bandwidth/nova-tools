package deal

import (
	"testing"
	"time"
)

// TestBenchFreeShrinksByCILegs (nova-tools#4293): a bench's free slots are
// its declared slots minus the CI legs its beat counts minus its leases;
// legs past the slots leave none, and a bench with legs and no free slot is
// not eligible for a deal.
func TestBenchFreeShrinksByCILegs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slots, leased, ci, want int
	}{
		{8, 0, 0, 8},
		{8, 1, 0, 7},
		{8, 1, 4, 3},
		{8, 0, 8, 0},
		{8, 2, 9, 0},
	}
	for _, c := range cases {
		b := Bench{Slots: c.slots, Leased: c.leased, CI: c.ci}
		if got := b.Free(); got != c.want {
			t.Errorf("Bench{Slots %d, Leased %d, CI %d}.Free() = %d, want %d", c.slots, c.leased, c.ci, got, c.want)
		}
	}
	full := Bench{Name: "hulk", Up: true, Slots: 4, CI: 4}
	if eligible(full, time.Now(), 0) {
		t.Fatal("a bench whose CI legs fill its slots was eligible for a deal")
	}
}
