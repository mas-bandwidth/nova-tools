package ws

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// The one count of a stream set's cards (#4318): the set's ZCARD less the
// stream's sentinel when it is in that set. The sentinel is the stream's
// stop, not one of its cards, so every reader that counts cards counts
// through here: Counts (progress.go: sprint status, the table, ws counts,
// scope ls, stream ls), the progress duty's PROGRESS left=, and ws show's
// STREAM line (show.go counts the members it lists by the same rule).

// CardCountCmd is one queued card count: the ZCARD and the sentinel's ZSCORE
// on the same set, read in the caller's pipeline.
type CardCountCmd struct {
	n    *redis.IntCmd
	stop *redis.FloatCmd
}

// QueueCardCount queues the two reads of one set's card count on pipe.
func QueueCardCount(ctx context.Context, pipe redis.Pipeliner, stream, where string) *CardCountCmd {
	c := &CardCountCmd{n: pipe.ZCard(ctx, Key(stream, where))}
	if sid := SentinelID(stream); sid != "" {
		c.stop = pipe.ZScore(ctx, Key(stream, where), sid)
	}
	return c
}

// Result is the count: the ZCARD's error when it failed; the sentinel's
// absence (redis.Nil) is not an error.
func (c *CardCountCmd) Result() (int64, error) {
	n, err := c.n.Result()
	if err != nil {
		return 0, err
	}
	if c.stop != nil && c.stop.Err() == nil {
		n--
	}
	return n, nil
}

// Val is Result's count, 0 on an error.
func (c *CardCountCmd) Val() int64 {
	n, _ := c.Result()
	return n
}

// QueueStreamCounts queues one stream's card counts on pipe: the six sets of
// Stream in order, then parked. Counts and the progress duty read a stream's
// cells through it.
func QueueStreamCounts(ctx context.Context, pipe redis.Pipeliner, stream string) []*CardCountCmd {
	out := make([]*CardCountCmd, 0, CountsCells+1)
	for _, state := range Stream {
		out = append(out, QueueCardCount(ctx, pipe, stream, state))
	}
	return append(out, QueueCardCount(ctx, pipe, stream, Parked))
}

// CardCount reads one set's card count in one round trip.
func CardCount(ctx context.Context, c redis.Cmdable, stream, where string) (int64, error) {
	pipe := c.Pipeline()
	cmd := QueueCardCount(ctx, pipe, stream, where)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, err
	}
	return cmd.Result()
}
