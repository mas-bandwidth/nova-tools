package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// dealtLife drives a member of width 1 through a card done, a card working past its take
// deadline and a card waiting in its ready queue, and checks what Dealt reads: the working
// and the ready card, never the done one, the work table's dealt bound, and the open
// judgment on the late card's primary. The harness is a Mem store or a Redis one.
func dealtLife(t *testing.T, h *harness) {
	t.Helper()
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 1}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	d, err := h.st.Dealt(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, d.Cards, "nothing dealt yet")
	h.startMachine()
	h.machine()
	take := func() *sprint.Card {
		h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
		w := h.snap().Fleet.Cell("m1", sprint.Working)
		require.Len(t, w, 1)
		return w[0]
	}
	done := take()
	h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{done.ID}}, Gens: map[string]int{done.ID: done.Int("gen")}, Head: "h1", Who: "m1"}))
	h.machine()
	working := take()
	ready := h.snap().Fleet.Cell("m1", sprint.Ready)
	require.Len(t, ready, 1, "the third card waits in the ready queue")
	for range 9 { // two hours and a quarter: the working card is late, not finished
		h.tick(15 * time.Minute)
		h.machine()
	}

	d, err = h.st.Dealt(h.ctx)
	require.NoError(t, err)
	got := map[string]string{}
	for _, c := range d.Cards {
		assert.Equal(t, "m1", c.Row, c.ID)
		got[c.ID] = c.Col
	}
	assert.Equal(t, map[string]string{working.ID: string(sprint.Working), ready[0].ID: string(sprint.Ready)}, got, "the done card is never read")
	assert.Equal(t, sprint.DealtMaxDefault, (&sprint.Snapshot{Work: d.Work}).DealtMax(), "the work table's properties come with the cards")
	var late []string
	for _, o := range d.Open {
		late = append(late, o.Note.Type+" "+o.Subject())
	}
	assert.Contains(t, late, sprint.NWorkLate+" "+working.F(sprint.PrimaryField))
}

// Dealt reads the ready and working cells of the fleet and nothing done.
func TestDealtReadsTheCardsInFlightAlone(t *testing.T) {
	t.Parallel()
	dealtLife(t, newHarness(t))
}
