package route

import (
	"fmt"
	"hash/fnv"
	"strings"
	"text/tabwriter"
)

// ViaDatacenter is Mercury's via: the route's model carries the harness's
// provider (inception) itself.
const ViaDatacenter = "datacenter"

// SpreadRow is one provider of one tier in the swarm's per-card spread (Glenn
// 2026-09-24 7:10 PM ET: distribute across all four providers). Routes[0] is
// the route a card picked for this provider runs on; the rest are documented
// alternates, best first, that the pick never uses.
type SpreadRow struct {
	Tier, Provider, Via string
	Routes              []string
	Why                 string
}

// Spread returns the spread rows of one tier in file order, the order Pick
// indexes them.
func (t *Table) Spread(tier string) []SpreadRow {
	var out []SpreadRow
	for _, s := range t.spread {
		if s.Tier == tier {
			out = append(out, s)
		}
	}
	return out
}

// SpreadTiers returns the tiers that carry spread rows, in file order.
func (t *Table) SpreadTiers() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range t.spread {
		if !seen[s.Tier] {
			seen[s.Tier] = true
			out = append(out, s.Tier)
		}
	}
	return out
}

// SpreadIndex is the provider index a card label picks among n providers:
// fnv32a(label) mod n. No state: the same label always lands on the same
// provider, so a retried card keeps its provider.
func SpreadIndex(label string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(label))
	return int(h.Sum32() % uint32(n))
}

// Pick returns the route a card of this tier and label runs on: the first
// route of the spread row SpreadIndex picks, and that row.
func (t *Table) Pick(tier, label string) (Row, SpreadRow, error) {
	if label == "" {
		return Row{}, SpreadRow{}, fmt.Errorf("an empty card label picks no provider")
	}
	rows := t.Spread(tier)
	if len(rows) == 0 {
		return Row{}, SpreadRow{}, fmt.Errorf("tier %s has no spread rows; the tiers are %s", field(tier), strings.Join(t.SpreadTiers(), " and "))
	}
	s := rows[SpreadIndex(label, len(rows))]
	return t.rows[t.byRoute[s.Routes[0]]], s, nil
}

// RenderSpread prints the spread: one line per tier and provider with the
// picked route, its launch string and state, then the alternates.
func (t *Table) RenderSpread() string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "tier\tindex\tprovider\troute\tlaunch\tstate\talternates")
	for _, tier := range t.SpreadTiers() {
		for i, s := range t.Spread(tier) {
			r := t.rows[t.byRoute[s.Routes[0]]]
			alt := "-"
			if len(s.Routes) > 1 {
				alt = strings.Join(s.Routes[1:], " ")
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", s.Tier, i, s.Provider, r.Route, r.Launch(), r.State, alt)
		}
	}
	_ = tw.Flush()
	return b.String()
}

func (t *Table) addSpread(it map[string]string) error {
	var s SpreadRow
	for key, raw := range it {
		if key == "\x00section" {
			continue
		}
		switch key {
		case "tier", "provider", "via", "why":
			v, err := scalar(raw)
			if err != nil {
				return err
			}
			switch key {
			case "tier":
				s.Tier = v
			case "provider":
				s.Provider = v
			case "via":
				s.Via = v
			case "why":
				s.Why = v
			}
		case "routes":
			if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
				return fmt.Errorf("spread routes wants a [flow, list] of routes")
			}
			for _, a := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",") {
				if a = strings.TrimSpace(a); a != "" {
					s.Routes = append(s.Routes, a)
				}
			}
		default:
			return fmt.Errorf("unknown spread key %q", key)
		}
	}
	if s.Tier == "" || s.Provider == "" || s.Via == "" || len(s.Routes) == 0 || s.Why == "" {
		return fmt.Errorf("a spread row needs tier, provider, via, routes and why")
	}
	t.spread = append(t.spread, s)
	return nil
}

// checkSpread refuses a spread row that names an unknown route, a route of
// another rung, a route whose via is not the row's, or a dropped or dead
// route; a provider twice in one tier; and a provider whose via differs
// between tiers. A held route is admitted: the spread's providers are chosen,
// not ranked.
func (t *Table) checkSpread() error {
	seen := map[string]bool{}
	viaOf := map[string]string{}
	for _, s := range t.spread {
		key := s.Tier + "\x00" + s.Provider
		if seen[key] {
			return fmt.Errorf("spread %s %s appears twice", s.Tier, s.Provider)
		}
		seen[key] = true
		if v, ok := viaOf[s.Provider]; ok && v != s.Via {
			return fmt.Errorf("spread provider %s has via %s on one tier and %s on another", s.Provider, v, s.Via)
		}
		viaOf[s.Provider] = s.Via
		for _, route := range s.Routes {
			i, ok := t.byRoute[route]
			if !ok {
				return fmt.Errorf("spread %s %s names unknown route %s", s.Tier, s.Provider, route)
			}
			r := t.rows[i]
			switch {
			case r.Rung != s.Tier:
				return fmt.Errorf("spread %s %s names %s, a rung %s route", s.Tier, s.Provider, route, r.Rung)
			case r.Via != s.Via:
				return fmt.Errorf("spread %s %s (via %s) names %s, whose via is %s", s.Tier, s.Provider, s.Via, route, r.Via)
			case r.State == Dropped:
				return fmt.Errorf("spread %s %s names %s, which is dropped: %s", s.Tier, s.Provider, route, r.Why)
			case r.Flag == FlagDead:
				return fmt.Errorf("spread %s %s names %s, which is dead", s.Tier, s.Provider, route)
			}
		}
	}
	return nil
}
