package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A primary at its redeal bound is ready, its work card withdrawn: rework
// includes it in the pool and plans its next attempt.
func TestReworkAcceptsPrimaryAtRedealBound(t *testing.T) {
	t.Parallel()

	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	card := func() *Card { return w.s.Fleet.Card("s1-1.w1") }
	card().Fields["redeals"] = itoa(MaxRedeals)

	from := card().Row
	presenceWith(w, otherOf(from))
	presenceWith(w, "m1", "m2")

	from = card().Row
	takeIt(w, "s1-1.w1")
	presenceWith(w, otherOf(from))
	require.NotNil(t, AtRedealBound(w.s, w.s.Work.Card("s1-1")), "primary must be at its redeal bound")

	p := Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "rework fix"})
	require.Empty(t, p.Refused, "rework must not be refused: %+v", p.Refused)
	require.NotEmpty(t, p.Units, "rework must produce units")
	w.must(p)
	assert.Equal(t, Working, w.state("s1-1"), "primary moved to working")
}
