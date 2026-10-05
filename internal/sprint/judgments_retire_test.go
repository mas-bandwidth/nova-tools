package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judgment whose every card has landed, been dropped or been replaced retires on the
// tick: the tick end's check closes it, answered by the machine with the card's event, and
// the inbox never shows it. A judgment about the landed work itself (a low score) stays,
// and so does one with a card still on the table (docs/SPEC-SPRINT.md, "A judgment
// retires with its card"). The two the coordinator found on 2026-10-05: "no route serves
// the tier" on a card that had landed, "returned to review" on a card already dropped.
func TestAJudgmentRetiresWhenItsCardLeavesTheTable(t *testing.T) {
	t.Parallel()
	w := setup(t, 5)
	landedCards(w, "s1-1")
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-3"}}, Reason: "replaced by s1-3b"}))
	// judgments the step that moved each card left open, as a tick written on the read
	// before it leaves them
	open := func(typ string, ids ...string) string {
		w.note(Note{Kind: Judgment, Type: typ, Stream: "s1", Primaries: ids, Count: len(ids), At: w.s.Now,
			Decisions: append([]string(nil), TickDecisions[typ]...)})
		return w.notes[len(w.notes)-1].ID
	}
	noRoute := open(NNoRoute, "s1-1")
	returned := open(NReturned, "s1-2")
	stalled := open(NStalled, "s1-3")
	both := open(NNoRoute, "s1-2", "s1-4") // s1-4 is still on the table
	low := open(NScoredLow, "s1-1")        // about the landed work: the coordinator's
	w.tick(time.Second)

	p := w.part(TickCheck, TickReq{})
	retired := map[string]Note{}
	for _, n := range p.Notes {
		if n.Kind == Decided {
			retired[n.Answers] = n
		}
	}
	require.Len(t, retired, 3, "retired: %+v", retired)
	for id, event := range map[string]string{noRoute: "s1-1 landed", returned: "s1-2 dropped (obsolete)", stalled: "s1-3 replaced by s1-3b"} {
		d, ok := retired[id]
		require.True(t, ok, "%s not retired: %+v", id, retired)
		assert.Equal(t, MachineActor, d.Who, "%s answered by %q", id, d.Who)
		assert.Equal(t, NRetired+": "+event, d.What, "%s", id)
	}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		for _, o := range w.openOn(id) {
			assert.Contains(t, []string{low, both}, o.Note.ID, "a judgment about %s is still open: %+v", id, o.Note)
		}
	}
	assert.Len(t, w.openOn("s1-1"), 1, "the landed score's judgment is the coordinator's")
	assert.Len(t, w.openOn("s1-4"), 1, "a judgment with a card on the table stays")
	for _, g := range Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open}) {
		for _, id := range []string{noRoute, returned, stalled} {
			assert.NotContains(t, g.Notes, id, "the inbox shows a retired judgment: %+v", g)
		}
	}

	// the next tick finds nothing to retire
	p, due := TickCheck(w.s, TickReq{})
	assert.Zero(t, due)
	assert.Empty(t, p.Closes, "retired twice")
	for _, n := range p.Notes {
		assert.NotEqual(t, Decided, n.Kind, "retired twice: %+v", n)
	}
}
