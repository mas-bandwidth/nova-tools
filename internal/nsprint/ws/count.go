package ws

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The one count of a stream set's cards (#4318): the set's ZCARD less the
// stream's sentinel when it is in that set. The sentinel is the stream's
// stop, not one of its cards, so every reader that counts cards counts
// through here: Counts (progress.go: sprint status, the table, ws counts,
// scope ls, stream ls), the progress duty's PROGRESS left=, and ws show's
// STREAM line (show.go counts the members it lists by the same rule).

// CardCountCmd is one queued card count: the ordered set's count with the
// sentinel left out (internal/ntable.CountCmd: the ZCARD and the excluded
// member's ZSCORE on the same set, read in the caller's pipeline). The
// sprint table's stream block counts through the same primitive, bound to
// the same sets (internal/nsprint/table/streams.go), so the two never
// disagree.
type CardCountCmd struct {
	*ntable.CountCmd
}

// QueueCardCount queues the two reads of one set's card count on pipe.
func QueueCardCount(ctx context.Context, pipe redis.Pipeliner, epoch uint64, stream, where string) *CardCountCmd {
	return &CardCountCmd{CountCmd: ntable.QueueCount(ctx, pipe, KeyAt(epoch, stream, where), SentinelID(stream))}
}

// QueueStreamCounts queues one stream's card counts on pipe: the six sets of
// Stream in order, then parked, under epoch (#4238). Counts and the progress
// duty read a stream's cells through it.
func QueueStreamCounts(ctx context.Context, pipe redis.Pipeliner, epoch uint64, stream string) []*CardCountCmd {
	out := make([]*CardCountCmd, 0, CountsCells+1)
	for _, state := range Stream {
		out = append(out, QueueCardCount(ctx, pipe, epoch, stream, state))
	}
	return append(out, QueueCardCount(ctx, pipe, epoch, stream, Parked))
}

// CardCount reads one set's card count in one round trip.
func CardCount(ctx context.Context, c redis.Cmdable, stream, where string) (int64, error) {
	epoch, err := Epoch(ctx, c)
	if err != nil {
		return 0, err
	}
	pipe := c.Pipeline()
	cmd := QueueCardCount(ctx, pipe, epoch, stream, where)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, err
	}
	return cmd.Result()
}

// ParkedAt is the parked cards of every stream of ws:order under epoch e,
// sentinels aside: two round trips. sprint clear reads it for the epoch it
// left, whose parked cards stay parked there (#4238 with #4411).
func ParkedAt(ctx context.Context, c redis.Cmdable, e uint64) (int64, error) {
	streams, err := c.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil && err != redis.Nil {
		return 0, err
	}
	pipe := c.Pipeline()
	cmds := make([]*CardCountCmd, len(streams))
	for i, s := range streams {
		cmds[i] = QueueCardCount(ctx, pipe, e, s, Parked)
	}
	if len(cmds) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return 0, err
		}
	}
	var n int64
	for _, cmd := range cmds {
		k, err := cmd.Result()
		if err != nil {
			return 0, err
		}
		n += k
	}
	return n, nil
}
