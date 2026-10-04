package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The reader who found the defect checks the fix (docs/SPEC-SPRINT.md section 6; readers.go
// finderFirst; the owner, 2026-10-04): after a rework for a finding, the next attempt's
// first read is asked of the reader who wrote the finding, when they are up, free at the
// attempt and have room, so the check is against the finding and not a fresh opinion; the
// second reader stays fresh. The finder's read moves the readers' index not at all.

// brokenBy has the primary's one outstanding read found broken by its reader, and says who.
func (h *harness) brokenBy(id, finding string) string {
	h.t.Helper()
	rc := h.askedRead(id)
	h.readOne(rc, "broken", finding)
	return rc.Row
}

func TestTheReaderWhoFoundTheDefectChecksTheFix(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"})
	h.must(DealStep(sprint.DealReq{}))
	h.finishAttempt("s1-1", false, "h1")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	finder := h.brokenBy("s1-1", "internal/x.go:3: wrong")
	require.Equal(t, "reader-a", finder, "the first read went round the readers from the start")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, finder, pr.F(sprint.FieldFindingReader), "the rework names the finder")
	assert.Equal(t, "1", pr.F(sprint.FieldFindingAttempt))
	index, _ := h.snap().Readers.Prop(sprint.PropAskIndex)

	// attempt 2: the first read is the finder's, out of turn; the index does not move
	h.finishAttempt("s1-1", false, "h2")
	res := h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	first := h.askedRead("s1-1")
	assert.Equal(t, finder, first.Row, "the finder checks the fix")
	assert.Contains(t, res.Moved[0], "asked of reader-a (who found attempt 1 broken: it checks the fix)")
	after, _ := h.snap().Readers.Prop(sprint.PropAskIndex)
	assert.Equal(t, index, after, "the finder's read is out of turn: the index stays")
	// the second read, once the first came back ok, is a fresh reader's, round the readers
	h.readOne(first, "ok", "")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	second := h.askedRead("s1-1")
	assert.Equal(t, "reader-b", second.Row, "the second reader is fresh, the next round the readers")
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
		first := h.askedRead("s1-1")
		assert.NotEqual(t, finder, first.Row, "a finder away is not asked: the round's reader is")
		h.clean("the finder away")
	})
	t.Run("no room", func(t *testing.T) {
		t.Parallel()
		// reader-m1 reads at m1's width, 1: holding one read, it has no room for the check
		h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
		require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
		h.beat()
		h.addReady("s1", 2, briefOf("pro", ""))
		for _, id := range []string{"s1-1", "s1-2"} {
			h.setPrimary(id, map[string]string{sprint.FieldTierNow: "pro"})
		}
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", false, "h1")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		first := h.askedRead("s1-1")
		// the finder is reader-m1: its read taken back from the round's reader and asked of it instead
		if first.Row != "reader-m1" {
			h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Instead: first.Row}))
			first = h.askedRead("s1-1")
			for first.Row != "reader-m1" {
				h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Instead: first.Row}))
				first = h.askedRead("s1-1")
			}
		}
		h.readOne(first, "broken", "internal/x.go:3: wrong")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
		assert.Equal(t, "reader-m1", h.snap().Work.Card("s1-1").F(sprint.FieldFindingReader))
		// reader-m1 holds s1-2's read: at its machine's width, no room
		h.finishAttempt("s1-2", false, "h1")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
		other := h.askedRead("s1-2")
		for other.Row != "reader-m1" {
			h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Instead: other.Row}))
			other = h.askedRead("s1-2")
		}
		h.finishAttempt("s1-1", false, "h2")
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		check := h.askedRead("s1-1")
		assert.NotEqual(t, "reader-m1", check.Row, "a finder with no room is not asked: the round's reader is")
		h.clean("the finder without room")
	})
}
