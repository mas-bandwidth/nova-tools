package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Rule 3 of the sprint's cost rules (nova-tools#5174; the owner, 2026-10-02): "A route
// whose children end without a result three times is rested by the machine, never redealt
// on." The tick counts the ended takes of each route over a sliding window, the last
// RouteRestWindow takes on it that ended after its last rest began, and when
// RouteRestAfter of them left no result it rests the route for RouteRestFor: the rest is
// a fleet table property, route_rest_<route>, written in the deal's batch with a happened
// note to the coordinator naming the cards; the deal draws no work card (a first deal, a
// redeal or a rework's) on a resting route, and the rest ends by itself at its time. The
// rest is the sprint's, never config: nova-config's enabled stays the coordinator's.
const (
	RouteRestWindow = 10
	RouteRestAfter  = 3
	RouteRestFor    = 30 * time.Minute
)

// PropRouteRest is the fleet table's property that holds a route's last rest.
func PropRouteRest(route string) string { return "route_rest_" + route }

// NRouteRested is the happened note of a rest the tick wrote, to the coordinator.
const NRouteRested = "a route rested: its children ended with no result"

// RouteRest is a route's last rest: when it began, when it ends, and the cards whose
// takes on it left no result.
type RouteRest struct {
	Route string    `json:"route"`
	At    time.Time `json:"at"`
	Until time.Time `json:"until"`
	Cards []string  `json:"cards"`
}

// Resting says the rest holds at now.
func (r RouteRest) Resting(now time.Time) bool { return now.Before(r.Until) }

// value is the rest as the property holds it: its start, its end, the cards.
func (r RouteRest) value() string {
	return stamp(r.At) + " " + stamp(r.Until) + " " + strings.Join(r.Cards, ",")
}

// RouteRests is the last rest the fleet table records for each route of routes that has one.
func RouteRests(routes []Route, fleet *Table) map[string]RouteRest {
	out := map[string]RouteRest{}
	for _, r := range routes {
		v, _ := fleet.Prop(PropRouteRest(r.Name))
		f := strings.Fields(v)
		if len(f) < 2 {
			continue
		}
		at, e1 := time.Parse(time.RFC3339, f[0])
		until, e2 := time.Parse(time.RFC3339, f[1])
		if e1 != nil || e2 != nil {
			continue
		}
		rest := RouteRest{Route: r.Name, At: at, Until: until}
		if len(f) > 2 {
			rest.Cards = Split(f[2])
		}
		out[r.Name] = rest
	}
	return out
}

// routeEnd is one ended take on a route: the work card, when it ended, and whether its
// child left no result.
type routeEnd struct {
	card     string
	take     int
	at       time.Time
	noResult bool
}

// routeEnds is every ended take the fleet table's work cards record, by route: each take
// a provider failed or that left no result (ProviderTakes), and each card's own finish.
// A take that ended with its member down keeps no record and is not counted.
func routeEnds(fleet *Table) map[string][]routeEnd {
	out := map[string][]routeEnd{}
	for _, c := range fleet.Column(Ready, Working, DoneOK, DoneFailed, Withdrawn) {
		takes, numbers := ProviderTakes(c)
		for i, t := range takes {
			at, err := time.Parse(time.RFC3339, t.Finished)
			if t.Route == "" || t.Route == RoutePin || err != nil {
				continue
			}
			out[t.Route] = append(out[t.Route], routeEnd{card: c.ID, take: numbers[i], at: at, noResult: IsNoResult(t.Error)})
		}
		if c.Col != DoneOK && c.Col != DoneFailed {
			continue
		}
		at, err := time.Parse(time.RFC3339, c.F("finished"))
		if r := c.F(FieldRoute); r != "" && r != RoutePin && err == nil {
			out[r] = append(out[r], routeEnd{card: c.ID, take: c.Int("redeals") + 1, at: at})
		}
	}
	return out
}

// RestsDue is the rests the rule writes now: each route of the store not resting at s.Now
// whose window (its last RouteRestWindow ended takes after its last rest began, in the
// order they ended) holds RouteRestAfter or more that left no result, rested from s.Now
// for RouteRestFor, naming those takes' cards; in route name order.
func RestsDue(s *Snapshot) []RouteRest {
	if len(s.Routes) == 0 {
		return nil
	}
	rests, ends := RouteRests(s.Routes, s.Fleet), routeEnds(s.Fleet)
	var out []RouteRest
	for _, r := range s.Routes {
		last, had := rests[r.Name]
		if had && last.Resting(s.Now) {
			continue
		}
		var window []routeEnd
		for _, e := range ends[r.Name] {
			if !had || e.at.After(last.At) {
				window = append(window, e)
			}
		}
		slices.SortFunc(window, cmpEnd)
		window = window[max(0, len(window)-RouteRestWindow):]
		var cards []string
		for _, e := range window {
			if e.noResult {
				cards = append(cards, e.card)
			}
		}
		if len(cards) >= RouteRestAfter {
			out = append(out, RouteRest{Route: r.Name, At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: cards})
		}
	}
	slices.SortFunc(out, func(a, b RouteRest) int { return strings.Compare(a.Route, b.Route) })
	return out
}

// cmpEnd orders ended takes by when they ended, then by card and take.
func cmpEnd(a, b routeEnd) int {
	if c := a.at.Compare(b.at); c != 0 {
		return c
	}
	if c := strings.Compare(a.card, b.card); c != 0 {
		return c
	}
	return a.take - b.take
}

// withRests is the snapshot with the routes resting at s.Now settled (rests): those the
// fleet table records and those the rule rests now (RestsDue, returned as due), so a step
// that deals reads them once. A snapshot that has them already is returned as it is.
func (s *Snapshot) withRests() (*Snapshot, []RouteRest) {
	if s.rests != nil {
		return s, nil
	}
	n := *s
	n.rests = map[string]RouteRest{}
	for name, r := range RouteRests(s.Routes, s.Fleet) {
		if r.Resting(s.Now) {
			n.rests[name] = r
		}
	}
	due := RestsDue(s)
	for _, r := range due {
		n.rests[r.Route] = r
	}
	return &n, due
}

// resting is the rest that holds a route at s.Now (withRests), and whether one does.
func (s *Snapshot) resting(route string) (RouteRest, bool) {
	rests := s.rests
	if rests == nil {
		t, _ := s.withRests()
		rests = t.rests
	}
	r, ok := rests[route]
	return r, ok
}

// restWrites puts each due rest in the plan: the fleet table's property, guarded on the
// value read, and a happened note to the coordinator naming the cards.
func restWrites(p *Plan, s *Snapshot, due []RouteRest, who string) {
	for _, r := range due {
		was, had := s.Fleet.Prop(PropRouteRest(r.Route))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropRouteRest(r.Route), Value: r.value(), Was: was, WasAbsent: !had})
		tier := ""
		for _, x := range s.Routes {
			if x.Name == r.Route {
				tier = x.Tier
			}
		}
		n := happened(NRouteRested, TierSubject(tier), s.Now)
		n.To, n.Who = s.Coordinator, who
		n.What = fmt.Sprintf("route %s rested until %s: %d of its last %d ended takes or fewer left no result (%s); the deal draws no work card on it until then; nova-sprint routes shows it",
			r.Route, stamp(r.Until), len(r.Cards), RouteRestWindow, strings.Join(r.Cards, ", "))
		p.Notes = append(p.Notes, n)
	}
}
