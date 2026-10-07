package store

import (
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The deal's rolling index is in the store (errata 3, amendment 5): a stop and
// a start of the machine, and a new loop on the same store, go on round the
// fleet where the last deal left off.
func TestTheRollingIndexesSurviveAStopAndAStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(6)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	st := h.st
	deal := func(id string) string {
		t.Helper()
		_, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
		require.NoError(t, err)
		return h.snap().Fleet.Card(sprint.WorkCardID(id, 1)).Row
	}
	restart := func() {
		t.Helper()
		for _, run := range []bool{true, false, true} {
			_, _, _, err := st.SetMachine(h.ctx, run)
			require.NoError(t, err)
		}
		// a new loop: a store of its own on the same backend, holding nothing
		st = &Store{B: h.st.B, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	}
	var got []string
	for _, id := range []string{"s1-1", "s1-2"} {
		got = append(got, deal(id))
	}
	restart()
	for _, id := range []string{"s1-3", "s1-4"} {
		got = append(got, deal(id))
	}
	want := []string{"m1", "m2", "m3", "m1"}
	require.True(t, slices.Equal(got, want), "deals %v, want %v: the index goes on past m2 after the restart", got, want)

}
