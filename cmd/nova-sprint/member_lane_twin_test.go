package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// heldPusher is a pusher whose push is held in flight until the test lets it go, as a push
// to a forge is for a second or more: began says a push has begun.
type heldPusher struct {
	began, release chan struct{}
	push           member.Push
}

func (p *heldPusher) Push(member.Packet, member.Result) member.Push {
	p.began <- struct{}{}
	<-p.release
	return p.push
}

// The owner, 2026-10-01: "this \"squishiness\" of having > width in the working set has me
// concerned." / "You must not do heavy work in-line." / "The state machines and logic should
// never have long steps in them. Anything long can be added to a queue, and performed
// async." A member at its width whose child exits: its pass begins the push and returns
// without waiting for it, reports nothing and starts nothing (the lane is held until the
// card is reported), and the sprint gives it no card past its width whatever it asks. When
// the push ends the member's loop is woken, and the next pass reports the card and fills
// the lane. No card is started twice.
func TestALaneIsHeldUntilItsCardIsReportedAndThePassNeverWaitsOnAPush(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	twin := func(line string) string {
		t.Helper()
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
		return o
	}
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 6",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	} {
		twin(line)
	}
	rn := &twinRunner{children: map[string]*twinChild{}}
	pu := &heldPusher{began: make(chan struct{}, 1), release: make(chan struct{}), push: member.Push{Sha: "0123456789abcdef0123456789abcdef01234567"}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "m1", Background: true}, twinSprint{file: file, actor: "m1"}, rn, pu, &log)
	tick := func() {
		t.Helper()
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err, log.String())
	}
	tick()
	require.Len(t, rn.packets, 2, "at its width of 2: %s", log.String())

	c := rn.children[rn.packets[0].Card]
	c.mu.Lock()
	c.done, c.res = true, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "did it"}
	c.mu.Unlock()
	tick() // returns with the push in flight: the test would hang here if the pass waited for it
	<-pu.began
	assert.NotContains(t, log.String(), "finish ", "nothing is reported before its push ends")
	assert.Len(t, rn.packets, 2, "the lane is held until the report: no third card is started: %s", log.String())
	assert.Equal(t, 2, m.Running())
	// the sprint holds the width itself: a take past it moves nothing, whatever is asked
	assert.Contains(t, twin("nova-sprint take --as m1 --limit 5 --json"), `"moved":[]`, "m1 has two working of a width of two")
	tick()
	assert.NotContains(t, log.String(), "finish ", "still in flight: still nothing to report")

	close(pu.release)
	<-m.Wake() // the push ended: the loop is woken (a member that is not would hang here)
	tick()
	assert.Equal(t, 1, strings.Count(log.String(), "finish "+rn.packets[0].Card+" ok=true"), "reported once, by the pass after the push ended: %s", log.String())
	require.Len(t, rn.packets, 3, "the lane the report freed is filled in the same pass: %s", log.String())
	assert.Equal(t, 2, m.Running(), "back at the width, never past it")
	seen := map[string]bool{}
	for _, p := range rn.packets {
		assert.False(t, seen[p.Card], "%s started twice: %s", p.Card, log.String())
		seen[p.Card] = true
	}
}
