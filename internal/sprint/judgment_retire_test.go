package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A judgment whose every card has left the table (landed, dropped, or dropped
// replaced by its twin) retires itself on the tick: closed, answered by the
// machine with the card's event, and gone from the inbox. One about a card
// still on the table, one with a card of it still on the table, and one about
// the landing itself stay open.
func TestAJudgmentRetiresWhenItsCardLeavesTheTable(t *testing.T) {
	t.Parallel()
	w := setup(t, 5)
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Equal(t, Landed, w.state("s1-1"), "s1-1 did not land")
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	w.must(Recut(w.s, RecutReq{ID: "s1-3", Tier: "heavy", Who: "coordinator"}))
	require.Nil(t, w.s.Work.Placed("s1-3"), "s1-3 was not replaced")
	w.tick(time.Hour)

	// the judgments the coordinator found today: each outlived its card
	noRoute := judgment(NNoRoute, TierSubject("heavy"), w.s.Now, 0, "s1-1")
	noRoute.StreamLevel, noRoute.What = true, "1 primaries of tier heavy wait: no route serves it"
	returned := judgment(NReturned, "s1", w.s.Now, 0, "s1-2")
	bound := judgment(NBound, "s1", w.s.Now, 0, "s1-3")
	// and the ones that stay
	stays := judgment(NReturned, "s1", w.s.Now, 0, "s1-4")
	both := judgment(NNoRoute, TierSubject("pro"), w.s.Now, 0, "s1-1", "s1-5")
	both.StreamLevel = true
	scored := judgment(NScoredLow, "s1", w.s.Now, 0, "s1-1")
	w.note(noRoute, returned, bound, stays, both, scored)
	ids := map[string]string{}
	for _, o := range w.s.Open {
		ids[o.Note.Type+"|"+o.Subject()] = o.Note.ID
	}
	gone := []string{ids[NNoRoute+"|"+StreamSubject(TierSubject("heavy"))], ids[NReturned+"|s1-2"], ids[NBound+"|s1-3"]}

	w.tick(time.Minute)
	w.part(TickCheck, TickReq{})
	open := map[string]bool{}
	for _, o := range w.s.Open {
		open[o.Note.ID] = true
	}
	for _, id := range gone {
		require.False(t, open[id], "judgment %s outlived its card: %+v", id, w.s.Open)
	}
	require.True(t, open[ids[NReturned+"|s1-4"]], "a judgment on a card on the table retired: %+v", w.s.Open)
	require.True(t, open[ids[NNoRoute+"|"+StreamSubject(TierSubject("pro"))]], "a judgment with a card still on the table retired: %+v", w.s.Open)
	require.True(t, open[ids[NScoredLow+"|s1-1"]], "a judgment about the landing retired: %+v", w.s.Open)

	twin := ""
	for _, c := range w.s.Work.Cards() {
		if c.F(FieldReplaces) == "s1-3" {
			twin = c.ID
		}
	}
	require.NotEmpty(t, twin, "no twin replaces s1-3")
	// each retirement is logged: answered by the machine, with the card's event
	want := map[string]string{
		gone[0]: "retired with its card: s1-1 landed",
		gone[1]: "retired with its card: s1-2 dropped (obsolete)",
		gone[2]: "retired with its card: s1-3 replaced by " + twin,
	}
	got := map[string]Note{}
	for _, n := range w.notes {
		if n.Kind == Decided {
			got[n.Answers] = n
		}
	}
	for id, what := range want {
		d, ok := got[id]
		require.True(t, ok, "no decided note answers %s: %+v", id, w.notes)
		require.Equal(t, MachineActor, d.Who, "who answered %s: %+v", id, d)
		require.Equal(t, what, d.What, "the event %s retired with: %+v", id, d)
	}

	// the inbox never shows them
	for _, g := range Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open}) {
		for _, id := range gone {
			require.NotContains(t, g.Notes, id, "the inbox shows a judgment about a card off the table: %+v", g)
		}
	}

	// a second tick finds nothing more to retire
	p, _ := TickCheck(w.s, TickReq{})
	require.Empty(t, p.Closes, "retired twice: %+v", p.Closes)
}
