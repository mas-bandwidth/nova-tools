package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdSprint(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "nova-pulse sprint: subverb required (start, status, set, table, funnel, stop); run: nova-pulse help\n")
		return 2
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		f := newFlags("sprint start")
		plan := f.fs.String("plan", "", "")
		queue := f.fs.String("queue", "", "")
		machines := f.fs.String("machines", "", "")
		ssh := f.fs.String("ssh", "", "")
		timeout := f.fs.Int("timeout", 120, "")
		if !f.parse(rest, stderr) {
			return 2
		}
		f.want(*plan, "plan", "the plan TSV file")
		f.want(*queue, "queue", "the queue directory")
		f.want(*machines, "machines", "the machines registry file")
		if *timeout < 1 {
			f.problems = append(f.problems, fmt.Sprintf("--timeout wants a positive number of seconds, got %d", *timeout))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintStart(pulse.SprintStartInput{
			PlanPath:     *plan,
			Queue:        *queue,
			MachinesPath: *machines,
			SSH:          *ssh,
			Timeout:      time.Duration(*timeout) * time.Second,
			Stdout:       stdout,
			Stderr:       stderr,
			Now:          func() time.Time { return now },
		})

	case "status":
		f := newFlags("sprint status")
		plan := f.fs.String("plan", "", "")
		queue := f.fs.String("queue", "", "")
		machines := f.fs.String("machines", "", "")
		ssh := f.fs.String("ssh", "", "")
		timeout := f.fs.Int("timeout", 120, "")
		max := f.fs.Int("max", 0, "")
		if !f.parse(rest, stderr) {
			return 2
		}
		f.want(*plan, "plan", "the plan TSV file")
		f.want(*queue, "queue", "the queue directory")
		f.want(*machines, "machines", "the machines registry file")
		if *timeout < 1 {
			f.problems = append(f.problems, fmt.Sprintf("--timeout wants a positive number of seconds, got %d", *timeout))
		}
		if *max < 0 {
			f.problems = append(f.problems, fmt.Sprintf("--max wants a non-negative number, got %d", *max))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintStatus(pulse.SprintStatusInput{
			PlanPath:     *plan,
			Queue:        *queue,
			MachinesPath: *machines,
			SSH:          *ssh,
			Timeout:      time.Duration(*timeout) * time.Second,
			Max:          *max,
			Stdout:       stdout,
			Stderr:       stderr,
			Now:          func() time.Time { return now },
		})

	case "set":
		f := newFlags("sprint set")
		queue := f.fs.String("queue", "", "")
		if !f.parseAny(rest, stderr) {
			return 2
		}
		f.want(*queue, "queue", "the queue directory")
		pos := f.fs.Args()
		if len(pos) != 3 {
			f.problems = append(f.problems, fmt.Sprintf("sprint set wants 3 positional arguments (<bench> <key> <value>), got %d", len(pos)))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintSet(pulse.SprintSetInput{
			Queue:  *queue,
			Bench:  pos[0],
			Key:    pos[1],
			Value:  pos[2],
			Stdout: stdout,
			Stderr: stderr,
		})

	case "table":
		f := newFlags("sprint table")
		plan := f.fs.String("plan", "", "")
		queue := f.fs.String("queue", "", "")
		bus := f.fs.String("bus", "", "")
		bench := f.fs.String("bench", "", "")
		once := f.fs.Bool("once", false, "")
		redis := f.fs.String("redis", "", "")
		if !f.parse(rest, stderr) {
			return 2
		}
		f.want(*queue, "queue", "the queue directory")
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintTable(pulse.SprintTableInput{
			PlanPath: *plan,
			Queue:    *queue,
			Bus:      *bus,
			Bench:    *bench,
			Once:     *once,
			Redis:    *redis,
			Stdout:   stdout,
			Stderr:   stderr,
			Now:      func() time.Time { return now },
		})

	case "funnel":
		f := newFlags("sprint funnel")
		plan := f.fs.String("plan", "", "")
		queue := f.fs.String("queue", "", "")
		wave := f.fs.String("wave", "", "")
		max := f.fs.Int("max", 0, "")
		if !f.parse(rest, stderr) {
			return 2
		}
		f.want(*queue, "queue", "the queue directory")
		if *max < 0 {
			f.problems = append(f.problems, fmt.Sprintf("--max wants a non-negative number, got %d", *max))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintFunnel(pulse.SprintFunnelInput{
			PlanPath: *plan,
			Queue:    *queue,
			Wave:     *wave,
			Max:      *max,
			Stdout:   stdout,
			Stderr:   stderr,
		})

	case "stop":
		f := newFlags("sprint stop")
		plan := f.fs.String("plan", "", "")
		queue := f.fs.String("queue", "", "")
		machines := f.fs.String("machines", "", "")
		ssh := f.fs.String("ssh", "", "")
		timeout := f.fs.Int("timeout", 120, "")
		if !f.parse(rest, stderr) {
			return 2
		}
		f.want(*plan, "plan", "the plan TSV file")
		f.want(*queue, "queue", "the queue directory")
		f.want(*machines, "machines", "the machines registry file")
		if *timeout < 1 {
			f.problems = append(f.problems, fmt.Sprintf("--timeout wants a positive number of seconds, got %d", *timeout))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.SprintStop(pulse.SprintStopInput{
			PlanPath:     *plan,
			Queue:        *queue,
			MachinesPath: *machines,
			SSH:          *ssh,
			Timeout:      time.Duration(*timeout) * time.Second,
			Stdout:       stdout,
			Stderr:       stderr,
			Now:          func() time.Time { return now },
		})

	default:
		fmt.Fprintf(stderr, "nova-pulse sprint: unknown subverb %q; run: nova-pulse help\n", strings.TrimSpace(sub))
		return 2
	}
}
