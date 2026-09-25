package main

// The run verb: the loop that holds the ticks so the coordinator's turns are the decisions
// (G1 of pit stop 3, #828). It makes no model call, and it calls a person once per
// undecided case, never once per tick.

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
	deadline := f.fs.Int("deadline", int(pulse.DefaultCardDeadline/time.Second), "")
	timeout := f.fs.Int("timeout", 120, "")
	tempGlob := f.fs.String("temp-glob", "", "")
	tempRoot := f.fs.String("temp-root", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	decide := f.fs.Bool("decide", false, "")
	floor := f.fs.Float64("floor", 0, "")
	keyEnv := f.fs.String("key-env", "", "")
	baseURL := f.fs.String("base-url", "", "")

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
	if *deadline < 1 {
		f.add(fmt.Sprintf("--deadline wants a whole number of seconds, got %d; it is the deadline a card may not outlive", *deadline))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d; it bounds every gh, git and nova-swarm child", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *decide && (*floor < 0 || *floor > 1) {
		f.add(fmt.Sprintf("--floor is a confidence between 0 and 1, got %v; 0.9 is how a caller says a typed decision must be sure before the loop acts on it", *floor))
	}
	if f.refused(stderr) {
		return 2
	}
	h := *hours
	if h < 0 {
		h = 0
	}

	in := pulse.RunInput{
		Queue: *queue, Roots: *roots, Repo: *repo, Branch: *branch,
		Hours: h, Tick: time.Duration(*tick) * time.Second, Once: *once,
		Bus: *bus, As: *as, Max: *max,
		Stdout: stdout, Stderr: stderr,
		Now: func() time.Time { return time.Now().UTC() },
	}
	// The seams, wired to the shipped verbs: gate, harvest, sweep, reap, refill (cut --kind)
	// and launch. The configuration is read once per tick by the loop and handed here, so
	// the steps run on the values the WIDTH line was written under.
	cfg := pulse.DefaultConfig()
	in.Configured = func(c pulse.Config) { cfg = c }
	pulse.Wire(&in, pulse.NewWiring(pulse.WiringInput{
		Queue: *queue, Roots: *roots, Repo: *repo, Branch: *branch,
		Deadline: time.Duration(*deadline) * time.Second,
		Timeout:  time.Duration(*timeout) * time.Second,
		Max:      *max,
		TempGlob: *tempGlob,
		TempRoot: *tempRoot,
		Now:      func() time.Time { return time.Now().UTC() },
		Config:   func() pulse.Config { return cfg },

		Decide:        *decide,
		DecideFloor:   *floor,
		DecideKeyEnv:  *keyEnv,
		DecideBaseURL: *baseURL,
	}))
	return pulse.Run(in)
}

func cmdTriage(args []string, stdout, stderr io.Writer) int {
	f := newFlags("triage")
	kind := f.fs.String("case", "", "")
	queue := f.fs.String("queue", "", "")
	out := f.fs.String("out", "", "")
	ref := f.fs.String("ref", "", "")
	evidence := f.fs.String("evidence", "", "")
	decideOn := f.fs.Bool("decide", false, "")
	dedupe := f.fs.Bool("dedupe", false, "")
	issues := f.fs.String("issues", "", "")
	floor := f.fs.Float64("floor", pulse.DefaultDedupeFloor, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*kind, "case", "one of "+joinKinds())
	f.want(*queue, "queue", "the queue directory holding RULES.tsv and the undecided evidence")
	f.want(*out, "out", "the card file this packet is written to")
	if (*decideOn || *dedupe) && (*floor < 0 || *floor > 1) {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g", *floor))
	}
	if *dedupe {
		f.want(*issues, "issues", "the open issues file, one number<TAB>title per line")
	}
	if f.refused(stderr) {
		return 2
	}
	in := pulse.TriageInput{
		Case: *kind, Queue: *queue, Out: *out, Ref: *ref, Evidence: *evidence,
		Stdout: stdout, Stderr: stderr,
	}
	if *decideOn || *dedupe {
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, " triage", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		in.Decide, in.Dedupe, in.Issues, in.Floor, in.Decider = *decideOn, *dedupe, *issues, *floor, client
	}
	return pulse.Triage(in)
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
