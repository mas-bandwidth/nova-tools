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
// briefs' tier lines, each stream's dollars per landed card, and each stream's landed
// spend split by the tier its attempts and reads ran on (the cost records' tier, not the
// card's ceiling: a flash card escalated to pro shows both).
//
// The split is of the work row's cost cell, the stream's landed cards' costs (FieldCost):
// every cent of it is in exactly one of the four tiers, so the tiers add up to the cost
// (the owner, 2026-10-04 4:20 PM: "status shows pro $5, flash $2, total $88"). There is no
// other bucket: a tier is a class every dollar has (the owner, 2026-10-04 2:47 PM, "that's
// not a thing").

// TierCosts is one stream's tiers and spend as the where view carries them.
type TierCosts struct {
	// Tiers counts the stream's cards by the tier their briefs name (TierWord).
	Tiers map[string]int `json:"tiers,omitempty"`
	// PerLanded is the stream's landed cards' cost per landed card, dollars and cents
	// rounded up (MoneyText); "-" with nothing landed or nothing priced.
	PerLanded string `json:"per_landed"`
	// CostByTier is the stream's landed cost (the sum of its landed cards' FieldCost) by
	// the tier each attempt and read ran on (RecordTier), in dollars and cents that add up
	// to the landed cost as MoneyText shows it (SplitCents). Only the four tiers are keys.
	CostByTier map[string]string `json:"cost_by_tier,omitempty"`
}

// Tiers are the four tiers every dollar is spent on, cheapest first (the owner,
// 2026-10-04: flash, pro, heavy, frontier).
var Tiers = []string{cardhdr.RouteFlash, cardhdr.RoutePro, TierHeavy, cardhdr.RouteFrontier}

// TierHeavy is the third tier: the headless subscription harnesses (Opus, Sol, Grok).
const TierHeavy = "heavy"

// IsTier says whether a word is one of the four tiers.
func IsTier(w string) bool { return slices.Contains(Tiers, w) }

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

// TierRules are what a cost record's tier is found from when it carries none: the route
// table's tier of each route by name (Routes) and of each model it runs, provider/model
// (Models; a model two tiers' routes run names neither).
type TierRules struct {
	Routes map[string]string
	Models map[string]string
}

// RulesOf are the tier rules of a route table.
func RulesOf(routes []Route) TierRules {
	r := TierRules{Routes: map[string]string{}, Models: map[string]string{}}
	split := map[string]bool{}
	for _, rt := range routes {
		if !IsTier(rt.Tier) {
			continue
		}
		r.Routes[rt.Name] = rt.Tier
		m := rt.Provider + "/" + rt.Model
		if t, ok := r.Models[m]; ok && t != rt.Tier {
			split[m] = true
		}
		r.Models[m] = rt.Tier
	}
	for m := range split {
		delete(r.Models, m)
	}
	return r
}

// modelClasses are the model families whose class is a tier wherever they run, for a
// record whose route and model the route table does not know (the owner, 2026-10-04,
// the tier ladder: Opus and Sonnet headless are heavy; Fable, Astra and Argon frontier).
var modelClasses = []struct{ word, tier string }{
	{"fable", cardhdr.RouteFrontier}, {"astra", cardhdr.RouteFrontier}, {"argon", cardhdr.RouteFrontier}, {"mythos", cardhdr.RouteFrontier},
	{"opus", TierHeavy}, {"sonnet", TierHeavy},
}

// ModelClass is a model's tier by its family (modelClasses), "" for a family that has none.
func ModelClass(model string) string {
	m := strings.ToLower(model)
	for _, c := range modelClasses {
		if strings.Contains(m, c.word) {
			return c.tier
		}
	}
	return ""
}

// CardTier is the tier a primary is on: the tier the machine last dealt it on
// (FieldTierNow), else the tier it was pinned to (FieldTier), else its brief's (TierWord).
func CardTier(pr *Card) string {
	for _, t := range []string{pr.F(FieldTierNow), pr.F(FieldTier)} {
		if IsTier(t) {
			return t
		}
	}
	return TierWord(pr)
}

// RecordTier is a cost record's tier, always one of the four: its own; else its route's
// in the route table; else its route name's first word when that is a tier
// (pro-grok47-opencode is pro); else its model's in the route table; else its model's
// family's (ModelClass); else the tier of the card it was spent for (CardTier). byCard
// is false when the record named none of its own, its route's or its model's, and the
// card's tier was taken.
func RecordTier(con Consumer, pr *Card, r TierRules) (tier string, byCard bool) {
	if IsTier(con.Tier) {
		return con.Tier, false
	}
	if t := r.Routes[con.Route]; t != "" {
		return t, false
	}
	if w, _, cut := strings.Cut(con.Route, "-"); cut && IsTier(w) {
		return w, false
	}
	if t := r.Models[con.Model]; t != "" {
		return t, false
	}
	if t := ModelClass(con.Model); t != "" {
		return t, false
	}
	return CardTier(pr), true
}

// ChargedOf is a record's one figure, as the card's total sums it (cardcost.Total.Add): its
// actual cost where that is an amount, else its predicted one; nil when neither is.
func ChargedOf(u cardcost.Usage) *big.Rat {
	for _, v := range []string{u.Actual, u.Predicted} {
		if r, err := amountOf(v); err == nil && r != nil {
			return r
		}
	}
	return nil
}

// CardTierSpend is a landed primary's cost (FieldCost) by tier: each record's figure
// (ChargedOf) on its tier (RecordTier), and what the cost holds that no listed record
// does (the records past MaxCostRecords, in the total and not in the list; FieldCostCut)
// on the card's tier (CardTier). The tiers add up to the cost exactly.
func CardTierSpend(pr *Card, r TierRules) map[string]*big.Rat {
	out := map[string]*big.Rat{}
	add := func(tier string, v *big.Rat) {
		if out[tier] == nil {
			out[tier] = new(big.Rat)
		}
		out[tier].Add(out[tier], v)
	}
	cost, err := amountOf(pr.F(FieldCost))
	if err != nil || cost == nil {
		return out
	}
	rest := new(big.Rat).Set(cost)
	for _, con := range CardCostOf(pr).Consumers {
		v := ChargedOf(con.Usage)
		if v == nil {
			continue
		}
		tier, _ := RecordTier(con, pr, r)
		add(tier, v)
		rest.Sub(rest, v)
	}
	if rest.Sign() != 0 {
		add(CardTier(pr), rest)
	}
	return out
}

// SplitCents is amounts in dollars and cents that add up to their sum as MoneyText shows
// it (rounded up to the cent): each amount's whole cents, then the cents the rounded sum
// has left over one each to the amounts with the largest remainders (the earlier tier on
// a tie). An amount at no cent is left out.
func SplitCents(by map[string]*big.Rat) map[string]string {
	sum := new(big.Rat)
	for _, v := range by {
		sum.Add(sum, v)
	}
	hundred := big.NewInt(100)
	type part struct {
		tier  string
		cents *big.Int
		rem   *big.Rat
	}
	var parts []part
	have := new(big.Int)
	for _, tier := range sortedTiers(by) {
		c := new(big.Rat).Mul(by[tier], new(big.Rat).SetInt(hundred))
		floor := new(big.Int).Div(c.Num(), c.Denom()) // Euclidean: the floor for a positive denominator
		parts = append(parts, part{tier, floor, new(big.Rat).Sub(c, new(big.Rat).SetInt(floor))})
		have.Add(have, floor)
	}
	total := new(big.Rat).Mul(sum, new(big.Rat).SetInt(hundred))
	want := new(big.Int).Div(total.Num(), total.Denom())
	if !total.IsInt() {
		want.Add(want, big.NewInt(1))
	}
	order := make([]int, len(parts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return parts[order[a]].rem.Cmp(parts[order[b]].rem) > 0 })
	for left := new(big.Int).Sub(want, have); left.Sign() > 0; left.Sub(left, big.NewInt(1)) {
		for _, i := range order {
			if parts[i].rem.Sign() > 0 {
				parts[i].cents.Add(parts[i].cents, big.NewInt(1))
				parts[i].rem.SetInt64(0)
				break
			}
		}
	}
	out := map[string]string{}
	for _, p := range parts {
		if p.cents.Sign() != 0 {
			out[p.tier] = "$" + new(big.Rat).SetFrac(p.cents, hundred).FloatString(2)
		}
	}
	return out
}

// sortedTiers are the keys of by in the tiers' order, any other key after them by name.
func sortedTiers(by map[string]*big.Rat) []string {
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if i := slices.Index(Tiers, k); i >= 0 {
			return i
		}
		return len(Tiers)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b)) })
	return keys
}

// StreamTierCosts is each stream's TierCosts, by stream, over the work table's rows, a
// record with no tier of its own taking one by the rules (RecordTier).
func StreamTierCosts(s *Snapshot, r TierRules) map[string]TierCosts {
	out := map[string]TierCosts{}
	for _, st := range s.Work.Rows() {
		out[st] = streamTierCosts(s, st, r)
	}
	return out
}

func streamTierCosts(s *Snapshot, stream string, r TierRules) TierCosts {
	t := TierCosts{Tiers: map[string]int{}, PerLanded: "-", CostByTier: map[string]string{}}
	byTier := map[string]*big.Rat{}
	var landedCost []string
	landed := 0
	for _, col := range States {
		for _, c := range s.Work.Cell(stream, col) {
			if IsSentinel(c) {
				continue
			}
			t.Tiers[TierWord(c)]++
			if col != Landed {
				continue
			}
			landed++
			if v := c.F(FieldCost); v != "" {
				landedCost = append(landedCost, v)
			}
			for tier, v := range CardTierSpend(c, r) {
				if byTier[tier] == nil {
					byTier[tier] = new(big.Rat)
				}
				byTier[tier].Add(byTier[tier], v)
			}
		}
	}
	if sum, ok := cardcost.Sum(landedCost...); ok && landed > 0 && len(landedCost) > 0 {
		if total, err := amountOf(sum); err == nil && total != nil {
			t.PerLanded = cardcost.Cents(total.Quo(total, big.NewRat(int64(landed), 1)))
		}
	}
	t.CostByTier = SplitCents(byTier)
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
