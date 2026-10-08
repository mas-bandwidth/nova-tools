package sprint_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// newSubscriptionRig sets up a twin store with one subscription friend and one api friend,
// plus a route, and five pro cards ready.
func newSubscriptionRig(t *testing.T) *subscriptionRig {
	t.Helper()
	r := &subscriptionRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.RowsAdd(r.ctx, "t-readers", []string{"reader-a"}))
	require.NoError(t, r.st.EnsureReaderTiers(r.ctx))
	for _, rd := range []string{"reader-a"} {
		require.NoError(t, r.m.RowSet(r.ctx, "t-readers", rd, map[string]string{sprint.ReaderTiers: "flash,pro"}))
	}
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	r.m.SetRoutes([]sprint.Route{
		{Name: "pro-a", Tier: "pro", Provider: "prov-pro-a", Model: "model-pro-a", Tokens: 1000, Deadline: 10, Enabled: true},
	})
	r.beat()
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	// subscription friend sub1 with width 4 (room 4)
	r.must(store.FriendStep(sprint.FriendReq{Op: "up", Friend: "sub1", Width: 4, Slots: 4, Tiers: []string{"pro"}, Billing: "subscription"}))
	// api friend api1 with width 4 (room 4)
	r.must(store.FriendStep(sprint.FriendReq{Op: "up", Friend: "api1", Width: 4, Slots: 4, Tiers: []string{"pro"}, Billing: "api"}))
	// five pro cards
	for i := 0; i < 5; i++ {
		r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: fmt.Sprintf("c: the work tier: pro\nREPO: test\n\n%s\n", i)}))
	}
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// dealAll runs the deal for all ready cards.
func (r *subscriptionRig) dealAll() {
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{All: true}}))
}

func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()
	r := newSubscriptionRig(t)
	r.dealAll()

	s := r.snap()
	// Check how many cards went to the subscription friend
	sub1Dealt := s.Fleet.DealtFleetRow("t-friends", sprint.FriendRow("sub1"))
	api1Dealt := s.Fleet.DealtFleetRow("t-friends", sprint.FriendRow("api1"))

	// Subscription friend should get 4 cards (room 4), api friend gets 1 (the overflow)
	require.Greater(t, sub1Dealt, api1Dealt, "subscription friend should get more cards than api friend")
	require.Equal(t, 4, sub1Dealt, "subscription friend should get 4 cards")
	require.Equal(t, 1, api1Dealt, "api friend should get 1 card (overflow)")
}
