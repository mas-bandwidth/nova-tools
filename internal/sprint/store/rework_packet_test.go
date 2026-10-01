package store

import (
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
	t.Run("a first attempt carries none", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.must(DealStep(sprint.DealReq{}))
		p := packetOf(h, "s1-1.w1")
		assert.Empty(t, p.Fix+p.Finding+p.Why)
	})
}
