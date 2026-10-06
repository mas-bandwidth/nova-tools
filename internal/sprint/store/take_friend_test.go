package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A take for a friend reads her presence on the twin store (docs/SPEC-SPRINT.md section 1,
// a take for a friend): TakeStep reads the friends' seats when it names a friend's row, so
// a beating friend whose row has no control card status takes her ready card, and a held
// one is refused with the hold's words.
func TestTwinStoreTakeForABeatingFriendReadsHerPresence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2, Class: "flash"}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	brief := "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\n\nThe task."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: brief}, {ID: "s1-2", Brief: brief}, {ID: "s1-3", Brief: brief}}}))
	h.startMachine()
	h.machine()
	amy := sprint.FriendRow("amy")
	snap := h.snap()
	require.Equal(t, 2, snap.Fleet.Count(amy, sprint.Working))
	wc := snap.Fleet.Card("s1-3.w1")
	require.NotNil(t, wc)
	require.Equal(t, sprint.Ready, wc.Col)
	require.Empty(t, snap.MemberCtl(amy).F("status"), "her row has no control card status")

	// her width raised to 3: a lane free, and her take of her ready card
	_, _, _, err = h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3, Class: "flash"}})
	require.NoError(t, err)
	take := TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, As: amy, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: amy})

	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", true, "coordinator", "away", time.Time{}, 0))
	res := h.run(take)
	require.Len(t, res.Refused, 1)
	assert.Equal(t, "friend amy is held: held by the coordinator (friend down): away", res.Refused[0].Why)
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(wc.ID).Col)

	require.NoError(t, h.st.SetFriendHeld(h.ctx, "amy", false, "coordinator", "", time.Time{}, 0))
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	h.must(take)
	assert.Equal(t, sprint.Working, h.snap().Fleet.Card(wc.ID).Col)
	assert.Equal(t, 3, h.snap().Fleet.Count(amy, sprint.Working))
}
