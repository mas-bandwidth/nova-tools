package reconcile

// The route duty (nova-tools #3323, #3199): the consumers ok-to-friend,
// pr-to-read and hold-to-fix had no verb, duty or unit, so a card that ended
// OK reached no read, a HOLD reached no fix task and a scored green PR sat
// where it was. `nova-sprint route` serves one sprint by flag; this duty runs
// inside the reconciler pass over every open sprint in the `sprints` set and
// makes the three moves from records alone (no GitHub call): each move is
// one Redis Function call of route_duty.lua with a receipt, fenced on the
// reconciler lease and idempotent under s:<S>:routed, so a second pass pushes
// nothing more.
//
// Two further legs read the pr:<repo>:<n> record (#3579, #3580; the record
// `pr record|lines` writes) for every task in the sprint stream's working and
// merging sets: an open PR whose head no counting friend line covers gets
// exactly one read task on the least-loaded live reader (never the author,
// who is the builder task's owner, not the branch name), the old head's read
// cancelled `superseded by <head>` or carried when the diff is identical;
// a HOLD at head with no open fix task gets exactly one fix task to the
// author (a recut to the coordinator when the swarm built it; close over
// recut on the third held head). The record's read_task/fix_task fields are
// the idempotency and the receipt, so a second pass pushes nothing.
//
// One round trip a pass (#3831): the pass is ONE ns_route_sweep call, which
// reads every open sprint's harvested cards, hold events, units and stream
// PRs and calls each leg's function in the order this duty called them. The
// duty made one call per harvested card per sprint every pass (420 round
// trips at the fleet's counts, SKIPs included), past a one second pass on a
// store 83 ms away.
//
// Bounded by the reconciler lease (#3805): the sweep does not start with
// less than the write margin of the lease left; the pass then returns an
// error wrapping ErrLeaseMargin, which proc:reconciler err records.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultBar is the read score a PR needs to move to merging (Glenn
// 2026-09-22: useful = 8+).
const DefaultBar = 8

// RouteGroup is the duty's consumer group on every s:<S>:hold:events.
const RouteGroup = "route"

// DefaultConsumerMaxIdle is how long a route consumer may sit idle with
// nothing pending before the sweep deletes it (#3808: every reconciler
// instance, each restart and each `reconcile --once` probe, reads under
// its own name, and the group held 258 of them).
const DefaultConsumerMaxIdle = time.Hour

// consumerSweepEvery bounds the consumer sweep to one XINFO CONSUMERS per
// stream per minute per instance, plus one on the instance's first pass.
const consumerSweepEvery = time.Minute

// RouteDuty is the reconciler's route duty. Its Run is a Duty.
type RouteDuty struct {
	Client *redis.Client
	// Actor is written on receipts; DefaultActor when empty.
	Actor string
	// Bar is the merging score bar; DefaultBar when zero.
	Bar int
	// Readers, when set, names the reading friends. Empty: the `readers`
	// SET, and when that is empty every friend in the `friends` SET. Only a
	// friend with a live beat (friend:<f>:beat) is dealt a read.
	Readers []string
	// ConsumerMaxIdle is the sweep's idle bar; DefaultConsumerMaxIdle when
	// zero.
	ConsumerMaxIdle time.Duration

	instance string
	lease    *Lease    // the pass's bound, set by Run; nil under Pass alone
	swept    time.Time // this instance's last consumer sweep
}

// RouteResult is what one pass moved, in receipt order.
type RouteResult struct {
	Reads   []string // task ids pushed
	Fixes   []string // fix, recut and close tasks pushed
	Merging []string
	Carried []string       // read tasks carried to a new head (identical diff)
	Skips   map[string]int // why -> count, for the pass line
}

// Run is one route pass over every open sprint.
func (d *RouteDuty) Run(ctx context.Context, l *Lease) (Counts, error) {
	if d.Client == nil || l == nil {
		return Counts{}, fmt.Errorf("route: client and lease are required")
	}
	if d.instance != l.Instance() {
		d.instance = l.Instance()
		d.swept = time.Time{}
	}
	d.lease = l
	res, err := d.Pass(ctx, l.Token())
	d.lease = nil
	c := Counts{Reads: len(res.Reads), Fixes: len(res.Fixes), Merging: len(res.Merging), Carried: len(res.Carried)}
	c.Routed = c.Reads + c.Fixes + c.Merging + c.Carried
	return c, err
}

// RouteSweepFunction is the one call a route pass makes (route_duty.lua).
const RouteSweepFunction = "ns_route_sweep"

// Pass runs the legs over every open sprint with the given fence token, in
// one ns_route_sweep call (#3831), and returns what moved. It starts nothing
// with less than the write margin of the lease left (#3805).
func (d *RouteDuty) Pass(ctx context.Context, token string) (RouteResult, error) {
	res := RouteResult{Skips: map[string]int{}}
	if err := d.bounded(); errors.Is(err, ErrFenced) {
		return res, err
	} else if err != nil {
		return res, fmt.Errorf("route: sweep not started, no open sprint routed this pass: %w", err)
	}
	// The consumer sweep (#3808) rides the call when the instance first
	// meets the streams and then once a minute.
	idle := int64(0)
	if now := time.Now(); d.swept.IsZero() || now.Sub(d.swept) >= consumerSweepEvery {
		bar := d.ConsumerMaxIdle
		if bar <= 0 {
			bar = DefaultConsumerMaxIdle
		}
		idle = max(bar.Milliseconds(), 1)
		d.swept = now
	}
	args := []any{token, d.actor(), strconv.Itoa(d.bar()), "reconciler-" + d.instance, idle}
	for _, r := range d.Readers {
		args = append(args, r)
	}
	reply, err := d.Client.FCall(ctx, RouteSweepFunction, nil, args...).StringSlice()
	if err != nil {
		return res, fmt.Errorf("route: %s: %w", RouteSweepFunction, err)
	}
	switch word(reply, 0) {
	case "FENCED":
		return res, ErrFenced
	case "OK":
	default:
		return res, fmt.Errorf("route: %s: %v", RouteSweepFunction, reply)
	}
	for i := 1; i+1 < len(reply); i += 2 {
		v := reply[i+1]
		switch reply[i] {
		case "R":
			res.Reads = append(res.Reads, v)
		case "C":
			res.Carried = append(res.Carried, v)
		case "F":
			res.Fixes = append(res.Fixes, v)
		case "M":
			res.Merging = append(res.Merging, v)
		case "S":
			res.Skips[v]++
		}
	}
	return res, nil
}

// bounded is the lease bound before the sweep (Lease.Bounded; nil without
// a lease).
func (d *RouteDuty) bounded() error { return d.lease.Bounded(0) }

func (d *RouteDuty) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return DefaultActor
}

func (d *RouteDuty) bar() int {
	if d.Bar > 0 {
		return d.Bar
	}
	return DefaultBar
}

func word(reply []string, i int) string {
	if i < len(reply) {
		return reply[i]
	}
	return ""
}
