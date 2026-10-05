package store

import (
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On the twin store the tick deals a friend's card to an idle friend before a full one,
// and levels the friends every tick with no verb, reading the seats and what her beat
// names running though no friend's card is ready (docs/SPEC-SPRINT.md section 1,
// friend-deal-idle-lanes-first.w1).
func TestTwinStoreDealsIdleFriendsFirstAndLevelsEveryTick(t *testing.T) {
	t.Parallel()
	amy, bob, cat := sprint.FriendRow("amy"), sprint.FriendRow("bob"), sprint.FriendRow("cat")
	held := func(snap *sprint.Snapshot, row string) int {
		return snap.Fleet.Count(row, sprint.Working) + snap.Fleet.Count(row, sprint.Ready)
	}

	t.Run("five new cards go to the two idle friends, none to the full one", func(t *testing.T) {
		t.Parallel()
		h, newest := fullAmy(t)
		for _, f := range []string{"bob", "cat"} {
			_, err := h.st.FriendBeat(h.ctx, f)
			require.NoError(t, err)
		}
		h.must(AddStep(sprint.AddReq{Stream: "s2", Cards: friendCards("s2", 5)}))
		h.machine()
		snap := h.snap()
		for i, want := range []string{bob, cat, bob, cat, bob} {
			wc := snap.Fleet.Placed("s2-" + strconv.Itoa(i+1) + ".w1")
			require.NotNil(t, wc)
			assert.Equal(t, want, wc.Row, "s2-%d: idle lanes first, then room, then name; never amy", i+1)
		}
		// the same tick levels amy's backlog (8) onto them up to their room: three move
		assert.Equal(t, 4, held(snap, bob))
		assert.Equal(t, 4, held(snap, cat))
		assert.Equal(t, 13, held(snap, amy))
		assert.Equal(t, amy, snap.Fleet.Placed(newest).Row, "the card her beat names running stays")
		h.machine()
		assert.Equal(t, 13, held(h.snap(), amy), "level: the next tick moves nothing")
		assert.Empty(t, sprint.Check(h.snap(), nil))
	})

	t.Run("a friend coming up is levelled on that tick, with no friend's card ready", func(t *testing.T) {
		t.Parallel()
		h, newest := fullAmy(t)
		_, err := h.st.FriendBeat(h.ctx, "bob")
		require.NoError(t, err)
		h.machine()
		snap := h.snap()
		assert.Equal(t, 2, snap.Fleet.Count(bob, sprint.Working), "his idle lanes first")
		assert.Equal(t, 4, held(snap, bob), "then evened to his room")
		assert.Equal(t, 12, held(snap, amy))
		assert.Equal(t, amy, snap.Fleet.Placed(newest).Row)
		assert.Empty(t, sprint.Check(snap, nil))
	})
}

// friendCards is n cards for any friend on the stream, ids <stream>-1, <stream>-2, ...
func friendCards(stream string, n int) []sprint.CardAdd {
	var cards []sprint.CardAdd
	for i := range n {
		cards = append(cards, sprint.CardAdd{ID: stream + "-" + strconv.Itoa(i+1), Brief: "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend\n\nThe task."})
	}
	return cards
}

// fullAmy is a twin store whose roster is amy (width 8), bob and cat (width 2), all of
// class flash,pro, with amy alone up and at her room: sixteen cards for any friend,
// working 8 and ready 8, the newest of her ready ones named running by her beat.
func fullAmy(t *testing.T) (*harness, string) {
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 8, Class: "flash,pro"},
		{Name: "bob", Width: 2, Class: "flash,pro"},
		{Name: "cat", Width: 2, Class: "flash,pro"},
	})
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: friendCards("s1", 16)}))
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	h.startMachine()
	h.machine()
	amy := sprint.FriendRow("amy")
	snap := h.snap()
	require.Equal(t, 8, snap.Fleet.Count(amy, sprint.Working))
	require.Equal(t, 8, snap.Fleet.Count(amy, sprint.Ready))
	queue := snap.Fleet.Cell(amy, sprint.Ready)
	sprint.SortCards(queue)
	newest := queue[len(queue)-1].ID
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{newest}}, nil)
	require.NoError(t, err)
	return h, newest
}
