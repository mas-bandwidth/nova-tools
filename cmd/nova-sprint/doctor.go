package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The doctor (the owner, 2026-10-10 ~04:18Z: "sprint doctor can check this, but i still
// dislike these silent stops/failures"). This is its stops layer, one check per kind of
// automatic stop (internal/sprint/stops.go, LiveStops) and the seat's waits (SeatWaitsOf),
// each live stop listed with its age, its effect and the verb that undoes it. The card
// the-doctor-checks-every-layer-and-refuses-a-red builds the layers below it (binary, store,
// routes and providers, machines, readers, friends, cards) onto this verb; each stop is
// said under the layer it stops.

func init() {
	verbEffect["doctor"] = "inspection: reads only, writes nothing: every automatic stop that holds (a member down the coordinator did not hold, a pin waiting on a friend not up, a friend stalled, a route or provider resting, a stream stopped) with its age, and the judgments waiting on the seat"
}

// stopLayer is the doctor's layer a kind of stop is said under.
var stopLayer = map[string]string{
	sprint.StopKindProviderRest:  "routes",
	sprint.StopKindRouteRest:     "routes",
	sprint.StopKindMemberDown:    "machines",
	sprint.StopKindMemberAdopt:   "machines",
	sprint.StopKindFriendStalled: "friends",
	sprint.StopKindFriendDown:    "friends",
	sprint.StopKindPinWaits:      "cards",
	sprint.StopKindStreamStopped: "cards",
}

// doctorLayers is the layers the stops are said under, in the doctor's order, then the seat.
var doctorLayers = []string{"routes", "machines", "friends", "cards"}

// cmdDoctor is `nova-sprint doctor`: each layer GREEN, or RED with every automatic stop that
// holds in it, one line each with its age, effect and undo verb; then the seat's waits. It
// counts them now from a fresh read, whether or not the machine ticks. Exit 0 when nothing is
// RED, 1 when a stop holds or a judgment waits past its deadline, 2 when the store does not
// read.
func (a *app) cmdDoctor(args []string, stdout, stderr io.Writer) int {
	const name = "doctor"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no positional words: it checks the whole sprint")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	rec, err := st.StopsNow(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint where\n", prog, name, oneline.Escape(noSprintYet(err).Error()))
		return 2
	}
	now := a.now()
	byLayer := map[string][]sprint.Stop{}
	for _, x := range rec.Stops {
		layer := stopLayer[x.Kind]
		if layer == "" {
			layer = "cards"
		}
		byLayer[layer] = append(byLayer[layer], x)
	}
	red := len(rec.Stops) > 0 || rec.Seat.Overdue > 0
	code := 0
	if red {
		code = 1
	}
	if c.json {
		type stopJSON struct {
			sprint.Stop
			Layer      string `json:"layer"`
			AgeSeconds int64  `json:"age_seconds"`
		}
		stops := []stopJSON{}
		for _, layer := range doctorLayers {
			for _, x := range byLayer[layer] {
				stops = append(stops, stopJSON{Stop: x, Layer: layer, AgeSeconds: int64(x.Age(now) / time.Second)})
			}
		}
		status := "ok"
		if red {
			status = "red"
		}
		b, err := json.Marshal(map[string]any{"verb": name, "status": status, "exit": code, "at": now.UTC(), "stops": stops, "seat": rec.Seat})
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint doctor\n", prog, name, oneline.Escape(err.Error()))
			return 2
		}
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, layer := range doctorLayers {
		stops := byLayer[layer]
		if len(stops) == 0 {
			fmt.Fprintf(stdout, "DOCTOR %s GREEN: no automatic stop holds\n", layer)
			continue
		}
		fmt.Fprintf(stdout, "DOCTOR %s RED: %d automatic stops hold\n", layer, len(stops))
		for _, x := range stops {
			fmt.Fprintln(stdout, "  STOP "+oneline.Escape(x.Line(now)))
		}
	}
	seat := "GREEN"
	if rec.Seat.Overdue > 0 {
		seat = "RED"
	}
	fmt.Fprintf(stdout, "DOCTOR seat %s: %s\n", seat, oneline.Escape(rec.Seat.Line(now)))
	word := "OK"
	if red {
		word = "RED"
	}
	fmt.Fprintf(stdout, "DOCTOR %s stops=%d judgments=%d overdue=%d\n", word, len(rec.Stops), rec.Seat.Judgments, rec.Seat.Overdue)
	return code
}
