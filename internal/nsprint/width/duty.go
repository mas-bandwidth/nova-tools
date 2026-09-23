package width

// The width duty (Stella's hold 3 on #3086): the width tick runs inside the
// reconciler pass, under lease:reconciler, and a child completion replaces
// itself in that same pass with no manual call.
//
// Each pass, after the pass has renewed the lease, the duty
//  1. reads every open sprint's s:<S>:log from its durable cursor
//     (s:<S>:width:cursor; plain XREAD, no consumer group) and collects the
//     owner of every `task done`, `task cancel` and `task expire` receipt: a
//     friend whose slot just freed;
//  2. runs one width tick with the lease token (desired, deficit, CAP, the
//     underfull rebalance, READ-BOUND, the coordinator's wake);
//  3. refills every friend with a fillable slot (the deficit the tick just
//     wrote, not only the friends with a completion), freed friends first,
//     reserving the ready ids it may start now with the lease token; each
//     reservation appends its spawn line to friend:<f>:wake (kind fill) in
//     the same Redis call, which is what the friend's harness spawns from;
//  4. advances the durable cursors to what it read, fenced (ns_width_cursor).
//
// Every write presents the token, so a stale instance writes nothing and the
// duty returns reconcile.ErrFenced, which stops the pass. Stella's hold 3 at
// 54755384: the cursor is never seeded at the log tip. It lives in Redis per
// sprint, is written only under the fence after a processed batch, and a new
// instance resumes from it, so a completion written between one instance's
// read and its restart or fencing is replayed by the next; no cursor yet reads
// the sprint's log from its start. The deficit refill in step 3 is the second
// guard: even a completion that is never read cannot leave a slot idle while
// ready work exists, because the refill follows the tick's fillable count on
// every pass, not completion events.

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
	// AfterRead, when set, sees the sorted owners of the completions the
	// pass read from the durable cursors (before the refill).
	AfterRead func(freed []string)

	mu sync.Mutex
}

// FunctionCursor advances the durable completion cursors under the fence.
const FunctionCursor = "ns_width_cursor"

// CursorKey is sprint S's durable width completion cursor: the last s:<S>:log
// id a processed width pass read.
func CursorKey(S string) string { return "s:" + S + ":width:cursor" }

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
	freed, cursors, err := d.completions(ctx)
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: %w", err)
	}
	if d.AfterRead != nil {
		d.AfterRead(freed)
	}
	res, err := Tick(ctx, d.Store, d.Policy, l.Token(), actor, "")
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: %w", err)
	}
	// Deficit refill: every friend with a fillable slot, freed friends first,
	// whether or not a completion was read for it.
	fillable := map[string]int{}
	var targets []string
	for _, r := range res.Rows {
		fillable[r.Friend] = r.Fillable
	}
	seen := map[string]bool{}
	for _, f := range freed {
		seen[f] = true
		targets = append(targets, f)
	}
	for _, r := range res.Rows {
		if !seen[r.Friend] {
			targets = append(targets, r.Friend)
		}
	}
	var c reconcile.Counts
	var dealt []Reserved
	var errs []error
	for _, f := range targets {
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
	if err := advance(ctx, d.Store, l.Token(), cursors); err != nil {
		if errors.Is(err, reconcile.ErrFenced) {
			return c, err
		}
		errs = append(errs, err)
	}
	if d.AfterTick != nil {
		d.AfterTick(res, dealt)
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("width duty: %w", errors.Join(errs...))
	}
	return c, nil
}

// advance writes the cursors read this pass, fenced; ids only move forward.
func advance(ctx context.Context, st *store.Store, fence string, cursors map[string]string) error {
	if len(cursors) == 0 {
		return nil
	}
	sprints := make([]string, 0, len(cursors))
	for S := range cursors {
		sprints = append(sprints, S)
	}
	sort.Strings(sprints)
	args := []any{fence}
	for _, S := range sprints {
		args = append(args, S, cursors[S])
	}
	reply, err := st.Client().FCall(ctx, FunctionCursor, nil, args...).Result()
	if err != nil {
		return fmt.Errorf("width cursor: %w", err)
	}
	return fencedReply("width cursor", reply)
}

// completions reads every open sprint's log from its durable cursor and
// returns the sorted owners of the done, cancel and expire receipts read,
// with the last id read per sprint (to advance once the pass is processed).
// A sprint with no cursor yet is read from its start.
func (d *Duty) completions(ctx context.Context) ([]string, map[string]string, error) {
	client := d.Store.Client()
	order, err := client.ZRange(ctx, "sprint:order", 0, -1).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("sprint order: %w", err)
	}
	pipe := client.Pipeline()
	status := make([]*redis.StringCmd, len(order))
	cursor := make([]*redis.StringCmd, len(order))
	for i, S := range order {
		status[i] = pipe.HGet(ctx, "s:"+S, "status")
		cursor[i] = pipe.Get(ctx, CursorKey(S))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("sprint status: %w", err)
	}
	var streams, from []string
	for i, S := range order {
		if status[i].Val() != "open" {
			continue
		}
		streams = append(streams, "s:"+S+":log")
		id := cursor[i].Val()
		if id == "" {
			id = "0-0" // no processed pass yet: the log from the sprint's open
		}
		from = append(from, id)
	}
	if len(streams) == 0 {
		return nil, nil, nil
	}
	read, err := client.XRead(ctx, &redis.XReadArgs{Streams: append(streams, from...), Count: 10000, Block: -1}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read logs: %w", err)
	}
	type done struct{ key, id string }
	var ids []done
	cursors := map[string]string{}
	for _, s := range read {
		sprint := s.Stream[len("s:") : len(s.Stream)-len(":log")]
		for _, m := range s.Messages {
			cursors[sprint] = m.ID
			if kind, _ := m.Values["kind"].(string); completionKinds[kind] {
				if id, _ := m.Values["id"].(string); id != "" {
					ids = append(ids, done{"s:" + sprint + ":task:" + id, id})
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, cursors, nil
	}
	pipe = client.Pipeline()
	owners := make([]*redis.StringCmd, len(ids))
	for i, t := range ids {
		owners[i] = pipe.HGet(ctx, t.key, "owner")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("owners: %w", err)
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
	return out, cursors, nil
}
