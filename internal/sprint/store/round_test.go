package store

import (
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The deal's and the ask's rolling indexes are in the store (errata 3,
// amendment 5): a stop and a start of the machine, and a new loop on the same
// store, go on round the fleet and the readers where the last deal and ask
// left off.
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

	// the ask: s1-1 and s1-2 in review, asked one at a time across a restart
	for _, id := range []string{"s1-1", "s1-2"} {
		c := h.snap().Fleet.Card(sprint.WorkCardID(id, 1))
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
		h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	}
	ask := func(id string) []string {
		t.Helper()
		_, err := st.Run(h.ctx, AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		require.NoError(t, err)
		return sprint.Split(h.snap().Work.Card(id).F("asked"))
	}
	first := ask("s1-1")
	restart()
	second := ask("s1-2")
	require.True(t, slices.Equal(first, []string{"reader-a", "reader-b"}), "asked %v then %v, want [reader-a reader-b] then [reader-c reader-a]: past reader-b after the restart", first, second)
	require.True(t, slices.Equal(second, []string{"reader-c", "reader-a"}), "asked %v then %v, want [reader-a reader-b] then [reader-c reader-a]: past reader-b after the restart", first, second)
}
