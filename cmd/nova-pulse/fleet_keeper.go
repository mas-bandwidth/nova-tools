package main

import (
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdFleetKeeper runs the hourly keeper wake: folds results, checks merge bases,
// checks status of durable loops (dealer, backpressure, harvest, sprint table),
// and records decisions as one receipt per hour under queue/wake/ (issue #2459, essential 10).
func cmdFleetKeeper(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet keeper")
	queue := f.fs.String("queue", "", "")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "origin/dev", "")
	roots := f.fs.String("roots", "", "")
	receipt := f.fs.String("receipt", "", "")
	strict := f.fs.Bool("strict", false, "")
	restart := f.fs.Bool("restart", false, "")
	var loops repeatable
	f.fs.Var(&loops, "loop", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding wake/ and the state files")
	if f.refused(stderr) {
		return 2
	}

	return pulse.FleetKeeper(pulse.FleetKeeperInput{
		Queue:       *queue,
		Repo:        *repo,
		Base:        *base,
		Roots:       *roots,
		Receipt:     *receipt,
		Strict:      *strict,
		RestartDead: *restart,
		Loops:       []string(loops),
		Stdout:      stdout,
		Stderr:      stderr,
		Now:         func() time.Time { return fleetNow().UTC() },
	})
}
