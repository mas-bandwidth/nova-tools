package sprint_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Heavy and pro cards go to the friends billed by subscription first; the api friends and the
// fleet's routes take only what is left (docs/SPEC-SPRINT.md section 1,
// deal-subscription-first-r-t-b.w2). On the twin store with a pro route and two members up,
// amy billed by subscription (width 2: room 4) and bob billed at API rates (width 2): five pro
// cards go four to amy and one elsewhere, a card naming bob keeps him, and a flash card is
// dealt as before, an idle lane first.
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	mem := r.st.B.(*store.Mem)
	mem.SetRoutes([]sprint.Route{{Name: "pro-a", Tier: "pro", Provider: "provider-a", Enabled: true}})
	mem.SetTiers(map[string][]string{"pro": {"pro-a"}})
	brief := func(tier, who string) string {
		b := "c: work tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\n"
		if who != "" {
			b += "WHO: " + who + "\n"
		}
		return b + "\nThe task."
	}
	// stopped, the adds write the work table themselves rather than queue for the next tick
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	var pro []sprint.CardAdd
	for _, id := range []string{"p1-1", "p1-2", "p1-3", "p1-4", "p1-5"} {
		pro = append(pro, sprint.CardAdd{ID: id, Brief: brief("pro", "")})
	}
	r.must(store.AddStep(sprint.AddReq{Stream: "p1", Cards: pro}))
	r.must(store.AddStep(sprint.AddReq{Stream: "w1", Cards: []sprint.CardAdd{{ID: "w1-1", Brief: brief("pro", "friend bob")}}}))
	r.beat()
	require.Len(t, r.snap().Work.Column(sprint.Ready), 6, "the six cards are ready")
	seats, err := r.st.FriendSeats(r.ctx, r.st.Now())
	require.NoError(t, err)
	billing := map[string]string{"amy": config.BillingSubscription, "bob": config.BillingAPI}
	for i := range seats {
		require.Equal(t, sprint.Up, seats[i].Status, "friend %s is up", seats[i].Name)
		seats[i].Billing = billing[seats[i].Name]
	}

	rows := func(p sprint.Plan) map[string]string {
		out := map[string]string{}
		for _, u := range p.Units {
			for _, c := range u.Changes {
				if c.Table == sprint.Fleet && c.Entry.Create != nil {
					out[u.Key] = c.Entry.Create.Row
				}
			}
		}
		return out
	}

	t.Run("pro cards fill the subscription friend first", func(t *testing.T) {
		t.Parallel()
		p, _ := sprint.TickDeal(r.snap(), sprint.TickReq{Friends: seats})
		got := rows(p)
		onAmy, elsewhere := 0, 0
		for _, id := range []string{"p1-1", "p1-2", "p1-3", "p1-4", "p1-5"} {
			row, ok := got[id]
			require.True(t, ok, "%s is dealt: %v", id, got)
			if row == sprint.FriendRow("amy") {
				onAmy++
			} else {
				elsewhere++
			}
		}
		assert.Equal(t, 4, onAmy, "four pro cards on the subscription friend: %v", got)
		assert.Equal(t, 1, elsewhere, "one pro card elsewhere: %v", got)
		assert.Equal(t, sprint.FriendRow("bob"), got["w1-1"], "a card naming the api friend keeps him: %v", got)
	})

	t.Run("a flash card is dealt as before", func(t *testing.T) {
		t.Parallel()
		// the billings swapped: amy, first by name, at API rates; a flash card is no
		// subscription card, so equal lanes and room still give it to her
		swapped := map[string]string{"amy": config.BillingAPI, "bob": config.BillingSubscription}
		var flash []sprint.FriendSeat
		for _, f := range seats {
			f.Tiers, f.Billing = []string{"flash"}, swapped[f.Name]
			flash = append(flash, f)
		}
		c := &sprint.Card{ID: "x-1", Row: "x", Col: sprint.Ready, Fields: map[string]string{"brief": brief("flash", "")}}
		got := rows(sprint.FriendDeal(r.snap(), []*sprint.Card{c}, flash))
		assert.Equal(t, sprint.FriendRow("amy"), got["x-1"], "billing is not read for a flash card: %v", got)
	})
}
