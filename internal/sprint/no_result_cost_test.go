package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// TestARunWithNoResultIsPricedOrCarriesItsReason is the pure terminal-finish cost
// contract on the in-memory twin (docs/SPEC-SPRINT.md, "Every run's cost, whatever
// its end"): a run that ended without a result is still billed by the provider, so
// its record is priced from the usage that came, else from the provider's
// per-request quote it carried, else from the launch prompt's own bytes; when no
// price can be named the record carries the reason and the stream counts it. No
// path invents a zero-dollar charge.
func TestARunWithNoResultIsPricedOrCarriesItsReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, usage, charged, why string
		sheet                     bool
		estimated                 bool
	}{
		{"reported usage", "input=500000 output=50000", "1", "", true, false},
		{"provider quote", "generation_id=gen-one generation_input=500000 generation_output=50000 generation_usd=0.75", "0.75", "", true, false},
		{"prompt estimate", "prompt_bytes=400", "0.0001", "", true, true},
		{"no price table", "prompt_bytes=400", "", cardcost.WhyNoSheet, false, false},
		{"actual without tokens", "actual_usd=0.5 actual_by=harness", "0.5", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			route := pricedRoute
			if !tc.sheet {
				route.Prices = cardcost.Prices{}
			}
			w.s.Routes = []Route{route}
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
			c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
			w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: true, Report: "no result: provider ended", Usage: tc.usage}))
			view := CardCostOf(w.s.Work.Card("s1-1"))
			require.Len(t, view.Consumers, 1)
			u := view.Consumers[0].Usage
			assert.Equal(t, tc.charged, view.Total.Charged)
			assert.Equal(t, tc.why, u.Unpriced)
			assert.Equal(t, tc.estimated, strings.Contains(u.String(), "estimated=yes"))
			stream := StreamTierCosts(w.s)["s1"]
			if tc.charged == "" {
				assert.Equal(t, 1, stream.UnpricedRuns)
			} else {
				assert.Zero(t, stream.UnpricedRuns)
				assert.Equal(t, MoneyText(tc.charged), stream.TotalCost)
			}
		})
	}
}

// TestAReapedRunIsChargedInTheStream is the reap's record (docs/SPEC-SPRINT.md,
// "Terminal cost records"): a moved or dropped claim is its own charged
// consumer, in the stream total, and in per_landed only once the primary is
// landed. A primary that has left the table keeps the charge on the control
// card. A different epoch writes nothing.
func TestAReapedRunIsChargedInTheStream(t *testing.T) {
	t.Parallel()
	quote := "generation_id=gen-one generation_input=500000 generation_output=50000 generation_usd=0.75"

	t.Run("placed", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Routes = []Route{pricedRoute}
		w.must(CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.w1", Stream: "s1", Kind: "work", Who: "m1",
			Route: "pro-a", Model: pricedRoute.Model, Tier: "pro", Gen: 1, Attempt: 1, Epoch: w.s.Epoch,
			Usage: "input=500000 output=50000", Reason: "claim-moved"}))
		pr := w.s.Work.Card("s1-1")
		view := CardCostOf(pr)
		require.Len(t, view.Consumers, 1)
		assert.Equal(t, "s1-1.w1#g1#reaped", view.Consumers[0].Key)
		assert.Equal(t, "reaped", view.Consumers[0].End)
		assert.Contains(t, view.Consumers[0].Usage.Extra, "reap=claim-moved")
		assert.Equal(t, "1", view.Total.Charged)
		assert.Empty(t, pr.F(FieldCost), "a card that has not landed has no landed cost")
		assert.Empty(t, w.s.StreamCtl("s1").F(FieldCost))
		stream := StreamTierCosts(w.s)["s1"]
		assert.Equal(t, MoneyText("1"), stream.TotalCost)
		assert.Equal(t, "-", stream.PerLanded)
		again := CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.w1", Stream: "s1", Kind: "work", Who: "m1",
			Route: "pro-a", Gen: 1, Attempt: 1, Epoch: w.s.Epoch, Usage: "input=500000 output=50000", Reason: "claim-moved"})
		assert.Empty(t, again.Units, "the same claim is not charged twice")
	})

	t.Run("landed", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Routes = []Route{pricedRoute}
		pr := w.s.Work.Card("s1-1")
		book(pr, Consumer{Kind: "work", Card: "s1-1.w1", Attempt: 1, Gen: 1, Who: "m1", Route: "pro-a", Tier: "pro", End: "ok",
			At: stamp(w.s.Now), Key: "s1-1.w1#g1", Usage: cardcost.ParseUsage("input=500000 output=50000").Priced(pricedRoute.Name, pricedRoute.Prices)})
		charged := cardcost.ParseTotal(pr.F(FieldCostTotal)).Charged
		require.Equal(t, "1", charged)
		pr.Col = Landed
		pr.Fields[FieldCost] = charged
		w.s.Work.Put(pr)
		ctl := w.s.StreamCtl("s1")
		require.NotNil(t, ctl)
		if ctl.Fields == nil {
			ctl.Fields = map[string]string{}
		}
		ctl.Fields[FieldCost] = charged
		w.s.Merge.Put(ctl)
		w.must(CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.w1", Stream: "s1", Kind: "work", Who: "m1",
			Route: "pro-a", Model: pricedRoute.Model, Tier: "pro", Gen: 2, Attempt: 2, Epoch: w.s.Epoch,
			Usage: quote, Reason: "claim-moved"}))
		pr = w.s.Work.Card("s1-1")
		assert.Equal(t, "1.75", cardcost.ParseTotal(pr.F(FieldCostTotal)).Charged)
		assert.Equal(t, "1.75", pr.F(FieldCost))
		assert.Equal(t, "1.75", w.s.StreamCtl("s1").F(FieldCost))
		var found bool
		for _, con := range CardCostOf(pr).Consumers {
			if con.Key == "s1-1.w1#g2#reaped" {
				found = true
				assert.Contains(t, con.Usage.Extra, "reap=claim-moved")
				assert.Equal(t, "0.75", con.Usage.Actual)
			}
		}
		require.True(t, found, "the reaped generation is its own record")
		stream := StreamTierCosts(w.s)["s1"]
		assert.Equal(t, MoneyText("1.75"), stream.TotalCost)
		assert.Equal(t, MoneyText("1.75"), stream.PerLanded)
	})

	t.Run("dropped", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Routes = []Route{pricedRoute}
		w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "left the table", Who: "m1"}))
		require.Nil(t, w.s.Work.Placed("s1-1"))
		require.True(t, w.s.Work.HasRow("s1"))
		require.NotNil(t, w.s.StreamCtl("s1"))
		w.must(CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.w1", Stream: "s1", Kind: "work", Who: "m1",
			Route: "pro-a", Tier: "pro", Gen: 4, Attempt: 1, Epoch: w.s.Epoch, Usage: quote, Reason: "dropped"}))
		ctl := w.s.StreamCtl("s1")
		assert.Empty(t, ctl.F(FieldCost), "the control card's cost stays the landed sum")
		view := CardCostOf(ctl)
		require.Len(t, view.Consumers, 1)
		assert.Equal(t, "s1-1.w1#g4#reaped", view.Consumers[0].Key)
		assert.Equal(t, "reaped", view.Consumers[0].End)
		assert.Contains(t, view.Consumers[0].Usage.Extra, "reap=dropped")
		assert.Equal(t, "0.75", view.Total.Charged)
		stream := StreamTierCosts(w.s)["s1"]
		assert.Equal(t, MoneyText("0.75"), stream.TotalCost)
		assert.Equal(t, "-", stream.PerLanded)
	})

	t.Run("read", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Routes = []Route{pricedRoute}
		w.must(CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.r1", Stream: "s1", Kind: "read", Who: "reader-a",
			Route: "pro-a", Tier: "pro", Gen: 1, Attempt: 3, Epoch: w.s.Epoch, Usage: quote, Reason: "dropped"}))
		view := CardCostOf(w.s.Work.Card("s1-1"))
		require.Len(t, view.Consumers, 1)
		assert.Equal(t, "s1-1.r1#a3#reaped", view.Consumers[0].Key)
		assert.Equal(t, "read", view.Consumers[0].Kind)
		assert.Equal(t, "0.75", view.Total.Charged)
	})

	t.Run("cleared epoch", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Routes = []Route{pricedRoute}
		before := w.s.Work.Card("s1-1").F(FieldCostTotal)
		p := CostReap(w.s, CostReapReq{Primary: "s1-1", Card: "s1-1.w1", Stream: "s1", Kind: "work", Who: "m1",
			Route: "pro-a", Gen: 1, Attempt: 1, Epoch: w.s.Epoch + 1, Usage: quote, Reason: "claim-moved"})
		assert.Empty(t, p.Units)
		assert.Empty(t, p.Refused)
		assert.Equal(t, before, w.s.Work.Card("s1-1").F(FieldCostTotal))
	})
}
