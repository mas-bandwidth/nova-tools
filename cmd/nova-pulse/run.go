package main

// The run verb: the loop that holds the ticks so the coordinator's turns are the decisions
// (G1 of pit stop 3, #828). It makes no model call, and it calls a person once per
// undecided case, never once per tick.

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdRun(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("run")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	hours := f.fs.Float64("hours", -1, "")
	tick := f.fs.Int("tick", int(pulse.DefaultTick/time.Second), "")
	once := f.fs.Bool("once", false, "")
	bus := f.fs.String("bus", "", "")
	as := f.fs.String("as", "", "")
	max := f.fs.Int("max", bounded.Default, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done, failed and the state files")
	f.want(*roots, "roots", "the benches this shift runs on, comma separated")
	if *hours < 0 && !*once {
		f.add(fmt.Sprintf("--hours is required and is 0 or more, got %v; --once runs exactly one tick and needs no hours", *hours))
	}
	if *tick < 1 {
		f.add(fmt.Sprintf("--tick wants a whole number of seconds, got %d", *tick))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	h := *hours
	if h < 0 {
		h = 0
	}
	return pulse.Run(pulse.RunInput{
		Queue: *queue, Roots: *roots, Repo: *repo, Branch: *branch,
		Hours: h, Tick: time.Duration(*tick) * time.Second, Once: *once,
		Bus: *bus, As: *as, Max: *max,
		Stdout: stdout, Stderr: stderr,
		Now: func() time.Time { return now },
	})
}

func cmdTriage(args []string, stdout, stderr io.Writer) int {
	f := newFlags("triage")
	kind := f.fs.String("case", "", "")
	queue := f.fs.String("queue", "", "")
	out := f.fs.String("out", "", "")
	ref := f.fs.String("ref", "", "")
	evidence := f.fs.String("evidence", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*kind, "case", "one of "+joinKinds())
	f.want(*queue, "queue", "the queue directory holding RULES.tsv and the undecided evidence")
	f.want(*out, "out", "the card file this packet is written to")
	if f.refused(stderr) {
		return 2
	}
	return pulse.Triage(pulse.TriageInput{
		Case: *kind, Queue: *queue, Out: *out, Ref: *ref, Evidence: *evidence,
		Stdout: stdout, Stderr: stderr,
	})
}

func joinKinds() string {
	s := ""
	for i, k := range pulse.TriageKinds {
		if i > 0 {
			s += ", "
		}
		s += k
	}
	return s
}
