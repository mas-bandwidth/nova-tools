package sprint_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAHeldFriendKeepsNoReadyCards: a held friend keeps no ready card, with or
// without --return (docs/SPEC-SPRINT.md sections 1 and 11, hold). A friend with
// two ready (not begun) and one working card is held without --return; after the
// step her row holds no ready card, the two are ready for the deal, and the
// working one is taken back as today.
func TestAHeldFriendKeepsNoReadyCards(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	// Bob has width 0 so all cards are initially dealt to amy.
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
		{Name: "amy", Width: 2, Class: "pro"},
		{Name: "bob", Width: 0, Class: "pro"},
	})
	require.NoError(t, err)

	cards := []sprint.CardAdd{
		{ID: "f1-1", Brief: friendsBrief("friend")},
		{ID: "f1-2", Brief: friendsBrief("friend")},
		{ID: "f1-3", Brief: friendsBrief("friend")},
	}
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: cards}))
	r.tick()

	// All 3 cards are dealt ready to amy.
	s := r.snap()
	require.Equal(t, []string{"f1-1.w1", "f1-2.w1", "f1-3.w1"}, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready))
	require.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working))

	// Amy starts only one card (f1-1.w1), so she has 1 working and 2 ready (not begun).
	r.must(store.FriendStartStep(sprint.FriendStartReq{
		Friend: "amy",
		IDs:    []string{"f1-1.w1"},
		Gens:   map[string]int{"f1-1.w1": 1},
	}))

	s = r.snap()
	require.Equal(t, []string{"f1-1.w1"}, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working))
	require.Equal(t, []string{"f1-2.w1", "f1-3.w1"}, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready))

	// Bring bob up to width 2 so bob is available to take ready cards after amy is held.
	_, _, _, err = r.st.SyncFriends(r.ctx, []store.FriendSpec{
		{Name: "amy", Width: 2, Class: "pro"},
		{Name: "bob", Width: 2, Class: "pro"},
	})
	require.NoError(t, err)

	// Hold amy without --return:
	res := r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "away for the night"})
	s = r.snap()

	// 1. After the step her row holds no ready card (and no working card).
	assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready), "a held friend keeps no ready card")
	assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working), "a held friend keeps no working card")

	// 2. The two unstarted cards and the working one are withdrawn on the fleet table.
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Card("f1-2.w1").Col)
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Card("f1-3.w1").Col)

	// 3. The working one is taken back as today (withdrawn, marked taken back by the hold).
	w1 := s.Fleet.Card("f1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w1.Col, "working card taken back")
	assert.Equal(t, "taken back by the hold of friend amy", w1.F(sprint.FieldTakenBack))
	assert.Empty(t, w1.F(sprint.FieldTakenFrom), "the hold does not pin it from her")

	// One MOVED line per card.
	assert.Len(t, res.Moved, 3, "one MOVED line per card")

	// The log note records that all cards were withdrawn.
	lines := r.holdLines()
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[len(lines)-1], "friend amy held: away for the night; every card she holds, begun or not, is handed back now")
	assert.Contains(t, lines[len(lines)-1], "withdrew 3")

	// On the next tick, the cards ready for the deal are dealt to bob.
	r.tick()
	s = r.snap()
	bobReady := onRow(s, sprint.FriendRow("bob"), "f1", sprint.Ready)
	assert.Equal(t, []string{"f1-1.w1", "f1-2.w1", "f1-3.w1"}, bobReady, "the cards ready for the deal are dealt to bob")
	assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready, sprint.Working), "amy still holds no cards")
}
