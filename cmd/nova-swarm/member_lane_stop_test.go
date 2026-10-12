package main

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// THE MEMBER'S OWN STOP ENDS ITS LANES (v1.2.15). Each native leads a session of its own
// (member_lane_reap_unix_test.go), which neither a supervisor's signal to the member's group
// nor a terminal's interrupt reaches, so memberLoop ends every lane itself: past a SIGTERM
// drain's bound, and at once on an interrupt.

// runLanes is termRig.run with an interrupt channel and the lanes' end handed in.
func (r *termRig) runLanes(t *testing.T, limit int, intr <-chan os.Signal, endLanes func() int) (int, bool) {
	t.Helper()
	at := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- r.clock
		return c
	}
	var errb bytes.Buffer
	return memberLoop(r.m, loopRun{every: time.Hour, limit: limit, term: r.term, intr: intr, endLanes: endLanes,
		now: func() time.Time { return r.clock }, after: at}, r.out, &errb)
}

func TestTheDrainBoundEndsEveryLaneStillRunning(t *testing.T) {
	t.Parallel()
	r := newTermRig(t)
	r.deadline = 1800
	r.onQueue = func(call int) {
		if call == 1 {
			r.term <- syscall.SIGTERM
		}
		r.clock = r.clock.Add(10 * time.Minute)
	}
	ended := 0
	_, replaced := r.runLanes(t, 50, nil, func() int { ended++; return 1 })
	assert.False(t, replaced)
	bound := member.DrainBound(30 * time.Minute)
	assert.Equal(t, 1, ended, "the drain's bound ends the lanes once")
	assert.Contains(t, r.out.String(), "MEMBER STOP SIGTERM: the drain's bound "+bound.String()+" passed with 1 running; the member ended 1 lane(s)")
}

func TestAnInterruptEndsEveryLaneAndStopsTheMemberAtOnce(t *testing.T) {
	t.Parallel()
	r := newTermRig(t)
	intr := make(chan os.Signal, 1)
	r.onQueue = func(call int) {
		if call == 1 {
			intr <- os.Interrupt
		}
	}
	ended := 0
	n, replaced := r.runLanes(t, 20, intr, func() int { ended++; return 1 })
	assert.False(t, replaced)
	require.Less(t, n, 20, "the interrupt stops the member, not the tick limit")
	assert.Equal(t, 1, ended, "the interrupt ends the lanes once")
	assert.Contains(t, r.out.String(), "MEMBER STOP interrupt: the member ended 1 lane(s)")
}
