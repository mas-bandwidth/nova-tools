package main

import (
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbClasses["routes rest"] = classCoordinator
	verbClasses["routes wake"] = classCoordinator
	verbEffect["routes rest"] = "local write: rests every route of the provider, or the one route, in the sprint's store until the time or until routes wake (the deal draws no work card on them); --dry-run writes nothing"
	verbEffect["routes wake"] = "local write: ends the rest holding the provider or the route in the sprint's store now, with the reason (never a payment: that is funded's); --dry-run writes nothing"
	stepDryRun["routes rest"] = true
	stepDryRun["routes wake"] = true
}

// cmdRoutesRest is routes rest <provider|route> --reason <text> [--for <duration> | --until
// <RFC3339>]: the coordinator's rest of a provider's routes or of one route (the owner,
// 2026-10-10: a low balance is raised to the coordinator, never rested by the machine;
// sprint/route_coordinator.go, tla/RouteRest.tla).
func (a *app) cmdRoutesRest(args []string, stdout, stderr io.Writer) int {
	const name = "routes rest"
	fs, c := a.verbSetup(name)
	reason := fs.String("reason", "", "why the routes rest, in a few words (required)")
	dur := fs.String("for", "", "how long they rest, a duration from now (30m, 2h); without --for or --until they rest until routes wake")
	until := fs.String("until", "", "when they serve again, an RFC3339 time, instead of --for")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one word, a provider or a route as nova-sprint routes names it, ", err, pos...))
	}
	now := a.now()
	var at time.Time
	switch {
	case *dur != "" && *until != "":
		return refuse(stderr, name, "give --for <duration> or --until <RFC3339>, not both")
	case *dur != "":
		d, err := time.ParseDuration(*dur)
		if err != nil || d <= 0 {
			return refuse(stderr, name, "--for wants a duration above zero (30m, 2h), found "+*dur)
		}
		at = now.Add(d)
	case *until != "":
		t, err := time.Parse(time.RFC3339, *until)
		if err != nil {
			return refuse(stderr, name, "--until wants an RFC3339 time (2026-10-10T06:00:00Z), found "+*until)
		}
		at = t
	}
	if strings.TrimSpace(*reason) == "" {
		return refuse(stderr, name, "--reason <text> is required: why the routes rest")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, store.RouteRestStep(sprint.RouteRestReq{Target: pos[0], Reason: *reason, Until: at, Who: c.actor}), stdout, stderr)
}

// cmdRoutesWake is routes wake <provider|route> --reason <text>: the coordinator ends the
// rest holding a provider (any cause) or a route now. It is never a payment: funded says one.
func (a *app) cmdRoutesWake(args []string, stdout, stderr io.Writer) int {
	const name = "routes wake"
	fs, c := a.verbSetup(name)
	reason := fs.String("reason", "", "why the routes serve again, in a few words (required; a payment is funded's)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one word, a provider or a route as nova-sprint routes names it, ", err, pos...))
	}
	if strings.TrimSpace(*reason) == "" {
		return refuse(stderr, name, "--reason <text> is required: why the routes serve again")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	return a.runStep(name, *c, st, store.RouteWakeStep(sprint.RouteWakeReq{Target: pos[0], Reason: *reason, Who: c.actor}), stdout, stderr)
}
