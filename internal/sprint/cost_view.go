package sprint

import (
	"cmp"
	"errors"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Cost visibility (docs/SPEC-SPRINT.md section 1, the where view; the owner, 2026-10-04):
// what the sprint spends by tier and per landed card, counted by the tick from the
// sprint it reads anyway (the where record, store/where.go) and never from per-card
// reads at `where`: the cards' tiers from their briefs' tier lines, each stream's
// dollars per landed card, and each stream's spend split by the tier its attempts ran
// on (the cost records' tier, not the card's ceiling: a flash card escalated to pro
// shows both).

// TierCosts is one stream's tiers and spend as the where view carries them.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is the stream's landed cards' cost per landed card, dollars and cents
	// rounded up (MoneyText); "-" with nothing landed or nothing priced.
	PerLanded string `json:"per_landed"`
	// CostByTier is the stream's spend by the tier each attempt and read ran on, over every
	// card of the stream, accounting for every dollar of TotalCost: a record with no tier
	// takes its route's (RunTier), the records past a card's list bound take the card's, and
	// the cents are split so the tiers sum to TotalCost exactly (splitCents); "untiered" only
	// for a record with no tier, no route and a card with none.
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
	// TotalCost is the stream's complete recorded spend: every take and read of every card
	// of it in any column, landed or not, at each record's charged figure (the harness's
	// cost, else its tokens at the route's prices), dollars and cents rounded up; "" when
	// nothing of it was priced.
	TotalCost string `json:"total_cost,omitempty"`
	// UnpricedRuns counts the stream's records that carry no cost at all: runs whose usage
	// never reached the sprint (cardcost.WhyNoTokens), which the total cannot hold.
	UnpricedRuns int `json:"unpriced_runs,omitempty"`
	// Unreconciled is the SPRINT's, the same on every stream's record: what the providers
	// counted beyond the sprint's records since the epoch began (UnreconciledSpend,
	// cost_reconcile.go), dollars and cents rounded up; "" when nothing is.
	Unreconciled string `json:"unreconciled,omitempty"`
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

// StreamTierCosts is each stream's TierCosts, by stream, over the work table's rows.
func StreamTierCosts(s *Snapshot) map[string]TierCosts {
	out := map[string]TierCosts{}
	for _, st := range s.Work.Rows() {
		out[st] = streamTierCosts(s, st)
	}
	return out
}

func streamTierCosts(s *Snapshot, stream string) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}}
	routes := map[string]string{}
	for _, r := range s.Routes {
		routes[r.Name] = r.Tier
	}
	byTier := map[string]*big.Rat{}
	add := func(tier string, usd *big.Rat) {
		if byTier[tier] == nil {
			byTier[tier] = new(big.Rat)
		}
		byTier[tier].Add(byTier[tier], usd)
	}
	var landedCost []string
	var allCost []string
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
			// the card's whole record, whatever its column: every take and read behind it,
			// the records past the list's bound included (FieldCostTotal)
			cc := CardCostOf(c)
			tot := cc.Total
			if tot.Charged != "" {
				allCost = append(allCost, tot.Charged)
			}
			t.UnpricedRuns += tot.Records - tot.ChargedOf
			listed := new(big.Rat)
			for _, con := range cc.Consumers {
				usd, err := amountOf(cmp.Or(con.Usage.Actual, con.Usage.Predicted))
				if err != nil || usd == nil {
					continue
				}
				listed.Add(listed, usd)
				add(RunTier(routes, con, c), usd)
			}
			// the records past the list's bound are in the card's total and in no list: they
			// ran on the card's tier, and without them the tiers would not sum to the total
			if all, err := amountOf(tot.Charged); err == nil && all != nil {
				if rest := new(big.Rat).Sub(all, listed); rest.Sign() != 0 {
					add(cmp.Or(attemptTier(c), untiered), rest)
				}
			}
		}
	}
	if sum, ok := cardcost.Sum(landedCost...); ok && landed > 0 && len(landedCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.PerLanded = cardcost.Cents(total.Quo(total, big.NewRat(int64(landed), 1)))
		}
	}
	if sum, ok := cardcost.Sum(allCost...); ok && len(allCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.TotalCost = cardcost.Cents(total)
		}
	}
	maps.Copy(t.CostByTier, splitCents(byTier))
	if unrec := UnreconciledSpend(s); unrec > 0 {
		t.Unreconciled = cardcost.Cents(new(big.Rat).SetFloat64(unrec))
	}
	return t
}

// untiered is the tier of a run with no tier, no route and a card with none.
const untiered = "untiered"

// RunTier is the tier a cost record ran on: the tier it recorded, else its route's (the
// route row's tier, by name in routes, else the tier the route's name begins with: pro-*,
// flash-*, heavy-*, frontier-*), else the card's tier (attemptTier), else "untiered".
func RunTier(routes map[string]string, con Consumer, c *Card) string {
	if con.Tier != "" {
		return con.Tier
	}
	if r := cmp.Or(con.Route, con.Usage.Route); r != "" {
		if tier := routes[r]; tier != "" {
			return tier
		}
		for _, tier := range cardhdr.Routes {
			if strings.HasPrefix(r, tier+"-") {
				return tier
			}
		}
	}
	return cmp.Or(attemptTier(c), untiered)
}

// attemptTier is the tier the card's attempt ran on: the tier it is pinned to, else the tier
// the deal put it on; "" when it has neither.
func attemptTier(c *Card) string {
	if c == nil {
		return ""
	}
	return cmp.Or(c.F(FieldTier), c.F(FieldTierNow))
}

// splitCents is each tier's dollars as money strings that sum to the whole rounded up to
// the cent (cardcost.Cents of the sum): each tier's cents rounded down, and the cents left
// given one each to the tiers with the largest remainders, ties by name.
func splitCents(byTier map[string]*big.Rat) map[string]string {
	out := map[string]string{}
	if len(byTier) == 0 {
		return out
	}
	hundred := big.NewRat(100, 1)
	whole := new(big.Rat)
	type part struct {
		tier  string
		cents *big.Int
		frac  *big.Rat
	}
	var parts []part
	given := new(big.Int)
	for _, tier := range slices.Sorted(maps.Keys(byTier)) {
		c := new(big.Rat).Mul(byTier[tier], hundred)
		whole.Add(whole, c)
		down := new(big.Int).Div(c.Num(), c.Denom()) // floor, for a negative part too
		parts = append(parts, part{tier, down, new(big.Rat).Sub(c, new(big.Rat).SetInt(down))})
		given.Add(given, down)
	}
	want := new(big.Int).Div(whole.Num(), whole.Denom())
	if !whole.IsInt() {
		want.Add(want, big.NewInt(1))
	}
	left := new(big.Int).Sub(want, given).Int64()
	order := slices.Clone(parts)
	slices.SortStableFunc(order, func(a, b part) int { return b.frac.Cmp(a.frac) })
	extra := map[string]int64{}
	for i := 0; left > 0 && len(order) > 0; i, left = i+1, left-1 {
		extra[order[i%len(order)].tier]++
	}
	for _, p := range parts {
		c := new(big.Int).Add(p.cents, big.NewInt(extra[p.tier]))
		out[p.tier] = "$" + new(big.Rat).SetFrac(c, big.NewInt(100)).FloatString(2)
	}
	return out
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
