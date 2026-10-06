package sprint_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheDealHandsOnlyTiersOfHerModels: a friend's class is the tier of her strongest
// model and the tiers she may be dealt are the tiers of all her models (nova-config's
// FriendClass, which friend sync copies to the friends table). On the twin: zhi lists one
// pro model, johnny a heavy then a pro model, and amy and bob are pro. A flash card for any
// friend is dealt to none of them and goes to the fleet; a heavy card goes to johnny; a pro card pinned to johnny goes to him,
// though his class is heavy, because a model of his is pro; and the friends table and its
// seats carry each friend's class, tiers and models.
func TestTheDealHandsOnlyTiersOfHerModels(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
		{Name: "amy", Width: 2, Class: "pro"}, {Name: "bob", Width: 2, Class: "pro"}, // the rig's, a roster from before friend sync copied tiers
		{Name: "zhi", Width: 2, Class: "pro", Tiers: []string{"pro"}, Models: []string{"deepseek-v4"}},
		{Name: "johnny", Width: 2, Class: "heavy", Tiers: []string{"heavy", "pro"}, Models: []string{"grok-4-7-xhigh", "grok-4-7"}},
	})
	require.NoError(t, err)
	for _, f := range []string{"zhi", "johnny"} {
		_, err = r.st.FriendBeat(r.ctx, f)
		require.NoError(t, err)
		_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(t, err)
	}
	friend := func(tier, who string) string {
		return "c: a friend's card tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
	}
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{
		{ID: "f1-1", Brief: friend("flash", "only friend")},
		{ID: "f1-2", Brief: friend("heavy", "only friend")},
		{ID: "f1-3", Brief: friend("pro", "only friend johnny")},
	}}))
	r.must(store.BriefStep(sprint.BriefReq{ID: "f1-1", Tier: "flash", Who: "coordinator"}))
	r.must(store.BriefStep(sprint.BriefReq{ID: "f1-2", Tier: "heavy", Who: "coordinator"}))
	r.must(store.BriefStep(sprint.BriefReq{ID: "f1-3", Tier: "pro", Who: "coordinator"}))
	r.tick()

	s := r.snap()
	var dealt []*sprint.Card
	for _, row := range s.Fleet.Rows() {
		for _, col := range []sprint.State{sprint.Ready, sprint.Working} {
			dealt = append(dealt, s.Fleet.Cell(row, col)...)
		}
	}
	packets, err := r.st.Packets(r.ctx, dealt)
	require.NoError(t, err)
	rows := map[string]string{}
	for i, p := range packets {
		rows[p.Primary] = dealt[i].Row
	}
	rowOf := func(primary string) string { return rows[primary] }
	_, onFriend := sprint.FriendOfRow(rowOf("f1-1"))
	assert.False(t, onFriend, "a flash card is no friend's, none listing a flash model: it goes to the fleet (on %s)", rowOf("f1-1"))
	assert.Equal(t, sprint.FriendRow("johnny"), rowOf("f1-2"), "the heavy card goes to johnny, whose strongest model is heavy")
	assert.Equal(t, sprint.FriendRow("johnny"), rowOf("f1-3"), "the pro card pinned to johnny goes to him: his second model is pro")

	seats, err := r.st.FriendSeats(r.ctx, r.st.Now())
	require.NoError(t, err)
	got := map[string]sprint.FriendSeat{}
	for _, f := range seats {
		got[f.Name] = f
	}
	assert.Equal(t, "heavy", got["johnny"].Class)
	assert.Equal(t, []string{"heavy", "pro"}, got["johnny"].Tiers)
	assert.Equal(t, []string{"pro"}, got["zhi"].Tiers)
	friends, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(t, err)
	for _, f := range friends {
		if f.Name == "johnny" {
			assert.Equal(t, []string{"grok-4-7-xhigh", "grok-4-7"}, f.Models, "her models, strongest first")
			assert.Equal(t, []string{"heavy", "pro"}, f.Tiers)
		}
	}
}
