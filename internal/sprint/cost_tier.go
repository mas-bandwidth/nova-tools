package sprint

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The friends cost category (docs/SPEC-SPRINT.md, "What a card cost", the friends
// category). tiers and cost_by_tier are the same rows: one per machine tier that
// spent, then friends. A machine tier carries token counts and its charged dollars.
// The friends row carries token counts and no dollar field. The tick counts both
// into the where record from the twin it already holds, and where copies them on,
// so the view reads no card for them.

// costTierOrder is the machine tiers, weakest first, the ladder the deal climbs
// (cardhdr.Routes, flash first).
var costTierOrder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteFrontier}

// TokenCounts are the token classes a category reported. A class the runs did not
// report stays unreported and is left out of the JSON, never written as 0. Total
// is the reported classes summed.
type TokenCounts struct {
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Reasoning  int64
	Total      int64
}

// MarshalJSON leaves out a class that was not reported (docs/SPEC-SPRINT.md, "What
// a card cost": a class not reported is left out, never written as 0).
func (t TokenCounts) MarshalJSON() ([]byte, error) {
	m := map[string]int64{"total": t.Total}
	for _, kv := range []struct {
		k string
		n int64
	}{{"input", t.Input}, {"cache_read", t.CacheRead}, {"cache_write", t.CacheWrite}, {"output", t.Output}, {"reasoning", t.Reasoning}} {
		if kv.n >= 0 {
			m[kv.k] = kv.n
		}
	}
	return json.Marshal(m)
}

// CostCategory is one row of tiers and of cost_by_tier. USD is the charged
// dollars of a machine tier, the exact decimal, empty when none was priced.
// The friends row has no dollar field (docs/SPEC-SPRINT.md, the friends category).
type CostCategory struct {
	Name   string
	Tokens TokenCounts
	USD    string
}

// MarshalJSON is the row as where --json and the dashboard read it. The friends
// row has tier and tokens and no dollar key, whatever a caller left in USD.
func (c CostCategory) MarshalJSON() ([]byte, error) {
	if c.Name == Friends {
		return json.Marshal(struct {
			Tier   string      `json:"tier"`
			Tokens TokenCounts `json:"tokens"`
		}{Tier: c.Name, Tokens: c.Tokens})
	}
	return json.Marshal(struct {
		Tier   string      `json:"tier"`
		Tokens TokenCounts `json:"tokens"`
		USD    string      `json:"usd,omitempty"`
	}{Tier: c.Name, Tokens: c.Tokens, USD: c.USD})
}

// TierCosts is tiers and cost_by_tier for the snapshot (docs/SPEC-SPRINT.md, the
// friends category). The two are the same rows, so the friends row cannot carry
// a dollar in one and not the other. A consumer whose who is a friend's row is
// the friends category: its tokens count and its dollars are dropped, not moved
// onto a machine tier. Every other consumer is its tier (the tier on the record,
// else the tier of its route) and its charged dollars (actual where reported,
// else predicted). A category with nothing reported is left out. Machine tiers
// come first, in ladder order, then any other tier in name order, then friends.
func TierCosts(s *Snapshot) (tiers, costByTier []CostCategory) {
	if s == nil {
		return nil, nil
	}
	rows := costCategories(s)
	return append([]CostCategory(nil), rows...), append([]CostCategory(nil), rows...)
}

// costCategories is the rows of TierCosts, in the order where shows them.
func costCategories(s *Snapshot) []CostCategory {
	got := map[string]cardcost.Total{}
	add := func(name string, u cardcost.Usage) {
		cur, ok := got[name]
		if !ok {
			cur = cardcost.NoTotal()
		}
		got[name] = cur.Add(u)
	}
	for _, p := range statsPrimaries(s) {
		for _, c := range CardCostOf(p).Consumers {
			if IsFriendRow(c.Who) {
				add(Friends, c.Usage)
				continue
			}
			tier := machineTier(s, c)
			if tier == "" || tier == Friends {
				continue // no tier to name, and friends is not a machine tier
			}
			add(tier, c.Usage)
		}
	}
	var rows []CostCategory
	seen := map[string]bool{}
	emit := func(name string) {
		t, ok := got[name]
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		if name == Friends {
			if !t.Tokens.Reported() {
				return // dollars alone are not a friends row: there is no dollar field to put them in
			}
			rows = append(rows, CostCategory{Name: name, Tokens: tokenCounts(t.Tokens)})
			return
		}
		if !t.Tokens.Reported() && t.Charged == "" {
			return
		}
		rows = append(rows, CostCategory{Name: name, Tokens: tokenCounts(t.Tokens), USD: t.Charged})
	}
	for _, name := range costTierOrder {
		emit(name)
	}
	var extra []string
	for name := range got {
		if !seen[name] && name != Friends {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	for _, name := range extra {
		emit(name)
	}
	emit(Friends)
	return rows
}

// machineTier is a consumer's machine tier: the tier on its record, else the
// tier of the route that served it.
func machineTier(s *Snapshot, c Consumer) string {
	if c.Tier != "" {
		return c.Tier
	}
	name := cmp.Or(c.Route, c.Usage.Route)
	for _, r := range s.Routes {
		if r.Name == name && r.Tier != "" {
			return r.Tier
		}
	}
	return ""
}

// tokenCounts is a summed total's tokens. An unreported class stays unreported.
func tokenCounts(t cardcost.Tokens) TokenCounts {
	return TokenCounts{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Reasoning: t.Reasoning, Total: t.Total()}
}
