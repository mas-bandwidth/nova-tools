package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FriendRows is the roster with width and status only: a friend's ready,
// working, ok and failed are her sprint cards' counts, filled by where from
// her fleet row (friend.<name>), never read or counted here — the store holds
// no job record.
func TestFriendRowsReturnsNameWidthStatusOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}, {Name: "bob", Width: 1}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	_, _, _, err = h.health("amy", "tester", sprint.Up, h.now, 1)
	require.NoError(t, err)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "bob", true, "c", "", time.Time{}, 0))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// up first, then held (FleetOrder); the counts are all zero, never read; her beat's time
	// is carried (view coordinator reads how stale her report is), and the evidence her
	// status rests on, her session's pong, never her beat
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up, Evidence: "session pong 0s ago", Beat: h.now.UTC().Truncate(time.Second), Health: &sprint.FriendHealth{State: sprint.Up, Seen: h.now, Generation: 1}},
		{Name: "bob", Width: 1, Status: sprint.Held, Evidence: "held"},
	}, rows)
}

// up is a friend's beat and a wake ping her session answered, observed by the seat's
// holder at the clock now: a friend up, as the tests that deal to her want her.
func (h *harness) up(friend string) {
	h.t.Helper()
	_, err := h.st.FriendBeat(h.ctx, friend)
	require.NoError(h.t, err)
	_, _, _, err = h.health(friend, "tester", sprint.Up, h.now, 1)
	require.NoError(h.t, err)
}

// The twin tick gives batch friends their ready backlog, while one-shot friends
// share their configured physical width without a backlog (SPEC-SPRINT section 1).
func TestTwinStoreDealingRespectsFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	for _, width := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("one-shot width %d", width), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
				{Name: "amy", Width: 2, Mode: "batch", Class: "flash"},
				{Name: "bob", Width: width, Mode: "one-shot", Class: "flash"},
			})
			require.NoError(t, err)
			h.up("amy")
			h.up("bob")
			var cards []sprint.CardAdd
			for _, owner := range []struct {
				name  string
				count int
			}{{"amy", 4}, {"bob", width + 2}} {
				for i := 1; i <= owner.count; i++ {
					cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("%s-%d", owner.name, i),
						Brief: "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend " + owner.name + "\n\nThe task."})
				}
			}
			h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: cards}))
			h.startMachine()
			h.machine()
			h.start("amy", 2)
			h.start("bob", width)
			amy, bob := sprint.FriendRow("amy"), sprint.FriendRow("bob")
			snap := h.snap()
			assert.Equal(t, 2, snap.Fleet.Count(amy, sprint.Working))
			assert.Equal(t, 2, snap.Fleet.Count(amy, sprint.Ready), "batch keeps a backlog")
			assert.Equal(t, width, snap.Fleet.Count(bob, sprint.Working))
			assert.Zero(t, snap.Fleet.Count(bob, sprint.Ready), "one-shot has no backlog beyond its width")
			waiting := fmt.Sprintf("bob-%d", width+1)
			assert.Equal(t, sprint.Ready, snap.StateOf(waiting))
			h.machine()
			assert.Equal(t, width, h.snap().Fleet.Count(bob, sprint.Working), "a full row accepts no extra card")
			assert.Zero(t, h.snap().Fleet.Count(bob, sprint.Ready))

			h.must(FinishStep(sprint.FinishReq{As: bob, Sel: sprint.Sel{IDs: []string{"bob-1.w1"}},
				Gens: map[string]int{"bob-1.w1": 1}, Head: "abc"}))
			assert.Equal(t, width-1, h.snap().Fleet.Count(bob, sprint.Working), "already-started siblings are preserved")
			h.machine()
			h.start("bob", 1)
			snap = h.snap()
			assert.Equal(t, width, snap.Fleet.Count(bob, sprint.Working), "the next tick refills the free lane")
			assert.Zero(t, snap.Fleet.Count(bob, sprint.Ready))
			assert.Equal(t, sprint.Working, snap.StateOf(waiting))
			assert.Equal(t, sprint.Ready, snap.StateOf(fmt.Sprintf("bob-%d", width+2)))
		})
	}
}

// A config switch retains started work and native reads. Finishes promote one
// queued card only below the configured shared physical cap (SPEC-SPRINT section 1).
func TestTwinStoreConfigSyncToOneShotGatesQueuedPromotionUntilConfiguredCapacityIsAvailable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                                 string
		width                                                int
		read                                                 bool
		firstWorking, firstReady, secondWorking, secondReady int
	}{
		{"width one waits for the last work", 1, false, 1, 2, 1, 1},
		{"width two refills beside active work", 2, false, 2, 1, 2, 0},
		{"read above width one holds promotion", 1, true, 2, 2, 1, 2},
		{"read at width two holds promotion", 2, true, 2, 2, 2, 1},
		{"width three refills beside work and read", 3, true, 3, 1, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			batchWidth := 2
			if tc.read {
				h = inReview(t, 1)
				batchWidth = 3
				for _, reader := range []string{"reader-a", "reader-b", "reader-c"} {
					require.NoError(t, h.st.SetReaderAway(h.ctx, reader, true, "coordinator"))
				}
			}
			_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: batchWidth, Mode: "batch", Class: "flash,pro"}})
			require.NoError(t, err)
			h.up("amy")
			row := sprint.FriendRow("amy")
			readID := sprint.ReadCardID("s1-1", 1, "amy")
			if tc.read {
				h.machine()
				require.NotNil(t, h.snap().Fleet.Card(readID), "a genuine review reserves a native read")
				require.Equal(t, sprint.Working, h.snap().Fleet.Card(readID).Col)
			}
			var cards []sprint.CardAdd
			for i := 1; i <= 4; i++ {
				cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("s2-%d", i),
					Brief: "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy\n\nThe task."})
			}
			h.must(AddStep(sprint.AddReq{Stream: "s2", Cards: cards}))
			if !tc.read {
				h.startMachine()
			}
			h.machine()
			h.start("amy", 2)
			active := 2
			if tc.read {
				active++
			}
			require.Equal(t, active, h.snap().Fleet.Count(row, sprint.Working))
			require.Equal(t, 2, h.snap().Fleet.Count(row, sprint.Ready))
			_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: tc.width, Mode: "one-shot", Class: "flash,pro"}})
			require.NoError(t, err)
			assert.Equal(t, []string{"amy"}, updated)
			assert.Equal(t, active, h.snap().Fleet.Count(row, sprint.Working), "mode/width changes retain every started reservation")
			finish := func(id string) {
				t.Helper()
				h.must(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: 1}, Head: "abc"}))
			}
			finish("s2-1.w1")
			snap := h.snap()
			assert.Equal(t, tc.firstWorking, snap.Fleet.Count(row, sprint.Working))
			assert.Equal(t, tc.firstReady, snap.Fleet.Count(row, sprint.Ready))
			assert.Equal(t, sprint.Working, snap.Fleet.Card("s2-2.w1").Col, "the other started work survives")
			if tc.read {
				assert.Equal(t, sprint.Working, snap.Fleet.Card(readID).Col, "the native read still consumes a slot")
			}
			ready := snap.Fleet.Cell(row, sprint.Ready)
			require.NotEmpty(t, ready)
			res := h.run(TakeStep(sprint.TakeReq{As: row, Sel: sprint.Sel{IDs: []string{ready[0].ID}}, Gens: map[string]int{ready[0].ID: 1}}))
			require.Len(t, res.Refused, 1, "a row at or above its physical cap cannot take another job")
			assert.Equal(t, tc.firstWorking, h.snap().Fleet.Count(row, sprint.Working))
			finish("s2-2.w1")
			snap = h.snap()
			assert.Equal(t, tc.secondWorking, snap.Fleet.Count(row, sprint.Working))
			assert.Equal(t, tc.secondReady, snap.Fleet.Count(row, sprint.Ready))
			reads := 0
			if tc.read {
				reads = 1
				assert.Equal(t, sprint.Working, snap.Fleet.Card(readID).Col, "finishing work cannot erase a read reservation")
				if tc.width == 1 {
					assert.Equal(t, sprint.Ready, snap.Fleet.Card("s2-3.w1").Col, "the sole read holds both queued work cards")
					h.friendRead("amy", "s1-1", "Verdict: LAND\n")
					h.start("amy", 1)
					reads = 0
				}
			}
			assert.Equal(t, sprint.Working, h.snap().Fleet.Card("s2-3.w1").Col)
			finish("s2-3.w1")
			snap = h.snap()
			assert.Equal(t, 1+reads, snap.Fleet.Count(row, sprint.Working))
			assert.Zero(t, snap.Fleet.Count(row, sprint.Ready), "the final queued work advances when a physical slot is free")
			assert.Equal(t, sprint.Working, snap.Fleet.Card("s2-4.w1").Col)
		})
	}
}
