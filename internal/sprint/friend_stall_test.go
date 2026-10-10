package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friend stall ladder (docs/SPEC-SPRINT.md section friend-stall-ladder-r.w1;
// the model is tla/StallLadder.tla).
//
// Invariants verified:
//   NoCardHeldPastBound: no unstarted card is held past stall_after + 4*step (taken back at rung 4).
//   NoStartedRedealt: started cards stay with friend and finish; only unstarted cards are taken back.
//   ReleasedOnlyByActivity: a friend marked down for stall is released to up only by her activity (session, running beat, finish),
//     never by card progress alone.

func TestFriendStallLadderClimbsAndTakesBackUnstarted(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)

	wc1 := w.s.Fleet.Card("s1-1.w1")
	wc2 := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, wc1)
	require.NotNil(t, wc2)
	require.Equal(t, Working, wc1.Col)
	require.Equal(t, Working, wc2.Col)

	// Friend amy starts s1-1 (progress stamped)
	wc1.Fields[FieldProgress] = stamp(w.s.Now)
	// s1-2 is left unstarted

	t0 := w.s.Now
	var woken []int
	wakeFn := func(friend string, rung int, d time.Duration) error {
		if friend == "amy" {
			woken = append(woken, rung)
		}
		return nil
	}

	// At t0: not stalled (< 20m)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "rung 0 has no property")
	assert.Empty(t, woken)

	// Rung 1: at t0 + 21m (between 20m and 25m)
	w.s.Now = t0.Add(21 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "1", rung)
	assert.Equal(t, []int{1}, woken)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)

	// Rung 2: at t0 + 26m (between 25m and 30m)
	w.s.Now = t0.Add(26 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "2", rung)
	assert.Equal(t, []int{1, 2}, woken)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)

	// Rung 3: at t0 + 31m (between 30m and 35m)
	w.s.Now = t0.Add(31 * time.Minute)
	p3 := w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "3", rung)
	var judged bool
	for _, n := range p3.Notes {
		if n.Kind == Judgment && n.Type == NFriendStalled {
			judged = true
			assert.Contains(t, n.What, "friend amy stalled")
			assert.Contains(t, n.What, "two wakes unanswered")
		}
	}
	assert.True(t, judged, "judgment note for stall emitted at rung 3")

	// Rung 4: at t0 + 36m (between 35m and 40m)
	// Unstarted card s1-2.w1 should be taken back; started card s1-1.w1 stays!
	w.s.Now = t0.Add(36 * time.Minute)
	w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "4", rung)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col, "unstarted card taken back")
	assert.Equal(t, Ready, w.s.Work.Card("s1-2").Col, "primary back to ready")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "started card stays with friend")

	// Rung 5: at t0 + 41m (>= 40m)
	// Friend marked down
	w.s.Now = t0.Add(41 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{WakeFriend: wakeFn, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "5", rung)
	downStamp, hasDown := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.True(t, hasDown, "stall down property recorded")
	assert.NotEmpty(t, downStamp)
	require.NotNil(t, p5.Health)
	assert.Equal(t, "amy", p5.Health.Friend)
	assert.Equal(t, Down, p5.Health.Health.State)
	assert.Equal(t, "stalled", p5.Health.Health.Reason)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "started card stays even when marked down")

	// Friend session activity arrives -> released to up and ladder resets to 0!
	w.s.Now = t0.Add(42 * time.Minute)
	beats := map[string]Beat{
		"amy": {
			Friend: &FriendReport{
				Active: w.s.Now,
			},
		},
	}
	pRel := w.part(TickFriendStall, TickReq{Beats: beats, Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "rung reset to 0")
	downStamp, _ = w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "stall down cleared")
	assert.Nil(t, pRel.Health, "the release writes no observation")
	assert.Equal(t, []string{"amy"}, pRel.HealthClear, "the release removes rung 5's observation: her beat rule decides again")
}

func TestFriendStallLadderResetsOnProgress(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)

	t0 := w.s.Now
	// Advance to rung 2 (26m)
	w.s.Now = t0.Add(26 * time.Minute)
	w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	require.Equal(t, "2", rung)

	// Now friend stamps progress on the card
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	// Next tick sees the fresh progress: stall resets to rung 0!
	w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "progress resets rung to 0")
}

func TestFriendStallLadderProgressDoesNotReleaseDownFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)

	// Started card stays through stall ladder
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)

	t0 := w.s.Now
	// Advance to rung 5 (41m)
	w.s.Now = t0.Add(41 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{Friends: seats})
	require.NotNil(t, p5.Health)
	assert.Equal(t, Down, p5.Health.Health.State)

	// Stamping card progress resets rung, but DOES NOT release from Down (ReleasedOnlyByActivity)
	w.s.Fleet.Card("s1-1.w1").Fields[FieldProgress] = stamp(w.s.Now)
	pProg := w.part(TickFriendStall, TickReq{Friends: seats})
	assert.Nil(t, pProg.Health, "card progress alone does not release a down friend to up")
	assert.Empty(t, pProg.HealthClear, "card progress alone does not release a down friend to up")
	_, hasDown := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.True(t, hasDown, "stall down remains")

	// Session activity releases her to Up
	w.s.Now = w.s.Now.Add(time.Minute)
	beats := map[string]Beat{
		"amy": {
			Friend: &FriendReport{
				Active: w.s.Now,
			},
		},
	}
	pRel := w.part(TickFriendStall, TickReq{Beats: beats, Friends: seats})
	assert.Nil(t, pRel.Health, "the release writes no observation")
	assert.Equal(t, []string{"amy"}, pRel.HealthClear, "session activity releases her: her observation is removed")
	downStamp, _ := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "stall down cleared on session activity")
}

// A one-shot lane friend, or one whose cards run in child agents, moves no session: her beat
// naming running cards is her activity at the beat's time. It keeps her at rung 0 while she
// works, and releases her from a stall down, removing the observation rung 5 wrote and
// writing none (a written up observation would hold her down ten seconds later for good).
func TestFriendStallLadderBeatNamingRunningCardsIsActivity(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}}
	dealStarted(w, seats...)
	t0 := w.s.Now

	running := func(at time.Time) map[string]Beat {
		return map[string]Beat{"amy": {At: at, Friend: &FriendReport{Running: []string{"s1-1.w1"}}}}
	}
	idle := func(at time.Time) map[string]Beat {
		return map[string]Beat{"amy": {At: at, Friend: &FriendReport{}}}
	}

	// working an hour on a running beat: never stalled
	for _, m := range []int{21, 26, 31, 36, 41, 60} {
		w.s.Now = t0.Add(time.Duration(m) * time.Minute)
		p := w.part(TickFriendStall, TickReq{Beats: running(w.s.Now), Friends: seats})
		rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
		assert.Empty(t, rung, "a beat naming running cards keeps her at rung 0 (minute %d)", m)
		assert.Nil(t, p.Health)
	}
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	// a beat with nothing running is no activity: with no session activity, progress or
	// finish she climbs (from the deal: a running beat is activity only while it is the beat)
	last := w.s.Now
	w.s.Now = last.Add(41 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{Beats: idle(w.s.Now), Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "5", rung)
	require.NotNil(t, p5.Health)
	assert.Equal(t, Down, p5.Health.Health.State)

	// her beat names a running card again: released, the observation removed, none written
	w.s.Now = w.s.Now.Add(time.Minute)
	pRel := w.part(TickFriendStall, TickReq{Beats: running(w.s.Now), Friends: seats})
	downStamp, _ := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "stall down cleared")
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung)
	assert.Nil(t, pRel.Health, "the release writes no observation")
	assert.Equal(t, []string{"amy"}, pRel.HealthClear)

	// and her status is her session's evidence alone: a beat naming running cards is none
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: running(w.s.Now)["amy"], Generation: FirstSeatGeneration}, w.s.Now))
}

// A finish on her row within friend_stall_after is her activity: a friend whose cards run in
// child agents shows only finishes. It holds her at rung 0 and releases a stall down.
func TestFriendStallLadderFinishIsActivity(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 3, Status: Up, Class: "flash,pro"}}
	dealStarted(w, seats...)
	t0 := w.s.Now
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	// s1-2 finishes at 15m: at 21m she is not stalled (finish 6m ago)
	w.place(w.s.Fleet, "s1-2.w1", FriendRow("amy"), DoneOK)
	w.s.Fleet.Card("s1-2.w1").Fields["finished"] = stamp(t0.Add(15 * time.Minute))
	w.s.Now = t0.Add(21 * time.Minute)
	w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "a finish within friend_stall_after is activity")

	// 41m after that finish she is down
	w.s.Now = t0.Add(56 * time.Minute)
	p5 := w.part(TickFriendStall, TickReq{Friends: seats})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	require.Equal(t, "5", rung)
	require.NotNil(t, p5.Health)

	// a finish after the down releases her, removing the observation and writing none
	w.s.Now = w.s.Now.Add(time.Minute)
	w.place(w.s.Fleet, "s1-1.w1", FriendRow("amy"), DoneFailed)
	w.s.Fleet.Card("s1-1.w1").Fields["finished"] = stamp(w.s.Now)
	pRel := w.part(TickFriendStall, TickReq{Friends: seats})
	downStamp, _ := w.s.Fleet.Prop(PropFriendStallDown("amy"))
	assert.Empty(t, downStamp, "a finish after the down releases her")
	assert.Nil(t, pRel.Health)
	assert.Equal(t, []string{"amy"}, pRel.HealthClear)
}

// A friend whose only evidence is a fresh finish (the store's record: the card has left her
// row) or a fresh session proof, while her daemon's walk reads a stale write, is never
// stalled: the ladder reads the newest of her evidence, never one stale field. With no
// evidence past the bound, her unstarted card is taken back (2026-10-06: stella reported
// hourly and sent bus notes every few minutes while her walk read 3d, and two of her working
// cards were taken back as stalled).
func TestAFriendWithAReportInTheWindowIsNeverStalled(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)
	t0 := w.s.Now
	stale := t0.Add(-72 * time.Hour)
	beat := func(at time.Time, proof time.Time) map[string]Beat {
		return map[string]Beat{"amy": {At: at, Friend: &FriendReport{Active: stale}, Proof: proof}}
	}
	withFinish := func(at time.Time) []FriendSeat {
		s := seats[0]
		s.Active, s.Finished = stale, at
		return []FriendSeat{s}
	}

	// a fresh finish and a stale walk, past every rung of the ladder: never stalled
	for _, m := range []int{21, 26, 31, 36, 41, 60} {
		w.s.Now = t0.Add(time.Duration(m) * time.Minute)
		p := w.part(TickFriendStall, TickReq{Beats: beat(w.s.Now, time.Time{}), Friends: withFinish(w.s.Now.Add(-5 * time.Minute))})
		rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
		assert.Empty(t, rung, "a finish 5m ago holds her at rung 0 (minute %d)", m)
		assert.Nil(t, p.Health)
	}
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col, "her card stays with her")
	at, what := FriendWorked(w.s, "amy", friendWorkOf(TickReq{Beats: beat(w.s.Now, time.Time{}), Friends: withFinish(w.s.Now.Add(-5 * time.Minute))}, "amy"))
	assert.Equal(t, w.s.Now.Add(-5*time.Minute), at)
	assert.Equal(t, "finish", what)

	// a fresh session proof (a bus message of hers) with the walk stale: never stalled
	for _, m := range []int{90, 120} {
		w.s.Now = t0.Add(time.Duration(m) * time.Minute)
		w.part(TickFriendStall, TickReq{Beats: beat(w.s.Now, w.s.Now.Add(-2*time.Minute)), Friends: withFinish(stale)})
		rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
		assert.Empty(t, rung, "a bus message 2m ago holds her at rung 0 (minute %d)", m)
	}
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)

	// a report written on her card counts as hers, and its card is started: never taken back
	last := w.s.Now
	w.s.Fleet.Card("s1-1.w1").Fields[FieldReported] = stamp(last)
	w.s.Now = last.Add(10 * time.Minute)
	w.part(TickFriendStall, TickReq{Beats: beat(w.s.Now, time.Time{}), Friends: withFinish(stale)})
	rung, _ := w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Empty(t, rung, "a report 10m ago holds her at rung 0")

	// no evidence past the bound: the ladder climbs to rung 4 and takes back the card she
	// has not started; the card with her report on it stays
	w.s.Now = last.Add(36 * time.Minute)
	w.part(TickFriendStall, TickReq{Beats: beat(w.s.Now, time.Time{}), Friends: withFinish(stale)})
	rung, _ = w.s.Fleet.Prop(PropFriendStallRung("amy"))
	assert.Equal(t, "4", rung)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col, "no evidence past the bound: her unstarted card is taken back")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "a card with a report of hers on it is started and stays")
}

// A report or a session proof stamped after the server's clock counts as that
// clock (FriendWorked), not as evidence that stays fresh until the stamp
// arrives, and the future stamp is logged once. Friend health already refuses
// a proof from the future; the stall ladder used to take the stamp as the
// time of the work.
func TestFutureDatedEvidenceCountsAsNow(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	now := w.s.Now
	past := now.Add(-time.Minute)
	future := now.Add(48 * time.Hour).UTC().Truncate(time.Second)

	at, what := FriendWorked(w.s, "amy", friendWorkOf(TickReq{Beats: map[string]Beat{"amy": {Proof: past}}}, "amy"))
	assert.True(t, at.Equal(past), "a proof before now is that time, got %s", at)
	assert.Equal(t, "session proof", what)
	assert.Equal(t, 0, futureEvidenceLogCount("amy", past))

	at, what = FriendWorked(w.s, "amy", friendWorkOf(TickReq{Beats: map[string]Beat{"amy": {Proof: future}}}, "amy"))
	assert.True(t, at.Equal(now), "a future proof counts as the server's now, got %s", at)
	assert.Equal(t, "session proof", what)
	assert.Equal(t, 1, futureEvidenceLogCount("amy", future), "the future stamp is logged once")

	at, what = FriendWorked(w.s, "amy", friendWorkOf(TickReq{Beats: map[string]Beat{"amy": {Proof: future}}}, "amy"))
	assert.True(t, at.Equal(now), "a future proof still counts as now")
	assert.Equal(t, "session proof", what)
	assert.Equal(t, 1, futureEvidenceLogCount("amy", future), "the same future stamp is not logged again")

	row := FriendRow("amy")
	w.s.Fleet.Put(&Card{
		ID: "s1-1.w1", Row: row, Col: Working, Rev: 1,
		Fields: map[string]string{"kind": "work", FieldReported: stamp(future.Add(time.Hour))},
	})
	reportAt := future.Add(time.Hour)
	at, what = FriendWorked(w.s, "amy", FriendWork{})
	assert.True(t, at.Equal(now), "a future report counts as the server's now, got %s", at)
	assert.Equal(t, "report", what)
	assert.Equal(t, 1, futureEvidenceLogCount("amy", reportAt), "the future report is logged once")
	at, _ = FriendWorked(w.s, "amy", FriendWork{})
	assert.True(t, at.Equal(now))
	assert.Equal(t, 1, futureEvidenceLogCount("amy", reportAt), "the same future report is not logged again")
}

// futureEvidenceLogCount is how many times friend f's evidence stamped at
// stamped was logged for being after the server's clock. The stall ladder
// logs each such stamp once.
func futureEvidenceLogCount(friend string, stamped time.Time) int {
	v, ok := futureEvidenceOnce.Load(futureEvidenceKey(friend, stamped))
	if !ok {
		return 0
	}
	n, _ := v.(int)
	return n
}
