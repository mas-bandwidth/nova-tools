package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friend-delivery check (docs/SPEC-SPRINT.md, section friend-width-and-delivery; the
// model is tla/FriendWidth.tla, DownOnlyAfterTwoWindows): a friend up with lanes busy and
// no report of hers for DeliveryWindow is one judgment, raised again at most every window,
// her session pinged once by rule on the first window and she marked down by rule on the
// second, closed when a report of hers arrives, she has no card working, or she is not up.
// Every test runs on a fake clock.

// deliveryWorld is a friend with three cards dealt on her row under a width of two: two
// working, taken at t0, one queued.
func deliveryWorld(t *testing.T) (*world, []FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)
	require.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working))
	require.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready))
	return w, seats
}

func TestAFriendWithLanesBusyAndNoReportForTheWindowIsJudgedAndRaisedAgainEachWindow(t *testing.T) {
	t.Parallel()
	w, seats := deliveryWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendDeliveryPrefix, "amy"}
	req := TickReq{Friends: seats}
	assert.Equal(t, 30*time.Minute, w.s.DeliveryWindow(), "the default is the friend-finish window")

	// under the window: nothing, and no state in the store
	w.s.Now = t0.Add(29 * time.Minute)
	p := w.part(TickFriendDelivery, req)
	assert.Empty(t, judgmentsOf(p, NFriendDelivery))
	assert.False(t, e.any(w.s))

	// the window: one judgment on her row, naming her busy lanes and how long
	w.s.Now = t0.Add(30 * time.Minute)
	p = w.part(TickFriendDelivery, req)
	js := judgmentsOf(p, NFriendDelivery)
	require.Len(t, js, 1)
	assert.Equal(t, []string{amy}, js[0].Primaries)
	assert.Contains(t, js[0].What, "friend amy has 2 lanes busy and delivered nothing for 30m0s")
	assert.Contains(t, js[0].What, "no report of hers yet")
	assert.Contains(t, js[0].What, "s1-1.w1, s1-2.w1")
	assert.Equal(t, deliveryDecisions("amy", time.Time{}, w.s.Now.Add(30*time.Minute)), js[0].Decisions)
	assert.Contains(t, js[0].Decisions[1], "friend down amy --reason 'no delivery since the start of her work' --until "+stamp(t0.Add(60*time.Minute)))
	assert.Equal(t, "1", e.get(w.s, epRaised))
	require.Len(t, openOf(w, NFriendDelivery, amy), 1)

	// not again within the window; a window on, raised again in place with the push
	w.s.Now = t0.Add(45 * time.Minute)
	p = w.part(TickFriendDelivery, req)
	assert.Empty(t, judgmentsOf(p, NFriendDelivery))
	assert.Empty(t, p.Updates)
	w.s.Now = t0.Add(60 * time.Minute)
	p = w.part(TickFriendDelivery, req)
	assert.Empty(t, judgmentsOf(p, NFriendDelivery))
	require.Len(t, p.Updates, 1)
	assert.Equal(t, 1, p.Updates[0].Before)
	assert.Contains(t, p.Updates[0].What, "for 1h0m0s")
	require.Len(t, pushesOf(p), 1)
	assert.Equal(t, "2", e.get(w.s, epRaised))
	require.Len(t, openOf(w, NFriendDelivery, amy), 1, "one judgment an episode")

	// a report of hers arrives (a HOLD writes reported on the card): the judgment closes
	// and the clock is cleared
	w.s.Now = t0.Add(61 * time.Minute)
	w.s.Fleet.Card("s1-1.w1").Fields[FieldReported] = stamp(w.s.Now)
	p = w.part(TickFriendDelivery, req)
	require.Len(t, p.Closes, 1)
	assert.Equal(t, NFriendDelivery, p.Closes[0].Note.Type)
	assert.Empty(t, w.openOn(amy))
	assert.False(t, e.any(w.s))

	// from that report the window counts again
	w.s.Now = t0.Add(90 * time.Minute)
	p = w.part(TickFriendDelivery, req)
	assert.Empty(t, judgmentsOf(p, NFriendDelivery))
	w.s.Now = t0.Add(91 * time.Minute)
	p = w.part(TickFriendDelivery, req)
	js = judgmentsOf(p, NFriendDelivery)
	require.Len(t, js, 1)
	assert.Contains(t, js[0].What, "her last report at "+stamp(t0.Add(61*time.Minute)))
}

func TestAFriendWithNoCardWorkingOrNotUpIsNotJudgedByDelivery(t *testing.T) {
	t.Parallel()
	w, seats := deliveryWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendDeliveryPrefix, "amy"}

	// the store's record of her last finish counts as a delivery
	w.s.Now = t0.Add(40 * time.Minute)
	finished := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash", Finished: t0.Add(20 * time.Minute)}}
	p := w.part(TickFriendDelivery, TickReq{Friends: finished})
	assert.Empty(t, judgmentsOf(p, NFriendDelivery), "a finish 20 minutes old is a delivery inside the window")

	// judged at the window
	p = w.part(TickFriendDelivery, TickReq{Friends: seats})
	require.Len(t, judgmentsOf(p, NFriendDelivery), 1)

	// she is not up: the episode ends
	down := []FriendSeat{{Name: "amy", Width: 2, Status: Down, Class: "flash"}}
	p = w.part(TickFriendDelivery, TickReq{Friends: down})
	assert.Len(t, p.Closes, 1)
	assert.False(t, e.any(w.s))
	assert.Empty(t, w.openOn(amy))

	// her working cards finish: no card working, not judged however long ago she delivered
	w.s.Now = t0.Add(2 * time.Hour)
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		w.place(w.s.Fleet, id, amy, DoneOK)
	}
	p = w.part(TickFriendDelivery, TickReq{Friends: seats})
	assert.Empty(t, judgmentsOf(p, NFriendDelivery))
	assert.False(t, e.any(w.s))

	// the window is a setting: its own, else the friend-finish window
	w.must(WithFriendFinish(Set(w.s, SetReq{Who: "coordinator"}), w.s, "45m"))
	assert.Equal(t, 45*time.Minute, w.s.DeliveryWindow(), "the friend-finish window when none of its own is set")
	p = w.must(Set(w.s, SetReq{DeliveryWindow: "20m", Who: "coordinator"}))
	assert.Contains(t, movedLines(p)[0], "delivery-window 20m")
	assert.Equal(t, 20*time.Minute, w.s.DeliveryWindow())
	w.must(Set(w.s, SetReq{DeliveryWindow: ReadTierDefault, Who: "coordinator"}))
	assert.Equal(t, 45*time.Minute, w.s.DeliveryWindow())
	refused := Set(w.s, SetReq{DeliveryWindow: "-1m", Who: "coordinator"})
	require.Len(t, refused.Refused, 1)
	assert.Contains(t, refused.Refused[0].Why, "--delivery-window wants a duration above zero")
}

func TestTheFriendDeliveryRulePingsOnTheFirstWindowAndMarksHerDownOnTheSecond(t *testing.T) {
	t.Parallel()
	w, seats := deliveryWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendDeliveryPrefix, "amy"}
	var pings []string
	ping := func(friend, subject, body string) error {
		pings = append(pings, friend+": "+subject+" | "+body)
		return nil
	}
	req := TickReq{Friends: seats, AnswerRules: true, PingFriend: ping}

	// the first window: pinged once, with her working card ids; the judgment stays
	w.s.Now = t0.Add(30 * time.Minute)
	w.part(TickFriendDelivery, req)
	answers := RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, RuleFriendDelivery, answers[0].Rule)
	assert.Equal(t, ActPing, answers[0].Act)
	p := w.part(TickRuleFriendPing, req)
	require.Len(t, pings, 1)
	assert.Contains(t, pings[0], "amy: no delivery: friend amy has 2 lanes busy and has reported nothing for 30m0s")
	assert.Contains(t, pings[0], "s1-1.w1, s1-2.w1")
	require.Len(t, p.Units, 1)
	assert.Equal(t, "rule friend-delivery: pinged amy with 2 cards (s1-1.w1, s1-2.w1)", p.Units[0].Moved)
	assert.Equal(t, stamp(w.s.Now), e.get(w.s, epPinged))
	p = w.part(TickRuleFriendDown, req)
	assert.Nil(t, p.Health, "not down on the first window")
	require.Len(t, openOf(w, NFriendDelivery, amy), 1)

	// between the windows: pinged already, down not yet: a mind's
	w.s.Now = t0.Add(45 * time.Minute)
	answers = RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, ActLeft, answers[0].Act)
	assert.Contains(t, answers[0].Why, "down by rule after the second window")
	w.part(TickRuleFriendPing, req)
	assert.Len(t, pings, 1, "pinged once an episode")

	// the second window: marked down with the reason until a window on, her queued card
	// taken back and her started cards left with her, the judgment answered by rule
	w.s.Now = t0.Add(60 * time.Minute)
	w.part(TickFriendDelivery, req)
	answers = RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, ActFriendDown, answers[0].Act)
	p = w.part(TickRuleFriendDown, req)
	require.NotNil(t, p.Health)
	assert.Equal(t, "amy", p.Health.Friend)
	assert.Equal(t, Down, p.Health.Health.State)
	assert.Equal(t, "no delivery since the start of her work", p.Health.Health.Reason)
	assert.Equal(t, t0.Add(90*time.Minute), p.Health.Health.Until)
	assert.Equal(t, w.s.Now, p.Health.Health.Seen)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-3.w1").Col, "her queued card is taken back")
	assert.Equal(t, Ready, w.s.Work.Card("s1-3").Col, "its primary waits for the next deal")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "a started card stays with her and finishes")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Empty(t, openOf(w, NFriendDelivery, amy), "the judgment is answered by rule")
	assert.Equal(t, stamp(w.s.Now), e.get(w.s, epDown))
	var moved, decidedBy string
	for _, u := range p.Units {
		if u.Key == amy {
			moved = u.Moved
			for _, n := range u.Notes {
				if n.Kind == Decided {
					decidedBy = n.What
				}
			}
		}
	}
	assert.Contains(t, moved, "rule friend-delivery: friend amy down: no delivery since the start of her work, until "+stamp(t0.Add(90*time.Minute)))
	assert.Contains(t, decidedBy, "answered by rule friend-delivery")

	// once an episode: nothing more by rule while she stays as she is
	p = w.part(TickRuleFriendDown, req)
	assert.Nil(t, p.Health)
	w.s.Now = t0.Add(90 * time.Minute)
	w.part(TickFriendDelivery, req)
	answers = RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, ActLeft, answers[0].Act)
	assert.Contains(t, answers[0].Why, "marked down by this rule at "+stamp(t0.Add(60*time.Minute)))
}
