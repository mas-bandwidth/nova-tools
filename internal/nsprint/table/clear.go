// clear.go: `nova-sprint table clear` (#3637). Zeroes the table's landed and
// done columns in under a second and leaves waiting untouched: every member of
// every ws:<s>:landed set moves to closed (in no set, task:<id> state=closed,
// one ws:log entry each, the any->closed move of the ws index, #3662),
// and each friend's current done count is stored in ws:done0 so the friend
// block counts from zero. working and merging are live task states on the ws
// index, not counters, so a clear never moves them.
//
// Round trips: the stream and friend lists, the landed members and done
// counts, the landed tasks' fields (the checkpoint), then ONE MULTI/EXEC that
// applies every move. The caller writes the checkpoint before Apply.
package table

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ClearPlan is what a clear will move, read before anything is written.
type ClearPlan struct {
	At      time.Time
	Streams []string
	Landed  map[string][]redis.Z         // stream -> landed members (score landed_at ms)
	Tasks   map[string]map[string]string // task id -> task:<id> fields; absent when no hash
	Friends []string
	Done    map[string]string // friend -> done count now
}

// CheckpointFields are the task fields a clear checkpoint keeps, in order.
var CheckpointFields = []string{"title", "pr", "head", "owner", "kind", "route", "ref", "created_at", "state_at"}

// PlanClear reads what a clear at now would move. friends empty reads the
// friends SET.
func PlanClear(ctx context.Context, client redis.UniversalClient, friends []string, now time.Time) (*ClearPlan, error) {
	p := &ClearPlan{At: now, Landed: map[string][]redis.Z{}, Tasks: map[string]map[string]string{}, Done: map[string]string{}}
	pipe := client.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	var fset *redis.StringSliceCmd
	if len(friends) == 0 {
		fset = pipe.SMembers(ctx, "friends")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, fmt.Errorf("read ws:order: %w", err)
	}
	var err error
	if p.Streams, err = order.Result(); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("zrange ws:order: %w", err)
	}
	p.Friends = friends
	if fset != nil {
		p.Friends, _ = fset.Result()
		sort.Strings(p.Friends)
	}

	pipe = client.Pipeline()
	landed := make([]*redis.ZSliceCmd, len(p.Streams))
	for i, s := range p.Streams {
		landed[i] = pipe.ZRangeWithScores(ctx, "ws:"+s+":landed", 0, -1)
	}
	done := make([]*redis.StringCmd, len(p.Friends))
	for i, f := range p.Friends {
		done[i] = pipe.HGet(ctx, "friend:"+f, "done")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, fmt.Errorf("read landed sets: %w", err)
	}
	var ids []string
	for i, s := range p.Streams {
		zs, err := landed[i].Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("zrange ws:%s:landed: %w", s, err)
		}
		if len(zs) > 0 {
			p.Landed[s] = zs
		}
		for _, z := range zs {
			ids = append(ids, fmt.Sprint(z.Member))
		}
	}
	for i, f := range p.Friends {
		if v, err := done[i].Result(); err == nil && v != "" && strings.Trim(v, "0123456789") == "" {
			p.Done[f] = v
		}
	}
	if len(ids) == 0 {
		return p, nil
	}
	pipe = client.Pipeline()
	hm := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		hm[i] = pipe.HGetAll(ctx, "task:"+id)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, fmt.Errorf("read landed tasks: %w", err)
	}
	for i, id := range ids {
		if h, err := hm[i].Result(); err == nil && len(h) > 0 {
			p.Tasks[id] = h
		}
	}
	return p, nil
}

// Count is the number of landed members the clear moves.
func (p *ClearPlan) Count() int {
	n := 0
	for _, zs := range p.Landed {
		n += len(zs)
	}
	return n
}

// Checkpoint is the durable record of what the clear moves, as TSV: a
// comment line, then one `task` row per landed member (stream, id,
// landed_at ms, then CheckpointFields) and one `friend` row per done count.
func (p *ClearPlan) Checkpoint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# nova-sprint table clear checkpoint at=%s landed=%d friends=%d\n", p.At.UTC().Format(time.RFC3339), p.Count(), len(p.Done))
	b.WriteString("# task\tstream\tid\tlanded_at\t" + strings.Join(CheckpointFields, "\t") + "\n# friend\tname\tdone\n")
	for _, s := range p.Streams {
		for _, z := range p.Landed[s] {
			id := fmt.Sprint(z.Member)
			cells := []string{"task", tsvCell(s), tsvCell(id), strconv.FormatInt(int64(z.Score), 10)}
			for _, f := range CheckpointFields {
				cells = append(cells, tsvCell(p.Tasks[id][f]))
			}
			b.WriteString(strings.Join(cells, "\t") + "\n")
		}
	}
	for _, f := range p.Friends {
		if v, ok := p.Done[f]; ok {
			fmt.Fprintf(&b, "friend\t%s\t%s\n", tsvCell(f), v)
		}
	}
	return b.String()
}

// Apply moves every planned landed member to closed and stores the done
// base, in one MULTI/EXEC. receipt is stored as ws:checkpoint. A member that
// left its landed set since the plan is still closed (its hash says so and
// the ZREM is a no-op).
func (p *ClearPlan) Apply(ctx context.Context, client redis.UniversalClient, by, why, receipt string) error {
	ms := strconv.FormatInt(p.At.UnixMilli(), 10)
	_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, s := range p.Streams {
			zs := p.Landed[s]
			if len(zs) == 0 {
				continue
			}
			members := make([]any, len(zs))
			for i, z := range zs {
				members[i] = z.Member
			}
			pipe.ZRem(ctx, "ws:"+s+":landed", members...)
			for _, z := range zs {
				id := fmt.Sprint(z.Member)
				if _, ok := p.Tasks[id]; ok {
					pipe.HSet(ctx, "task:"+id, "state", "closed", "state_at", ms)
				}
				pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", Values: []any{"id", id, "stream", s, "from", "landed", "to", "closed", "by", by, "why", why, "at", ms}})
			}
		}
		if len(p.Done) > 0 {
			fields := make([]any, 0, 2*len(p.Done))
			for _, f := range p.Friends {
				if v, ok := p.Done[f]; ok {
					fields = append(fields, f, v)
				}
			}
			pipe.HSet(ctx, DoneBaseKey, fields...)
		}
		if receipt != "" {
			pipe.Set(ctx, "ws:checkpoint", receipt, 0)
		}
		return nil
	})
	return err
}

func tsvCell(v string) string { return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v) }
