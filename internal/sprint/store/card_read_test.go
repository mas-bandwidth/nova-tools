package store

import (
	"maps"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card log index finds a line under a card's id exactly when the line is
// about the card (sprint.Line.About): its own move lines, its work and read
// cards' (<primary>.w<n>, <primary>.r<n>.<reader>; every dot-prefix of a name,
// as About reads one, so "a.w2.w1" is found under "a" and "a.w2"), a move line
// by its primary, and a notification by its subjects and the cards it names.
func TestLogKeysFindExactlyTheLinesAboutACard(t *testing.T) {
	t.Parallel()
	lines := []sprint.Line{
		{Kind: sprint.LineMove, Card: "a.w2", Primary: "a.w2", Table: sprint.Work},
		{Kind: sprint.LineMove, Card: "a.w2.w1", Primary: "a.w2", Table: sprint.Fleet},
		{Kind: sprint.LineMove, Card: "a.w2.r1.reader-a", Primary: "a.w2", Table: sprint.Readers},
		{Kind: sprint.LineMove, Card: "s1-1", Cards: []string{"s1-1", "s1-2", "s1-10"}, Table: sprint.Work},
		{Kind: sprint.LineMove, Card: "x.w1", Primary: "s1-2", Table: sprint.Fleet},
		{Kind: sprint.Judgment, Note: &sprint.Note{ID: "n1", Kind: sprint.Judgment, Type: sprint.NStalled, Card: "s1-1.w1", Other: "a.w2"}},
		{Kind: sprint.Happened, Note: &sprint.Note{ID: "n2", Kind: sprint.Happened, Primaries: []string{"s1-10"}, Card: "a"}},
		{Kind: sprint.Happened, Note: &sprint.Note{ID: "n3", Kind: sprint.Happened, Stream: "s1"}, Primary: "s1-1"},
	}
	ids := []string{"a", "a.w2", "a.w2.w1", "s1-1", "s1-2", "s1-10", "s1", "x", "x.w1", "stream:s1"}
	for i, l := range lines {
		keys := logKeys(l)
		for _, id := range ids {
			assert.Equal(t, l.About(id), slices.Contains(keys, id), "line %d (%v) under %q: About and the index disagree", i, keys, id)
		}
	}
}

// A card's log (CardLog) is the whole log's lines about it, in order, read from
// the card log index and the tail the tick has not indexed, for every card (a
// sentinel and the cards behind it, a card with needs, a card dealt and
// worked), never the log from its start once a tick has indexed it; a card
// added after the tick is told from the tail. Its hold, needs and place come
// from the one table read CardHeld makes, as Held tells them.
func TestCardLogIsTheWholeLogsLinesAboutIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(6)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 3, Brief: proBrief}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"n2"}, Needs: []string{"s1-1", "s2-1"}, Brief: proBrief}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	snap := h.snap()
	ids := []string{"n2"}
	for _, c := range snap.Work.Column(sprint.States...) {
		ids = append(ids, c.ID)
	}
	st, err := h.st.pin(h.ctx)
	require.NoError(t, err)
	whole, err := st.Log(h.ctx)
	require.NoError(t, err)
	check := func() {
		t.Helper()
		for _, id := range ids {
			before := maps.Clone(h.m.Calls)
			v, err := st.CardHeld(h.ctx, id)
			require.NoError(t, err)
			if v.Primary != nil { // a card not on the table has no hold to read
				assert.Equal(t, 1, h.m.Calls["cells"]-before["cells"], "card %s: one table read for its card, needs, place and hold", id)
				hd, err := st.Held(h.ctx, id)
				require.NoError(t, err)
				require.NotNil(t, v.Hold, "card %s: a hold", id)
				assert.Equal(t, hd, *v.Hold, "card %s: the one read holds as Held does", id)
			}
			lines, err := st.CardLog(h.ctx, id)
			require.NoError(t, err)
			var about []sprint.Line
			for _, l := range whole {
				if l.About(id) {
					about = append(about, l)
				}
			}
			assert.Equal(t, about, lines, "card %s: its lines of the log, in order", id)
		}
	}
	check()
	gate, err := st.CardHeld(h.ctx, "gate")
	require.NoError(t, err)
	assert.NotEmpty(t, gate.NeededBy, "the cards behind the sentinel")
	n, err := st.CardHeld(h.ctx, "n2")
	require.NoError(t, err)
	assert.Len(t, n.Needs, 2)

	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"late"}, Needs: []string{"gate"}, Brief: proBrief}))
	ids = append(ids, "late")
	whole, err = st.Log(h.ctx)
	require.NoError(t, err)
	check()
}
