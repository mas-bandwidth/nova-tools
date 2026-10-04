package sprint

import (
	"cmp"
	"errors"
	"math/big"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Cost visibility (docs/SPEC-SPRINT.md section 1, the where view; the owner, 2026-10-04):
// what the sprint spends by tier and per landed card, from the cost records the cards
// already hold (store.TierCosts reads them for where): the cards' tiers from their
// briefs' tier lines, each stream's dollars per landed card, and each stream's spend
// split by the tier its attempts ran on (the cost records' tier, not the card's ceiling:
// a flash card escalated to pro shows both).

// TierCosts is one stream's tiers and spend as the where view carries them.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is the stream's landed cards' cost per landed card, dollars and cents
	// rounded up (MoneyText); "-" with nothing landed or nothing priced.
	PerLanded string `json:"per_landed"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, dollars
	// and cents rounded up, over every card of the stream; a record with no tier of its own
	// takes its route's (RouteTierOf). Only tiers are keys: there is no bucket for a record
	// whose tier cannot be found.
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// NoTierCost is a diagnostic, never a tier: the spend of the stream's cost records whose
	// tier could not be found (no tier of their own, and a route the route table does not
	// know whose name names no tier), dollars and cents rounded up; NoTierRoutes are those
	// records' route names, "-" for a record with none. Every route has a tier, so either is
	// a data bug to trace.
	NoTierCost   string   `json:"cost_no_tier,omitempty"`
	NoTierRoutes []string `json:"no_tier_routes,omitempty"`
}

// TierWord is the tier a card's brief names on its line 1 (any word the brief carries),
// flash when it names none.
func TierWord(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return cmp.Or(m.Tier, cardhdr.RouteFlash)
}

// TierCounts counts every primary on the work table (sentinels left out) by tier
// (TierWord).
func TierCounts(s *Snapshot) map[string]int {
	out := map[string]int{}
	for _, c := range s.Work.Column(States...) {
		if !IsSentinel(c) {
			out[TierWord(c)]++
		}
	}
	return out
}

// StreamTierCosts is each stream's TierCosts, by stream, over the work table's rows. A
// cost record with no tier of its own (one written before records carried it) takes its
// route's: routes names each route's tier (the route table), else the route name's first
// word when that is a tier (pro-grok47-opencode is pro); a record neither names is left
// out of the tiers and counted in NoTierCost.
func StreamTierCosts(s *Snapshot, routes map[string]string) map[string]TierCosts {
	out := map[string]TierCosts{}
	for _, st := range s.Work.Rows() {
		out[st] = streamTierCosts(s, st, routes)
	}
	return out
}

// tierWords are the tiers a route name may begin with (RouteTierOf).
var tierWords = []string{cardhdr.RouteFlash, cardhdr.RoutePro, "heavy", cardhdr.RouteFrontier}

// RouteTierOf is a cost record's tier: its own, else its route's in routes, else its
// route name's first word when that is a tier; ok false when none of them names one.
func RouteTierOf(con Consumer, routes map[string]string) (tier string, ok bool) {
	if con.Tier != "" {
		return con.Tier, true
	}
	if t := routes[con.Route]; t != "" {
		return t, true
	}
	if w, _, cut := strings.Cut(con.Route, "-"); cut && slices.Contains(tierWords, w) {
		return w, true
	}
	return "", false
}

func streamTierCosts(s *Snapshot, stream string, routes map[string]string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}}
	byTier := map[string]*big.Rat{}
	noTier, noRoutes := new(big.Rat), map[string]bool{}
	var landedCost []string
	landed := 0
	for _, col := range States {
		for _, c := range s.Work.Cell(stream, col) {
			if IsSentinel(c) {
				continue
			}
			t.Tiers[TierWord(c)]++
			if col == Landed {
				landed++
				if v := c.F(FieldCost); v != "" {
					landedCost = append(landedCost, v)
				}
			}
			for _, con := range CardCostOf(c).Consumers {
				usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted))
				if err != nil || usd == nil {
					continue
				}
				tier, ok := RouteTierOf(con, routes)
				if !ok {
					noTier.Add(noTier, usd)
					noRoutes[cmp.Or(con.Route, "-")] = true
					continue
				}
				if byTier[tier] == nil {
					byTier[tier] = new(big.Rat)
				}
				byTier[tier].Add(byTier[tier], usd)
			}
		}
	}
	if sum, ok := cardcost.Sum(landedCost...); ok && landed > 0 && len(landedCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.PerLanded = cardcost.Cents(total.Quo(total, big.NewRat(int64(landed), 1)))
		}
	}
	if len(noRoutes) > 0 {
		t.NoTierCost = cardcost.Cents(noTier)
		for r := range noRoutes {
			t.NoTierRoutes = append(t.NoTierRoutes, r)
		}
		sort.Strings(t.NoTierRoutes)
	}
	tiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		t.CostByTier[tier] = cardcost.Cents(byTier[tier])
	}
	return t
}

// errBadAmount is a dollar amount that is no decimal.
var errBadAmount = errors.New("not a decimal amount")

// amountOf is a decimal dollar string as a rational, nil for an empty one.
func amountOf(usd string) (*big.Rat, error) {
	if usd == "" {
		return nil, nil
	}
	r, ok := new(big.Rat).SetString(usd)
	if !ok {
		return nil, errBadAmount
	}
	return r, nil
}

// PerLandedOf is a stream's dollars per landed card from the where view's cells alone
// (the cost cell, MoneyText, over the landed count): what the text table shows when the
// tick's record is not there; "-" with nothing landed or no cost.
func PerLandedOf(costCell string, landed int) string {
	if landed <= 0 || len(costCell) < 2 || costCell[0] != '$' {
		return "-"
	}
	r, err := amountOf(costCell[1:])
	if err != nil || r == nil {
		return "-"
	}
	return cardcost.Cents(r.Quo(r, big.NewRat(int64(landed), 1)))
}
