package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	verbEffect["fleet quiet"] = "local write: sets or ends the member's quiet in the sprint's store (the deal gives it nothing until then, every worker's view carries a QUIET line); --dry-run writes nothing"
}

// cmdFleetQuiet is fleet quiet <member> (--for <duration> | --until <RFC3339>) --reason <text>,
// or --end: the coordinator's quiet on a machine (docs/SPEC-SPRINT.md section 5,
// fleet-quiet-machine-b.w7; sprint/fleet_quiet.go). The deal gives the member nothing until
// the time, its dealt work finishes, every worker's view says QUIET, and it ends by itself at
// the time.
func (a *app) cmdFleetQuiet(args []string, stdout, stderr io.Writer) int {
	const name = "fleet quiet"
	fs, c := a.verbSetup(name)
	dur := fs.String("for", "", "how long the member stays quiet, a duration from now (11m, 1h30m)")
	until := fs.String("until", "", "when the member's quiet ends, an RFC3339 time (2026-10-04T15:50:00-07:00), instead of --for")
	reason := fs.String("reason", "", "why, in words: every worker's QUIET line and the log carry it; a quiet wants one")
	end := fs.Bool("end", false, "end the member's quiet now, before its time")
	dry := fs.Bool("dry-run", false, "check the member, the time and the reason, print the quiet it would set or end, and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	now := a.now()
	var probs []string
	if len(pos) != 1 {
		probs = append(probs, "wants one fleet member: nova-sprint fleet quiet <member> --for <duration> --reason <text>")
	} else if !sprint.ValidID(pos[0]) {
		probs = append(probs, "a member name wants letters, digits, _ and -: "+pos[0])
	}
	var at time.Time
	switch {
	case *end && (*dur != "" || *until != "" || *reason != ""):
		probs = append(probs, "--end takes no --for, --until or --reason: it ends the quiet in force now")
	case *end:
	case *dur != "" && *until != "":
		probs = append(probs, "give --for <duration> or --until <RFC3339>, not both")
	case *dur != "":
		d, err := time.ParseDuration(*dur)
		if err != nil || d <= 0 {
			probs = append(probs, "--for wants a duration above zero (11m, 1h30m), found "+*dur)
		}
		at = now.Add(d)
	case *until != "":
		t, err := time.Parse(time.RFC3339, *until)
		switch {
		case err != nil:
			probs = append(probs, "--until wants an RFC3339 time (2026-10-04T15:50:00-07:00), found "+*until)
		case !t.After(now):
			probs = append(probs, "--until wants a time after now ("+now.UTC().Format(time.RFC3339)+"), found "+*until)
		}
		at = t
	default:
		probs = append(probs, "wants how long: --for <duration> or --until <RFC3339> (or --end to end a quiet now)")
	}
	if !*end && strings.TrimSpace(*reason) == "" {
		probs = append(probs, "--reason <text> is required: why the machine is quiet, on every worker's QUIET line and in the log")
	}
	if len(probs) > 0 {
		return refuse(stderr, name, strings.Join(probs, "; "))
	}
	if *dry {
		what := "until=" + at.UTC().Format(time.RFC3339)
		if *end {
			what = "end"
		}
		fmt.Fprintf(stdout, "FLEET QUIET DRY-RUN member=%s %s; nothing was written\n", pos[0], what)
		return 0
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	r := sprint.FleetReq{Op: "quiet", Member: pos[0], Until: at, End: *end, Reason: *reason, Who: c.actor}
	return a.runStep(name, *c, st, store.FleetStep(r), stdout, stderr)
}
