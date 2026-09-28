package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdStatus surveys the runs queued since a boundary (docs/SPEC-TEST.md,
// verb status; behaviours 15-18): one STATUS RUN line per run, in queue
// order, with queue, cancellation drain, execution and end-to-end latency,
// attempt identities and prior failures, then one STATUS OK summary.
func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	store := fs.String("store", "", "run store directory (required)")
	since := fs.String("since", "", "boundary: RFC 3339 instant or duration back from the clock (required)")
	nowFlag := fs.String("now", "", "RFC 3339 clock override for tests and replays")
	max := fs.Int("max", 20, "run lines before one MORE line; 0 prints all")
	given, ok := parse(fs, args, stderr, "store", "since")
	if !ok {
		return 2
	}
	now := time.Now().UTC()
	if given["now"] {
		t, err := instant(*nowFlag)
		if err != nil {
			return refuse(stderr, " status", fmt.Sprintf("--now must parse as RFC 3339 (got %q)", *nowFlag))
		}
		now = t
	}
	boundary, err := instant(*since)
	if err != nil {
		d, derr := time.ParseDuration(strings.TrimSpace(*since))
		if derr != nil || d < 0 {
			return refuse(stderr, " status", fmt.Sprintf("--since must be an RFC 3339 instant or a non-negative duration (got %q)", *since))
		}
		boundary = now.Add(-d)
	}
	runs, err := loadRuns(*store)
	if err != nil {
		return refuse(stderr, " status", err.Error())
	}

	var recent []Run
	older := 0
	for _, r := range runs {
		if r.Queued.Before(boundary) {
			older++
			continue
		}
		recent = append(recent, r)
	}
	sort.Slice(recent, func(i, j int) bool {
		if !recent[i].Queued.Equal(recent[j].Queued) {
			return recent[i].Queued.Before(recent[j].Queued)
		}
		return recent[i].ID < recent[j].ID
	})

	list := bounded.Capped(stdout, *max, "STATUS", "run", "--max 0")
	for _, r := range recent {
		list.Line(runLine(r, now))
	}
	list.More()
	fmt.Fprintf(stdout, "STATUS OK store=%s since=%s now=%s runs=%d shown=%d older=%d\n",
		oneline.Field(*store), boundary.Format(time.RFC3339), now.Format(time.RFC3339), list.Total(), list.Shown(), older)
	return 0
}

// runLine renders one run. Every interval is measured from the store's own
// stamps; an interval that began and has not ended is measured to the clock
// and marked with a trailing +, and one that never began is -.
func runLine(r Run, now time.Time) string {
	end := r.Finished
	if end.IsZero() {
		end = r.Cancelled
	}
	queueEnd := r.Started
	if queueEnd.IsZero() {
		queueEnd = r.Cancelled // cancelled before it ever started
	}
	attempts := make([]string, 0, len(r.Attempts))
	var prior []string
	for i, a := range r.Attempts {
		attempts = append(attempts, a.ID)
		if i < len(r.Attempts)-1 && a.State == "failed" {
			p := a.ID
			if a.Failure != "" {
				p += ":" + a.Failure
			}
			prior = append(prior, p)
		}
	}
	return fmt.Sprintf("STATUS RUN id=%s state=%s queued=%s queue=%s drain=%s exec=%s e2e=%s attempts=%s prior_failures=%s",
		oneline.Field(r.ID), oneline.Field(r.State), r.Queued.Format(time.RFC3339),
		span(r.Queued, queueEnd, now),
		span(r.CancelRequested, r.Cancelled, now),
		span(r.Started, end, now),
		span(r.Queued, end, now),
		list(attempts), list(prior))
}

// span is end-start; with no start it is -, and with no end it is the time to
// now followed by +.
func span(start, end, now time.Time) string {
	switch {
	case start.IsZero():
		return "-"
	case end.IsZero():
		return now.Sub(start).String() + "+"
	default:
		return end.Sub(start).String()
	}
}

func list(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	for i, s := range items {
		items[i] = oneline.Field(s)
	}
	return strings.Join(items, ",")
}
