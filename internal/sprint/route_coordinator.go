package sprint

import (
	"fmt"
	"strings"
	"time"
)

// THE COORDINATOR RESTS AND WAKES ROUTES (the owner, 2026-10-10, through the seat: a low
// balance "should raise it to you as a thing to do, but not do it automatically."). The
// machine rests a route only on a provider-typed failure (route_rest.go); every other rest
// is the coordinator's word, and so is ending any rest sooner than its time:
//
//   - routes rest <provider|route> --reason <why> [--until <time>] rests every route of the
//     provider (its one property, PropProviderRest) or the one route (its line in
//     PropRule3Rest), cause RestCoordinator, until the time given, else until routes wake;
//   - routes wake <provider|route> --reason <why> ends the rest that holds the provider (any
//     cause: a refusal of credit or of its key, the coordinator's) or the route (its own),
//     now, the reason on the rest's words. It is never a payment: funded stays the word for
//     one, and the seat never says funded to lift a rest no payment ended.
//
// Each writes one property, guarded on the value read, with a happened note to the
// coordinator (tla/RouteRest.tla, CoordinatorRest and CoordinatorWake).

// NRouteWoken is the happened note of a rest the coordinator ended (routes wake), and
// NRouteRestedByCoordinator of one the coordinator began (routes rest).
const (
	NRouteWoken               = "a route serves again: the coordinator woke it"
	NRouteRestedByCoordinator = "a route rested: the coordinator rested it"
)

// RouteRestReq is the coordinator's rest of a provider's routes or of one route: Target
// names a provider (as the routes name it) or a route; Until is zero for a rest until woken.
type RouteRestReq struct {
	Target string
	Reason string
	Until  time.Time
	Who    string
}

// RouteWakeReq is the coordinator's end of the rest that holds a provider or a route.
type RouteWakeReq struct {
	Target string
	Reason string
	Who    string
}

// routeTarget is what a target names: a provider (provider true, its routes) or one route
// (its provider); ok is false when it names neither.
func routeTarget(s *Snapshot, target string) (provider string, routes []string, isProvider, ok bool) {
	for _, r := range s.Routes {
		if r.Name == target {
			return r.Provider, []string{r.Name}, false, true
		}
	}
	for _, r := range s.Routes {
		if r.Provider == target {
			routes = append(routes, r.Name)
		}
	}
	return target, routes, true, len(routes) > 0
}

// RestRoutes is routes rest: the provider's routes or the one route rest from s.Now, cause
// RestCoordinator, until r.Until (zero: until woken), refused with no reason, a target that
// names no route or provider, or a time not after now.
func RestRoutes(s *Snapshot, r RouteRestReq) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse(r.Target, "routes rest wants --reason: why the routes rest, in a few words")
		return p
	}
	provider, routes, isProvider, ok := routeTarget(s, r.Target)
	if !ok {
		p.refuse(r.Target, r.Target+" names no route and no provider of a route (nova-sprint routes lists them)")
		return p
	}
	until := OpenUntil
	if !r.Until.IsZero() {
		if !r.Until.After(s.Now) {
			p.refuse(r.Target, "--until "+stamp(r.Until)+" is not after now ("+stamp(s.Now)+")")
			return p
		}
		until = r.Until
	}
	rest := RouteRest{At: s.Now, Until: until, Cause: RestCoordinator, Why: oneLine("rested by " + r.Who + ": " + r.Reason)}
	// a rest that holds is never replaced (tla/RouteRest.tla, RestProvider and RestRoute: a
	// rest begins only on a serving provider or route): a refused take's credit rest would
	// end with the coordinator's time and take the provider out of the all-out stop
	if pr, ok := ProviderRests(s.Fleet)[provider]; ok && pr.Resting(s.Now) && isProvider {
		p.refuse(r.Target, "provider "+provider+" rests already until "+pr.UntilSaid()+" ("+pr.Cause+": "+pr.Said()+"): nova-sprint routes wake "+provider+" ends it first")
		return p
	}
	if !isProvider {
		if own, ok := parseRule3(func() string { v, _ := s.Fleet.Prop(PropRule3Rest(provider)); return v }())[routes[0]]; ok && own.Resting(s.Now) {
			p.refuse(r.Target, "route "+routes[0]+" rests already until "+own.UntilSaid()+" ("+own.Cause+": "+own.Said()+"): nova-sprint routes wake "+routes[0]+" ends it first")
			return p
		}
	}
	if isProvider {
		rest.Provider = provider
		was, had := s.Fleet.Prop(PropProviderRest(provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(provider), Value: rest.value(), Was: was, WasAbsent: !had})
	} else {
		rest.Route = routes[0]
		name := PropRule3Rest(provider)
		was, had := s.Fleet.Prop(name)
		merged := parseRule3(was)
		merged[rest.Route] = rest
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: rule3Text(merged), Was: was, WasAbsent: !had})
	}
	n := happened(NRouteRestedByCoordinator, ProviderSubject(provider), s.Now)
	n.To, n.Who = s.Coordinator, r.Who
	n.What = fmt.Sprintf("routes %s rest until %s: %s; the deal draws no work card on them until then; nova-sprint routes wake %s ends it", strings.Join(routes, ", "), rest.UntilSaid(), rest.Why, r.Target)
	p.Notes = append(p.Notes, n)
	p.Units = append(p.Units, Unit{Key: r.Target, Moved: "routes " + strings.Join(routes, ", ") + " rest until " + rest.UntilSaid()})
	return p
}

// WakeRoutes is routes wake: the rest holding the provider (its one property, any cause)
// and its routes' own rests, or the one route's own rest, ends at s.Now with the reason on
// its words; refused with no reason, a target that names no route or provider, nothing
// resting, or a route whose provider's rest holds it (wake the provider).
func WakeRoutes(s *Snapshot, r RouteWakeReq) Plan {
	var p Plan
	if strings.TrimSpace(r.Reason) == "" {
		p.refuse(r.Target, "routes wake wants --reason: why the routes serve again, in a few words (a payment is funded's)")
		return p
	}
	provider, routes, isProvider, ok := routeTarget(s, r.Target)
	if !ok {
		p.refuse(r.Target, r.Target+" names no route and no provider of a route (nova-sprint routes lists them)")
		return p
	}
	end := func(rest RouteRest) RouteRest {
		rest.Until = s.Now // the rest's end: a refusal launched before it starts no new rest
		rest.Why = oneLine(rest.Why + "; ended: woken by " + r.Who + ": " + r.Reason)
		return rest
	}
	pr, hasProvider := ProviderRests(s.Fleet)[provider]
	providerResting := hasProvider && pr.Resting(s.Now)
	if !isProvider && providerResting {
		p.refuse(r.Target, "route "+r.Target+" rests with its provider "+provider+" ("+pr.Said()+"): nova-sprint routes wake "+provider+" ends it")
		return p
	}
	woke := false
	if isProvider && providerResting {
		was, _ := s.Fleet.Prop(PropProviderRest(provider))
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropProviderRest(provider), Value: end(pr).value(), Was: was})
		woke = true
	}
	name := PropRule3Rest(provider)
	was, had := s.Fleet.Prop(name)
	own := parseRule3(was)
	changed := false
	for _, route := range routes {
		if rest, ok := own[route]; ok && !rest.Retired() && rest.Resting(s.Now) {
			own[route] = end(rest)
			changed = true
		}
	}
	if changed {
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: rule3Text(own), Was: was, WasAbsent: !had})
		woke = true
	}
	if !woke {
		p.refuse(r.Target, r.Target+" does not rest: nothing to wake (nova-sprint routes shows each route's rest)")
		return p
	}
	n := happened(NRouteWoken, ProviderSubject(provider), s.Now)
	n.To, n.Who = s.Coordinator, r.Who
	n.What = fmt.Sprintf("routes %s serve again: woken by %s: %s", strings.Join(routes, ", "), r.Who, oneLine(r.Reason))
	p.Notes = append(p.Notes, n)
	p.Units = append(p.Units, Unit{Key: r.Target, Moved: "routes " + strings.Join(routes, ", ") + " woken"})
	return p
}
