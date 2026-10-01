package store

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// packetOf is the packet the store hands for the card.
func (h *harness) packetOf(id string) sprint.Packet {
	h.t.Helper()
	ps, err := h.st.Packets(h.ctx, []*sprint.Card{h.snap().Fleet.Card(id)})
	require.NoError(h.t, err)
	require.Len(h.t, ps, 1)
	return ps[0]
}

// The work packet's branch round-trips: the member pushes to it and finishes naming it, the work
// card keeps it, and a reader is handed it. After a clear, the same card id is dealt at the next
// epoch onto another branch, so pass 2 never pushes onto pass 1's (the quack sprint, 2026-10-01).
func TestTheAttemptBranchRoundTripsAndIsNewAfterAClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{}))
	first := h.packetOf("s1-1.w1")
	assert.Equal(t, "sprint/t-s1-1.w1.e"+strconv.FormatUint(first.Epoch, 10), first.Branch)
	wc := h.snap().Fleet.Card("s1-1.w1")
	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Head: "abc123", Branch: first.Branch, Report: "ok", Who: wc.Row}))
	assert.Equal(t, first.Branch, h.snap().Fleet.Card("s1-1.w1").F("branch"), "the work card keeps the branch the member pushed")

	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	h.must(DealStep(sprint.DealReq{}))
	second := h.packetOf("s1-1.w1")
	assert.Greater(t, second.Epoch, first.Epoch)
	assert.Equal(t, "sprint/t-s1-1.w1.e"+strconv.FormatUint(second.Epoch, 10), second.Branch)
	assert.NotEqual(t, first.Branch, second.Branch, "pass 2 pushes to a branch of its own")
}
