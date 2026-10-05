package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAGroupReworkGivesEachCardItsOwnFinding pins a group rework (2026-10-05):
// two cards a friend reader found broken, each with its own finding, are
// reworked in one step. With no --fix each card's fix is the finding of the
// read that put it in the group, never the group's first; a --fix given once
// is every card's fix as written; and each card carries only its own finding
// to its next attempt. A friend's read leaves no readers-table row: its
// finding is on its judgment (FriendReadClose), which the rework reads.
func TestAGroupReworkGivesEachCardItsOwnFinding(t *testing.T) {
	t.Parallel()
	const (
		one = "internal/up/plan.go:12 setup-nova-up-localb leaves the docs catalogue unrowed"
		two = "internal/units/units.go:40 uncatalogued internal/units"
		fix = "the coordinator's fix, for every card"
	)
	brokenPair := func(t *testing.T) *world {
		t.Helper()
		w := newWorld(t, "reader-a")
		for _, id := range []string{"s1-1", "s1-2"} {
			putReview(w, id, id+": read this (s1) tier: frontier\n\nthe body\n", 2, 1, "head-"+id)
		}
		askReaders(t, w, []FriendSeat{frontierSeat("amy", 2, Up, t.TempDir())})
		w.must(FriendReadClose(w.s, "amy", "s1-1", "Verdict: HOLD\n"+one+"\n"))
		w.must(FriendReadClose(w.s, "amy", "s1-2", "Verdict: HOLD\n"+two+"\n"))
		require.Len(t, w.notesOf(NReadBroken), 2)
		return w
	}
	group := Sel{Only: []string{"s1-1", "s1-2"}}

	t.Run("each takes its own finding", func(t *testing.T) {
		t.Parallel()
		w := brokenPair(t)
		w.must(Rework(w.s, ReworkReq{Sel: group, Who: "coordinator"}))
		for id, own := range map[string]string{"s1-1": one, "s1-2": two} {
			c := w.s.Work.Card(id)
			require.Equal(t, Ready, c.Col, id)
			require.Equal(t, own, c.F("fix"), "%s: its fix is its own finding", id)
			require.Equal(t, own, c.F("finding"), "%s: its finding is its own", id)
		}
		require.NotContains(t, w.s.Work.Card("s1-2").F("fix"), "setup-nova-up-localb")
		require.NotContains(t, w.s.Work.Card("s1-1").F("fix"), "internal/units")
	})

	t.Run("a fix given once is every card's as written", func(t *testing.T) {
		t.Parallel()
		w := brokenPair(t)
		w.must(Rework(w.s, ReworkReq{Sel: group, Fix: fix, Who: "coordinator"}))
		for id, own := range map[string]string{"s1-1": one, "s1-2": two} {
			c := w.s.Work.Card(id)
			require.Equal(t, fix, c.F("fix"), id)
			require.Equal(t, own, c.F("finding"), "%s: the next attempt carries only its own finding", id)
		}
	})
}
