package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The reads of a card (docs/SPEC-SPRINT.md section 6; readers.go ReadsWanted). The night of
// 2026-10-03 asked 3,513 reads for 844 landings, 4.2 per landing against a design of 2, and
// the reads were then asked one at a time. The interim rule of 2026-10-06 (the owner, 6:02
// PM ET: "send out multiple consumer cards in ||") asks them together again: a pro card's
// two reads go out at once, a read outstanding counts toward the two, and only a broken
// read stops the rest.

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

// A pro card whose read finds attempt 1 broken: attempt 1 is asked both reads together,
// and the broken one stops any more; attempt 2 is asked both together, both ok, accepted.
// Four reads for the landing (reads are asked together, sprint.ReadsWanted).
func TestAProCardsReadsAreAskedTogether(t *testing.T) {
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

	// attempt 1: both reads together; one broken, and no other read is asked
	h.finishAttempt("s1-1", false, "h1")
	ask("attempt 1", 2)
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 2)
	assert.NotEqual(t, reads[0].Row, reads[1].Row, "two different readers")
	h.readOne(reads[0], "broken", "internal/x.go:3: wrong")
	require.Len(t, h.openOf(sprint.NReadBroken), 1, "the broken read is the judgment")
	ask("attempt 1 after the broken read", 0)
	assert.Equal(t, sprint.Review, h.snap().Work.Card("s1-1").Col)

	// attempt 2: both reads together, both ok; accepted
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	h.finishAttempt("s1-1", false, "h2")
	ask("attempt 2", 2)
	reads = h.readsOf("s1-1")
	require.Len(t, reads, 2)
	assert.NotEqual(t, reads[0].Row, reads[1].Row, "two different readers")
	ask("attempt 2 while both reads are outstanding", 0)
	h.readOne(reads[0], "ok", "")
	assert.Empty(t, h.openOf(sprint.NReadsExhausted), "one ok of two is not reads exhausted: the other is outstanding")
	ask("attempt 2 while the second read is outstanding", 0)
	h.readOne(reads[1], "ok", "")
	require.Empty(t, h.openOf(sprint.NReadyToAccept), "the tick's to accept, no judgment")
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, sprint.Merging, h.snap().Work.Card("s1-1").Col)

	// the measure: reads asked per landing on a card that failed a read once
	t.Logf("reads asked per landing: %d", asked)
	assert.Equal(t, 4, asked, "reads asked for one landing with one broken read")
	h.clean("a pro card's reads asked together")
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
	require.Empty(t, h.openOf(sprint.NReadyToAccept), "the tick's to accept, no judgment")
	h.clean("a flash card read once")
}

// A pro card's read taken back from a reader that went away is asked again of one other
// reader, the other read standing; and ask on a card whose reads are outstanding says it
// is asked already (reads are asked together, sprint.ReadsWanted).
func TestAReadTakenBackIsAskedAgainOfOneOtherReader(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 2, "both reads asked together")
	r := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, r.Refused, 1)
	assert.Equal(t, "asked already", r.Refused[0].Why)
	require.NoError(t, h.st.SetReaderAway(h.ctx, reads[0].Row, true, "tester"))
	h.beat()
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	var again []string
	for _, rc := range h.readsOf("s1-1") {
		if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
			again = append(again, rc.Row)
		}
	}
	require.Len(t, again, 2, "the read taken back is asked of one other reader: %v", again)
	assert.NotContains(t, again, reads[0].Row)
	assert.Contains(t, again, reads[1].Row, "the other read stands")
	h.clean("a read taken back asked again")
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
