// clear.go: `nova-sprint table clear` (#3637). Zeroes the table's landed and
// done columns in under a second and leaves waiting untouched: every member of
// every ws:<s>:landed set moves to done/ok through the one task move
// (ns_tcard_move, fn/lua/02_card_move.lua, #3778: landed -> done/ok; the card
// stays, in ws:<s>:done, which the stream table does not print), and each
// friend's current done count (ZCARD of its done, merging and landed card
// sets) is stored in ws:done0 so the friend block counts from zero. working
// and merging are live places, not counters, so a clear never moves them.
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
	done := make([][]*redis.IntCmd, len(p.Friends))
	for i, f := range p.Friends {
		for _, w := range FriendDoneWheres {
			done[i] = append(done[i], pipe.ZCard(ctx, FriendCardsKey(f, w)))
		}
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
		var n int64
		ok := true
		for _, c := range done[i] {
			v, err := c.Result()
			ok = ok && err == nil
			n += v
		}
		if ok {
			p.Done[f] = strconv.FormatInt(n, 10)
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

// Apply moves every planned landed member to done/ok through the one task
// move and stores the done base, in one MULTI/EXEC. receipt is stored as
// ws:checkpoint. A member the move refuses (it left landed since the plan,
// or its record is gone) stays where it is and is named in the error.
func (p *ClearPlan) Apply(ctx context.Context, client redis.UniversalClient, by, why, receipt string) error {
	var moves []*redis.Cmd
	var ids []string
	cmds, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, s := range p.Streams {
			for _, z := range p.Landed[s] {
				id := fmt.Sprint(z.Member)
				ids = append(ids, id)
				moves = append(moves, pipe.FCall(ctx, clearMoveFn, nil, id, "done", by, why, "ok", "ok"))
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
	_ = cmds
	if err != nil && !isReplyError(err) {
		return err
	}
	var refused []string
	for i, m := range moves {
		v, merr := m.Result()
		if s, _ := v.(string); merr != nil || strings.HasPrefix(s, "REFUSED") {
			refused = append(refused, fmt.Sprintf("%s: %v%v", ids[i], s, merr))
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%d landed tasks not cleared: %s", len(refused), strings.Join(refused, "; "))
	}
	return nil
}

// clearMoveFn is the one task move (fn/lua/02_card_move.lua).
const clearMoveFn = "ns_tcard_move"

func tsvCell(v string) string { return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v) }
