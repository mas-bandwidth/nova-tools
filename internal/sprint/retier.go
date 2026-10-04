package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// COSTS RETIER (docs/SPEC-SPRINT.md section 1, the where view; the owner, 2026-10-04
// 4:22 PM: "Fix it so it stops happening, and also fix it up retrospectively"): every
// record is written with its tier (addConsumer) and every primary keeps its tier totals
// (FieldCostTier) from then on; costs retier is the one-time backfill of what was written
// before. On each primary of the work table it writes the tier into every cost record
// that has none (RecordTier) and the tier totals when they are missing or do not add up
// to its charged figure (recordTierTotals), so its landed cost is placed by no read-time
// rule (CardTierSpend's guard). It is idempotent: a primary with every record tiered and
// complete totals is left alone, and a second run writes nothing.

// RetierReq is costs retier: Max bounds the primaries one step writes (0 is all), so a
// big table is retiered over several runs, each a bounded step. FriendUsage is the usage
// an api friend's harness kept of a work record that holds none (the record's key, its
// Consumer.Key, to a usage line: the tokens, provider/model and the harness's cost), which
// the cmd recovers from the harness's own store (costs retier, friendusage.go): each is
// priced as a member's take is (the route of its model's price sheet) and put in the
// record, its total, and, when it landed, its cost and its stream's.
type RetierReq struct {
	Max         int               `json:"max,omitempty"`
	FriendUsage map[string]string `json:"friend_usage,omitempty"`
	Who         string            `json:"who,omitempty"`
}

// RetierResult is what Retier plans and reports: the plan, each stream's before and after,
// how many primaries are left for another run, and each friend record priced (its key to
// its charged figure).
type RetierResult struct {
	Plan    Plan
	Streams []RetierStream
	Left    int
	Priced  map[string]string
}

// RetierStream is one stream's retier as costs retier prints it: the primaries it writes
// and the records it tiers, and its landed cost (FieldCost) by tier before and after
// (SplitCents). Before is what the cards hold with no read-time rule: a record's own tier,
// or the complete tier totals, and NoTierWord for the rest; after is CardTierSpend's once
// written, Guard what it still places by the rule (a landed cost unlike its charged total).
type RetierStream struct {
	Stream  string            `json:"stream"`
	Cards   int               `json:"cards"`
	Records int               `json:"records"`
	Cost    string            `json:"cost"`
	Was     string            `json:"was,omitempty"` // the landed cost before, when priced friend records moved it
	Before  map[string]string `json:"before"`
	After   map[string]string `json:"after"`
	Guard   string            `json:"guard,omitempty"`
}

// NoTierWord is the before column's name for a landed dollar no record's own tier or tier
// total holds: what costs retier is for. It is never a tier, and never in cost_by_tier.
const NoTierWord = "no_tier"

// retierCard is what costs retier writes on one primary: the fields set and unset, and
// how many records it tiers. A primary with nothing to write has an empty set and unset.
func retierCard(pr *Card, r TierRules) (set map[string]string, unset []string, records int) {
	set = map[string]string{}
	for k, line := range pr.Fields {
		key, ok := strings.CutPrefix(k, FieldCostRecord)
		if !ok {
			continue
		}
		con := parseConsumer(key, line)
		if IsTier(con.Tier) {
			continue
		}
		tier, _ := RecordTier(con, pr, r)
		set[k] = withTierWord(line, tier)
		records++
	}
	if _, ok := storedTierTotals(pr); ok {
		return set, nil, records
	}
	by := recordTierTotals(pr, r)
	for tier, v := range by {
		if v.Sign() < 0 { // the records hold more than the total: a bug the totals cannot carry
			return map[string]string{}, nil, 0
		}
		if v.Sign() > 0 {
			set[FieldCostTier+tier] = cardcost.Text(v)
		}
	}
	for k := range pr.Fields {
		if strings.HasPrefix(k, FieldCostTier) && set[k] == "" {
			unset = append(unset, k)
		}
	}
	sort.Strings(unset)
	return set, unset, records
}

// withTierWord is a record's line with its tier: on_tier=<tier> in place of the "-" a
// record with none was written with, or, in a line from before records carried it, after
// its on_model word (Consumer.line's order). The rest of the line is kept as it is.
func withTierWord(line, tier string) string {
	words := strings.Fields(line)
	for i, w := range words {
		if strings.HasPrefix(w, "on_tier=") {
			words[i] = "on_tier=" + tier
			return strings.Join(words, " ")
		}
	}
	at := slices.IndexFunc(words, func(w string) bool { return strings.HasPrefix(w, "on_model=") }) + 1
	return strings.Join(slices.Insert(words, at, "on_tier="+tier), " ")
}

// ownTierSpend is a landed primary's cost (FieldCost) by tier with no read-time rule: its
// complete tier totals when they add up to it, else each record's own tier and NoTierWord
// for every other dollar.
func ownTierSpend(pr *Card) map[string]*big.Rat {
	out := map[string]*big.Rat{}
	cost, err := amountOf(pr.F(FieldCost))
	if err != nil || cost == nil {
		return out
	}
	if stored, ok := storedTierTotals(pr); ok && sumOf(stored).Cmp(cost) == 0 {
		return stored
	}
	rest := new(big.Rat).Set(cost)
	for _, con := range CardCostOf(pr).Consumers {
		if v := ChargedOf(con.Usage); v != nil && IsTier(con.Tier) {
			if out[con.Tier] == nil {
				out[con.Tier] = new(big.Rat)
			}
			out[con.Tier].Add(out[con.Tier], v)
			rest.Sub(rest, v)
		}
	}
	if rest.Sign() != 0 {
		out[NoTierWord] = rest
	}
	return out
}

// priceFriend is the primary's records with the friend usage r names put in: each record
// of a key in usage that holds no token is priced (costRecord's pricing: the route of its
// model's price sheet, the harness's cost as its actual) and written with its wait and run
// kept, and the total, and a landed card's cost, take its figure. priced is each record's
// charged figure.
func priceFriend(s *Snapshot, pr *Card, usage map[string]string) (set map[string]string, priced map[string]string) {
	set, priced = map[string]string{}, map[string]string{}
	if len(usage) == 0 {
		return set, priced
	}
	total := cardcost.ParseTotal(pr.F(FieldCostTotal))
	changed := false
	for k, line := range pr.Fields {
		key, ok := strings.CutPrefix(k, FieldCostRecord)
		if !ok || usage[key] == "" {
			continue
		}
		con := parseConsumer(key, line)
		if con.Usage.Tokens.Reported() {
			continue
		}
		u := cardcost.ParseUsage(usage[key])
		if !u.Tokens.Reported() {
			continue
		}
		u.Wait, u.Run = con.Usage.Wait, con.Usage.Run
		if r, ok := s.priceRoute("", u.Model, false); ok {
			u = u.Priced(r.Name, r.Prices)
		} else {
			u = u.Priced("", cardcost.Prices{})
		}
		con.Usage, con.Model = u, cmp.Or(u.Model, con.Model)
		con.Tier = "" // its tier is its model's now (RecordTier)
		set[k] = con.line()
		priced[key] = u.Charged()
		add := u
		add.Wait, add.Run = cardcost.Unreported, cardcost.Unreported // in the total already
		total = total.Add(add)
		total.Records--
		changed = true
	}
	if changed {
		set[FieldCostTotal] = total.String()
		if pr.Col == Landed && total.Charged != "" {
			set[FieldCost] = total.Charged
		}
	}
	return set, priced
}

// Retier is costs retier over the work table: the primaries it writes, at most r.Max of
// them in table order (0 is all), one unit each, and the control card of a stream whose
// landed cost a priced friend record changed (its cost, the sum of its landed cards', as
// a landing writes it); the streams' before and after over every primary, written in this
// step or not; and how many primaries are left for another run.
func Retier(s *Snapshot, r RetierReq) RetierResult {
	var res RetierResult
	res.Plan.on(s)
	res.Priced = map[string]string{}
	rules := RulesOf(s.Routes)
	written := 0
	for _, stream := range s.Work.Rows() {
		rs := RetierStream{Stream: stream}
		before, after := map[string]*big.Rat{}, map[string]*big.Rat{}
		cost, was, guard := new(big.Rat), new(big.Rat), new(big.Rat)
		var landedCosts []string
		costMoved := false
		for _, col := range States {
			for _, c := range s.Work.Cell(stream, col) {
				if IsSentinel(c) {
					continue
				}
				set, priced := priceFriend(s, c, r.FriendUsage)
				priceSet := withFields(c, set, nil)
				tierSet, unset, records := retierCard(priceSet, rules)
				for k, v := range tierSet {
					set[k] = v
				}
				now := c
				if len(set) > 0 || len(unset) > 0 {
					if r.Max > 0 && written >= r.Max {
						res.Left++
					} else {
						written++
						rs.Cards++
						rs.Records += records
						maps.Copy(res.Priced, priced)
						res.Plan.Units = append(res.Plan.Units, Unit{Key: c.ID, Stream: stream, Changes: []Change{change(Work, setEntry(c, set, unset...))},
							Moved: fmt.Sprintf("%s costs retiered: %d records tiered, %d friend records priced, %s", c.ID, records, len(priced), tierWords(set))})
						now = withFields(c, set, unset)
						costMoved = costMoved || set[FieldCost] != ""
					}
				}
				if col != Landed {
					continue
				}
				if v := now.F(FieldCost); v != "" {
					landedCosts = append(landedCosts, v)
				}
				if v, err := amountOf(now.F(FieldCost)); err == nil && v != nil {
					cost.Add(cost, v)
				}
				if v, err := amountOf(c.F(FieldCost)); err == nil && v != nil {
					was.Add(was, v)
				}
				addAll(before, ownTierSpend(c))
				by, guarded := CardTierSpend(now, rules)
				addAll(after, by)
				guard.Add(guard, guarded)
			}
		}
		if ctl := s.StreamCtl(stream); costMoved && ctl != nil {
			if sum, ok := cardcost.Sum(landedCosts...); ok && sum != ctl.F(FieldCost) {
				last := &res.Plan.Units[len(res.Plan.Units)-1]
				last.Changes = append(last.Changes, change(Merge, setEntry(ctl, map[string]string{FieldCost: sum})))
			}
		}
		if rs.Cards == 0 && cost.Sign() == 0 {
			continue
		}
		rs.Cost, rs.Before, rs.After = cardcost.Cents(cost), SplitCents(before), SplitCents(after)
		if was.Cmp(cost) != 0 {
			rs.Was = cardcost.Cents(was)
		}
		if guard.Sign() > 0 {
			rs.Guard = cardcost.Cents(guard)
		}
		res.Streams = append(res.Streams, rs)
	}
	return res
}

// withFields is a copy of the card with the fields set and unset.
func withFields(c *Card, set map[string]string, unset []string) *Card {
	cp := *c
	cp.Fields = make(map[string]string, len(c.Fields)+len(set))
	for k, v := range c.Fields {
		cp.Fields[k] = v
	}
	for k, v := range set {
		cp.Fields[k] = v
	}
	for _, k := range unset {
		delete(cp.Fields, k)
	}
	return &cp
}

// addAll adds amounts into to, key by key.
func addAll(to, from map[string]*big.Rat) {
	for k, v := range from {
		if to[k] == nil {
			to[k] = new(big.Rat)
		}
		to[k].Add(to[k], v)
	}
}

// tierWords are the tier totals a retier writes, as tier=amount words in the tiers'
// order, "totals kept" when it writes none.
func tierWords(set map[string]string) string {
	by := map[string]*big.Rat{}
	for k, v := range set {
		if tier, ok := strings.CutPrefix(k, FieldCostTier); ok {
			by[tier], _ = amountOf(v)
		}
	}
	if len(by) == 0 {
		return "totals kept"
	}
	var w []string
	for _, tier := range sortedTiers(by) {
		w = append(w, tier+"="+set[FieldCostTier+tier])
	}
	return "totals " + strings.Join(w, " ")
}

// RetierLine is a stream's retier as costs retier prints it, one line:
//
//	RETIER stream=nongo cards=10 records=136 cost=$49.53 before=no_tier:$49.53 after=pro:$49.53
func RetierLine(rs RetierStream) string {
	cost := rs.Cost
	if rs.Was != "" {
		cost = rs.Was + "->" + rs.Cost
	}
	line := fmt.Sprintf("RETIER stream=%s cards=%d records=%d cost=%s before=%s after=%s", rs.Stream, rs.Cards, rs.Records, cost, moneyWords(rs.Before), moneyWords(rs.After))
	if rs.Guard != "" {
		line += " guard=" + rs.Guard
	}
	return line
}

// moneyWords are amounts as tier:amount words joined by commas, in the tiers' order, the
// before column's NoTierWord last; "-" for none.
func moneyWords(by map[string]string) string {
	if len(by) == 0 {
		return "-"
	}
	keys := map[string]*big.Rat{}
	for k := range by {
		keys[k] = nil
	}
	var w []string
	for _, k := range sortedTiers(keys) {
		w = append(w, k+":"+by[k])
	}
	return strings.Join(w, ",")
}
