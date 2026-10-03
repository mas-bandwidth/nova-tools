package main

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// termRig is the replace rig driven by an injected signal and clock: no real time
// passes, each wait between passes is at once, and the clock moves only when the
// test moves it (nova-tools#5096 item 26).
type termRig struct {
	*replaceRig
	m     *member.Member
	out   *bytes.Buffer
	term  chan os.Signal
	clock time.Time
}

func newTermRig(t *testing.T) *termRig {
	t.Helper()
	r, m, out, _ := newReplaceRig(t)
	return &termRig{replaceRig: r, m: m, out: out, term: make(chan os.Signal, 1), clock: time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)}
}

func (r *termRig) run(t *testing.T, limit int) (int, bool) {
	t.Helper()
	at := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- r.clock
		return c
	}
	var errb bytes.Buffer
	return memberLoop(r.m, loopRun{every: time.Hour, limit: limit, term: r.term, now: func() time.Time { return r.clock }, after: at}, r.out, &errb)
}

// A unit restart is a SIGTERM to the member (systemd's KillMode=mixed, launchd's
// bootout): the member takes nothing new, reports the card it runs when its child
// ends, and stops; it is not killed mid-card and its card is not lost.
func TestMemberDrainsOnSIGTERMAndStopsWhenTheLastChildIsReported(t *testing.T) {
	t.Parallel()
	r := newTermRig(t)
	r.onQueue = func(call int) {
		switch call {
		case 1: // tick 1 takes c1; the supervisor stops the unit while it runs
			r.term <- syscall.SIGTERM
		case 4:
			r.childEnd = true
		}
	}
	n, replaced := r.run(t, 20)
	assert.False(t, replaced, "a SIGTERM is no replaced binary: exit 0, not 3")
	assert.Equal(t, []string{"c1"}, r.started, "c2 was ready and room was left, and it was not taken after the SIGTERM")
	assert.Equal(t, 1, r.takes)
	assert.Equal(t, []string{"c1@1"}, r.finishes, "the running child was reported, not killed")
	assert.Equal(t, 1, strings.Count(r.out.String(), "MEMBER DRAIN SIGTERM"), "said once")
	assert.Contains(t, r.out.String(), "MEMBER STOP SIGTERM: the last child is reported")
	assert.Equal(t, 4, n)
	assert.Equal(t, 0, r.m.Running())
}

// With nothing running the SIGTERM stops the member at once.
func TestMemberStopsAtOnceOnSIGTERMWithNothingRunning(t *testing.T) {
	t.Parallel()
	r := newTermRig(t)
	r.empty = true
	r.term <- syscall.SIGTERM
	n, replaced := r.run(t, 20)
	assert.False(t, replaced)
	assert.Equal(t, 0, n, "no pass after the SIGTERM")
	assert.Contains(t, r.out.String(), "MEMBER STOP SIGTERM: nothing running")
}

// A child that never ends does not hold the stop for ever: the drain is bounded by
// the longest deadline of the cards the member runs, plus the long work's own bound
// (member.LongStall) for its push and report; past it the member stops and says what
// it left running. The unit's stop timeout is above the longest bound (DrainMost).
func TestMemberDrainOnSIGTERMIsBoundedByTheLongestDeadline(t *testing.T) {
	t.Parallel()
	r := newTermRig(t)
	r.deadline = 1800 // the packets' route deadline, seconds
	began := r.clock
	r.onQueue = func(call int) {
		if call == 1 {
			r.term <- syscall.SIGTERM
		}
		r.clock = r.clock.Add(10 * time.Minute)
	}
	n, replaced := r.run(t, 50)
	assert.False(t, replaced)
	bound := 30*time.Minute + member.LongStall
	assert.Contains(t, r.out.String(), "MEMBER DRAIN SIGTERM: taking no new card, 1 running; it stops when the last child is reported, at most "+bound.String())
	assert.Contains(t, r.out.String(), "MEMBER STOP SIGTERM: the drain's bound "+bound.String()+" passed with 1 running")
	assert.Equal(t, 1, r.m.Running(), "the child is left to its supervisor and the sprint, not reported")
	assert.Empty(t, r.finishes)
	require.Positive(t, n)
	assert.LessOrEqual(t, r.clock.Sub(began), bound+20*time.Minute, "it stopped within a pass of the bound")
	assert.GreaterOrEqual(t, r.clock.Sub(began), bound)
}

// The bound is the longest deadline the running cards name (the member's own
// --deadline for a card that names none) plus LongStall, never past DrainMost.
func TestDrainBound(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 30*time.Minute+member.LongStall, member.DrainBound(30*time.Minute))
	assert.Equal(t, member.LongStall, member.DrainBound(0))
	assert.Equal(t, member.DrainMost, member.DrainBound(10*time.Hour))
}
