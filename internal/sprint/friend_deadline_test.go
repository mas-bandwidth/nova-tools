package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lateCards is the work cards the tick's deadline part names late now.
func lateCards(w *world) []string {
	p, _ := TickDeadlines(w.s, TickReq{})
	var out []string
	for _, n := range p.Notes {
		if n.Type == NWorkLate {
			out = append(out, n.Card)
		}
	}
	return out
}

// A friend's card's working deadline is the larger of DeadlineUnfinished and
// FriendDeadlineK times her median run wall over her last ok attempts (her take to her
// report), set on the card as she takes it, as nova-tools#5300 gives a machine's card its
// member's when it is dealt.
func TestAFriendsCardsDeadlineIsThreeTimesHerMedianWall(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 3, Status: Up, Class: "flash,pro"})
	startCards(w, "amy", "s1-1.w1", "s1-2.w1", "s1-3.w1")
	amy := FriendRow("amy")
	_, n := FriendMedianWall(w.s, "amy")
	require.Zero(t, n)
	assert.Empty(t, w.s.Fleet.Card("s1-1.w1").F(FieldFriendDeadline), "no ok attempt yet: the fleet's")
	assert.Equal(t, DeadlineUnfinished, unfinishedLimit(w.s.Fleet.Card("s1-1.w1")))

	// she reports s1-1 after 60 minutes, s1-2 after 70, s1-3 after 80, each collected ten
	// minutes later: her median run wall is 70 minutes, never the collection's
	t0 := w.s.Now
	for i, at := range []time.Duration{60 * time.Minute, 70 * time.Minute, 80 * time.Minute} {
		id := WorkCardID("s1-"+itoa(i+1), 1)
		reported := t0.Add(at)
		w.s.Now = reported.Add(10 * time.Minute)
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{id}}, As: amy, Gens: gensOf(w.s, id), Head: "abc", Reported: reported}))
	}
	median, n := FriendMedianWall(w.s, "amy")
	assert.Equal(t, 3, n)
	assert.Equal(t, 4200.0, median)

	// s1-4, ready behind them, was taken by her first finish (at 70 minutes, with no ok
	// attempt before it: the fleet's); taken back and dealt to her again, it takes hers
	wc := w.s.Fleet.Card("s1-4.w1")
	require.Equal(t, Working, wc.Col)
	assert.Equal(t, DeadlineUnfinished, unfinishedLimit(wc))
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-4"}, Hold: true}))
	dealWith(w, FriendSeat{Name: "amy", Width: 3, Status: Up, Class: "flash,pro"})
	wc = w.s.Fleet.Card("s1-4.w1")
	require.Equal(t, Ready, wc.Col, "dealt again, not started")
	assert.Empty(t, wc.F(FieldFriendDeadline), "the deadline waits for the start")
	startCards(w, "amy", "s1-4.w1")
	wc = w.s.Fleet.Card("s1-4.w1")
	require.Equal(t, Working, wc.Col)
	assert.Equal(t, "12600", wc.F(FieldFriendDeadline), "three times 70 minutes, stamped at the start")
	t1 := w.s.Now
	w.s.Now = t1.Add(2*time.Hour + time.Minute)
	assert.Empty(t, lateCards(w), "past two hours it is not late")
	w.s.Now = t1.Add(3*time.Hour + 31*time.Minute)
	assert.Equal(t, []string{"s1-4.w1"}, lateCards(w), "past three and a half it is")
}
