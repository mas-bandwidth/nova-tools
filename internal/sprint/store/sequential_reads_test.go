package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Sequential reads (docs/SPEC-SPRINT.md section 6; readers.go ReadsWanted). The night of
// 2026-10-03 asked 3,513 reads for 844 landings, 4.2 per landing against a design of 2: a
// pro card's two reads were asked together, so when the first reader found it broken the
// second read was spent on work already sent back. Now the first read is asked alone and
// the second only after the first comes back ok; a broken first read goes straight to
// its judgment and the rework, with no second read.

// readsOf is the primary's reads at its current attempt, by reader, in reader row order.
func (h *harness) readsOf(id string) []*sprint.Card {
	h.t.Helper()
	return h.snap().Readers.Of(id)
}

// readOne has the reader of the one read card read it with the verdict.
func (h *harness) readOne(rc *sprint.Card, verdict, finding string) {
	h.t.Helper()
	h.must(ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: verdict, Finding: finding, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
}

// A pro card whose first read finds attempt 1 broken: attempt 1 costs one read (not two),
// and attempt 2 costs two, one at a time; three reads per landing where the pair cost four.
func TestAProCardsReadsAreAskedOneAtATime(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"), route("pro-b", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"}) // on pro, as the machine's escalation leaves it
	h.must(DealStep(sprint.DealReq{}))
	asked := 0
	ask := func(when string, want int) {
		h.t.Helper()
		before := len(h.readsOf("s1-1"))
		h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		got := len(h.readsOf("s1-1")) - before
		assert.Equal(t, want, got, "%s: reads asked", when)
		asked += got
	}

	// attempt 1: the first read alone; broken, and no second read is asked
	h.finishAttempt("s1-1", false, "h1")
	ask("attempt 1, the first read", 1)
	first := h.readsOf("s1-1")[0]
	h.readOne(first, "broken", "internal/x.go:3: wrong")
	require.Len(t, h.openOf(sprint.NReadBroken), 1, "the broken first read is the judgment")
	ask("attempt 1 after the broken read", 0)
	assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col)

	// attempt 2: the first read, ok; then the second, of a different reader, ok; accepted
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	h.finishAttempt("s1-1", false, "h2")
	ask("attempt 2, the first read", 1)
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 1)
	ask("attempt 2 while the first read is outstanding", 0)
	h.readOne(reads[0], "ok", "")
	assert.Empty(t, h.openOf(sprint.NReadsExhausted), "one ok of two is not reads exhausted: the second read is the ask's")
	ask("attempt 2, the second read", 1)
	reads = h.readsOf("s1-1")
	require.Len(t, reads, 2)
	assert.NotEqual(t, reads[0].Row, reads[1].Row, "the second read is a different reader's")
	assert.Equal(t, sprint.OK, reads[0].Col)
	h.readOne(reads[1], "ok", "")
	require.Len(t, h.openOf(sprint.NReadyToAccept), 1)
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, sprint.Merging, h.snap().Work.Card("s1-1").Col)

	// the measure: reads asked per landing on a card that failed its first read once
	t.Logf("reads asked per landing: %d (the pair asked 4)", asked)
	assert.Equal(t, 3, asked, "reads asked for one landing with one broken first read")
	h.clean("a pro card read one read at a time")
}

// A flash card is read once, as before: one read, ok, accepted.
func TestAFlashCardIsStillReadOnce(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 1)
	h.readOne(reads[0], "ok", "")
	r := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, r.Refused, 1, "asked already: %+v", r)
	assert.Equal(t, "asked already", r.Refused[0].Why)
	require.Len(t, h.openOf(sprint.NReadyToAccept), 1)
	h.clean("a flash card read once")
}

// A pro card's first read taken back from a reader that went away is asked again, still
// alone; and ask on a card whose first read is outstanding says why it asks nothing.
func TestTheFirstReadTakenBackIsAskedAgainAlone(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 1)
	r := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, r.Refused, 1)
	assert.Equal(t, "asked already: its reads are asked one at a time, and the next is asked when the one outstanding comes back ok", r.Refused[0].Why)
	require.NoError(t, h.st.SetReaderAway(h.ctx, reads[0].Row, true, "tester"))
	h.beat()
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	again := h.readsOf("s1-1")
	require.Len(t, again, 1, "the read taken back is asked of one other reader, alone")
	assert.NotEqual(t, reads[0].Row, again[0].Row)
	h.clean("the first read asked again")
}

// readersRead names the tiers on every reader row, as reader set --tiers does.
func (h *harness) readersRead(tiers string) {
	h.t.Helper()
	require.NoError(h.t, h.st.EnsureReaderTiers(h.ctx))
	rows, err := h.st.ReaderRows(h.ctx)
	require.NoError(h.t, err)
	for _, rd := range rows {
		require.NoError(h.t, h.m.RowSet(h.ctx, h.st.Names.Table(sprint.Readers), rd, map[string]string{sprint.ReaderTiers: tiers}))
	}
}
