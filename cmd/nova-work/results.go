package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/record"
)

// cmdResults lists the recorded card results, newest first, filtered by --since, --bench
// and --failed, and bounded at --max with one MORE line naming the rest.
func cmdResults(args []string, stdout, stderr io.Writer, d deps) int {
	f := newFlags("results")
	dsn := f.fs.String("postgres", "", "")
	since := f.fs.Duration("since", 0, "")
	bench := f.fs.String("bench", "", "")
	failed := f.fs.Bool("failed", false, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*dsn, "postgres", "the DSN of the Postgres that holds card_results")
	if *since < 0 {
		f.add(fmt.Sprintf("--since is 0 or more, got %s; a negative window is a typo with two readings", oneline.Escape(since.String())))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already prints every row", *max))
	}
	if f.refused(stderr) {
		return 2
	}

	ctx := context.Background()
	store, err := d.openStore(*dsn)
	if err != nil {
		return refuse(stderr, " results", oneline.Err(err))
	}
	defer store.Close()

	var sinceT time.Time
	if *since > 0 {
		sinceT = d.now().Add(-*since)
	}
	rows, err := store.List(ctx, record.Filter{Since: sinceT, Bench: *bench, Failed: *failed})
	if err != nil {
		return refuse(stderr, " results", oneline.Err(err))
	}

	l := bounded.Capped(stdout, *max, "RESULTS", "rows", "widen --max or pass --max 0")
	for _, r := range rows {
		l.Line(record.FormatRow(r))
	}
	l.More()
	if err := l.Err(); err != nil {
		return refuse(stderr, " results", oneline.Err(err))
	}
	return 0
}
