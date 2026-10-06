package sprint_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One tier decides every friend deal, level, take-back and the queue packet
// (docs/SPEC-SPRINT.md, friend-deal-one-tier-b.w1). On the in-memory twin: a
// re-tiered card leaves a holder who cannot serve it; an untiered card's packet
// carries flash; a WHO friend who does not serve the tier is skipped, a hard
// pin included; a flash card levels between friends whose tier lists differ.

func tierBrief(tier, who string) string {
	line := "c: a friend's card"
	if tier != "" {
		line += " tier: " + tier
	}
	return line + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

// tickFriends advances the twin one second and runs one tick. It beats the
// machines and the named friends only: newHoldRig's tick also beats amy and bob,
// who a replaced roster no longer has.
func tickFriends(r *holdRig, names ...string) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for _, f := range names {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
		_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(r.t, err)
	}
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func workOf(r *holdRig, id string) *sprint.Card {
	r.t.Helper()
	s := r.snap()
	pr := s.Primary(id)
	require.NotNil(r.t, pr, id)
	wc := s.Fleet.Placed(pr.F("work"))
	require.NotNil(r.t, wc, "%s has no work card", id)
	return wc
}

func packetTier(r *holdRig, id string) string {
	r.t.Helper()
	s := r.snap()
	wc := workOf(r, id)
	pk, err := r.st.Packets(r.ctx, []*sprint.Card{wc})
	require.NoError(r.t, err)
	require.Len(r.t, pk, 1)
	assert.Equal(r.t, sprint.FriendTier(s, s.Primary(id)), pk[0].Tier, "the packet names FriendTier")
	return pk[0].Tier
}

func TestEveryFriendDecisionReadsTheOneTierOfTheCard(t *testing.T) {
	t.Parallel()

	t.Run("a re-tier leaves a holder who cannot serve it", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
			{Name: "emma", Width: 2, Class: "flash,pro"},
			{Name: "gus", Width: 2, Class: "heavy"},
		})
		require.NoError(t, err)
		r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{
			{ID: "f1-1", Brief: tierBrief("", "friend")},
		}}))
		tickFriends(r, "emma", "gus")
		wc := workOf(r, "f1-1")
		assert.Equal(t, sprint.FriendRow("emma"), wc.Row, "untiered is flash, and only emma serves flash")
		assert.Equal(t, "flash", packetTier(r, "f1-1"), "an untiered card's packet carries the default tier")

		r.must(store.BriefStep(sprint.BriefReq{ID: "f1-1", Tier: "heavy", Who: "coordinator"}))
		tickFriends(r, "emma", "gus")
		wc = workOf(r, "f1-1")
		assert.Equal(t, sprint.FriendRow("gus"), wc.Row, "re-tiered heavy, taken back from emma and dealt to gus")
		assert.NotEqual(t, sprint.FriendRow("emma"), wc.Row)
		assert.Equal(t, "heavy", packetTier(r, "f1-1"))
		assert.Equal(t, "emma", wc.F(sprint.FieldFriendsLeft))
	})

	t.Run("a named friend who does not serve the tier is skipped", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
			{Name: "fay", Width: 2, Class: "flash"},
			{Name: "emma", Width: 2, Class: "flash,pro"},
		})
		require.NoError(t, err)
		r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{
			{ID: "f1-1", Brief: tierBrief("pro", "friend fay")},
			{ID: "f1-2", Brief: tierBrief("pro", "only friend fay")},
		}}))
		tickFriends(r, "fay", "emma")
		for _, id := range []string{"f1-1", "f1-2"} {
			wc := workOf(r, id)
			assert.Equal(t, sprint.FriendRow("emma"), wc.Row, "%s skips fay, who does not serve pro", id)
			assert.NotEqual(t, sprint.Ready, r.snap().StateOf(id), "%s does not wait on the pin", id)
			assert.Equal(t, "pro", packetTier(r, id))
		}
	})

	t.Run("a flash card levels across tier lists", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
			{Name: "ned", Width: 1, Class: "flash,frontier,heavy,pro"},
			{Name: "bob", Width: 1, Class: "flash,pro"},
		})
		require.NoError(t, err)
		r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{
			{ID: "f1-1", Brief: tierBrief("", "friend ned")},
			{ID: "f1-2", Brief: tierBrief("", "friend ned")},
		}}))
		// the deal places both on ned (one working, one ready); the level of that
		// tick does not see them yet, and the next tick moves the ready one to bob
		tickFriends(r, "ned", "bob")
		tickFriends(r, "ned", "bob")
		rows := map[string]int{}
		for _, id := range []string{"f1-1", "f1-2"} {
			wc := workOf(r, id)
			rows[wc.Row]++
			assert.Equal(t, "flash", packetTier(r, id))
		}
		assert.Equal(t, map[string]int{sprint.FriendRow("ned"): 1, sprint.FriendRow("bob"): 1}, rows,
			"a flash card levels between friends whose tier lists differ")
	})
}
