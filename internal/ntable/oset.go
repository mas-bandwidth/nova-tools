package ntable

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// The primitive: an ordered set, a Redis ZSET. Every body cell is one, and
// nothing here knows what a table is.

// FnMove is the library function Move calls: ZREM from, ZADD to, in one
// call, refused NOTMEMBER when the member is not in from
// (internal/nsprint/fn/lua/table.lua).
const FnMove = "ns_oset_move"

// ErrNotMember is Move's refusal: the member is not in the set it would
// leave.
var ErrNotMember = errors.New("NOTMEMBER")

// Add puts member into the set at key with score (ZADD).
func Add(ctx context.Context, c redis.Cmdable, key, member string, score float64) error {
	if err := c.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err(); err != nil {
		return fmt.Errorf("zadd %q member %q: %w", key, member, err)
	}
	return nil
}

// Remove takes member out of the set at key (ZREM); a member not there is
// not an error.
func Remove(ctx context.Context, c redis.Cmdable, key, member string) error {
	if err := c.ZRem(ctx, key, member).Err(); err != nil {
		return fmt.Errorf("zrem %q member %q: %w", key, member, err)
	}
	return nil
}

// Move takes member from the set at from and puts it in the set at to, in
// one library call, keeping its score unless keepScore is false, in which
// case score is used. ErrNotMember when from does not hold it.
func Move(ctx context.Context, c redis.Cmdable, from, to, member string, keepScore bool, score float64) error {
	arg := ""
	if !keepScore {
		arg = fmt.Sprint(score)
	}
	reply, err := c.FCall(ctx, FnMove, []string{from, to}, member, arg).Slice()
	if err != nil {
		return fmt.Errorf("%s %s -> %s: %w", FnMove, from, to, err)
	}
	if len(reply) >= 2 && fmt.Sprint(reply[0]) == "REFUSED" {
		if fmt.Sprint(reply[1]) == "NOTMEMBER" {
			return fmt.Errorf("ordered set %q -> %q member %q: %w; inspect the source with ZRANGE before retrying the move", from, to, member, ErrNotMember)
		}
		return fmt.Errorf("%s: REFUSED %v", FnMove, reply[1])
	}
	return nil
}

// Card is the set's cardinality (ZCARD).
func Card(ctx context.Context, c redis.Cmdable, key string) (int64, error) {
	n, err := c.ZCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("zcard %s: %w", key, err)
	}
	return n, nil
}

// MembersOf is every member of the set at key in score order (ZRANGE
// WITHSCORES).
func MembersOf(ctx context.Context, c redis.Cmdable, key string) ([]Member, error) {
	zs, err := c.ZRangeWithScores(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange %s: %w", key, err)
	}
	return members(zs), nil
}

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
// count (internal/nsprint/ws.QueueCardCount) leaves the stream's sentinel
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

// Val is Result's count, 0 on an error.
func (c *CountCmd) Val() int64 {
	n, _ := c.Result()
	return n
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
