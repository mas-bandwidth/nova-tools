package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdSprint (the "nova-pulse sprint" dispatcher, including "funnel") lives in sprint.go:
// this file owns only the funnel sub-verb's own implementation, cmdSprintFunnel.

func cmdSprintFunnel(args []string, stdout, stderr io.Writer, now time.Time) int {
	actionFromPositional := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub := strings.ToLower(args[0])
		switch sub {
		case "record", "void", "report":
			actionFromPositional = sub
			rest = args[1:]
		default:
			fmt.Fprintf(stderr, "nova-pulse sprint funnel: unknown action %q (valid actions are report, record, void; run: nova-pulse help)\n", args[0])
			return 2
		}
	}

	f := newFlags("sprint funnel")
	queue := f.fs.String("queue", "", "")
	recordFlag := f.fs.Bool("record", false, "")
	voidFlag := f.fs.Bool("void", false, "")
	eventFlag := f.fs.String("event", "", "")
	cardFlag := f.fs.String("card", "", "")
	attemptFlag := f.fs.String("attempt", "", "")
	modelFlag := f.fs.String("model", "", "")
	benchFlag := f.fs.String("bench", "", "")
	verdictFlag := f.fs.String("verdict", "", "")
	spendFlag := f.fs.String("spend", "", "")
	detailFlag := f.fs.String("detail", "", "")
	onelineFlag := f.fs.Bool("oneline", false, "")
	jsonFlag := f.fs.Bool("json", false, "")

	// Convenience flags
	admitCard := f.fs.String("admit", "", "")
	launchCard := f.fs.String("launch", "", "")
	harvestCard := f.fs.String("harvest", "", "")
	gateCard := f.fs.String("gate", "", "")
	landCard := f.fs.String("land", "", "")
	voidCard := f.fs.String("void-card", "", "")

	if !f.parse(rest, stderr) {
		return 2
	}

	f.want(*queue, "queue", "the queue directory the funnel log hangs under")
	if f.refused(stderr) {
		return 2
	}

	action := actionFromPositional
	event := *eventFlag
	card := *cardFlag

	switch {
	case *admitCard != "":
		action = "record"
		event = pulse.EventAdmit
		card = *admitCard
	case *launchCard != "":
		action = "record"
		event = pulse.EventLaunch
		card = *launchCard
	case *harvestCard != "":
		action = "record"
		event = pulse.EventHarvest
		card = *harvestCard
	case *gateCard != "":
		action = "record"
		event = pulse.EventGate
		card = *gateCard
	case *landCard != "":
		action = "record"
		event = pulse.EventLand
		card = *landCard
	case *voidCard != "":
		action = "void"
		card = *voidCard
	case *recordFlag:
		action = "record"
	case *voidFlag:
		action = "void"
	}

	if action == "" {
		action = "report"
	}

	return pulse.SprintFunnel(pulse.SprintFunnelInput{
		Queue:   *queue,
		Action:  action,
		Event:   event,
		Card:    card,
		Attempt: *attemptFlag,
		Model:   *modelFlag,
		Bench:   *benchFlag,
		Verdict: *verdictFlag,
		Spend:   *spendFlag,
		Detail:  *detailFlag,
		Oneline: *onelineFlag,
		JSON:    *jsonFlag,
		Now:     func() time.Time { return now },
		Stdout:  stdout,
		Stderr:  stderr,
	})
}
