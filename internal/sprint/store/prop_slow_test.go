//go:build slow

package store

import (
	"os"
	"strconv"
	"testing"
)

// The longer run of random sprints: 2,000 seeds by default (it needs no
// store: the in-memory one). PROP_FROM and PROP_TO choose other seeds.
func TestRandomSprintsLong(t *testing.T) {
	t.Parallel()
	from, to := uint64(1), uint64(2001)
	if v, err := strconv.ParseUint(os.Getenv("PROP_FROM"), 10, 64); err == nil {
		from = v
	}
	if v, err := strconv.ParseUint(os.Getenv("PROP_TO"), 10, 64); err == nil {
		to = v
	}
	propSeeds(t, from, to, 300)
}

// The first seed of every failure class the long run has found, run again:
// 303 (a wait's expiry left the deadlines part owing), 709 and 1043 (an
// operation in flight at a tick's start finished during it, and the fleet's
// cells were left to the next tick).
func TestRandomSprintsFoundSeeds(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{303, 709, 1043} {
		propSeeds(t, seed, seed+1, 300)
	}
}
