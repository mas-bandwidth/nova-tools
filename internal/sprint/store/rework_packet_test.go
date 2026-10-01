package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A rework carries what it is for (docs/SPEC-SPRINT.md, rework): on the mem twin, the
// coordinator's --fix, the broken read's finding and how the attempt before ended are written
// on the next attempt's work card and ride in its packet, so the child is told why it exists.
func TestReworkCarriesTheFixTheFindingAndWhyInTheNextPacket(t *testing.T) {
	t.Parallel()
	packetOf := func(h *harness, id string) sprint.Packet {
		t.Helper()
		wc := h.snap().Fleet.Card(id)
		require.NotNil(t, wc, id)
		ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc})
		require.NoError(t, err)
		return ps[0]
	}
	t.Run("a broken read and a --fix", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.a2ToReview("s1-1", false)
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		rc := h.snap().Readers.Of("s1-1")
		h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
		h.must(ReadStep(sprint.ReadReq{As: rc[1].Row, Verdict: "broken", Finding: "fix correct, the required test is missing", Sel: sprint.Sel{IDs: []string{rc[1].ID}}}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "add the required test"}))
		p := packetOf(h, "s1-1.w2")
		assert.Equal(t, "add the required test", p.Fix)
		assert.Equal(t, "fix correct, the required test is missing", p.Finding)
		assert.Equal(t, "attempt 1 finished and a reader found it broken", p.Why)
	})
	t.Run("failed work and no --fix", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.a2ToReview("s1-1", true)
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		p := packetOf(h, "s1-1.w2")
		assert.Empty(t, p.Finding, "no read found anything")
		assert.Contains(t, p.Why, "attempt 1 failed: ")
		assert.NotEmpty(t, p.Fix, "the report of the failed work is the fix taken")
		assert.Contains(t, p.Why, p.Fix)
	})
	t.Run("a rework with no member up carries them across the deferred deal", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.a2ToReview("s1-1", false)
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		rc := h.snap().Readers.Of("s1-1")
		h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
		h.must(ReadStep(sprint.ReadReq{As: rc[1].Row, Verdict: "broken", Finding: "the test is missing", Sel: sprint.Sel{IDs: []string{rc[1].ID}}}))
		h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "add the test"}))
		require.Equal(t, sprint.Ready, h.state("s1-1"), "no member is up: the rework waits for start")
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
		h.must(DealStep(sprint.DealReq{}))
		p := packetOf(h, "s1-1.w2")
		assert.Equal(t, "add the test", p.Fix)
		assert.Equal(t, "the test is missing", p.Finding)
		assert.Equal(t, "attempt 1 finished and a reader found it broken", p.Why)
	})
	t.Run("a long finding and a long report are cut, never refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		h.a2ToReview("s1-1", false)
		h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		rc := h.snap().Readers.Of("s1-1")
		for i, c := range rc[:2] {
			h.must(ReadStep(sprint.ReadReq{As: c.Row, Verdict: "broken", Finding: strings.Repeat(string(rune('a'+i)), 5000), Sel: sprint.Sel{IDs: []string{c.ID}}}))
		}
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "short"}))
		p := packetOf(h, "s1-1.w2")
		assert.Len(t, p.Finding, MaxCardTextBytes, "two 5000-byte findings are cut to the bound")
		assert.True(t, strings.HasSuffix(p.Finding, "..."))
		// a failed finish with a report at the bound, and no --fix: the fix taken and why are cut too
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
		wc := h.snap().Fleet.Card("s1-2.w1")
		require.NotNil(t, wc)
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: 1}}))
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: 1}, Failed: true, Report: strings.Repeat("r", MaxCardTextBytes-12)}))
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
		p = packetOf(h, "s1-2.w2")
		assert.LessOrEqual(t, len(p.Why), MaxCardTextBytes)
		assert.LessOrEqual(t, len(p.Fix), MaxCardTextBytes)
		assert.True(t, strings.HasSuffix(p.Why, "..."))
		assert.Len(t, p.Fix, MaxCardTextBytes-12, "the report is the fix taken, whole")
	})
	t.Run("a first attempt carries none", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.must(DealStep(sprint.DealReq{}))
		p := packetOf(h, "s1-1.w1")
		assert.Empty(t, p.Fix+p.Finding+p.Why)
	})
}

// With the machine running, the coordinator's rework is applied to the fleet table at once
// (its work card, with the fix, finding and why) and to the work table at the next tick's
// drain, so a member can take the new attempt before its primary shows it. The packet then
// reads the work card's own words, never the primary's, and the drain is no second rework
// (the live log's "reworked by the machine" line is that one queued write).
func TestAReworkTakenBeforeTheDrainStillCarriesItsWords(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.takeAndFinish(false, "s1-1")
	h.machine()
	h.machine()
	rc := h.snap().Readers.Of("s1-1")
	require.Len(t, rc, 2)
	h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(ReadStep(sprint.ReadReq{As: rc[1].Row, Verdict: "broken", Finding: "fix correct, the required test is missing", Sel: sprint.Sel{IDs: []string{rc[1].ID}}}))
	h.machine()
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "add the required test"}))
	wc := h.snap().Fleet.Card("s1-1.w2")
	require.NotNil(t, wc, "the rework dealt its card at once")
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: 1}}))
	want := func(when string) {
		ps, err := h.st.Packets(h.ctx, []*sprint.Card{h.snap().Fleet.Card("s1-1.w2")})
		require.NoError(t, err)
		assert.Equal(t, "add the required test", ps[0].Fix, when)
		assert.Equal(t, "fix correct, the required test is missing", ps[0].Finding, when)
		assert.Equal(t, "attempt 1 finished and a reader found it broken", ps[0].Why, when)
	}
	want("taken, before the drain")
	h.machine()
	want("after the drain")
	s := h.snap()
	assert.Equal(t, 2, s.Work.Card("s1-1").Int("attempt"), "exactly one rework")
	assert.Nil(t, s.Fleet.Card("s1-1.w3"))
}
