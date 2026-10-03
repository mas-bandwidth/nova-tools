package sprint

import (
	"cmp"
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

// PropProviderRest is the fleet table's property that holds a provider's last rest: ONE per
// provider, never a copy on each of its routes, so a provider's rest is one write and the
// table's properties (ntable.LimitTableProps) grow with the providers, not the routes.
func PropProviderRest(provider string) string { return "provider_rest_" + provider }

// NRouteRested is the happened note of a rest the tick wrote, to the coordinator, and
// NProviderRested the note of a provider's (provider_funds.go).
const (
	NRouteRested    = "a route rested: its children ended with no result"
	NProviderRested = "a provider rested: its funds or its key"
)

// RouteRest is a rest: rule 3's of a route (Route, PropRouteRest) or a provider's (Provider,
// PropProviderRest; read through RouteRests it names each route of the provider too): when
// it began, when it ends (for a rest that has ended, when it ended), the cards whose takes
// rested it, and why: the cause (RestNoResult, rule 3's; RestCredit, RestAuth and
// RestBalance, the provider's, provider_funds.go) and, for the provider's, its words.
type RouteRest struct {
	Route     string
	Provider  string
	At, Until time.Time
	Cards     []string
	Cause     string
	Why       string
}

// The causes of a rest: rule 3's children that ended with no result; a provider out of
// credit (a take it refused for want of credit, or a balance at or under zero); a take it
// refused for its key; and a provider low on funds, its balance over zero but not over one
// hour of its spend (balance.go). Only out of credit counts toward stopping the sprint.
const (
	RestNoResult = "no-result"
	RestCredit   = "out-of-credit"
	RestAuth     = "auth"
	RestBalance  = "balance"
)

// OpenUntil is the end of a rest that has no time: a provider out of credit rests until a
// balance poll reads a balance again, or the coordinator says it was paid (funded), never
// until a clock (the owner, 2026-10-03: "exclude that provider moving forward"). The
// property holds it as `open`.
var OpenUntil = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// restOpen is how the property and the lines say OpenUntil.
const restOpen = "open"

// Open says the rest has no time: it holds until a balance returns.
func (r RouteRest) Open() bool { return r.Until.Equal(OpenUntil) }

// UntilSaid is when the rest ends as a line says it: its time, or until a balance returns.
func (r RouteRest) UntilSaid() string {
	if r.Open() {
		return "a balance returns"
	}
	return stamp(r.Until)
}

// Resting says the rest holds at now.
func (r RouteRest) Resting(now time.Time) bool { return now.Before(r.Until) }

// Funds says the rest is the provider's want of funds: out of credit, or low.
func (r RouteRest) Funds() bool { return r.Cause == RestCredit || r.Cause == RestBalance }

// Out says the provider is out of credit (RestCredit): the one rest that counts toward
// stopping the sprint (AllOutOfCredit). A provider low on funds still has money, and the
// sprint never stops while one does.
func (r RouteRest) Out() bool { return r.Cause == RestCredit }

// value is the rest as the property holds it: its start, its end, the cards ("-" for
// none), its cause and its words. A value of the first three alone (a rest written before
// the cause) reads as rule 3's.
func (r RouteRest) value() string {
	cards := strings.Join(r.Cards, ",")
	if cards == "" {
		cards = "-"
	}
	until := stamp(r.Until)
	if r.Open() {
		until = restOpen
	}
	return strings.TrimSpace(stamp(r.At) + " " + until + " " + cards + " " + cmp.Or(r.Cause, RestNoResult) + " " + r.Why)
}

// Said is the rest's reason as a line says it: the provider's words, else rule 3's.
func (r RouteRest) Said() string {
	if r.Why != "" {
		return r.Why
	}
	return "its children ended with no result"
}

// parseRest is a rest as its property holds it (value); ok is false for a value that is
// not one.
func parseRest(v string) (RouteRest, bool) {
	f := strings.Fields(v)
	if len(f) < 2 {
		return RouteRest{}, false
	}
	at, e1 := time.Parse(time.RFC3339, f[0])
	until, e2 := OpenUntil, error(nil)
	if f[1] != restOpen {
		until, e2 = time.Parse(time.RFC3339, f[1])
	}
	if e1 != nil || e2 != nil {
		return RouteRest{}, false
	}
	rest := RouteRest{At: at, Until: until, Cause: RestNoResult}
	if len(f) > 2 && f[2] != "-" {
		rest.Cards = Split(f[2])
	}
	if len(f) > 3 {
		rest.Cause, rest.Why = f[3], strings.Join(f[4:], " ")
	}
	return rest, true
}

// ProviderRests is the last rest the fleet table records for each provider that has one.
func ProviderRests(fleet *Table) map[string]RouteRest {
	out := map[string]RouteRest{}
	if fleet == nil {
		return out
	}
	for name, v := range fleet.Props() {
		p, ok := strings.CutPrefix(name, PropProviderRest(""))
		if !ok {
			continue
		}
		if rest, ok := parseRest(v); ok {
			rest.Provider = p
			out[p] = rest
		}
	}
	return out
}

// RouteRests is the rest that decides each route of routes that has one: its own last rest
// (rule 3's) or its provider's, whichever ends later, the provider's on a tie, named for
// the route.
func RouteRests(routes []Route, fleet *Table) map[string]RouteRest {
	out := map[string]RouteRest{}
	byProvider := ProviderRests(fleet)
	for _, r := range routes {
		v, _ := fleet.Prop(PropRouteRest(r.Name))
		own, hasOwn := parseRest(v)
		pr, hasProvider := byProvider[r.Provider]
		switch {
		case r.Provider != "" && hasProvider && (!hasOwn || !own.Until.After(pr.Until)):
			pr.Route = r.Name
			out[r.Name] = pr
		case hasOwn:
			own.Route = r.Name
			out[r.Name] = own
		}
	}
	return out
}

// restedRoutes is the routes a rest holds: its route, or every route of its provider.
func restedRoutes(r RouteRest, routes []Route) []string {
	if r.Route != "" {
		return []string{r.Route}
	}
	var out []string
	for _, x := range routes {
		if x.Provider == r.Provider {
			out = append(out, x.Name)
		}
	}
	return out
}

// routeEnd is one ended take on a route: the work card, when it ended, when it was taken
// (its child launched; zero when its record holds none), whether its child left no result,
// and the provider's line when the provider refused it for credit or for its key (refusal,
// provider_funds.go), "" otherwise.
type routeEnd struct {
	card      string
	take      int
	at, taken time.Time
	noResult  bool
	refused   string
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
			taken, _ := time.Parse(time.RFC3339, t.Taken)
			out[t.Route] = append(out[t.Route], routeEnd{card: c.ID, take: numbers[i], at: at, taken: taken, noResult: IsNoResult(t.Error), refused: refusal(t.Error)})
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
// for RouteRestFor, naming those takes' cards; and each provider a refusal rests
// (providerRestsDue: one rest, Route "", over rule 3's on its routes); the providers' first,
// then the routes', in name order.
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
			out = append(out, RouteRest{Route: r.Name, At: s.Now, Until: s.Now.Add(RouteRestFor), Cards: cards, Cause: RestNoResult})
		}
	}
	// a provider's refusal rests the provider, every route of it, over rule 3's on its routes
	byProvider := providerRestsDue(s, ProviderRests(s.Fleet), ends)
	if len(byProvider) == 0 {
		return out
	}
	rested := map[string]bool{}
	for _, p := range byProvider {
		for _, name := range restedRoutes(p, s.Routes) {
			rested[name] = true
		}
	}
	return append(byProvider, slices.DeleteFunc(out, func(r RouteRest) bool { return rested[r.Route] })...)
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
	if s.restScans != nil {
		*s.restScans++
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
		for _, name := range restedRoutes(r, s.Routes) {
			x := r
			x.Route = name
			n.rests[name] = x
		}
	}
	return &n, due
}

// resting is the rest that holds a route at s.Now, and whether one does: the rests a step
// that deals settled once (withRests: TickDeal, Deal, Rework and the held rule's newHeld),
// never computed here, so no draw scans the fleet table per card.
func (s *Snapshot) resting(route string) (RouteRest, bool) {
	r, ok := s.rests[route]
	return r, ok
}

// restWrites puts each due rest in the plan: the fleet table's property (the route's, or the
// provider's one), guarded on the value read, and a happened note to the coordinator naming
// the cards, or the provider's words.
func restWrites(p *Plan, s *Snapshot, due []RouteRest, who string) {
	for _, r := range due {
		if r.Route == "" {
			was, had := s.Fleet.Prop(PropProviderRest(r.Provider))
			p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(r.Provider), Value: r.value(), Was: was, WasAbsent: !had})
			n := happened(NProviderRested, ProviderSubject(r.Provider), s.Now)
			n.To, n.Who = s.Coordinator, who
			n.What = fmt.Sprintf("provider %s rested until %s, its routes %s: %s; the deal draws no work card on them until then; nova-sprint routes shows it",
				r.Provider, r.UntilSaid(), strings.Join(restedRoutes(r, s.Routes), ", "), r.Why)
			p.Notes = append(p.Notes, n)
			continue
		}
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
