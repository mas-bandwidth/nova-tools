package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleet up counts as a beat (docs/SPEC-SPRINT.md section 5): a member released
// from a hold whose last beat is old is up at the next tick, within the beat
// window, and keeps the cards the release dealt it. Without it the live run of
// 2026-10-01 marked four just-released members down at the next tick and took
// their cards back.
func TestFleetUpOnAMemberWithAnOldBeatKeepsItUpAtTheNextTick(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 8")
	ta.ok("start")
	ta.ok("fleet down m1")
	ta.ok("tick")
	ta.live = nil // neither member beats from here on: m1's last beat gets old
	ta.a.sleep(10 * time.Minute)
	ta.ok("fleet up m1")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, sprint.Up, w.Tables["fleet"]["m1"]["status"], "released and up at once")
	dealt := cardsOf(w, "m1")
	require.NotEqual(t, "0", w.Tables["fleet"]["m1"]["ready"], "the release dealt m1 cards")
	ta.ok("tick")
	ta.json("where", &w)
	assert.Equal(t, sprint.Up, w.Tables["fleet"]["m1"]["status"], "m1 at the next tick")
	assert.GreaterOrEqual(t, cardsOf(w, "m1"), dealt, "m1's cards stay dealt")
}

// A member that never beat has no beat to touch: fleet up adds it down, as it
// did (TestFleetDownHoldsAndFleetUpReleases).
func TestFleetUpOnAMemberThatNeverBeatTouchesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("fleet up m9")
	ta.ok("tick")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, sprint.Down, w.Tables["fleet"]["m9"]["status"])
}

// cardsOf is the work cards a member holds in the view: ready and working.
func cardsOf(w whereView, member string) int {
	row := w.Tables["fleet"][member]
	ready, _ := strconv.Atoi(row["ready"])
	working, _ := strconv.Atoi(row["working"])
	return ready + working
}
