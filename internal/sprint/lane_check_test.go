package sprint_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judgment checks the lane before it rises (friend_deadline.go, LaneChecked;
// a-judgment-checks-the-lane-before-it-rises.w1): on the night of 2026-10-05 about 250
// judgments reached the coordinator, most of them a stall, a deadline or "finishes none" for
// a friend whose lane was live and inside its run time. On the coordinator's pass rig, a
// friend whose beat names her card running past the finish window raises nothing, and the
// tick's heartbeat counts the judgment it kept quiet, its row "running 31m of 120m"; the
// moment her beat stops naming it, "finishes none" rises.

// laneTick is the pass rig's tick with the friend's beat naming the cards running.
func (r *passRig) laneTick(d time.Duration, friend string, running ...string) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for f, pong := range r.pongs {
		if pong.After(r.proved[f]) {
			// her daemon asked a check and her session answered it at pong (sprint.ProveBeat)
			r.mu.Lock()
			now := r.now
			r.now = pong
			r.mu.Unlock()
			nonce := fmt.Sprintf("n%d", pong.Unix())
			_, _, err := r.st.FriendBeatProof(r.ctx, f, sprint.FriendReport{Active: pong}, nil, sprint.BeatWords{Run: "run1", Check: nonce, Pong: nonce})
			require.NoError(r.t, err)
			r.mu.Lock()
			r.now = now
			r.mu.Unlock()
			r.proved[f] = pong
		}
		rep := sprint.FriendReport{Active: r.clock()}
		if f == friend {
			rep.Running = running
			if rep.Running == nil {
				rep.Running = []string{} // a named empty list clears the last known lane
			}
		}
		_, err := r.st.FriendBeatReport(r.ctx, f, rep, nil)
		require.NoError(r.t, err)
		if pong.After(r.answered[f]) {
			_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: pong, Generation: sprint.FirstSeatGeneration}, "")
			require.NoError(r.t, err)
			r.answered[f] = pong
		}
	}
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// quiet is the judgments the last tick kept quiet, as its heartbeat says them.
func (r *passRig) quiet() []sprint.LaneQuiet {
	r.t.Helper()
	_, hb, err := r.st.Machine(r.ctx)
	require.NoError(r.t, err)
	return hb.Quiet
}

// suppressed is the heartbeat's count of the judgments kept quiet since the epoch began.
func (r *passRig) suppressed() sprint.Suppressed {
	r.t.Helper()
	_, hb, err := r.st.Machine(r.ctx)
	require.NoError(r.t, err)
	return hb.Suppressed
}

func TestNoStallRisesOverALiveLane(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc, "the friend's card is dealt")
	require.Equal(t, sprint.Working, fc.Col)
	holder, _ := sprint.FriendOfRow(fc.Row)

	// 31 minutes with no finish, her beat naming the card running every tick: inside its
	// cap (DeadlineUnfinished, 2h, with no friend deadline yet), so no judgment rises
	for _, d := range []time.Duration{10 * time.Minute, 10 * time.Minute, 10*time.Minute + 30*time.Second, 30 * time.Second} {
		r.pongs[holder] = r.clock()
		r.laneTick(d, holder, fc.ID)
	}
	assert.Nil(t, r.open(sprint.NFriendIdle, fc.Row), "a live lane inside its cap finishes none and raises nothing")
	assert.Zero(t, r.count(sprint.Judgment, sprint.NFriendIdle, ""), "never written")
	assert.Zero(t, r.count(sprint.Judgment, sprint.NStalled, ""), "no stall")
	assert.Zero(t, r.count(sprint.Judgment, sprint.NWorkLate, fc.ID), "no deadline")
	quiet := r.quiet()
	require.Len(t, quiet, 1, "the coordinator's count: one judgment kept quiet")
	assert.Equal(t, sprint.NFriendIdle, quiet[0].Type)
	assert.Equal(t, fc.Row, quiet[0].Subject)
	assert.Equal(t, "friend "+holder+": "+fc.ID+" running 31m of 120m (her beat names it running)", quiet[0].Why)
	assert.Equal(t, holder, quiet[0].Friend)
	assert.Equal(t, "running 31m of 120m", quiet[0].Run, "her row's words")
	sup := r.suppressed()
	assert.Equal(t, sprint.Suppressed{Epoch: r.snap().Epoch, N: 1, Lane: 1}, sup, "counted once, however many ticks kept it quiet")

	// her beat names it by its job too (the stored id): still her lane
	r.pongs[holder] = r.clock()
	r.laneTick(time.Minute, holder, sprint.StoredID(fc.ID, r.snap().Epoch))
	assert.Nil(t, r.open(sprint.NFriendIdle, fc.Row), "her job's name is her card")

	// her beat stops naming it: no lane is live, and finishes none rises at once
	r.pongs[holder] = r.clock()
	r.laneTick(time.Second, holder)
	idle := r.open(sprint.NFriendIdle, fc.Row)
	require.NotNil(t, idle, "no live lane: finishes none rises")
	assert.Contains(t, idle.What, fc.ID)
	assert.Empty(t, r.quiet(), "nothing kept quiet")
	assert.Equal(t, sup, r.suppressed(), "what was kept quiet stays counted")
}

// The count of the judgments suppressed (Suppressed.Counted): each quiet once while the
// ticks keep it quiet, again when it is kept quiet anew, each by its cause, and from none in
// another epoch.
func TestTheSuppressedCountCountsEachJudgmentOnceByItsCause(t *testing.T) {
	t.Parallel()
	lane := sprint.LaneQuiet{Type: sprint.NFriendIdle, Subject: "friend.amy"}
	late := sprint.LaneQuiet{Type: sprint.NWorkLate, Subject: "s1-1.w1"}
	readers := sprint.LaneQuiet{Type: sprint.NReadersBehind, Subject: sprint.SprintSubject}
	tier := sprint.LaneQuiet{Type: sprint.NRaiseReadTier, Subject: sprint.StreamSubject("s1")}

	var c sprint.Suppressed
	c = c.Counted(3, nil, []sprint.LaneQuiet{lane, readers})
	assert.Equal(t, sprint.Suppressed{Epoch: 3, N: 2, Lane: 1, Readers: 1}, c)
	c = c.Counted(3, []sprint.LaneQuiet{lane, readers}, []sprint.LaneQuiet{lane, readers, late, tier})
	assert.Equal(t, sprint.Suppressed{Epoch: 3, N: 4, Lane: 2, Readers: 1, Tier: 1}, c, "the two kept quiet still are not counted again")
	c = c.Counted(3, []sprint.LaneQuiet{lane, readers, late, tier}, nil)
	c = c.Counted(3, nil, []sprint.LaneQuiet{lane})
	assert.Equal(t, sprint.Suppressed{Epoch: 3, N: 5, Lane: 3, Readers: 1, Tier: 1}, c, "kept quiet anew after it rose: a judgment again")
	c = c.Counted(4, []sprint.LaneQuiet{lane}, []sprint.LaneQuiet{lane})
	assert.Equal(t, sprint.Suppressed{Epoch: 4, N: 1, Lane: 1}, c, "a clear starts the count from none")
}

// The lane is live only inside its cap: a card running past it is late however the beat
// names it, and a beat older than FriendLaneLive is no lane.
func TestALiveLaneIsLiveInsideItsCapAndOnAFreshBeat(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc)
	holder, _ := sprint.FriendOfRow(fc.Row)
	r.pongs[holder] = r.clock()
	r.laneTick(30*time.Minute, holder, fc.ID)

	s = r.snap()
	c := s.Fleet.Card(fc.ID)
	beats := map[string]sprint.Beat{holder: {At: s.Now, Friend: &sprint.FriendReport{Running: []string{fc.ID}}}}
	l, ok := sprint.LiveLane(s, sprint.TickReq{Beats: beats}, c)
	require.True(t, ok)
	assert.Equal(t, "running 30m of 120m", l.Row())

	_, ok = sprint.LiveLane(s, sprint.TickReq{Beats: map[string]sprint.Beat{holder: {At: s.Now.Add(-sprint.FriendLaneLive - time.Second), Friend: beats[holder].Friend}}}, c)
	assert.False(t, ok, "a beat older than FriendLaneLive is no lane")

	_, ok = sprint.LiveLane(s, sprint.TickReq{Friends: []sprint.FriendSeat{{Name: holder, Running: []string{fc.ID}}}}, c)
	assert.True(t, ok, "her daemon's lane (her seat's running) is a live lane")

	_, ok = sprint.LiveLane(s, sprint.TickReq{Beats: beats, Stopped: func(from, to time.Time) time.Duration { return -2 * time.Hour }}, c)
	assert.False(t, ok, "past its cap the lane is late, whatever the beat says")

	_, ok = sprint.LiveLane(s, sprint.TickReq{Beats: map[string]sprint.Beat{holder: {At: s.Now, Friend: &sprint.FriendReport{Running: []string{"other"}}}}}, c)
	assert.False(t, ok, "a beat naming another card is not this card's lane")
}

// A friend seat carries the last stored beat's running list, so it cannot revive a
// lane whose timestamp the lane check already refused (docs/SPEC-SPRINT.md section 8).
func TestAStoredFriendSeatCannotReviveAStaleLane(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	s := r.snap()
	fc := s.Fleet.Card(s.Work.Card("f1-1").F("work"))
	require.NotNil(t, fc)
	holder, _ := sprint.FriendOfRow(fc.Row)
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{
		{"old stored beat", sprint.FriendLaneLive + time.Second},
		{"future stored beat", -time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := sprint.Beat{At: s.Now.Add(-tc.age), Friend: &sprint.FriendReport{Running: []string{fc.ID}}}
			_, live := sprint.LiveLane(s, sprint.TickReq{
				Beats:   map[string]sprint.Beat{holder: b},
				Friends: []sprint.FriendSeat{{Name: holder, Status: sprint.Up, Beat: b, Running: b.Friend.Running}},
			}, fc)
			assert.False(t, live, "a stored running list is not a current daemon lane")
		})
	}
}
