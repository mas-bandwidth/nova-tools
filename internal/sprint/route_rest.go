package sprint

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Rule 3 of the sprint's cost rules (nova-tools#5174; the owner, 2026-10-02): "A route
// whose children end without a result three times is rested by the machine, never redealt
// on." The tick counts the ended takes of each route over a sliding window, the last
// RouteRestWindow takes on it that ended after its last rest began, and when
// RouteRestAfter of them left no result it rests the route for RouteRestFor: the rest is
// one line in the fleet table's property rule3_rest_<provider>, one property per provider
// however many of its routes rest, written in the deal's batch with a happened note to the
// coordinator naming the cards; the deal draws no work card (a first deal, a redeal or a
// rework's) on a resting route, and the rest ends by itself at its time. The rest is the
// sprint's, never config: nova-config's enabled stays the coordinator's.
const (
	RouteRestWindow = 10
	RouteRestAfter  = 3
	RouteRestFor    = 30 * time.Minute
)

// PropRule3Rest is the fleet table's property that holds one provider's rule-3 rests:
// one property per provider, one line per route that has rested, so the table's
// properties (ntable.LimitTableProps) grow with the providers and never with the routes.
func PropRule3Rest(provider string) string { return "rule3_rest_" + provider }

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

// RouteRest is a rest: rule 3's of a route (Route, one line of PropRule3Rest) or a provider's
// (Provider, PropProviderRest; read through RouteRests it names each route of the provider too): when
// it began, when it ends (for a rest that has ended, when it ended), the cards whose takes
// rested it, and why: the cause (RestNoResult, rule 3's; RestCredit, RestAuth and
// RestBalance, the provider's, provider_funds.go) and, for the provider's, its words. A
// provider's rest a refused take began keeps the balance the poll last read then (Balance,
// HasBalance false when there was none known), the mark a payment is seen against
// (balance.go).
type RouteRest struct {
	Route      string
	Provider   string
	At, Until  time.Time
	Cards      []string
	Cause      string
	Balance    float64
	HasBalance bool
	Why        string
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

// OpenUntil is the end of a rest that has no time: a provider resting for its funds rests
// until it is paid, a payment the balance poll sees (balance.go) or the coordinator's word
// that it was paid (funded), never until a clock (the owner, 2026-10-03: "exclude that
// provider moving forward"). The property holds it as `open`.
var OpenUntil = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// restOpen is how the property and the lines say OpenUntil.
const restOpen = "open"

// Open says the rest has no time: it holds until the provider is paid.
func (r RouteRest) Open() bool { return r.Until.Equal(OpenUntil) }

// UntilSaid is when the rest ends as a line says it: its time, or until the provider is paid.
func (r RouteRest) UntilSaid() string {
	if r.Open() {
		return "paid"
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

// Refused says the provider is out of credit because it refused a take (the rest names the
// take's card), not because a balance read at or under zero: OpenRouter refuses a request
// whose estimated cost the balance cannot cover, so a provider that refuses can still read a
// small balance over zero, and only a payment seen or funded ends this rest (balance.go).
func (r RouteRest) Refused() bool { return r.Out() && len(r.Cards) > 0 }

// restBalance is the token before a rest's words that holds the balance read when a refused
// take began it (RouteRest.Balance).
const restBalance = "balance="

// value is the rest as the property holds it: its start, its end, the cards ("-" for
// none), its cause, the balance read when a refused take began it (`balance=<x>`, when
// known) and its words. A value of the first three alone (a rest written before the cause)
// reads as rule 3's.
func (r RouteRest) value() string {
	cards := strings.Join(r.Cards, ",")
	if cards == "" {
		cards = "-"
	}
	until := stamp(r.Until)
	if r.Open() {
		until = restOpen
	}
	bal := ""
	if r.HasBalance {
		bal = " " + restBalance + strconv.FormatFloat(r.Balance, 'f', -1, 64)
	}
	return strings.TrimSpace(stamp(r.At) + " " + until + " " + cards + " " + cmp.Or(r.Cause, RestNoResult) + bal + " " + r.Why)
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
		rest.Cause, f = f[3], f[4:]
		if len(f) > 0 {
			if v, ok := strings.CutPrefix(f[0], restBalance); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					rest.Balance, rest.HasBalance, f = x, true, f[1:]
				}
			}
		}
		rest.Why = strings.Join(f, " ")
	}
	return rest, true
}

// parseRule3 is one provider's rule-3 rests as PropRule3Rest holds them: one line per
// route, `<route> <rest>`, the rest as parseRest reads it. A line that is not one is
// skipped. The route name is the line's first field, one token.
func parseRule3(v string) map[string]RouteRest {
	out := map[string]RouteRest{}
	for _, line := range strings.Split(v, "\n") {
		line = strings.TrimSpace(line)
		route, restv, ok := strings.Cut(line, " ")
		if !ok || route == "" {
			continue
		}
		rest, ok := parseRest(restv)
		if !ok {
			continue
		}
		rest.Route = route
		out[route] = rest
	}
	return out
}

// rule3Text is the provider's rule-3 rests as PropRule3Rest holds them, lines in
// route-name order, so a later write guards on the same bytes.
func rule3Text(m map[string]RouteRest) string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(m[name].value())
	}
	return b.String()
}

// rule3Rests is each provider's rule-3 rests as the fleet table records them.
// A route_rest_<route> property is not read.
func rule3Rests(fleet *Table) map[string]map[string]RouteRest {
	out := map[string]map[string]RouteRest{}
	if fleet == nil {
		return out
	}
	for name, v := range fleet.Props() {
		p, ok := strings.CutPrefix(name, PropRule3Rest(""))
		if !ok {
			continue
		}
		out[p] = parseRule3(v)
	}
	return out
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
	rule3 := rule3Rests(fleet)
	for _, r := range routes {
		own, hasOwn := rule3[r.Provider][r.Name]
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

// cardRest is the rest that holds, at s.Now, the route a ready work card was dealt on: the
// rests a dealing step settled (withRests) when it has them, else its provider's property
// (the provider its model line names), so a take, which loads no routes, reads one property
// and no more. ok is false for a pinned card and for a route that serves. The take reads no
// route's own rest (rule 3's): only the tick writes one, and the same plan withdraws every
// ready card on that route (restWithdrawals), so no ready card is ever on one; a provider's
// rest is also written between ticks, by the balance poll.
func cardRest(s *Snapshot, c *Card) (RouteRest, bool) {
	route := c.F(FieldRoute)
	if route == "" || route == RoutePin {
		return RouteRest{}, false
	}
	if s.rests != nil {
		return s.resting(route)
	}
	provider, _, _ := strings.Cut(c.F(FieldModel), "/")
	v, _ := s.Fleet.Prop(PropProviderRest(provider))
	if rest, ok := parseRest(v); ok && rest.Resting(s.Now) {
		return rest, true
	}
	return RouteRest{}, false
}

// restWithdrawals is the tick's units that withdraw each ready work card dealt on a route
// resting at s.Now (nova-tools#5205, the third cold read): a card dealt before its
// provider's rest began is never taken and refused there (takeOne refuses it), so it is
// withdrawn while ready by the path a member going down takes (withdrawCard): no take ended,
// so no redeal is spent (redeal), and its primary goes back to ready for the next deal to
// place on a route that serves (routeOf), its timeline noting why (NRestWithdrawn).
func restWithdrawals(s *Snapshot, who string) []Unit {
	var out []Unit
	for _, c := range s.Fleet.Column(Ready) {
		if rest, ok := cardRest(s, c); ok {
			out = append(out, withdrawCard(s, c, false, NRestWithdrawn, who, "taken back: its route "+c.F(FieldRoute)+" rests ("+rest.Said()+")"))
		}
	}
	return out
}

// restWrites puts each due rest in the plan: the provider's one property, or one
// property per provider for the routes' rule-3 rests (every route of that provider
// that rests, the lines already held and the ones due now), guarded on the value
// read, and a happened note to the coordinator naming the cards, or the provider's words.
func restWrites(p *Plan, s *Snapshot, due []RouteRest, who string) {
	type batch struct {
		routes []RouteRest
	}
	grouped := map[string]*batch{}
	var order []string
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
		prov := routeProvider(s, r.Route)
		b := grouped[prov]
		if b == nil {
			b = &batch{}
			grouped[prov] = b
			order = append(order, prov)
		}
		b.routes = append(b.routes, r)
	}
	for _, prov := range order {
		b := grouped[prov]
		name := PropRule3Rest(prov)
		was, had := s.Fleet.Prop(name)
		merged := parseRule3(was)
		for _, r := range b.routes {
			merged[r.Route] = r
		}
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: rule3Text(merged), Was: was, WasAbsent: !had})
		for _, r := range b.routes {
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
}

// routeProvider is the provider the route's rule-3 rest is filed under. A route
// the snapshot does not know is filed with the routes that name no provider.
func routeProvider(s *Snapshot, route string) string {
	for _, x := range s.Routes {
		if x.Name == route {
			return x.Provider
		}
	}
	return ""
}
