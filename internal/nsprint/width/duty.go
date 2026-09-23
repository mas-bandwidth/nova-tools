package width

// The width duty (Stella's hold 3 on #3086): the width tick runs inside the
// reconciler pass, under lease:reconciler, and a child completion replaces
// itself in that same pass with no manual call.
//
// Each pass, after the pass has renewed the lease, the duty
//  1. reads every open sprint's s:<S>:log from its own cursor (plain XREAD,
//     no consumer group) and collects the owner of every `task done`,
//     `task cancel` and `task expire` receipt: a friend whose slot just freed;
//  2. runs one width tick with the lease token (desired, deficit, CAP, the
//     underfull rebalance, READ-BOUND, the coordinator's wake);
//  3. for each friend with a completion and a fillable slot, reserves the
//     ready ids it may start now with the lease token; each reservation
//     appends its spawn line to friend:<f>:wake (kind fill) in the same
//     Redis call, which is what the friend's harness spawns from.
//
// Every write presents the token, so a stale instance writes nothing and the
// duty returns reconcile.ErrFenced, which stops the pass. A new instance
// starts its cursors at the log tips (a restart is not a completion); a sprint
// that opens later is read from its start.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// DutyActor is the actor the width duty writes on receipts.
const DutyActor = "reconciler-width"

// completionKinds are the s:<S>:log receipts that free a friend's slot.
var completionKinds = map[string]bool{"task done": true, "task cancel": true, "task expire": true}

// Duty is the reconciler's width duty. Its Run is a reconcile.Duty.
type Duty struct {
	Store  *store.Store
	Policy Policy
	// Actor is written on receipts; DutyActor when empty.
	Actor string
	// AfterTick, when set, sees every tick the duty ran and the replacements
	// it dealt in that pass.
	AfterTick func(Result, []Reserved)

	mu       sync.Mutex
	instance string            // the lease instance the cursors belong to
	cursors  map[string]string // s:<S>:log -> last id read
}

// Run is one width pass: completions, one fenced tick, fenced replacements.
func (d *Duty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	if d.Store == nil || l == nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: store and lease are required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	actor := d.Actor
	if actor == "" {
		actor = DutyActor
	}
	fresh := d.instance != l.Instance()
	if fresh {
		d.instance, d.cursors = l.Instance(), map[string]string{}
	}
	freed, err := d.completions(ctx, fresh)
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: %w", err)
	}
	res, err := Tick(ctx, d.Store, d.Policy, l.Token(), actor, "")
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: %w", err)
	}
	fillable := map[string]int{}
	for _, r := range res.Rows {
		fillable[r.Friend] = r.Fillable
	}
	var c reconcile.Counts
	var dealt []Reserved
	var errs []error
	for _, f := range freed {
		if fillable[f] <= 0 {
			continue
		}
		got, err := fill(ctx, d.Store, f, l.Token(), actor, "")
		dealt = append(dealt, got...)
		c.Dealt += len(got)
		if errors.Is(err, reconcile.ErrFenced) {
			return c, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("replace %s: %w", f, err))
		}
	}
	if d.AfterTick != nil {
		d.AfterTick(res, dealt)
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("width duty: %w", errors.Join(errs...))
	}
	return c, nil
}

// completions advances the cursors over every open sprint's log and returns
// the sorted owners of the done, cancel and expire receipts read. On a fresh
// instance the cursors start at the log tips and nothing is returned.
func (d *Duty) completions(ctx context.Context, fresh bool) ([]string, error) {
	client := d.Store.Client()
	order, err := client.ZRange(ctx, "sprint:order", 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("sprint order: %w", err)
	}
	pipe := client.Pipeline()
	status := make([]*redis.StringCmd, len(order))
	for i, S := range order {
		status[i] = pipe.HGet(ctx, "s:"+S, "status")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("sprint status: %w", err)
	}
	var streams []string
	for i, S := range order {
		if status[i].Val() == "open" {
			streams = append(streams, "s:"+S+":log")
		}
	}
	if len(streams) == 0 {
		return nil, nil
	}
	if fresh {
		pipe := client.Pipeline()
		tips := make([]*redis.XMessageSliceCmd, len(streams))
		for i, key := range streams {
			tips[i] = pipe.XRevRangeN(ctx, key, "+", "-", 1)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("log tips: %w", err)
		}
		for i, key := range streams {
			d.cursors[key] = "0-0"
			if msgs := tips[i].Val(); len(msgs) > 0 {
				d.cursors[key] = msgs[0].ID
			}
		}
		return nil, nil
	}
	args := make([]string, 0, 2*len(streams))
	args = append(args, streams...)
	for _, key := range streams {
		id, ok := d.cursors[key]
		if !ok {
			id = "0-0" // a sprint opened since the last pass: all of it is new
		}
		args = append(args, id)
	}
	read, err := client.XRead(ctx, &redis.XReadArgs{Streams: args, Count: 10000, Block: -1}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read logs: %w", err)
	}
	type done struct{ key, id string }
	var ids []done
	for _, s := range read {
		sprint := s.Stream[len("s:") : len(s.Stream)-len(":log")]
		for _, m := range s.Messages {
			d.cursors[s.Stream] = m.ID
			if kind, _ := m.Values["kind"].(string); completionKinds[kind] {
				if id, _ := m.Values["id"].(string); id != "" {
					ids = append(ids, done{"s:" + sprint + ":task:" + id, id})
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	pipe = client.Pipeline()
	owners := make([]*redis.StringCmd, len(ids))
	for i, t := range ids {
		owners[i] = pipe.HGet(ctx, t.key, "owner")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("owners: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, o := range owners {
		if f := o.Val(); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out, nil
}
