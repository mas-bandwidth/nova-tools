package store

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The reads of a card (docs/SPEC-SPRINT.md section 6; read_cards.go readCardsWanted). The
// night of 2026-10-03 asked 3,513 reads for 844 landings, 4.2 per landing against a design
// of 2. Read cards go out together (the owner, 2026-10-06, 6:02 PM ET: "send out multiple
// consumer cards in ||"): a pro card's two read cards are cut at once, a read card placed
// counts toward the two, and only a broken read stops the rest.

// readsOf is the primary's read cards placed, in work order.
func (h *harness) readsOf(id string) []*sprint.Card {
	h.t.Helper()
	return placedReadsOf(h.snap(), id)
}

// readOne has the reader of the one read card read it with the verdict.
func (h *harness) readOne(rc *sprint.Card, verdict, finding string) {
	h.t.Helper()
	h.must(ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: verdict, Finding: finding, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
}

// A pro card whose read finds attempt 1 broken: attempt 1's two read cards are cut
// together, and the broken one stops any more; attempt 2's two are cut together, both ok,
// accepted. Four reads for the landing.
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
		h.askReads()
		got := len(h.readsOf("s1-1")) - before
		assert.Equal(t, want, got, "%s: reads asked", when)
		asked += got
	}

	// attempt 1: both read cards together; one broken, and no other is cut
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

	// the measure: read cards cut per landing on a card that failed a read once
	t.Logf("read cards per landing: %d", asked)
	assert.Equal(t, 4, asked, "read cards for one landing with one broken read")
	h.clean("a pro card's read cards cut together")
}

// A flash card is read once: one read card, ok, and no other is cut.
func TestAFlashCardIsStillReadOnce(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.askReads()
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 1)
	h.readOne(reads[0], "ok", "")
	h.askReads()
	assert.Empty(t, h.readsOf("s1-1"), "read once: no other read card")
	require.Empty(t, h.openOf(sprint.NReadyToAccept), "the tick's to accept, no judgment")
	h.clean("a flash card read once")
}

// A pro card's read card taken back off a reader that went down is cut again for one other
// reader, the other read standing; and a cut while both are placed cuts nothing.
func TestAReadTakenBackIsAskedAgainOfOneOtherReader(t *testing.T) {
	t.Parallel()
	h := readersHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.mu.Lock()
	h.live = append(h.live, "m4")
	h.mu.Unlock()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m4", Width: sprint.MaxWidth}))
	h.memberReaders("m4")
	h.readersRead("flash,pro,heavy,frontier")
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.askReads()
	reads := h.readsOf("s1-1")
	require.Len(t, reads, 2, "both read cards cut together")
	h.askReads()
	require.Len(t, h.readsOf("s1-1"), 2, "a cut while both are placed cuts nothing")
	gone := reads[0].Row
	h.mu.Lock()
	h.live = slices.DeleteFunc(h.live, func(m string) bool { return m == gone })
	h.mu.Unlock()
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: gone}))
	h.askReads()
	var again []string
	for _, rc := range h.readsOf("s1-1") {
		again = append(again, rc.Row)
	}
	require.Len(t, again, 2, "the read taken back is cut for one other reader: %v", again)
	assert.NotContains(t, again, gone)
	assert.Contains(t, again, reads[1].Row, "the other read stands")
	h.clean("a read taken back cut again")
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
