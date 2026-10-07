package store

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWhoIsAPreferenceOnTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Class: "pro,heavy"}})
	require.NoError(t, err)
	h.up("amy")
	seats, err := h.st.friendSeats(h.ctx, h.snap(), h.now)
	require.NoError(t, err)
	require.Len(t, seats, 1, "unpinned work still reads the friend roster")
	var due int
	h.must(TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: seats}, nil, nil, &due))
	assert.Equal(t, "friend.amy", h.snap().Fleet.Card("s1-1.w1").Row)
	h.clean("friend preference")
}

func TestUnpinVerbOnTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	brief := "job tier: pro\nWHO: only friend amy\n\nWork."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "job", Brief: brief}}}))
	h.must(UnpinStep(sprint.UnpinReq{IDs: []string{"job"}, Reason: "share", Who: "tester"}))
	assert.Empty(t, h.snap().Primary("job").F(sprint.FieldWho))
	assert.Equal(t, brief, h.snap().Primary("job").F("brief"))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"job"}}}))
	assert.Contains(t, []string{"m1", "m2"}, h.snap().Fleet.Card("job.w1").Row)
	h.clean("unpin")
}

func TestPreferenceOverflowOnTheTwin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, who, want string
		seats           []sprint.FriendSeat
	}{
		{"preferred", "friend amy", "friend.amy", []sprint.FriendSeat{{Name: "amy", Width: 1, Status: sprint.Up, Class: "flash,pro"}, {Name: "bob", Width: 8, Status: sprint.Up, Class: "pro"}}},
		{"another friend", "friend amy", "friend.bob", []sprint.FriendSeat{{Name: "amy", Width: 0, Status: sprint.Up, Class: "flash,pro"}, {Name: "bob", Width: 1, Status: sprint.Up, Class: "pro"}}},
		{"fleet", "friend amy", "fleet", []sprint.FriendSeat{{Name: "amy", Width: 0, Status: sprint.Up, Class: "flash,pro"}, {Name: "bob", Width: 1, Status: sprint.Up, Class: "flash"}}},
		{"only waits", "only friend amy", "", []sprint.FriendSeat{{Name: "amy", Width: 0, Status: sprint.Up, Class: "flash,pro"}, {Name: "bob", Width: 1, Status: sprint.Up, Class: "pro"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.setup(0)
			h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "job", Brief: "job tier: pro\nWHO: " + tc.who + "\n\nWork."}}}))
			var due int
			h.must(TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: tc.seats}, nil, nil, &due))
			wc := h.snap().Fleet.Card("job.w1")
			if tc.want == "" {
				assert.Nil(t, wc)
				assert.Equal(t, sprint.Ready, h.state("job"))
				return
			}
			require.NotNil(t, wc)
			if tc.want == "fleet" {
				assert.Contains(t, []string{"m1", "m2"}, wc.Row)
			} else {
				assert.Equal(t, tc.want, wc.Row)
			}
			h.clean("preference overflow")
		})
	}
}

// Real queued load fills both friends before the fifth preference reaches the
// fleet. A hard pin waits at that same boundary.
func TestPreferenceConsumesActualRoomOnTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	cards := []sprint.CardAdd{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		cards = append(cards, sprint.CardAdd{ID: id, Brief: "job tier: pro\nWHO: friend amy\n\nWork."})
	}
	cards = append(cards, sprint.CardAdd{ID: "f", Brief: "job tier: pro\nWHO: only friend amy\n\nWork."})
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: cards}))
	seats := []sprint.FriendSeat{{Name: "amy", Width: 1, Status: sprint.Up, Class: "pro"}, {Name: "bob", Width: 1, Status: sprint.Up, Class: "pro"}}
	var due int
	h.must(TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: seats}, nil, nil, &due))
	s := h.snap()
	for _, id := range []string{"a", "b"} {
		assert.Equal(t, "friend.amy", s.Fleet.Card(id+".w1").Row)
	}
	for _, id := range []string{"c", "d"} {
		assert.Equal(t, "friend.bob", s.Fleet.Card(id+".w1").Row)
	}
	assert.Contains(t, []string{"m1", "m2"}, s.Fleet.Card("e.w1").Row)
	assert.Equal(t, sprint.Ready, h.state("f"))
	assert.Nil(t, s.Fleet.Card("f.w1"))
	h.clean("actual room")
}

func TestUnpinRefusesOnlyTheStartedCardOnTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	brief := "job tier: pro\nWHO: friend amy\n\nWork."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "a", Brief: brief}, {ID: "b", Brief: brief}}}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"b"}}}))
	res, err := h.st.Run(h.ctx, UnpinStep(sprint.UnpinReq{IDs: []string{"a", "b"}, Reason: "share", Who: "tester"}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1)
	assert.Equal(t, "b", res.Refused[0].Key)
	assert.Empty(t, h.snap().Primary("a").F(sprint.FieldWho))
	assert.Equal(t, "friend.amy", h.snap().Primary("b").F(sprint.FieldWho))
}

func TestUnpinReturnedCardWhileRunningOnTheTwin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	brief := "job tier: pro\nWHO: only friend amy\n\nWork."
	h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "a", Brief: brief}, {ID: "b", Brief: brief}}}))
	var due int
	h.must(TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: []sprint.FriendSeat{{Name: "amy", Width: 1, Status: sprint.Up, Class: "flash,pro"}}}, nil, nil, &due))
	h.must(FriendTakeStep(sprint.FriendTakeReq{Friend: "amy", IDs: []string{"b"}, Reason: "share"}))
	h.startMachine()
	h.must(UnpinStep(sprint.UnpinReq{IDs: []string{"b"}, Reason: "share returned work", Who: "tester"}))
	// While running, the edit queues; the stopped machine drains it using the
	// same twin and retains the issued work-card identity.
	h.stopMachine()
	s := h.snap()
	assert.Empty(t, s.Primary("b").F(sprint.FieldWho))
	assert.Equal(t, brief, s.Primary("b").F("brief"))
	assert.Equal(t, 1, s.Primary("b").Int("attempt"))
	h.must(TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: []sprint.FriendSeat{{Name: "bob", Width: 1, Status: sprint.Up, Class: "pro"}}}, nil, nil, &due))
	assert.Equal(t, "friend.bob", h.snap().Fleet.Card("b.w1").Row)
	assert.Equal(t, 3, h.snap().Fleet.Card("b.w1").Int("gen"))
	h.clean("returned unpin")
}
