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
