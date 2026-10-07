//go:build perf

package testverbhelp

import (
	"fmt"
	"io"
	"time"
)

// Budget is how long help may take. Help is a print; anything that took longer
// than this dialed, waited or walked something. It is a wall-clock bound, so it
// is held only under -tags perf, on a quiet machine, never in the unit tier.
const Budget = 50 * time.Millisecond

// overBudget runs args five times and names an overrun of the fastest run: a
// loaded machine can stall any one run, while help that dials or waits is slow
// every time.
func overBudget(run Run, args []string) string {
	took := time.Duration(1<<63 - 1)
	for range 5 {
		start := time.Now()
		run(args, io.Discard, io.Discard)
		took = min(took, time.Since(start))
	}
	if took > Budget {
		return fmt.Sprintf("%q took %v at best of 5, over %v: help ran something", args, took, Budget)
	}
	return ""
}
