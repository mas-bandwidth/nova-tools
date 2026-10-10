package sprint

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The attempt cap's default answer (docs/SPEC-SPRINT.md, the attempt cap; the
// tier ladder settled 2026-10-04: escalation is by attempt cap), the tick's part
// before the deal: past the cap the card is dealt as a friend card to a frontier or
// heavy-class friend up with room, and with no such friend up with room the card
// at its redeal bound is the cap's judgment for the coordinator, the findings of
// every attempt and the spend, decisions brief and drop.

// capWorld is a world with s1-1 a machine's card past the attempt cap: four
// attempts (the default cap) on the brief it was added with, ready at its
// redeal bound with its fourth work card withdrawn on a machine, every
// attempt's finding kept.
func capWorld(t *testing.T) *world {
	t.Helper()
	w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	pr := w.s.Work.Placed("s1-1")
	pr.Fields["attempt"] = "4"
	pr.Fields["findings"] = "attempt 1: one way\nattempt 2: another\nattempt 3: a third\nattempt 4: a fourth"
	w.s.Fleet.Put(&Card{ID: "s1-1.w4", Row: "m1", Col: Withdrawn, Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": "s1-1", "stream": "s1", "attempt": "4", "gen": "2", "member": "m1", "redeals": "3"}})
	_, atCap := AtBriefBound(pr, "", w.s.AttemptsCap("s1"))
	require.True(t, atCap, "the card is past the attempt cap")
	return w
}

// onHerRow deals n of the world's friend cards naming friend to her by the tick's friend
// deal (TickDeal's friendDeal) alone, so no other ready card moves: her load.
func onHerRow(w *world, friend string, n int) {
	w.t.Helper()
	var cards []*Card
	for i := 0; i < n; i++ {
		id := "s2-" + friend + "-" + itoa(i+1)
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{{ID: id, Brief: friendBrief("friend " + friend)}}}))
		cards = append(cards, w.s.Primary(id))
	}
	p, _, _ := friendDeal(w.s, cards, []FriendSeat{{Name: friend, Width: n, Status: Up, Class: "flash,pro"}}) // the tick's friend deal (TickDeal)
	w.must(p)
}

// capDeal is the pump's cap deal and then its deal with these friends, applied, as the
// tick runs them.
func capDeal(w *world, seats ...FriendSeat) {
	w.t.Helper()
	w.part(TickCapDeal, TickReq{Friends: seats})
	w.part(TickDeal, TickReq{Friends: seats})
}

// takeEndedAtTheBound has the withdrawn work card's take ended at the redeal bound: the
// deal places it on no machine again.
func takeEndedAtTheBound(w *world, wc string) {
	w.t.Helper()
	c := w.s.Fleet.Card(wc)
	require.NotNil(w.t, c)
	c.Fields[FieldTakeEnded], c.Fields["redeals"] = "2026-10-04T12:00:00Z", itoa(MaxRedeals)
	c.Rev++
	w.s.Fleet.Put(c)
}

// boundJudgments is the bound judgments open on the primary's work cards.
func boundJudgments(s *Snapshot, id string) []Open {
	var out []Open
	for _, o := range s.Open {
		if o.Note.Type == NBound && slices.Contains(o.Note.Primaries, id) {
			out = append(out, o)
		}
	}
	return out
}

func TestTheAttemptCapJudgmentsDefaultAnswerDealsAFriendCard(t *testing.T) {
	t.Parallel()
	// the chooser over friends of mixed classes, widths and statuses: the
	// frontier or heavy friend up with the most free width, the first by name
	// among equals; a wrong class, a friend down or held, and a friend with no
	// room count for nothing
	w := capWorld(t)
	onHerRow(w, "eve", 2)
	onHerRow(w, "fay", 3)
	seats := []FriendSeat{
		{Name: "amy", Width: 8, Status: Up, Class: cardhdr.RouteFlash},
		{Name: "bob", Width: 8, Status: Up, Class: cardhdr.RoutePro},
		{Name: "cat", Width: 8, Status: Down, Class: cardhdr.RouteFrontier},
		{Name: "dee", Width: 8, Status: Held, Class: cardhdr.RouteHeavy},
		{Name: "eve", Width: 2, Status: Up, Class: cardhdr.RouteHeavy},
		{Name: "fay", Width: 4, Status: Up, Class: cardhdr.RouteHeavy},
		{Name: "gus", Width: 8, Status: Up, Class: cardhdr.RouteFrontier},
		{Name: "hal", Width: 8, Status: Up, Class: cardhdr.RouteHeavy},
	}

	// the no-friend fallback: no frontier or heavy friend up with room, and the
	// card at its redeal bound is the cap's judgment for the coordinator
	w2 := capWorld(t)
	takeEndedAtTheBound(w2, "s1-1.w4")
	onHerRow(w2, "eve", 1)
	capDeal(w2,
		FriendSeat{Name: "amy", Width: 8, Status: Up, Class: cardhdr.RouteFlash},
		FriendSeat{Name: "cat", Width: 8, Status: Down, Class: cardhdr.RouteFrontier},
		FriendSeat{Name: "dee", Width: 8, Status: Held, Class: cardhdr.RouteHeavy},
		FriendSeat{Name: "eve", Width: 1, Status: Up, Class: cardhdr.RouteHeavy})
	assert.Equal(t, Ready, w2.s.StateOf("s1-1"), "the card is left for the coordinator")
	assert.Empty(t, w2.s.Primary("s1-1").F(FieldWho), "its brief gains no WHO line")
	open := boundJudgments(w2.s, "s1-1")
	require.Len(t, open, 1, "the cap's judgment, the default that stays")
	n := open[0].Note
	assert.Equal(t, Decisions[NBriefWrong], n.Decisions, "brief and drop, never rework")
	assert.Contains(t, n.What, "s1-1: brief defect after 4 attempts")
	assert.Contains(t, n.What, "findings: attempt 1: one way; attempt 2: another; attempt 3: a third; attempt 4: a fourth", "every attempt's finding listed")

	// the friend comes up: the default answer is dealt and the judgment closes
	capDeal(w2, FriendSeat{Name: "gus", Width: 8, Status: Up, Class: cardhdr.RouteFrontier})
	assert.Equal(t, Working, w2.s.StateOf("s1-1"), "past the cap the card is dealt as a friend card")
	assert.Empty(t, boundJudgments(w2.s, "s1-1"), "the judgment closed when the default answer was dealt")
	assert.Equal(t, FriendRow("gus"), w2.s.Primary("s1-1").F(FieldWho))

	// the default answer: a card past the cap is dealt to the friend chosen,
	// its brief gaining her WHO line and the cap count resetting as a replaced
	// brief does, its work and findings kept, and no machine dealt it
	capDeal(w, seats...)
	pr := w.s.Primary("s1-1")
	assert.Equal(t, FriendRow("gus"), pr.F(FieldWho), "its brief gains WHO: friend gus: the most free width, the first by name among equals (gus and hal have 8 each, fay 1): amy and bob are the wrong class, cat is down, dee is held, eve is full")
	assert.Equal(t, FriendRow("gus"), WhoOfBrief(pr.F("brief")), "the WHO line, where the brief edit reads it")
	assert.Equal(t, "4", pr.F(FieldBriefAttempt), "the cap count resets as a replaced brief does")
	assert.Equal(t, Working, pr.Col)
	assert.Equal(t, "s1-1.w5", pr.F("work"))
	wc := w.s.Fleet.Card("s1-1.w5")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("gus"), wc.Row)
	assert.Equal(t, Ready, wc.Col, "a friend's card is ready on her row until she starts it")
	assert.Equal(t, "5", wc.F("attempt"))
	assert.Equal(t, "1", wc.F("gen"))
	_, atCap := AtBriefBound(pr, "", w.s.AttemptsCap("s1"))
	assert.False(t, atCap, "the reset brief is under the cap again")
	assert.Equal(t, "attempt 1: one way\nattempt 2: another\nattempt 3: a third\nattempt 4: a fourth", pr.F(FieldFindings), "the card keeps its findings")
	assert.Nil(t, w.s.Fleet.Placed("s1-1.w4"), "the capped attempt's work card is retired, its record kept")
	assert.Equal(t, "friend", w.s.Fleet.Card("s1-1.w4").F("retired_by"), "the card keeps its work")
	assert.True(t, w.s.Fleet.HasRow(FriendRow("gus")), "her row is declared by the plan")
	assert.Zero(t, w.s.Fleet.Count("m1", Ready)+w.s.Fleet.Count("m2", Ready), "no machine was dealt the capped card")
	assert.Empty(t, Check(w.s, nil), "what is always true holds with the capped card dealt as a friend's card")

	// with no friend to give it, a capped card below its redeal bound is dealt again
	// to a machine, its attempt finished as before the cap's answer
	w3 := capWorld(t)
	capDeal(w3)
	assert.Equal(t, Working, w3.s.StateOf("s1-1"))
	assert.Equal(t, "s1-1.w4", w3.s.Primary("s1-1").F("work"), "the same attempt, dealt again")
	assert.Empty(t, w3.s.Primary("s1-1").F(FieldWho))
}

// pastCap makes a ready machine card past the attempt cap: four attempts on the
// brief it was added with, its fourth work card withdrawn.
func pastCap(w *world, id string) {
	w.t.Helper()
	pr := w.s.Work.Placed(id)
	pr.Fields["attempt"] = "4"
	pr.Fields[FieldFindings] = "attempt 1: one way\nattempt 2: another\nattempt 3: a third\nattempt 4: a fourth"
	w.s.Fleet.Put(&Card{ID: id + ".w4", Row: "m1", Col: Withdrawn, Score: pr.Score, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": id, "stream": pr.Row, "attempt": "4", "gen": "2", "member": "m1", "redeals": "3"}})
}

// Two capped cards and one frontier friend of width 1: the plan puts one of them
// on her and leaves the other ready. Her width is the working cards she holds,
// and a second working card past it is the defect.
func TestTwoCappedCardsDoNotExceedAFriendsWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t,
		"c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.",
		"c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe other task.")
	pastCap(w, "s1-1")
	pastCap(w, "s1-2")
	takeEndedAtTheBound(w, "s1-1.w4") // at the redeal bound, a card that does not fit her is dealt to no machine
	takeEndedAtTheBound(w, "s1-2.w4")
	capDeal(w, FriendSeat{Name: "gus", Width: 1, Status: Up, Class: cardhdr.RouteFrontier})
	n := w.s.Fleet.Count(FriendRow("gus"), Working) + w.s.Fleet.Count(FriendRow("gus"), Ready)
	require.Equalf(t, 1, n, "friend.gus holds %d cards; her width is 1", n)
	var dealt, waiting string
	for _, id := range []string{"s1-1", "s1-2"} {
		switch w.s.StateOf(id) {
		case Working:
			dealt = id
			assert.Equal(t, FriendRow("gus"), w.s.Primary(id).F(FieldWho))
			assert.Equal(t, FriendRow("gus"), w.s.Fleet.Card(id+".w5").Row)
		case Ready:
			waiting = id
			assert.Empty(t, w.s.Primary(id).F(FieldWho), "the card that does not fit gains no WHO line")
			assert.Nil(t, w.s.Fleet.Card(id+".w5"), "it is not a second working card")
		default:
			t.Errorf("%s is %s, want working or ready", id, w.s.StateOf(id))
		}
	}
	assert.NotEmpty(t, dealt, "one capped card is dealt to her")
	assert.NotEmpty(t, waiting, "the other stays ready")
	assert.Empty(t, Check(w.s, nil), "what is always true holds with one card left ready")
}
