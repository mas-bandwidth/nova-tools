package main

// The gate verb: the one writer of STOP (SPEC-PULSE class C, issue #828). It reads the
// branch's latest ci run and prints one GATE line -- red writes STOP with the red's name in
// it, cancelled or still running changes nothing, green lifts only the STOP the gate wrote.

import (
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdGate(args []string, stdout, stderr io.Writer) int {
	f := newFlags("gate")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	queue := f.fs.String("queue", "", "")
	source := f.fs.String("source", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the repository the ci run belongs to, owner/name")
	f.want(*branch, "branch", "the integration branch this gate holds, such as main or dev")
	f.want(*queue, "queue", "the queue directory STOP lives in")
	if *timeout < 1 {
		f.add("--timeout wants a whole number of seconds; every child this verb starts is bounded")
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
	return pulse.Gate(pulse.GateInput{
		Repo: *repo, Branch: *branch, Queue: *queue, Source: src,
		Stdout: stdout, Stderr: stderr,
	})
}
