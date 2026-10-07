package store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The reader who found the defect checks the fix (docs/SPEC-SPRINT.md section 6; readers.go
// finderFirst; the owner, 2026-10-04): after a rework for a finding, the next attempt's
// first read is asked of the reader who wrote the finding, when they are up, free at the
// attempt and have room, so the check is against the finding and not a fresh opinion; the
// second reader stays fresh. The finder's read moves the readers' index not at all. Reads
// are asked together (sprint.ReadsWanted): the finder's and the fresh reader's in one ask.

// outstanding is the primary's reads outstanding (asked or reading), in reader row order.
func (h *harness) outstanding(id string) []*sprint.Card {
	h.t.Helper()
	s := h.snap()
	var out []*sprint.Card
	for _, row := range s.Readers.Rows() {
		for _, rc := range s.Readers.Of(id) {
			if rc.Row == row && (rc.Col == sprint.Asked || rc.Col == sprint.Reading) {
				out = append(out, rc)
			}
		}
	}
	return out
}

// outstandingOf is the primary's read outstanding of the reader, or nil.
func (h *harness) outstandingOf(id, reader string) *sprint.Card {
	h.t.Helper()
	for _, rc := range h.outstanding(id) {
		if rc.Row == reader {
			return rc
		}
	}
	return nil
}

// toReader takes the primary's reads outstanding back from other readers (ask --instead)
// until the reader holds one, and returns it.
func (h *harness) toReader(id, reader string) *sprint.Card {
	h.t.Helper()
	for i := 0; i < 10; i++ {
		if rc := h.outstandingOf(id, reader); rc != nil {
			return rc
		}
		rcs := h.outstanding(id)
		require.NotEmpty(h.t, rcs, "%s: a read outstanding", id)
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}, Instead: rcs[0].Row}))
	}
	h.t.Fatalf("%s: never asked of %s", id, reader)
	return nil
}

// brokenBy has the primary's first outstanding read, in reader row order, found broken
// by its reader, and says who.
func (h *harness) brokenBy(id, finding string) string {
	h.t.Helper()
	rcs := h.outstanding(id)
	require.NotEmpty(h.t, rcs, "%s: a read outstanding", id)
	h.readOne(rcs[0], "broken", finding)
	return rcs[0].Row
}

func TestTheReaderWhoFoundTheDefectChecksTheFix(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, h.outstanding("s1-1"), 2, "both reads asked together")
	finder := h.brokenBy("s1-1", "internal/x.go:3: wrong")
	require.Equal(t, "reader-a", finder, "the first read went round the readers from the start")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, finder, pr.F(sprint.FieldFindingReader), "the rework names the finder")
	assert.Equal(t, "1", pr.F(sprint.FieldFindingAttempt))
	index, _ := h.snap().Readers.Prop(sprint.PropAskIndex)

	// attempt 2: the finder's read, out of turn, and a fresh reader's, round the readers,
	// asked together; the index moves past the fresh reader only
	h.finishAttempt("s1-1", false, "h2")
	res := h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	reads := h.outstanding("s1-1")
	require.Len(t, reads, 2, "the finder's read and a fresh one, together")
	require.NotNil(t, h.outstandingOf("s1-1", finder), "the finder checks the fix")
	assert.Contains(t, strings.Join(res.Moved, "; "), "asked of reader-a (who found attempt 1 broken: it checks the fix)")
	assert.NotNil(t, h.outstandingOf("s1-1", "reader-c"), "the second reader is fresh, the next round the readers (past reader-b)")
	after, _ := h.snap().Readers.Prop(sprint.PropAskIndex)
	n, _ := strconv.Atoi(index)
	assert.Equal(t, strconv.Itoa(n+1), after, "the finder's read is out of turn: the index moves past the fresh reader alone")
	h.clean("the finder checked the fix")
}

func TestTheFinderIsNotPreferredWhenAwayOrWithoutRoom(t *testing.T) {
	t.Parallel()
	t.Run("away", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
		h.addReady("s1", 1, briefOf("pro", ""))
		h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", false, "h1")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		finder := h.brokenBy("s1-1", "internal/x.go:3: wrong")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
		require.NoError(t, h.st.SetReaderAway(h.ctx, finder, true, "tester"))
		h.beat()
		h.finishAttempt("s1-1", false, "h2")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		reads := h.outstanding("s1-1")
		require.Len(t, reads, 2, "both reads asked together")
		assert.Nil(t, h.outstandingOf("s1-1", finder), "a finder away is not asked: the round's readers are")
		h.clean("the finder away")
	})
	t.Run("no room", func(t *testing.T) {
		t.Parallel()
		// reader-m1 reads at m1's width, 1: holding one read, it has no room for the check
		h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
		require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-m1"}))
		h.readersRead("flash,pro,heavy,frontier") // the added row reads every tier, as the harness's do
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
		h.beat()
		h.addReady("s1", 2, briefOf("pro", ""))
		for _, id := range []string{"s1-1", "s1-2"} {
			h.setPrimary(id, map[string]string{sprint.FieldTierNow: "pro"})
		}
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", false, "h1")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		// the finder is reader-m1: a read taken back from the round's readers and asked of it instead
		first := h.toReader("s1-1", "reader-m1")
		h.readOne(first, "broken", "internal/x.go:3: wrong")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
		assert.Equal(t, "reader-m1", h.snap().Work.Card("s1-1").F(sprint.FieldFindingReader))
		// reader-m1 holds s1-2's read: at its machine's width, no room
		h.finishAttempt("s1-2", false, "h1")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
		h.toReader("s1-2", "reader-m1")
		h.finishAttempt("s1-1", false, "h2")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		require.NotEmpty(t, h.outstanding("s1-1"), "s1-1 attempt 2 asked")
		assert.Nil(t, h.outstandingOf("s1-1", "reader-m1"), "a finder with no room is not asked: the round's readers are")
		h.clean("the finder without room")
	})
}

// The finder is the first broken read's reader in reader ROW order (the readers table's
// declaration order), never in name order: here reader-0 is declared after reader-c, so
// it sorts first by name and comes last by row. With two reads of one attempt found
// broken (both asked together), the rework names reader-c.
func TestTheFinderIsTheFirstBrokenReadInReaderRowOrder(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-0"}))
	h.readersRead("flash,pro,heavy,frontier") // the added row reads every tier, as the harness's do
	require.Equal(t, []string{"reader-a", "reader-b", "reader-c", "reader-0"}, h.snap().Readers.Rows(), "rows not in name order")
	for _, r := range []string{"reader-a", "reader-b"} {
		require.NoError(t, h.st.SetReaderAway(h.ctx, r, true, "tester"))
	}
	h.beat()
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	var asked []string
	for _, rc := range h.snap().Readers.Of("s1-1") {
		require.Equal(t, sprint.Asked, rc.Col)
		asked = append(asked, rc.Row)
		h.readOne(rc, "broken", "internal/x.go:3: wrong, says "+rc.Row)
	}
	require.ElementsMatch(t, []string{"reader-c", "reader-0"}, asked, "the two readers up were asked")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	assert.Equal(t, "reader-c", h.snap().Work.Card("s1-1").F(sprint.FieldFindingReader),
		"the first broken read in reader row order (reader-c is declared before reader-0)")
	h.clean("the finder in row order")
}
