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

// A card read on the tick's card facts is the card read from the whole table
// (CardOf), for every card on it (a sentinel and the cards behind it, a card
// with needs, a card dealt and worked), reading no table whole; and its log is
// the whole log's lines about it, in order, read from the index and the tail.
// A move after the tick (a revision the facts are not of) is read as before.
func TestCardReadIsCardOfFromItsOwnRows(t *testing.T) {
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
	check := func(fresh bool) {
		t.Helper()
		for _, id := range ids {
			before := maps.Clone(h.m.Calls)
			got, err := st.CardRead(h.ctx, id)
			require.NoError(t, err)
			if fresh {
				assert.Equal(t, before["cells"], h.m.Calls["cells"], "card %s: no table read whole", id)
			}
			want, err := st.CardOf(h.ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, got, "card %s", id)
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
	check(true)
	gate, err := st.CardRead(h.ctx, "gate")
	require.NoError(t, err)
	assert.NotEmpty(t, gate.NeededBy, "the cards behind the sentinel")
	n, err := st.CardRead(h.ctx, "n2")
	require.NoError(t, err)
	assert.Len(t, n.Needs, 2)

	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"late"}, Needs: []string{"gate"}, Brief: proBrief}))
	ids = append(ids, "late")
	whole, err = st.Log(h.ctx)
	require.NoError(t, err)
	check(false)
}
