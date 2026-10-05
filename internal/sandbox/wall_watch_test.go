package sandbox

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestWatchCountsOnceMoreWhenItStops: a tree whose leader exits between two counts still
// answers to the cap. A fork bomb refused by RLIMIT_NPROC ends its own shell and leaves
// its children running; the wall's wait returns and the watch is stopped before its
// first tick (ubuntu-latest hosted, run 37344601638: 49 s of survivors holding the
// test's pipes). So stopping the watch is one more count, and a tree past the cap there
// is killed and named like one seen on a tick.
func TestWatchCountsOnceMoreWhenItStops(t *testing.T) {
	t.Parallel()
	p := &Policy{MaxProcs: 4}
	kills := 0
	stop := p.Watch(make(chan time.Time), func() (Usage, error) { return Usage{Procs: 5}, nil }, func() { kills++ })
	assert.Equal(t, "runaway: 5 processes (cap 4)", stop(), "a tree past the cap when the watch stops is a runaway")
	assert.Equal(t, 1, kills, "and its group is killed")
	assert.Equal(t, "runaway: 5 processes (cap 4)", stop(), "a second stop answers the same line")
	assert.Equal(t, 1, kills, "and kills nothing more")
}

// TestWatchStopLeavesATreeAtTheCapAlone: the last count kills only past a cap, and a count
// that fails kills nothing, as on a tick.
func TestWatchStopLeavesATreeAtTheCapAlone(t *testing.T) {
	t.Parallel()
	p := &Policy{MaxProcs: 4}
	kills := 0
	stop := p.Watch(make(chan time.Time), func() (Usage, error) { return Usage{Procs: 4}, nil }, func() { kills++ })
	assert.Empty(t, stop(), "at the cap is not past it")
	stop = p.Watch(make(chan time.Time), func() (Usage, error) { return Usage{}, errors.New("no /proc") }, func() { kills++ })
	assert.Empty(t, stop(), "a watch that cannot look must not kill what it cannot see")
	assert.Zero(t, kills)
}
