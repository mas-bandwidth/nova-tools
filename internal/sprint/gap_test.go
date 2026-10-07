package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Behaviours the 2026-10-02 break-it probes found no unit test for: each test
// here goes red when its line is broken.

// A group's before is the most times before of any of its notes, whichever
// note comes first.
func TestAGroupsBeforeIsTheMostOfItsNotes(t *testing.T) {
	t.Parallel()
	now := t0.Add(time.Hour)
	for _, c := range []struct {
		name   string
		before []int
		want   int
	}{
		{"one note", []int{2}, 2},
		{"the most first", []int{3, 1}, 3},
		{"the most last", []int{1, 3}, 3},
		{"none before", []int{0, 0}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var open []Open
			for i, b := range c.before {
				id := "n" + string(rune('1'+i))
				n := Note{ID: id, Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0.Add(time.Duration(i) * time.Minute), Before: b}
				open = append(open, Open{Key: OpenKey(id, "p"+id), Note: n})
			}
			g := Inbox(InboxReq{Now: now, Open: open})
			require.Len(t, g, 1, "groups: %+v", g)
			require.Equal(t, c.want, g[0].Before, "groups: %+v", g)
		})
	}
}

// The sweep of 2026-10-02: ten more decision points of the package whose
// break left every unit test green.

// Merged notes keep the most times before of any of them, and their primaries
// in name order, whichever note came first.
func TestMergedNotesKeepTheMostBeforeAndTheirPrimariesInNameOrder(t *testing.T) {
	t.Parallel()
	out := MergeNotes([]Note{
		{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", Primaries: []string{"p3", "p2"}, Before: 3},
		{ID: "n2", Kind: Judgment, Type: NWorkFailed, Stream: "s1", Primaries: []string{"p1"}, Before: 1},
	})
	require.Len(t, out, 1, "merged: %+v", out)
	require.Equal(t, []string{"p1", "p2", "p3"}, out[0].Primaries, "merged: %+v", out)
	require.Equal(t, 3, out[0].Before, "merged: %+v", out)
}

// A set move's line names the first card's words and up to three more cards,
// then how many more there are.
func TestASetMovesLineNamesThreeMoreCardsAtMost(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cards int
		with  string
	}{
		{2, "(with c2)"},
		{4, "(with c2, c3, c4)"},
		{5, "(with c2, c3, c4 and 1 more)"},
		{6, "(with c2, c3, c4 and 2 more)"},
	} {
		var cards []string
		for i := range c.cards {
			cards = append(cards, "c"+string(rune('1'+i)))
		}
		l := Line{Table: Work, Card: "c1", Cards: cards, From: "s1:ready", To: "s1:ready", Actor: "coordinator", Set: map[string]string{"score": "2"}}
		got := Render(l)
		require.True(t, strings.HasSuffix(got, " "+c.with), "%d cards: %q, want it to end %q", c.cards, got, c.with)
		require.True(t, strings.HasPrefix(got, string(rune('0'+c.cards))+" cards: "), "%d cards: %q", c.cards, got)
	}
}

// A change's fields are named in name order, however the change holds them.
func TestAChangesFieldsAreNamedInNameOrder(t *testing.T) {
	t.Parallel()
	for range 32 {
		l := Line{Table: Work, Card: "s1-1", From: "s1:ready", To: "s1:ready", Actor: "coordinator",
			Set: map[string]string{"score": "2", "needs": "x", "attempt": "3", "branch": "b", "head": "h"}}
		require.Equal(t, "s1-1 changed by coordinator: attempt=3, branch=b, head=h, needs=x, score=2", Render(l))
	}
}

// A member past its width (its width lowered under its working cards) has no
// room: a take by count takes nothing and a take by id is refused, as at its
// width.
func TestAMemberPastItsWidthTakesNothing(t *testing.T) {
	t.Parallel()
	for _, working := range []int{2, 3} {
		w := fleetWorld(t, 0, 2, "m1")
		for i := range working {
			putWorkCard(w, "x"+string(rune('0'+i)), "m1", Working, float64(i+1), nil)
		}
		putWorkCard(w, "r0", "m1", Ready, 10, nil)
		putWorkCard(w, "r1", "m1", Ready, 11, nil)
		p := Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}})
		require.Empty(t, p.Units, "%d working of 2, a take by count: %+v", working, p)
		require.Empty(t, p.Refused, "%d working of 2, a take by count: %+v", working, p)
		p = Take(w.s, TakeReq{As: "m1", Sel: Sel{IDs: []string{"r0.w1"}}, Gens: gensOf(w.s, "r0.w1")})
		require.Empty(t, p.Units, "%d working of 2, a take by id: %+v", working, p)
		require.Len(t, p.Refused, 1, "%d working of 2, a take by id: %+v", working, p)
		require.Contains(t, p.Refused[0].Why, "is at its width", "%d working of 2, a take by id: %+v", working, p)
	}
}

// A member holding past DealAhead times its width takes away none of another
// member's room: the deal still fills the other.
func TestAMemberPastItsRoomTakesNoneOfAnothersRoom(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 10, 2, "m1", "m2")
	for i := range DealAhead*2 + 3 {
		col := Ready
		if i < 2 {
			col = Working
		}
		putWorkCard(w, "x"+string(rune('a'+i)), "m1", col, float64(100+i), nil)
	}
	p, due := TickDeal(w.s, TickReq{})
	require.Len(t, p.Units, DealAhead*2, "dealt %d (due %d), want m2's room", len(p.Units), due)
	require.Zero(t, due, "dealt %d (due %d), want m2's room", len(p.Units), due)
}

// A group's primaries and notes are in name order, whatever order the open
// judgments come in.
func TestAGroupsPrimariesAndNotesAreInNameOrder(t *testing.T) {
	t.Parallel()
	a := Note{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0}
	b := Note{ID: "n2", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0.Add(time.Minute)}
	open := []Open{{Key: OpenKey("n2", "p2"), Note: b}, {Key: OpenKey("n1", "p1"), Note: a}}
	g := Inbox(InboxReq{Now: t0.Add(time.Hour), Open: open})
	require.Len(t, g, 1, "groups: %+v", g)
	require.Equal(t, []string{"p1", "p2"}, g[0].Primaries, "groups: %+v", g)
	require.Equal(t, []string{"n1", "n2"}, g[0].Notes, "groups: %+v", g)
}

// The people are kept in name order.
func TestGoalsSortKeepsThePeopleInNameOrder(t *testing.T) {
	t.Parallel()
	g := Goals{People: []Goal{{Name: "carol"}, {Name: "alice"}, {Name: "bob"}}}
	g.Sort()
	var names []string
	for _, p := range g.People {
		names = append(names, p.Name)
	}
	require.Equal(t, []string{"alice", "bob", "carol"}, names)
}

// The readers' text names them in name order, whatever the table's row order.
func TestTheReadersTextIsInNameOrder(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-c", "reader-a", "reader-b")
	w.s.ReaderStates = map[string]string{"reader-c": ReaderUp, "reader-a": ReaderDown, "reader-b": ReaderUp}
	require.Equal(t, "reader-a down, reader-b up, reader-c up", readersText(w.s))
}
