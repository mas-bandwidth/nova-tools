package main

// The rules, routes and brief verbs: class M of pit stop 3 (#828). A rule lives in
// pulse.toml, in RULES.tsv or in a test; the routes are configuration and `routes` is their
// only editor; and the pulse brief is rendered from both rather than hand-edited.

import (
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdRules(args []string, stdout, stderr io.Writer) int {
	f := newFlags("rules")
	queue := f.fs.String("queue", "", "")
	seedFrom := f.fs.String("seed-from", "", "")
	check := f.fs.Bool("check", false, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory RULES.tsv lives in")
	if f.refused(stderr) {
		return 2
	}
	return pulse.Rules(pulse.RulesInput{
		Queue: *queue, SeedFrom: *seedFrom, Check: *check, Max: *max,
		Now: func() time.Time { return time.Now().UTC() }, Stdout: stdout, Stderr: stderr,
	})
}

func cmdRoutes(args []string, stdout, stderr io.Writer) int {
	f := newFlags("routes")
	queue := f.fs.String("queue", "", "")
	class := f.fs.String("class", "", "")
	bench := f.fs.String("bench", "", "")
	unbench := f.fs.String("unbench", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory pulse.toml lives in")
	if f.refused(stderr) {
		return 2
	}
	return pulse.RoutesVerb(pulse.RoutesInput{
		Queue: *queue, Class: *class, Bench: *bench, Unbench: *unbench,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdBrief(args []string, stdout, stderr io.Writer) int {
	f := newFlags("brief")
	as := f.fs.String("as", "", "")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	bus := f.fs.String("bus", "", "")
	friend := f.fs.Bool("friend", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*as, "as", "the name the pulse answers as, such as Rowan or Stella")
	f.want(*queue, "queue", "the queue directory pulse.toml and RULES.tsv live in")
	if f.refused(stderr) {
		return 2
	}
	return pulse.Brief(pulse.BriefInput{
		As: *as, Queue: *queue, Roots: *roots, Bus: *bus, Friend: *friend,
		Stdout: stdout, Stderr: stderr,
	})
}
