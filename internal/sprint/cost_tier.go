package sprint

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The friends cost category (docs/SPEC-SPRINT.md section 2, the friends category):
// the rows `where --json` carries as `cost_by_tier` at the top and the dashboard's
// Cost breakdown shows, counted by the tick into the where record (store/where.go)
// from the twin it already holds, so where reads no card for them. One row per machine
// tier that spent (flash, pro, heavy, frontier, then any other tier by name), then a
// friends row. A machine tier carries token counts and its charged dollars; the friends
// row carries token counts and no dollar field (the owner, 2026-10-04: "i don't want
// dollar amounts for friends. token counts are fine."). On this tree `tiers` at the top
// stays every card by its brief's tier, and each stream's spend by tier stays in
// `stream_costs` (cost_view.go): the category's rows are `cost_by_tier` at the top.

// costTierOrder is the machine tiers, weakest first, the ladder the deal climbs
// (cardhdr.Routes reversed, flash first; the dashboard's TIERS in app.js).
var costTierOrder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy, cardhdr.RouteFrontier}

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

// MarshalJSON leaves out a class that was not reported (docs/SPEC-SPRINT.md section 2,
// "What a card cost": a class not reported is left out, never written as 0).
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

// UnmarshalJSON reads the object MarshalJSON writes (docs/SPEC-SPRINT.md section 2,
// "What a card cost": a class not reported is left out, never written as 0). A class
// the object omits stays unreported, and total, when the object omits it, is the
// reported classes summed.
func (t *TokenCounts) UnmarshalJSON(b []byte) error {
	var m map[string]*int64
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	take := func(k string) int64 {
		p, ok := m[k]
		if !ok || p == nil {
			return cardcost.Unreported
		}
		return *p
	}
	t.Input = take("input")
	t.CacheRead = take("cache_read")
	t.CacheWrite = take("cache_write")
	t.Output = take("output")
	t.Reasoning = take("reasoning")
	if p, ok := m["total"]; ok && p != nil {
		t.Total = *p
		return nil
	}
	t.Total = cardcost.Tokens{
		Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Reasoning: t.Reasoning,
	}.Total()
	return nil
}

// CostCategory is one row of the friends cost category's `cost_by_tier` (docs/SPEC-SPRINT.md
// section 2, the friends category). USD is the charged dollars of a machine tier, the
// exact decimal, empty when none was priced. The friends row has no dollar field.
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

// UnmarshalJSON reads the row MarshalJSON writes (docs/SPEC-SPRINT.md section 2, the
// friends category): tier and tokens, and usd on a machine tier. The friends row
// has no dollar field, so a usd key on it is dropped.
func (c *CostCategory) UnmarshalJSON(b []byte) error {
	var raw struct {
		Tier   string          `json:"tier"`
		Tokens json.RawMessage `json:"tokens"`
		USD    *string         `json:"usd"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.Name = raw.Tier
	c.USD = ""
	if raw.USD != nil && c.Name != Friends {
		c.USD = *raw.USD
	}
	if len(raw.Tokens) == 0 || string(raw.Tokens) == "null" {
		c.Tokens = TokenCounts{
			Input: cardcost.Unreported, CacheRead: cardcost.Unreported, CacheWrite: cardcost.Unreported,
			Output: cardcost.Unreported, Reasoning: cardcost.Unreported,
		}
		return nil
	}
	return json.Unmarshal(raw.Tokens, &c.Tokens)
}

// CostCategories is the friends cost category's rows of the snapshot (docs/SPEC-SPRINT.md
// section 2, the friends category): one per machine tier that spent and then a friends
// row, so the friends row cannot carry a dollar and a machine tier cannot carry hers. A
// consumer whose who is a friend's row is that friends row: its tokens count and its
// dollars are dropped, not moved onto a machine tier. Every other consumer is its tier
// (the tier on its record, else the tier of its route) and its charged dollars (actual
// where reported, else predicted). A category with nothing reported is left out.
// Machine tiers come first, in ladder order, then any other tier in name order, then
// friends.
func CostCategories(s *Snapshot) []CostCategory {
	if s == nil {
		return nil
	}
	return costCategories(s)
}

// costCategories is the rows of CostCategories, in the order where shows them.
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
