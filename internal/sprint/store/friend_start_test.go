package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// start is the friend's start receipt for the oldest n cards ready on her row, as friend sync
// reads her jobs begun (FriendStartStep): each goes to working, its deadline from now.
func (h *harness) start(friend string, n int) {
	h.t.Helper()
	ready := h.snap().Fleet.Cell(sprint.FriendRow(friend), sprint.Ready)
	sprint.SortCards(ready)
	r := sprint.FriendStartReq{Friend: friend, Gens: map[string]int{}}
	for _, c := range ready[:min(n, len(ready))] {
		r.IDs, r.Gens[c.ID] = append(r.IDs, c.ID), max(c.Int("gen"), 1)
	}
	if len(r.IDs) > 0 {
		h.must(FriendStartStep(r))
	}
}

// On the twin store a friend's card is dealt ready, friend sync's start receipt moves it to
// working with its deadline from then, and a card moved to working with no start of hers (a
// take) goes back ready on the next tick (docs/SPEC-SPRINT.md section 1, a friend's card is
// working once she starts it).
func TestTwinStoreAFriendCardIsWorkingOnlyOnceSheStartsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Class: "flash"}})
	require.NoError(t, err)
	h.up("amy")
	brief := "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\n\nThe task."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: brief}, {ID: "s1-2", Brief: brief}, {ID: "s1-3", Brief: brief}}}))
	h.startMachine()
	h.machine()
	amy := sprint.FriendRow("amy")
	snap := h.snap()
	assert.Equal(t, 3, snap.Fleet.Count(amy, sprint.Ready), "dealt ready, whatever her lanes")
	assert.Zero(t, snap.Fleet.Count(amy, sprint.Working))

	h.start("amy", 1)
	wc := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Working, wc.Col)
	assert.Equal(t, h.now.UTC().Format(time.RFC3339), wc.F("taken"), "its deadline runs from her start")
	assert.Equal(t, "1", wc.F(sprint.FieldStarted))

	// a take is no start: the next tick puts the card back ready, her started one stays
	take := TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: []string{"s1-2.w1"}}, As: amy, Gens: map[string]int{"s1-2.w1": 1}, Who: amy})
	h.must(take)
	require.Equal(t, sprint.Working, h.snap().Fleet.Card("s1-2.w1").Col)
	h.machine()
	snap = h.snap()
	assert.Equal(t, sprint.Ready, snap.Fleet.Card("s1-2.w1").Col, "working on a friend's row means started")
	assert.Empty(t, snap.Fleet.Card("s1-2.w1").F("taken"))
	assert.Equal(t, sprint.Working, snap.Fleet.Card("s1-1.w1").Col)
	assert.Empty(t, sprint.Check(snap, nil))
}
