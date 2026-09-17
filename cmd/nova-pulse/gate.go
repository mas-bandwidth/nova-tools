package main

// The gate verb: the one writer of STOP (SPEC-PULSE class C, issue #828). It reads the
// branch's latest ci run and prints one GATE line -- red writes STOP with the red's name in
// it, cancelled or still running changes nothing, green lifts only the STOP the gate wrote.

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdGate(args []string, stdout, stderr io.Writer) int {
	f := newFlags("gate")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	queue := f.fs.String("queue", "", "")
	source := f.fs.String("source", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	decideOn := f.fs.Bool("decide", false, "")
	floor := f.fs.Float64("floor", 0.9, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the repository the ci run belongs to, owner/name")
	f.want(*branch, "branch", "the integration branch this gate holds, such as main or dev")
	f.want(*queue, "queue", "the queue directory STOP lives in")
	if *timeout < 1 {
		f.add("--timeout wants a whole number of seconds; every child this verb starts is bounded")
	}
	if *decideOn && (*floor < 0 || *floor > 1) {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g", *floor))
	}
	if f.refused(stderr) {
		return 2
	}

	src := pulse.NewGHRunSource(time.Duration(*timeout) * time.Second)
	if *source != "" {
		fromFile, err := pulse.NewFileRunSource(*source)
		if err != nil {
			return refuse(stderr, " gate", err.Error())
		}
		src = fromFile
	}
	in := pulse.GateInput{
		Repo: *repo, Branch: *branch, Queue: *queue, Source: src,
		Stdout: stdout, Stderr: stderr,
	}
	if *decideOn {
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, " gate", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		in.Decide, in.Floor, in.Decider = true, *floor, client
	}
	return pulse.Gate(in)
}
