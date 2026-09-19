// Package record is the durable card-result record of docs/SPEC-STATE.md: one row per card
// result, consumed from the `cards:done` Redis stream and written to Postgres, idempotent on
// the stream id. The store is an interface so the unit tests run on an in-memory fake while
// the real Postgres path is the soak behind RECORD_TEST_PG; the Redis read is a consumer
// over go-redis so the tests can put miniredis behind the same seam.
//
// The record exists because results were scraped from job directories on the benches by a
// harvest loop over ssh, which re-harvested old jobs and could force-push stale commits
// (review #1263 F09, F11). A stream id that is a primary key makes a redelivered result a
// no-op, so replay is safe and a restart can read the whole stream from its first entry.
package record

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The stream this package consumes and the consumer group that makes a result delivered
// once. One group, not one per bench: the record is a single writer over one durable table.
const (
	Stream = "cards:done"
	Group  = "record"
)

// SchemaVersion is the migration version schema.sql installs. Bump it when a migration
// changes the shape; Migrate records it in schema_version.
const SchemaVersion = 1

// Row is one card result as it lands in card_results. The "when known" columns are the
// nullables: an empty PR is SQL NULL, and a result that carries no timestamps records NULL
// for pushed_at and done_at.
type Row struct {
	StreamID   string
	Label      string
	Bench      string
	Exit       int
	ResultLine string
	JobPath    string
	Commit     string
	Branch     string
	PR         string
	PushedAt   *time.Time
	DoneAt     *time.Time
	RecordedAt time.Time
}

// Filter is what `nova-work results` narrows on. The zero value is everything.
type Filter struct {
	Since  time.Time
	Bench  string
	Failed bool
}

// Store is the durable side of the record. Insert reports whether it created the row:
// false means the stream id was already recorded, which is not an error and is still safe
// to ack.
type Store interface {
	Migrate(ctx context.Context) error
	Insert(ctx context.Context, r Row) (bool, error)
	List(ctx context.Context, f Filter) ([]Row, error)
	Close() error
}

// Message is one entry read from the stream: its id is the idempotency key.
type Message struct {
	ID     string
	Fields map[string]string
}

// Consumer is the Redis side. Read takes new entries; ReadPending drains entries this
// consumer already owns but has not acked, so a crash after INSERT and before XACK is
// repaired on the next start instead of being lost behind the pending list.
type Consumer interface {
	Read(ctx context.Context, count int, block time.Duration) ([]Message, error)
	ReadPending(ctx context.Context, count int) ([]Message, error)
	Ack(ctx context.Context, id string) error
	Close() error
}

// RecordOptions is the loop's shape. Once reads the available entries of one pass and
// returns; Deadline bounds a resident loop; Now is injectable so tests never read the clock.
type RecordOptions struct {
	Once     bool
	Deadline time.Duration
	Batch    int
	Block    time.Duration
	Now      func() time.Time
}

// Counts is what one Consume did.
type Counts struct {
	Seen      int
	Inserted  int
	Duplicate int
	Malformed int
}

// RowFromMessage parses one stream entry into a Row. label (or card, or id) is the one
// required field; the rest default to empty, and an absent exit is zero. The stream
// message's own id is the row's StreamID.
func RowFromMessage(id string, fields map[string]string, now time.Time) (Row, error) {
	get := func(names ...string) string {
		for _, n := range names {
			if v, ok := fields[n]; ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	label := get("label", "card", "id")
	if label == "" {
		return Row{}, fmt.Errorf("result %s has no label; a row needs the card it reports on", id)
	}
	row := Row{
		StreamID:   id,
		Label:      label,
		Bench:      get("bench"),
		ResultLine: get("result", "result_line", "result line 1", "line"),
		JobPath:    get("job", "job_path", "job path"),
		Commit:     get("commit", "commit_sha", "sha"),
		Branch:     get("branch"),
		PR:         get("pr", "pr_url", "pr_number"),
		RecordedAt: now,
	}
	if v := get("exit"); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return Row{}, fmt.Errorf("result %s exit %q is not a number", id, v)
		}
		row.Exit = n
	}
	if v := get("pushed_at", "pushedAt"); v != "" {
		ts, err := parseTime(v)
		if err != nil {
			return Row{}, fmt.Errorf("result %s pushed_at %q: %w", id, v, err)
		}
		row.PushedAt = &ts
	}
	if v := get("done_at", "doneAt", "finished_at"); v != "" {
		ts, err := parseTime(v)
		if err != nil {
			return Row{}, fmt.Errorf("result %s done_at %q: %w", id, v, err)
		}
		row.DoneAt = &ts
	}
	return row, nil
}

// FormatRow renders one result as the one machine-scannable line `nova-work results`
// prints. Free text (the result line) is escaped; the keyed fields are single tokens.
func FormatRow(r Row) string {
	pr := "-"
	if r.PR != "" {
		pr = r.PR
	}
	done := r.RecordedAt
	if r.DoneAt != nil {
		done = *r.DoneAt
	}
	stamp := "-"
	if !done.IsZero() {
		stamp = done.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("RESULT id=%s card=%s bench=%s exit=%d commit=%s branch=%s pr=%s done=%s result=%s",
		oneline.Field(r.StreamID), oneline.Field(r.Label), oneline.Field(r.Bench), r.Exit,
		oneline.Field(r.Commit), oneline.Field(r.Branch), oneline.Field(pr),
		oneline.Field(stamp), oneline.Escape(r.ResultLine))
}

// Consume is the record loop: drain what this consumer already owes, then read new results,
// commit each to the store, and ack only after the commit has succeeded. A commit failure
// returns without acking, so the entry stays pending and the next start repairs it.
func Consume(ctx context.Context, store Store, consumer Consumer, opts RecordOptions, out io.Writer) (Counts, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	batch := opts.Batch
	if batch <= 0 {
		batch = 16
	}
	block := opts.Block
	if block == 0 {
		block = 2 * time.Second
	}
	var counts Counts

	for {
		msgs, err := consumer.ReadPending(ctx, batch)
		if err != nil {
			return counts, err
		}
		if len(msgs) == 0 {
			break
		}
		if err := commitAll(ctx, store, consumer, msgs, now, out, &counts); err != nil {
			return counts, err
		}
	}

	var deadline time.Time
	if opts.Deadline > 0 {
		deadline = time.Now().Add(opts.Deadline)
	}
	for {
		if opts.Once {
			block = -1
		}
		msgs, err := consumer.Read(ctx, batch, block)
		if err != nil {
			return counts, err
		}
		if err := commitAll(ctx, store, consumer, msgs, now, out, &counts); err != nil {
			return counts, err
		}
		if opts.Once {
			return counts, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return counts, nil
		}
	}
}

func commitAll(ctx context.Context, store Store, consumer Consumer, msgs []Message, now func() time.Time, out io.Writer, counts *Counts) error {
	for _, m := range msgs {
		counts.Seen++
		row, err := RowFromMessage(m.ID, m.Fields, now())
		if err != nil {
			counts.Malformed++
			fmt.Fprintf(out, "RECORD BAD id=%s err=%s\n", oneline.Field(m.ID), oneline.Err(err))
			if aerr := consumer.Ack(ctx, m.ID); aerr != nil {
				return aerr
			}
			continue
		}
		inserted, err := store.Insert(ctx, row)
		if err != nil {
			return fmt.Errorf("record %s: %w", m.ID, err)
		}
		if err := consumer.Ack(ctx, m.ID); err != nil {
			return fmt.Errorf("ack %s after commit: %w", m.ID, err)
		}
		if inserted {
			counts.Inserted++
		} else {
			counts.Duplicate++
		}
		fmt.Fprintf(out, "RECORD id=%s card=%s bench=%s exit=%d commit=%s branch=%s inserted=%t result=%s\n",
			oneline.Field(row.StreamID), oneline.Field(row.Label), oneline.Field(row.Bench), row.Exit,
			oneline.Field(row.Commit), oneline.Field(row.Branch), inserted, oneline.Escape(row.ResultLine))
	}
	return nil
}

func parseTime(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, v); err == nil {
			return ts.UTC(), nil
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("not an RFC3339 timestamp or a unix second count")
}
