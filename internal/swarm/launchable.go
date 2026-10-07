package swarm

import (
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/harness"
)

// WHICH ROUTES A MEMBER CAN LAUNCH (docs/SPEC-SWARM.md, the headless harnesses). A route
// names the harness a card on it runs under (internal/harness): opencode, launched through
// the providers table with a provider key, or a headless program (claude, codex, grok)
// that runs on the machine's own subscription login. Only some machines hold a given
// login, and the machine row says which (nova-config machine set <m> --harnesses
// claude,codex; opencode only by default; config.HarnessesOf). A member draws only the
// routes whose harness its machine lists: on 2026-10-06 a heavy subscription-claude route
// was drawn by four fleet members that hold no claude login, and 73
// attempts ended in one second "launch refused", each a failure of the card, the member
// and the route. A route no member up can launch is one judgment naming the route and the
// machines (UnservedRoute), never one failure per card.
//
// Everything here is a pure function of its arguments, so the deal and the ask
// (internal/sprint) and the member's own launch read one rule.

// LaunchRoute is what the draw reads of one route: its name and the harness it runs under
// ("" is opencode, the route kind's default).
type LaunchRoute struct {
	Name    string
	Harness string
}

// routeHarness is the harness word a route runs under, opencode for none.
func routeHarness(h string) string {
	if h == "" {
		return harness.OpenCode
	}
	return h
}

// CanLaunch says a member whose machine lists harnesses can launch a route that runs
// under kind: the harness is in the list. A list that is empty is the machine row's
// default, opencode only.
func CanLaunch(harnesses []string, kind string) bool {
	if len(harnesses) == 0 {
		harnesses = []string{harness.OpenCode}
	}
	return slices.Contains(harnesses, routeHarness(kind))
}

// Launchable is the routes, in their order, that a member whose machine lists harnesses
// can launch: what it may draw.
func Launchable(routes []LaunchRoute, harnesses []string) []LaunchRoute {
	var out []LaunchRoute
	for _, r := range routes {
		if CanLaunch(harnesses, r.Harness) {
			out = append(out, r)
		}
	}
	return out
}

// Serves says a member whose machine lists harnesses can serve a tier whose routes are
// routes: it can launch at least one of them. A tier with no route is served by every
// member (the member runs its own --model). The deal and the ask skip a member that does
// not serve the card's tier instead of dealing to it.
func Serves(harnesses []string, routes []LaunchRoute) bool {
	return len(routes) == 0 || len(Launchable(routes, harnesses)) > 0
}

// MembersServing is the members up, in name order, that serve a tier whose routes are
// routes (Serves); up is each member's machine harness list.
func MembersServing(up map[string][]string, routes []LaunchRoute) []string {
	var out []string
	for m, hs := range up {
		if Serves(hs, routes) {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// UnservedRoute is a route no member up can launch: its harness, and the machines up,
// none of which lists it.
type UnservedRoute struct {
	Route    string
	Harness  string
	Machines []string
}

// Unserved is the routes, in their order, that no member up can launch; up is each
// member's machine harness list. Each is one judgment (Judgment), raised once for the
// route, never one failure per card dealt on it.
func Unserved(routes []LaunchRoute, up map[string][]string) []UnservedRoute {
	machines := make([]string, 0, len(up))
	for m := range up {
		machines = append(machines, m)
	}
	sort.Strings(machines)
	var out []UnservedRoute
	for _, r := range routes {
		served := false
		for _, m := range machines {
			served = served || CanLaunch(up[m], r.Harness)
		}
		if !served {
			out = append(out, UnservedRoute{Route: r.Name, Harness: routeHarness(r.Harness), Machines: machines})
		}
	}
	return out
}

// Judgment is the one sentence a route no member up can launch is raised as: the route,
// its harness, the machines up, and the remedy.
func (u UnservedRoute) Judgment() string {
	on := "no member is up"
	if len(u.Machines) > 0 {
		on = "no machine up lists it (" + strings.Join(u.Machines, ", ") + ")"
	}
	return "route " + u.Route + " runs under " + u.Harness + " and " + on + ": log " + u.Harness + " in on a machine, list it there (nova-config machine set <m> --harnesses opencode," + u.Harness + ") and run nova-config apply, or disable the route"
}
