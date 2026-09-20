package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdRequeue(args []string, stdout, stderr io.Writer) int {
	f := newFlags("requeue")
	ready := f.fs.String("ready", "", "")
	launched := f.fs.String("launched", "", "")
	card := f.fs.String("card", "", "")
	route := f.fs.String("route", "", "")
	maxRetries := f.fs.Int("max", pulse.DefaultMaxProviderRetries, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*ready, "ready", "the directory to requeue the card into (e.g. queue/pending or queue/ready)")
	f.want(*launched, "launched", "the directory the card was launched from")
	f.want(*card, "card", "the card name or base (e.g. card-100.md or card-100)")
	if *maxRetries <= 0 {
		f.add(fmt.Sprintf("--max wants a positive number of retries, got %d", *maxRetries))
	}
	if f.refused(stderr) {
		return 2
	}

	requeued, nextAttempt, err := pulse.RequeueProviderCardWithRoute(*ready, *launched, *card, *route, *maxRetries)
	if err != nil {
		fmt.Fprintf(stderr, "REQUEUE REFUSED card=%s: %s\n", oneline.Field(*card), oneline.Err(err))
		return 2
	}
	if !requeued {
		fmt.Fprintf(stdout, "REQUEUE EXHAUSTED card=%s attempts=%d max=%d\n", oneline.Field(*card), nextAttempt, *maxRetries)
		return 1
	}
	fmt.Fprintf(stdout, "REQUEUE OK card=%s attempt=%d max=%d to=%s\n", oneline.Field(*card), nextAttempt, *maxRetries, oneline.Field(*ready))
	return 0
}
