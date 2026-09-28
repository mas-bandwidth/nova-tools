package route

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// ViaDatacenter is Mercury's via: the route's model carries the harness's
// provider (inception) itself.
const ViaDatacenter = "datacenter"

// SpreadRow is one provider of one tier in the swarm's per-card spread (Glenn
// 2026-09-24 7:10 PM ET: distribute across all four providers). Routes[0] is
// the route a card picked for this provider runs on; the rest are documented
// alternates, best first, that the pick never uses. Share is how many of the
// tier's pick slots the row owns (default 1, at most MaxShare): #3949 gave
// kimi-k3's former third of the pro cards to qwen3.6-plus as opencode share 2.
type SpreadRow struct {
	Tier, Provider, Via string
	Share               int
	Routes              []string
	Why                 string
}

// MaxShare caps one spread row's share, so a typo cannot hand one provider
// the whole tier.
const MaxShare = 8

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
// route of the spread row that owns slot SpreadIndex(label, sum of shares),
// the rows owning consecutive slots in file order, and that row. With every
// share 1 the slot is the row index.
func (t *Table) Pick(tier, label string) (Row, SpreadRow, error) {
	if label == "" {
		return Row{}, SpreadRow{}, fmt.Errorf("an empty card label picks no provider")
	}
	if pr := t.Probe(tier); pr != nil {
		r := t.rows[t.byRoute[pr.Routes[SpreadIndex(label, len(pr.Routes))]]]
		return r, SpreadRow{Tier: tier, Provider: providerOf(r.Via), Via: r.Via, Share: 1, Routes: []string{r.Route}, Why: pr.Why}, nil
	}
	rows := t.Spread(tier)
	if len(rows) == 0 {
		return Row{}, SpreadRow{}, fmt.Errorf("tier %s has no spread rows; the tiers are %s", field(tier), strings.Join(t.SpreadTiers(), " and "))
	}
	total := 0
	for _, s := range rows {
		total += s.Share
	}
	slot := SpreadIndex(label, total)
	for _, s := range rows {
		if slot < s.Share {
			return t.rows[t.byRoute[s.Routes[0]]], s, nil
		}
		slot -= s.Share
	}
	return Row{}, SpreadRow{}, fmt.Errorf("tier %s: slot past the shares (unreachable)", field(tier))
}

// RenderSpread prints the spread: one line per tier and provider with its
// share, the picked route, its launch string and state, then the alternates.
func (t *Table) RenderSpread() string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "tier\tindex\tprovider\tshare\troute\tlaunch\tstate\talternates")
	for _, tier := range t.SpreadTiers() {
		for i, s := range t.Spread(tier) {
			r := t.rows[t.byRoute[s.Routes[0]]]
			alt := "-"
			if len(s.Routes) > 1 {
				alt = strings.Join(s.Routes[1:], " ")
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%s\t%s\t%s\t%s\n", s.Tier, i, s.Provider, s.Share, r.Route, r.Launch(), r.State, alt)
		}
	}
	_ = tw.Flush()
	return b.String()
}

func (t *Table) addSpread(it map[string]string) error {
	s := SpreadRow{Share: 1}
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
		case "share":
			v, err := scalar(raw)
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > MaxShare {
				return fmt.Errorf("spread share %q wants a whole number 1..%d", v, MaxShare)
			}
			s.Share = n
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

// The probe (Glenn 2026-09-26 11:05 AM ET: "turn all models back on to try
// again, in case it is our fault"): a dated section that, while present,
// runs a tier's cards over every listed route, one slot each, whatever the
// route's measured state, and admits the route at the allowed check. It is
// the wrapper's trial, not the table's verdict: the states stay as the rule
// derives them, and deleting the section returns the measured spread.
type ProbeRow struct {
	Tier   string
	Routes []string
	Until  string // the date the probe ends, YYYY-MM-DD
	Why    string
}

// Probe returns the probe row of one tier, nil when the tier has none.
func (t *Table) Probe(tier string) *ProbeRow {
	for i := range t.probe {
		if t.probe[i].Tier == tier {
			return &t.probe[i]
		}
	}
	return nil
}

// probed says a route is on its rung's probe row.
func (t *Table) probed(rung, route string) bool {
	pr := t.Probe(rung)
	if pr == nil {
		return false
	}
	for _, r := range pr.Routes {
		if r == route {
			return true
		}
	}
	return false
}

// providerOf is the spread's provider name for a via.
func providerOf(via string) string {
	if via == "datacenter" {
		return "mercury"
	}
	return via
}

func (t *Table) addProbe(it map[string]string) error {
	var p ProbeRow
	for key, raw := range it {
		if key == "\x00section" {
			continue
		}
		switch key {
		case "tier", "until", "why":
			v, err := scalar(raw)
			if err != nil {
				return err
			}
			switch key {
			case "tier":
				p.Tier = v
			case "until":
				p.Until = v
			case "why":
				p.Why = v
			}
		case "routes":
			if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
				return fmt.Errorf("probe routes wants a [flow, list] of routes")
			}
			for _, a := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",") {
				if a = strings.TrimSpace(a); a != "" {
					p.Routes = append(p.Routes, a)
				}
			}
		default:
			return fmt.Errorf("unknown probe key %q", key)
		}
	}
	if p.Tier == "" || len(p.Routes) == 0 || p.Why == "" || p.Until == "" {
		return fmt.Errorf("a probe row needs tier, routes, until and why")
	}
	if _, err := time.Parse("2006-01-02", p.Until); err != nil {
		return fmt.Errorf("probe until %q wants YYYY-MM-DD", p.Until)
	}
	t.probe = append(t.probe, p)
	return nil
}

// checkProbe refuses a probe row that names an unknown route, a route of
// another rung, a tier twice, or a route twice.
func (t *Table) checkProbe() error {
	seenTier := map[string]bool{}
	for _, p := range t.probe {
		if seenTier[p.Tier] {
			return fmt.Errorf("probe %s appears twice", p.Tier)
		}
		seenTier[p.Tier] = true
		seen := map[string]bool{}
		for _, route := range p.Routes {
			i, ok := t.byRoute[route]
			if !ok {
				return fmt.Errorf("probe %s names %s, which is not a route", p.Tier, route)
			}
			if t.rows[i].Rung != p.Tier {
				return fmt.Errorf("probe %s names %s, a rung %s route", p.Tier, route, t.rows[i].Rung)
			}
			if seen[route] {
				return fmt.Errorf("probe %s names %s twice", p.Tier, route)
			}
			seen[route] = true
		}
	}
	return nil
}

// WithoutProbe is the table with its probe rows removed: the measured
// spread and the rule's verdicts as they stand, which is what the table's
// own tests hold to while a probe is on.
func (t *Table) WithoutProbe() *Table {
	c := *t
	c.probe = nil
	return &c
}
