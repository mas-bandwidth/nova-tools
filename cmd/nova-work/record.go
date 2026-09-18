package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/record"
)

// cmdRecord consumes cards:done and writes each result to card_results. --migrate applies
// the schema and exits; the loop also ensures it, because a fresh table is not a silent
// zero-row success. --once reads one pass and returns; otherwise the loop runs until
// --deadline.
func cmdRecord(args []string, stdout, stderr io.Writer, d deps) int {
	f := newFlags("record")
	redisAddr := f.fs.String("redis", "", "")
	dsn := f.fs.String("postgres", "", "")
	once := f.fs.Bool("once", false, "")
	deadline := f.fs.Duration("deadline", time.Hour, "")
	migrate := f.fs.Bool("migrate", false, "")
	stream := f.fs.String("stream", record.Stream, "")
	group := f.fs.String("group", record.Group, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*dsn, "postgres", "the DSN of the Postgres that holds card_results")
	if *deadline < 0 {
		f.add(fmt.Sprintf("--deadline is 0 or more, got %s; a negative deadline is a typo with two readings", oneline.Escape(deadline.String())))
	}
	if !*migrate {
		f.want(*redisAddr, "redis", "the address of the Redis that holds the cards:done stream")
	}
	if f.refused(stderr) {
		return 2
	}

	ctx := context.Background()
	store, err := d.openStore(*dsn)
	if err != nil {
		return refuse(stderr, " record", oneline.Err(err))
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return refuse(stderr, " record", oneline.Err(err))
	}
	if *migrate {
		fmt.Fprintf(stdout, "MIGRATE OK schema=%d\n", record.SchemaVersion)
		return 0
	}

	consumer, err := d.openConsumer(ctx, *redisAddr, *stream, *group, "")
	if err != nil {
		return refuse(stderr, " record", oneline.Err(err))
	}
	defer consumer.Close()
	counts, err := record.Consume(ctx, store, consumer, record.RecordOptions{
		Once:     *once,
		Deadline: *deadline,
		Now:      d.now,
	}, stdout)
	if err != nil {
		return refuse(stderr, " record", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "RECORD OK seen=%d inserted=%d duplicate=%d malformed=%d\n",
		counts.Seen, counts.Inserted, counts.Duplicate, counts.Malformed)
	return 0
}
