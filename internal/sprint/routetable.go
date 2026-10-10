package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// RouteTable is stats --routes (docs/SPEC-SPRINT.md, the verb stats): the route
// table from the log over a window, the program at route-score replaced by the
// verb. takes is the launches that ran (ok, failed, no-result). A provider
// take is the prov column unless its error is a no-result (IsNoResult), and a
// primary with more than fifty provider failures is a storm, counted once per
// route. landed is the latest ok take at or before the primary's accept (the
// verb accept or tick accept). 1stT is how many of those landings were the
// primary's first take on that tier. wrong is broken reviews over the ok takes
// a reader scored. Dollars per take and per landing count actual_usd whenever
// the usage writes it, and a finish that ran more than a minute with no price
// is imputed at the route's mean. The median wall is the upper median. A
// finish with no route is left out.
func RouteTable(lines []Line, since time.Time) string {
	takes := map[string]*rtTake{}
	dealt := map[string]string{}
	takenAt := map[string]time.Time{}
	verdict := map[string]string{}
	accepted := map[string][]time.Time{}
	landed := map[string]time.Time{}

	for _, l := range lines {
		// A finish before the window is left out, and the provider takes on
		// that finish line with it. A deal, a take, an accept and a read
		// before the window stay: they are the route, the wall and the review
		// of a finish inside it (docs/SPEC-SPRINT.md, the verb stats).
		if l.At.Before(since) && l.Table == Fleet && l.Verb == "finish" {
			continue
		}
		switch {
		case l.Table == "fleet" && l.Set["route"] != "" && l.Set["gen"] != "":
			dealt[l.Card+"#g"+l.Set["gen"]] = l.Set["route"]
		case l.Table == "fleet" && l.Verb == "take":
			if ts, err := time.Parse(time.RFC3339, l.Set["taken"]); err == nil {
				takenAt[fmt.Sprintf("%s#g%d", l.Card, l.Gen)] = ts
			}
		case l.Table == "fleet" && l.Verb == "finish":
			prim, att := rtAttempt(l.Card)
			if ok, has := l.Set["ok"]; has {
				u := rtFromUsage(l.Set["usage"])
				t := &rtTake{key: fmt.Sprintf("%s#g%d", l.Card, l.Gen), primary: prim, attempt: att, at: l.At, u: u}
				if ok == "yes" {
					t.end = "ok"
				} else {
					t.end = "failed"
				}
				if _, hasUsage := l.Set["usage"]; !hasUsage {
					if fin, err := time.Parse(time.RFC3339, l.Set["finished"]); err == nil {
						if tk, ok := takenAt[t.key]; ok {
							t.u.run = fin.Sub(tk).Seconds()
							t.u.wall = t.u.run
						}
					}
				}
				if t.u.route == "" {
					if (t.u.run >= 60 || t.end == "failed") && dealt[t.key] != "" {
						t.u.route = dealt[t.key]
					} else {
						t.u.route = "-"
					}
				}
				takes[t.key] = t
			}
			for k, v := range l.Set {
				if !strings.HasPrefix(k, FieldProviderTake) || v == "" {
					continue
				}
				// parseTake is the record takeEnded wrote. A no-result keeps
				// its prefix; every other provider take is the provider's
				// (docs/SPEC-SPRINT.md, the verb stats).
				pt := parseTake(v)
				u := rtFromUsage(pt.Usage)
				if u.route == "" {
					u.route = pt.Route
				}
				t := &rtTake{key: l.Card + "#" + k, primary: prim, attempt: att, at: l.At, u: u}
				if IsNoResult(pt.Error) {
					t.end = "no-result"
				} else {
					t.end = "provider"
				}
				takes[t.key] = t
			}
		case l.Table == "readers" && l.Verb == "read":
			if v, has := l.Set["verdict"]; has {
				prim, att := rtReadAttempt(l.Card)
				k := fmt.Sprintf("%s.w%d", prim, att)
				if _, seen := verdict[k]; !seen || v == "broken" {
					verdict[k] = v
				}
			}
		case l.Table == Work && (l.Verb == "accept" || l.Verb == "tick accept"):
			// the coordinator's accept, and the machine's tick accept
			// (docs/SPEC-SPRINT.md, the verb stats)
			accepted[l.Primary] = append(accepted[l.Primary], l.At)
		case l.Table == "work" && strings.HasSuffix(l.To, ":landed"):
			landed[l.Primary] = l.At
		}
	}

	landedTake := map[string]*rtTake{}
	byPrimary := map[string][]*rtTake{}
	for _, t := range takes {
		byPrimary[t.primary] = append(byPrimary[t.primary], t)
	}
	for prim, when := range landed {
		acc := when
		for _, a := range accepted[prim] {
			if !a.After(when) && (acc.Equal(when) || a.After(acc)) {
				acc = a
			}
		}
		var best *rtTake
		for _, t := range byPrimary[prim] {
			if t.end == "ok" && !t.at.After(acc.Add(2*time.Second)) && (best == nil || t.at.After(best.at)) {
				best = t
			}
		}
		if best != nil {
			landedTake[prim] = best
		}
	}

	routes := map[string]*rtStat{}
	get := func(n string) *rtStat {
		r := routes[n]
		if r == nil {
			r = &rtStat{name: n}
			routes[n] = r
		}
		return r
	}
	meanUSD := map[string][2]float64{}
	for _, t := range takes {
		if t.u.priced {
			m := meanUSD[t.u.route]
			m[0] += t.u.usd
			m[1]++
			meanUSD[t.u.route] = m
		}
	}
	provByPrim := map[string]int{}
	for _, t := range takes {
		if t.end == "provider" {
			provByPrim[t.primary]++
		}
	}
	storm := map[string]bool{}
	stormTotal := 0
	for p, n := range provByPrim {
		if n > 50 {
			storm[p] = true
			stormTotal += n
		}
	}
	stormSeen := map[string]bool{}
	for _, t := range takes {
		r := get(t.u.route)
		if t.end == "provider" && storm[t.primary] {
			k := t.u.route + "|" + t.primary
			if stormSeen[k] {
				continue
			}
			stormSeen[k] = true
		}
		if t.end != "provider" {
			r.takes++
		}
		switch t.end {
		case "ok":
			r.ok++
		case "failed":
			r.failed++
		case "no-result":
			r.noResult++
		case "provider":
			r.provider++
		}
		if t.u.priced {
			r.usd += t.u.usd
		} else if t.u.run > 60 {
			if m := meanUSD[t.u.route]; m[1] > 0 {
				r.usd += m[0] / m[1]
				t.imputed = true
				r.imputed++
			}
		}
		if t.u.wall > 0 && t.end != "provider" {
			r.walls = append(r.walls, t.u.wall)
		}
		if t.end == "ok" {
			if v, has := verdict[fmt.Sprintf("%s.w%d", t.primary, t.attempt)]; has {
				r.reviewed++
				if v == "broken" {
					r.wrong++
				}
			}
		}
	}
	tierOf := func(route string) string { return strings.SplitN(route, "-", 2)[0] }
	firstOnTier := func(t *rtTake) bool {
		for _, x := range byPrimary[t.primary] {
			if x.end != "provider" && tierOf(x.u.route) == tierOf(t.u.route) && x.at.Before(t.at) {
				return false
			}
		}
		return true
	}
	for _, t := range landedTake {
		r := get(t.u.route)
		r.landed++
		if firstOnTier(t) {
			r.first++
		}
	}

	names := make([]string, 0, len(routes))
	for n := range routes {
		names = append(names, n)
	}
	sort.Strings(names)
	stormNames := make([]string, 0, len(storm))
	for p := range storm {
		stormNames = append(stormNames, p)
	}
	sort.Strings(stormNames)

	var b strings.Builder
	fmt.Fprintf(&b, "ROUTE TABLE since %s (%d launches in the log, %d landed primaries, %d with a landed take; takes = launches that ran: ok+failed+noRes; prov = provider failures, with the %d on the storm cards %s counted once per route)\n",
		since.UTC().Format(time.RFC3339), len(takes), len(landed), len(landedTake), stormTotal, strings.Join(stormNames, ","))
	fmt.Fprintf(&b, "%-30s %5s %4s %6s %6s %5s %6s %5s %6s %7s %7s %7s %4s\n",
		"route", "takes", "ok", "failed", "noRes", "prov", "landed", "1stT", "wrong", "$/take", "$/land", "medWall", "imp")
	tier := map[string]*rtStat{"flash": {name: "FLASH (all)"}, "pro": {name: "PRO (all)"}}
	for _, n := range names {
		r := routes[n]
		if n == "-" {
			fmt.Fprintf(&b, "(%d finishes ran no model: script cards with no price_route; left out)\n", r.takes)
			continue
		}
		b.WriteString(rtFormat(r))
		if t := tier[strings.SplitN(n, "-", 2)[0]]; t != nil {
			t.takes += r.takes
			t.ok += r.ok
			t.failed += r.failed
			t.noResult += r.noResult
			t.provider += r.provider
			t.landed += r.landed
			t.first += r.first
			t.reviewed += r.reviewed
			t.wrong += r.wrong
			t.usd += r.usd
			t.imputed += r.imputed
			t.walls = append(t.walls, r.walls...)
		}
	}
	for _, k := range []string{"flash", "pro"} {
		b.WriteString(rtFormat(tier[k]))
	}
	return b.String()
}

type rtUsage struct {
	route          string
	wall, usd, run float64
	priced         bool
}

type rtTake struct {
	key     string
	primary string
	attempt int
	at      time.Time
	end     string
	u       rtUsage
	imputed bool
}

type rtStat struct {
	name                                  string
	takes, ok, failed, noResult, provider int
	landed, first                         int
	reviewed, wrong                       int
	usd                                   float64
	imputed                               int
	walls                                 []float64
}

// rtFromUsage reads a usage line the way sprint.Stats does (cardcost.ParseUsage).
// actual_usd counts when the line writes it, with or without a token count
// (docs/SPEC-SPRINT.md, the verb stats).
func rtFromUsage(line string) rtUsage {
	u := cardcost.ParseUsage(line)
	out := rtUsage{route: u.Route}
	if w, ok := wallSeconds(u.Wall); ok {
		out.wall = w
	}
	if u.Run != cardcost.Unreported {
		out.run = float64(u.Run)
	}
	if u.Actual != "" {
		if usd, err := strconv.ParseFloat(u.Actual, 64); err == nil {
			out.usd = usd
			out.priced = true
		}
	}
	return out
}

func rtAttempt(card string) (string, int) {
	i := strings.LastIndex(card, ".w")
	if i < 0 {
		return card, 0
	}
	n, _ := strconv.Atoi(card[i+2:])
	return card[:i], n
}

func rtReadAttempt(card string) (string, int) {
	parts := strings.Split(card, ".")
	for i := len(parts) - 1; i > 0; i-- {
		if strings.HasPrefix(parts[i], "r") {
			if n, err := strconv.Atoi(parts[i][1:]); err == nil {
				return strings.Join(parts[:i], "."), n
			}
		}
	}
	return card, 0
}

func rtMedian(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func rtFormat(r *rtStat) string {
	wrong := "-"
	if r.reviewed > 0 {
		wrong = fmt.Sprintf("%d/%d", r.wrong, r.reviewed)
	}
	perLand := "-"
	if r.landed > 0 {
		perLand = fmt.Sprintf("%.2f", r.usd/float64(r.landed))
	}
	first := "-"
	if r.landed > 0 {
		first = fmt.Sprintf("%d%%", 100*r.first/r.landed)
	}
	denom := r.takes
	if denom < 1 {
		denom = 1
	}
	return fmt.Sprintf("%-30s %5d %4d %6d %6d %5d %6d %5s %6s %7.2f %7s %6.0fs %4d\n",
		r.name, r.takes, r.ok, r.failed, r.noResult, r.provider, r.landed, first, wrong, r.usd/float64(denom), perLand, rtMedian(r.walls), r.imputed)
}
