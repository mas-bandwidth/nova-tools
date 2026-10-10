package sprint

import (
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// THE REPRICE (docs/SPEC-SPRINT.md, "What a card cost", the reprice; the owner, 2026-10-05:
// "Is it possible to fix historical prices for this sprint, or just to fix read prices moving
// forward? More accurate prices allow us to optimize better."). A consumer record keeps its
// tokens by class with the route that priced it (cardcost.Usage), so a route row's prices
// corrected after the fact can be carried back: `nova-sprint cost reprice` runs this step
// once, and every priced consumer record on every primary is computed again from its tokens
// at its route's current prices (Usage.Priced, the sheet copied beside it as at the end):
//
//   - the record's prediction and its sheet's copy are rewritten on the primary, its total
//     moved by the difference (cardcost.Total.Repriced, so records past the list's bound
//     keep their place in it), a landed primary's cost (FieldCost) set to its new charged
//     figure, and each stream's sum on its control card set again from its landed cards;
//   - the harness's own cost, where a record holds one, stays its charged figure (it was
//     never a price of ours): its prediction moves and it is counted held (HeldByActual);
//   - a record that reported no token (unpriced=no-tokens), a subscription read's, one no
//     route priced, and one whose route is no longer in the store have nothing to price
//     from and are left as they are, each counted;
//   - one log line per route says the price_as_of it used, the records, and the old and
//     new sums (RepriceKey).
//
// --route narrows it to the records those routes priced, --since to those that ended at
// or after a time. A record already at its route's prices is not written, so a second
// reprice at the same prices writes nothing.

// RepriceKey is the key of the step's log lines, one per route.
const RepriceKey = "cost reprice"

// RepriceReq is one reprice: the routes (none is every route) and the earliest end (RFC3339,
// "" for every record).
type RepriceReq struct {
	Routes []string `json:"routes,omitempty"`
	Since  string   `json:"since,omitempty"`
	Who    string   `json:"who,omitempty"`
}

// RepriceRoute is what the reprice did to the records one route priced: the sheet's
// price_as_of it used, the records it read and the ones whose prediction moved, and the
// sums before and after, predicted and charged (each record's actual where reported, else
// its predicted), exact decimals, "0" for none.
type RepriceRoute struct {
	Route        string `json:"route"`
	AsOf         string `json:"price_as_of"`
	Records      int    `json:"records"`
	Changed      int    `json:"changed"`
	OldPredicted string `json:"old_predicted_usd"`
	NewPredicted string `json:"new_predicted_usd"`
	OldCharged   string `json:"old_usd"`
	NewCharged   string `json:"new_usd"`
	// HeldByActual counts the records whose charged figure is the harness's own cost.
	HeldByActual int `json:"held_by_actual,omitempty"`
}

// RepriceReport is the reprice's account: each route's, by name, and the records it left.
type RepriceReport struct {
	Routes       []RepriceRoute `json:"routes"`
	NoTokens     int            `json:"no_tokens"`
	Subscription int            `json:"subscription,omitempty"`
	NoRoute      int            `json:"no_route,omitempty"`
	// RouteGone is the records whose route is no longer in the store, by its name.
	RouteGone map[string]int `json:"route_gone,omitempty"`
	// Cut is the records past their cards' list bound (FieldCostCut): in the totals, with
	// no tokens kept to price them by.
	Cut     int `json:"cut,omitempty"`
	Cards   int `json:"cards"`   // primaries rewritten
	Streams int `json:"streams"` // control cards rewritten
}

// RepriceOf is the reprice's plan and its account (see above).
func RepriceOf(s *Snapshot, r RepriceReq) (Plan, RepriceReport) {
	var p Plan
	rep := RepriceReport{RouteGone: map[string]int{}}
	var since time.Time
	if r.Since != "" {
		t, err := time.Parse(time.RFC3339, r.Since)
		if err != nil {
			p.refuse(RepriceKey, fmt.Sprintf("--since %q is not a time: give it as RFC3339, 2026-10-01T00:00:00Z", r.Since))
			return p, rep
		}
		since = t
	}
	routes := map[string]Route{}
	for _, rt := range s.Routes {
		routes[rt.Name] = rt
	}
	for _, name := range r.Routes {
		if _, ok := routes[name]; !ok {
			p.refuse(RepriceKey, fmt.Sprintf("route %s is not in the store: nova-sprint routes lists them", name))
		}
	}
	if len(p.Refused) > 0 {
		return p, rep
	}
	type sums struct {
		RepriceRoute
		oldPred, newPred, oldCharged, newCharged *big.Rat
	}
	by := map[string]*sums{}
	add := func(to *big.Rat, v string) {
		if a, err := amountOf(v); err == nil && a != nil {
			to.Add(to, a)
		}
	}
	charged := func(u cardcost.Usage) string {
		if a, err := amountOf(u.Actual); err == nil && a != nil {
			return u.Actual
		}
		return u.Predicted
	}
	newCost := map[string]string{} // a landed primary's cost after, by id
	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		v := CardCostOf(c)
		if since.IsZero() && len(r.Routes) == 0 {
			rep.Cut += v.Cut
		}
		total := v.Total
		set := map[string]string{}
		for _, con := range v.Consumers {
			if !since.IsZero() {
				if at, err := time.Parse(time.RFC3339, con.At); err != nil || at.Before(since) {
					continue
				}
			}
			u := con.Usage
			switch {
			case u.Unpriced == cardcost.WhyNoTokens || !u.Tokens.Reported():
				rep.NoTokens++
				continue
			case u.Unpriced == WhySubscription:
				rep.Subscription++
				continue
			case u.Route == "":
				rep.NoRoute++
				continue
			}
			if len(r.Routes) > 0 && !slices.Contains(r.Routes, u.Route) {
				continue
			}
			rt, ok := routes[u.Route]
			if !ok {
				rep.RouteGone[u.Route]++
				continue
			}
			now := u.Priced(rt.Name, rt.Prices)
			sm := by[rt.Name]
			if sm == nil {
				sm = &sums{RepriceRoute: RepriceRoute{Route: rt.Name, AsOf: rt.Prices.AsOf},
					oldPred: new(big.Rat), newPred: new(big.Rat), oldCharged: new(big.Rat), newCharged: new(big.Rat)}
				by[rt.Name] = sm
			}
			sm.Records++
			add(sm.oldPred, u.Predicted)
			add(sm.newPred, now.Predicted)
			add(sm.oldCharged, charged(u))
			add(sm.newCharged, charged(now))
			if a, err := amountOf(u.Actual); err == nil && a != nil {
				sm.HeldByActual++
			}
			if now.String() == u.String() {
				continue
			}
			sm.Changed++
			total = total.Repriced(u, now)
			con.Usage = now
			set[FieldCostRecord+con.Key] = con.line()
		}
		if len(set) == 0 {
			continue
		}
		set[FieldCostTotal] = total.String()
		if c.Col == Landed {
			newCost[c.ID] = total.Charged
			if total.Charged != c.F(FieldCost) {
				set[FieldCost] = total.Charged
			}
		}
		rep.Cards++
		var unset []string
		if c.Col == Landed && total.Charged == "" {
			unset = append(unset, FieldCost)
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, set, unset...))},
			Moved: c.ID + " repriced"})
	}
	// each stream's sum over its landed cards, set again where one of them moved
	if len(newCost) > 0 && s.Merge != nil {
		for _, st := range s.Work.Rows() {
			ctl := s.StreamCtl(st)
			if ctl == nil {
				continue
			}
			moved := false
			var sum []string
			for _, c := range s.Work.Cell(st, Landed) {
				v, ok := newCost[c.ID]
				moved = moved || ok
				if !ok {
					v = c.F(FieldCost)
				}
				if v != "" {
					sum = append(sum, v)
				}
			}
			total, ok := cardcost.Sum(sum...)
			if !moved || !ok || total == ctl.F(FieldCost) {
				continue
			}
			var unset []string
			if len(sum) == 0 {
				unset = append(unset, FieldCost)
				total = ""
			}
			rep.Streams++
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: st, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{FieldCost: total}, unset...))},
				Moved: st + " cost repriced"})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(by)) {
		sm := by[name]
		rr := sm.RepriceRoute
		rr.OldPredicted, rr.NewPredicted = cardcost.Text(sm.oldPred), cardcost.Text(sm.newPred)
		rr.OldCharged, rr.NewCharged = cardcost.Text(sm.oldCharged), cardcost.Text(sm.newCharged)
		rep.Routes = append(rep.Routes, rr)
		p.Units = append(p.Units, Unit{Key: RepriceKey, Moved: rr.Line()})
	}
	if len(rep.RouteGone) == 0 {
		rep.RouteGone = nil
	}
	return p, rep
}

// Line is the route's log line: the price_as_of it used, the records, and the sums.
func (rr RepriceRoute) Line() string {
	held := ""
	if rr.HeldByActual > 0 {
		held = fmt.Sprintf(" held_by_actual=%d", rr.HeldByActual)
	}
	return fmt.Sprintf("reprice route=%s price_as_of=%s records=%d changed=%d old_usd=%s new_usd=%s old_predicted_usd=%s new_predicted_usd=%s%s",
		rr.Route, orDash(strings.Join(strings.Fields(rr.AsOf), "_")), rr.Records, rr.Changed, rr.OldCharged, rr.NewCharged, rr.OldPredicted, rr.NewPredicted, held)
}
