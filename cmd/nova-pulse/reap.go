package main

// The reap verb's flags. The collection itself is internal/pulse/reap.go; the process table
// here is the real one, `ps` once and a signal per overdue pid (issue #828 class E).

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdReap(args []string, stdout, stderr io.Writer) int {
	f := newFlags("reap")
	roots := f.fs.String("roots", "", "")
	queue := f.fs.String("queue", "", "")
	deadline := f.fs.Int("deadline", 0, "")
	dryRun := f.fs.Bool("dry-run", false, "")
	timeout := f.fs.Int("timeout", 30, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*roots, "roots", "the swarm roots this reaper collects, comma separated")
	f.want(*queue, "queue", "the queue directory holding launched, pending and failed")
	if *deadline < 1 {
		f.add(fmt.Sprintf("--deadline is required and is at least 1, got %d; it is the batch deadline in whole seconds that nothing under a root may outlive", *deadline))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Reap(pulse.ReapInput{
		Roots:    *roots,
		Queue:    *queue,
		Deadline: time.Duration(*deadline) * time.Second,
		DryRun:   *dryRun,
		Procs:    pulse.OSProcs{Timeout: time.Duration(*timeout) * time.Second},
		Version:  buildinfo.Version(version),
		Now:      func() time.Time { return time.Now().UTC() },
		Stdout:   stdout,
		Stderr:   stderr,
	})
}
