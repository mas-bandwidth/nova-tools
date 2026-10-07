package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friend-width check (docs/SPEC-SPRINT.md, section friend-width-and-delivery; the
// model is tla/FriendWidth.tla): a friend up with free lanes beside cards queued on her
// row for WidthIdleAfter is one judgment, raised again at most every WidthIdleAfter, her
// session pinged once by rule with the queued card ids, closed when her lanes fill or her
// queue empties, and from the third raise on naming her observed concurrency beside her
// configured width, which the machine never changes. Every test runs on a fake clock.

// widthWorld is a friend with four cards dealt on her row, one of which she has started:
// one lane working, three cards queued, under a width of four.
func laneWorld(t *testing.T) (*world, []FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 4, Status: Up, Class: "flash"}}
	dealWith(w, seats...)
	startLanes(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"})
	require.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Working))
	require.Equal(t, 3, w.s.Fleet.Count(FriendRow("amy"), Ready))
	return w, seats
}

// widthJudgments is the judgment notes of the type the plan wrote.
func judgmentsOf(p Plan, typ string) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Kind == Judgment && n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

// pushesOf is the raised-again pushes of the plan.
func pushesOf(p Plan) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Kind == Happened && n.Type == NRaisedAgain {
			out = append(out, n)
		}
	}
	return out
}

func TestAFriendWithFreeLanesBesideQueuedCardsIsJudgedAfterTheWindowAndRaisedAgainEachWindow(t *testing.T) {
	t.Parallel()
	w, seats := laneWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendWidthPrefix, "amy"}
	req := TickReq{Friends: seats}

	// first seen: the clock starts in the store, no judgment yet
	p := w.part(TickFriendWidth, req)
	assert.Empty(t, judgmentsOf(p, NFriendWidth))
	assert.Equal(t, stamp(t0), e.get(w.s, epSince), "the clock is a fleet property, so a server switch keeps it")
	assert.Equal(t, "1", e.get(w.s, epMax))
	require.Len(t, p.Units, 1)
	assert.Contains(t, p.Units[0].Moved, "3 free lanes beside 3 queued cards first seen")

	// under the window: nothing
	w.s.Now = t0.Add(4 * time.Minute)
	p = w.part(TickFriendWidth, req)
	assert.Empty(t, judgmentsOf(p, NFriendWidth))
	assert.Empty(t, w.openOn(amy))

	// the window: one judgment on her row, naming her lanes, her queue and how long
	w.s.Now = t0.Add(5 * time.Minute)
	p = w.part(TickFriendWidth, req)
	js := judgmentsOf(p, NFriendWidth)
	require.Len(t, js, 1)
	assert.Equal(t, []string{amy}, js[0].Primaries)
	assert.Contains(t, js[0].What, "friend amy has 3 free lanes and 3 queued cards for 5m0s")
	assert.Contains(t, js[0].What, "working 1 of width 4")
	assert.Contains(t, js[0].What, "s1-2.w1, s1-3.w1, s1-4.w1")
	assert.NotContains(t, js[0].What, "never ran more than", "the concurrency note waits for the third raise")
	assert.Equal(t, widthDecisions("amy"), js[0].Decisions)
	assert.Contains(t, strings.Join(js[0].Decisions, "\n"), "nova-friend ping --as <coordinator> --to amy --wake")
	assert.Contains(t, strings.Join(js[0].Decisions, "\n"), "friend take amy --all-unstarted")
	assert.Equal(t, "1", e.get(w.s, epRaised))
	assert.Equal(t, stamp(w.s.Now), e.get(w.s, epLast))
	require.Len(t, openOf(w, NFriendWidth, amy), 1)

	// not again within the window
	w.s.Now = t0.Add(9 * time.Minute)
	p = w.part(TickFriendWidth, req)
	assert.Empty(t, judgmentsOf(p, NFriendWidth))
	assert.Empty(t, p.Updates)
	assert.Empty(t, pushesOf(p))
	assert.Equal(t, "1", e.get(w.s, epRaised))

	// a window on: raised again in place, never a second judgment, with the push
	w.s.Now = t0.Add(10 * time.Minute)
	p = w.part(TickFriendWidth, req)
	assert.Empty(t, judgmentsOf(p, NFriendWidth), "raised again in place, never a second judgment")
	require.Len(t, p.Updates, 1)
	assert.Equal(t, 1, p.Updates[0].Before)
	assert.Contains(t, p.Updates[0].What, "for 10m0s")
	require.Len(t, pushesOf(p), 1)
	assert.Equal(t, "coordinator", pushesOf(p)[0].To)
	assert.Equal(t, "2", e.get(w.s, epRaised))
	require.Len(t, openOf(w, NFriendWidth, amy), 1, "one judgment an episode")

	// the third raise says her observed concurrency beside her configured width, and
	// the nova-config line that would set it; the machine changes no width itself
	w.s.Now = t0.Add(15 * time.Minute)
	p = w.part(TickFriendWidth, req)
	require.Len(t, p.Updates, 1)
	assert.Equal(t, 2, p.Updates[0].Before)
	assert.Contains(t, p.Updates[0].What, "over the last 15m0s she never ran more than 1 lanes; her width in nova-config is 4; nova-config friend set amy --width 1 if that is her real width")
	for _, u := range p.Units {
		assert.Empty(t, u.Changes, "the check changes no row: %s", u.Moved)
	}
	assert.Equal(t, 4, seats[0].Width)

	// her lanes fill: the judgment closes and the clock is cleared
	startLanes(w, seats...)
	require.Equal(t, 4, w.s.Fleet.Count(amy, Working))
	w.s.Now = t0.Add(16 * time.Minute)
	p = w.part(TickFriendWidth, req)
	require.Len(t, p.Closes, 1)
	assert.Equal(t, NFriendWidth, p.Closes[0].Note.Type)
	assert.Empty(t, w.openOn(amy))
	assert.False(t, e.any(w.s), "every property of the episode is cleared")
}

func TestTheFriendWidthClockRestartsWhenTheConditionBreaksAndTheMostLanesSheRanIsKept(t *testing.T) {
	t.Parallel()
	w, seats := laneWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendWidthPrefix, "amy"}
	req := TickReq{Friends: seats}
	w.part(TickFriendWidth, req)

	// she starts a second card at 2m: the most she ran is 2, the clock runs on
	w.s.Now = t0.Add(2 * time.Minute)
	startLanes(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"})
	require.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	p := w.part(TickFriendWidth, req)
	assert.Equal(t, "2", e.get(w.s, epMax))
	assert.Equal(t, stamp(t0), e.get(w.s, epSince), "the clock runs on while free lanes sit beside queued cards")
	assert.Contains(t, strings.Join(movedLines(p), "\n"), "ran 2 lanes")

	// her queue empties at 3m (she starts the rest, under a width of six): the clock is
	// cleared with no judgment
	w.s.Now = t0.Add(3 * time.Minute)
	wide := []FriendSeat{{Name: "amy", Width: 6, Status: Up, Class: "flash"}}
	startLanes(w, wide...)
	require.Equal(t, 4, w.s.Fleet.Count(amy, Working))
	require.Equal(t, 0, w.s.Fleet.Count(amy, Ready))
	p = w.part(TickFriendWidth, TickReq{Friends: wide})
	assert.False(t, e.any(w.s))
	assert.Empty(t, judgmentsOf(p, NFriendWidth))
	assert.Empty(t, p.Closes)

	// cards queued again at 4m: a new clock; at 8m, not 5m, the judgment
	w.s.Now = t0.Add(4 * time.Minute)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-5", Brief: friendBrief("friend amy")}, {ID: "s1-6", Brief: friendBrief("friend amy")}}}))
	dealWith(w, wide...)
	require.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
	w.part(TickFriendWidth, TickReq{Friends: wide})
	assert.Equal(t, stamp(w.s.Now), e.get(w.s, epSince))
	w.s.Now = t0.Add(8 * time.Minute)
	p = w.part(TickFriendWidth, TickReq{Friends: wide})
	assert.Empty(t, judgmentsOf(p, NFriendWidth))
	w.s.Now = t0.Add(9 * time.Minute)
	p = w.part(TickFriendWidth, TickReq{Friends: wide})
	assert.Len(t, judgmentsOf(p, NFriendWidth), 1)

	// a friend not up is not judged: the episode ends with her status
	w.s.Now = t0.Add(10 * time.Minute)
	down := []FriendSeat{{Name: "amy", Width: 6, Status: Down, Class: "flash"}}
	p = w.part(TickFriendWidth, TickReq{Friends: down})
	assert.Len(t, p.Closes, 1)
	assert.False(t, e.any(w.s))
}

func TestTheFriendWidthWindowIsASettingAndAnAckKeepsTheJudgmentQuiet(t *testing.T) {
	t.Parallel()
	w, seats := laneWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendWidthPrefix, "amy"}
	req := TickReq{Friends: seats}

	// set --width-idle-after 2m
	p := w.must(Set(w.s, SetReq{WidthIdleAfter: "2m", Who: "coordinator"}))
	assert.Contains(t, movedLines(p)[0], "width-idle-after 2m")
	assert.Equal(t, 2*time.Minute, w.s.WidthIdleAfter())
	refused := Set(w.s, SetReq{WidthIdleAfter: "soon", Who: "coordinator"})
	require.Len(t, refused.Refused, 1)
	assert.Contains(t, refused.Refused[0].Why, "--width-idle-after wants a duration above zero")
	refused = Set(w.s, SetReq{Streams: []string{"s1"}, WidthIdleAfter: "2m", Who: "coordinator"})
	require.Len(t, refused.Refused, 1)
	assert.Contains(t, refused.Refused[0].Why, "the sprint's, not a stream's")

	w.part(TickFriendWidth, req)
	w.s.Now = t0.Add(2 * time.Minute)
	p = w.part(TickFriendWidth, req)
	require.Len(t, judgmentsOf(p, NFriendWidth), 1, "judged at the setting's window")

	// an acknowledgement keeps it quiet until the episode ends
	j := openOf(w, NFriendWidth, amy)[0]
	p = w.must(Ack(w.s, AckReq{Notes: []string{j.Note.ID}, Reason: "seen"}))
	assert.Empty(t, openOf(w, NFriendWidth, amy))
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Kind == Acknowledged {
				w.s.Acked = append(w.s.Acked, Open{Key: OpenKey("ack-1", amy), Note: n}) // as the store keeps the hold
			}
		}
	}
	require.Len(t, w.s.Acked, 1)
	w.s.Now = t0.Add(6 * time.Minute)
	p = w.part(TickFriendWidth, req)
	assert.Empty(t, judgmentsOf(p, NFriendWidth), "acknowledged: not written again")
	assert.Empty(t, p.Updates)
	assert.Equal(t, "1", e.get(w.s, epRaised))

	// the episode ends: the acknowledgement is closed with it
	startLanes(w, seats...)
	w.s.Now = t0.Add(7 * time.Minute)
	p = w.part(TickFriendWidth, req)
	require.Len(t, p.Closes, 1)
	assert.Equal(t, Acknowledged, p.Closes[0].Note.Kind)
	assert.Equal(t, "default (5m0s)", orDefault(ReadTierDefault, PropWidthIdleAfter))
}

func TestTheFriendWidthRulePingsHerSessionOnceWithTheQueuedCardIds(t *testing.T) {
	t.Parallel()
	w, seats := laneWorld(t)
	amy := FriendRow("amy")
	e := episodeProps{friendWidthPrefix, "amy"}
	var pings []string
	ping := func(friend, subject, body string) error {
		pings = append(pings, friend+": "+subject+" | "+body)
		return nil
	}
	req := TickReq{Friends: seats, AnswerRules: true, PingFriend: ping}

	// no judgment yet: nothing to answer
	w.part(TickFriendWidth, req)
	p := w.part(TickRuleFriendPing, req)
	assert.Empty(t, p.Units)
	assert.Empty(t, pings)

	// the judgment: the rule pings her once, with the queued card ids in the body, and the
	// log says so
	w.s.Now = t0.Add(5 * time.Minute)
	w.part(TickFriendWidth, req)
	answers := RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, RuleFriendWidth, answers[0].Rule)
	assert.Equal(t, ActPing, answers[0].Act)
	assert.Equal(t, amy, answers[0].Subject)
	p = w.part(TickRuleFriendPing, req)
	require.Len(t, pings, 1)
	assert.Contains(t, pings[0], "amy: free lanes: friend amy has 3 queued cards and 1 lanes working")
	assert.Contains(t, pings[0], "s1-2.w1, s1-3.w1, s1-4.w1")
	require.Len(t, p.Units, 1)
	assert.Equal(t, "rule friend-width: pinged amy with 3 cards (s1-2.w1, s1-3.w1, s1-4.w1)", p.Units[0].Moved)
	assert.Equal(t, stamp(w.s.Now), e.get(w.s, epPinged))
	require.Len(t, openOf(w, NFriendWidth, amy), 1, "the judgment stays for the coordinator")

	// once an episode: the next ticks leave it to a mind, and the judgment says she was pinged
	p = w.part(TickRuleFriendPing, req)
	assert.Empty(t, p.Units)
	assert.Len(t, pings, 1)
	answers = RuleAnswers(w.s, req)
	require.Len(t, answers, 1)
	assert.Equal(t, ActLeft, answers[0].Act)
	assert.Contains(t, answers[0].Why, "her session was pinged at "+stamp(w.s.Now))
	w.s.Now = t0.Add(10 * time.Minute)
	p = w.part(TickFriendWidth, req)
	require.Len(t, p.Updates, 1)
	assert.Contains(t, p.Updates[0].What, "pinged by rule friend-width at "+stamp(t0.Add(5*time.Minute)))

	// with the rules off, nothing is pinged
	w2, seats2 := laneWorld(t)
	off := TickReq{Friends: seats2, PingFriend: ping}
	w2.part(TickFriendWidth, off)
	w2.s.Now = t0.Add(5 * time.Minute)
	w2.part(TickFriendWidth, off)
	w2.part(TickRuleFriendPing, off)
	assert.Len(t, pings, 1)
}
