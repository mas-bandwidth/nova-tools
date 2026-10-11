package ntable

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// The primitive: an ordered set, a Redis ZSET. Every body cell is one, and
// nothing here knows what a table is.

// FnMove is the ordered-set move library function: ZREM from, ZADD to, in
// one call, refused NOTMEMBER when the member is not in from
// (pkg/nsprint/fn/lua/table.lua).
const FnMove = "ns_oset_move"

// ErrNotMember is the refusal of a move whose set does not hold the member
// it would leave.
var ErrNotMember = errors.New("NOTMEMBER")

func members(zs []redis.Z) []Member {
	out := make([]Member, 0, len(zs))
	for _, z := range zs {
		out = append(out, Member{Member: fmt.Sprint(z.Member), Score: z.Score})
	}
	return out
}

// CountCmd is one queued count of an ordered set: its ZCARD, less one when
// the excluded member is in it (its ZSCORE rides the same pipeline). It is
// the one count every reader of a set counts through: the sprint's card
// count leaves the stream's sentinel
// out this way.
type CountCmd struct {
	n    *redis.IntCmd
	stop *redis.FloatCmd
}

// QueueCount queues the count of the set at key on pipe, leaving exclude
// out when it is a member ("" excludes nothing).
func QueueCount(ctx context.Context, pipe redis.Pipeliner, key, exclude string) *CountCmd {
	c := &CountCmd{n: pipe.ZCard(ctx, key)}
	if exclude != "" {
		c.stop = pipe.ZScore(ctx, key, exclude)
	}
	return c
}

// Result is the count: the ZCARD's error when it failed; the excluded
// member's absence (redis.Nil) is not an error.
func (c *CountCmd) Result() (int64, error) {
	n, err := c.n.Result()
	if err != nil {
		return 0, err
	}
	if c.stop != nil && c.stop.Err() == nil {
		n--
	}
	return n, nil
}

// membersCmd is one queued ZRANGE WITHSCORES of a cell, its excluded member
// dropped on the way out.
type membersCmd struct {
	zs      *redis.ZSliceCmd
	exclude string
}

func queueMembers(ctx context.Context, pipe redis.Pipeliner, key, exclude string) *membersCmd {
	return &membersCmd{zs: pipe.ZRangeWithScores(ctx, key, 0, -1), exclude: exclude}
}

func (m *membersCmd) result() ([]Member, error) {
	zs, err := m.zs.Result()
	if err != nil {
		return nil, err
	}
	out := members(zs)
	if m.exclude == "" {
		return out, nil
	}
	kept := out[:0]
	for _, x := range out {
		if x.Member != m.exclude {
			kept = append(kept, x)
		}
	}
	return kept, nil
}
