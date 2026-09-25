package reconcile

// The route duty (nova-tools #3323, #3199): the consumers ok-to-friend,
// pr-to-read and hold-to-fix had no verb, duty or unit, so a card that ended
// OK reached no read, a HOLD reached no fix task and a scored green PR sat
// where it was. `nova-sprint route` serves one sprint by flag; this duty runs
// inside the reconciler pass over every open sprint in the `sprints` set and
// makes the three moves from records alone (no GitHub call): each move is
// one Redis Function call of route_duty.lua with a receipt, fenced on the
// reconciler lease and idempotent under s:<S>:routed, so a second pass pushes
// nothing more. Every pass is bounded pipelined reads plus one call per move.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// DefaultBar is the read score a PR needs to move to merging (Glenn
// 2026-09-22: useful = 8+).
const DefaultBar = 8

// RouteGroup is the duty's consumer group on every s:<S>:hold:events.
const RouteGroup = "route"

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

	instance string
	groups   map[string]bool
	// done holds the labels and units whose move is final for this instance
	// (created, duplicate, existing) so a pass calls no function for them.
	done map[string]bool
}

// RouteResult is what one pass moved, in receipt order.
type RouteResult struct {
	Reads   []string // task ids pushed
	Fixes   []string
	Merging []string
	Skips   map[string]int // why -> count, for the pass line
}

// Run is one route pass over every open sprint.
func (d *RouteDuty) Run(ctx context.Context, l *Lease) (Counts, error) {
	if d.Client == nil || l == nil {
		return Counts{}, fmt.Errorf("route: client and lease are required")
	}
	if d.instance != l.Instance() {
		d.instance = l.Instance()
		d.groups, d.done = map[string]bool{}, map[string]bool{}
	}
	res, err := d.Pass(ctx, l.Token())
	c := Counts{Reads: len(res.Reads), Fixes: len(res.Fixes), Merging: len(res.Merging)}
	c.Routed = c.Reads + c.Fixes + c.Merging
	return c, err
}

// Pass runs the three legs over every open sprint with the given fence
// token and returns what moved.
func (d *RouteDuty) Pass(ctx context.Context, token string) (RouteResult, error) {
	res := RouteResult{Skips: map[string]int{}}
	if d.groups == nil {
		d.groups, d.done = map[string]bool{}, map[string]bool{}
	}
	sprints, err := openSprints(ctx, d.Client)
	if err != nil {
		return res, fmt.Errorf("route: %w", err)
	}
	readers, err := d.liveReaders(ctx)
	if err != nil {
		return res, fmt.Errorf("route: readers: %w", err)
	}
	var errs []string
	for _, s := range sprints {
		stream, err := d.Client.HGet(ctx, "s:"+s, "stream").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return res, fmt.Errorf("route: %s: %w", s, err)
		}
		if stream == "" {
			stream = s
		}
		for _, leg := range []func(context.Context, string, string, string, []string, *RouteResult) error{
			d.reads, d.fixes, d.merging,
		} {
			err := leg(ctx, token, s, stream, readers, &res)
			if errors.Is(err, ErrFenced) {
				return res, err
			}
			if err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("route: %s", strings.Join(errs, "; "))
	}
	return res, nil
}

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

// openSprints is every member of `sprints` whose s:<S> status is open, in
// name order, read in one pipeline.
func openSprints(ctx context.Context, c *redis.Client) ([]string, error) {
	names, err := c.SMembers(ctx, "sprints").Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(names))
	for i, s := range names {
		cmds[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var open []string
	for i, s := range names {
		if cmds[i].Val() == "open" {
			open = append(open, s)
		}
	}
	return open, nil
}

// liveReaders is the reading friends with a live beat, in name order.
func (d *RouteDuty) liveReaders(ctx context.Context) ([]string, error) {
	names := d.Readers
	if len(names) == 0 {
		pipe := d.Client.Pipeline()
		readers := pipe.SMembers(ctx, "readers")
		friends := pipe.SMembers(ctx, "friends")
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		names = readers.Val()
		if len(names) == 0 {
			names = friends.Val()
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	pipe := d.Client.Pipeline()
	member := make([]*redis.BoolCmd, len(names))
	beat := make([]*redis.IntCmd, len(names))
	for i, f := range names {
		member[i] = pipe.SIsMember(ctx, "friends", f)
		beat[i] = pipe.Exists(ctx, "friend:"+f+":beat")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var live []string
	for i, f := range names {
		if member[i].Val() && beat[i].Val() == 1 && f != "jev" {
			live = append(live, f)
		}
	}
	sort.Strings(live)
	return live, nil
}

// reads: every harvested card of the sprint not yet final for this instance
// is offered to ns_route_read with the live readers. The function holds the
// guards (PR record, JEV line at head, idempotency) and the choice.
func (d *RouteDuty) reads(ctx context.Context, token, S, stream string, readers []string, res *RouteResult) error {
	labels, err := d.Client.SMembers(ctx, "s:"+S+":idx:card:harvested").Result()
	if err != nil {
		return fmt.Errorf("%s: harvested: %w", S, err)
	}
	sort.Strings(labels)
	if len(readers) == 0 && len(labels) > 0 {
		res.Skips["no-reader"] += len(labels)
		return nil
	}
	for _, label := range labels {
		key := "read/" + S + "/" + label
		if d.done[key] {
			continue
		}
		args := []any{token, S, label, stream, d.actor()}
		for _, r := range readers {
			args = append(args, r)
		}
		reply, err := d.Client.FCall(ctx, "ns_route_read", nil, args...).StringSlice()
		if err != nil {
			return fmt.Errorf("%s: ns_route_read %s: %w", S, label, err)
		}
		switch word(reply, 0) {
		case "FENCED":
			return ErrFenced
		case "CREATED":
			res.Reads = append(res.Reads, word(reply, 1))
			d.done[key] = true
		case "DUP", "EXISTS":
			d.done[key] = true
		case "SKIP":
			res.Skips[word(reply, 1)]++
		default:
			return fmt.Errorf("%s: ns_route_read %s: %v", S, label, reply)
		}
	}
	return nil
}

// fixes: the sprint's hold events under the route group. A pending entry a
// dead instance left is claimed first; every entry is acknowledged by the
// function (a hold) or here (a note or repair, which route nothing).
func (d *RouteDuty) fixes(ctx context.Context, token, S, stream string, _ []string, res *RouteResult) error {
	ev := "s:" + S + ":hold:events"
	if !d.groups[ev] {
		err := d.Client.XGroupCreateMkStream(ctx, ev, RouteGroup, "0").Err()
		if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("%s: group: %w", S, err)
		}
		d.groups[ev] = true
	}
	consumer := "reconciler-" + d.instance
	var msgs []redis.XMessage
	claimed, _, err := d.Client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: ev, Group: RouteGroup, Consumer: consumer, MinIdle: 0, Start: "0-0", Count: DefaultEventCount,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%s: claim: %w", S, err)
	}
	msgs = append(msgs, claimed...)
	read, err := d.Client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: RouteGroup, Consumer: consumer, Streams: []string{ev, ">"}, Count: DefaultEventCount, Block: -1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%s: read: %w", S, err)
	}
	for _, r := range read {
		msgs = append(msgs, r.Messages...)
	}
	var ack []string
	for _, m := range msgs {
		get := func(k string) string { s, _ := m.Values[k].(string); return s }
		if get("type") != "hold" {
			ack = append(ack, m.ID)
			continue
		}
		reply, err := d.Client.FCall(ctx, "ns_route_fix", nil,
			token, S, m.ID, get("unit"), get("repo"), get("pr"), get("who"), get("head"), stream, d.actor()).StringSlice()
		if err != nil {
			return fmt.Errorf("%s: ns_route_fix %s: %w", S, m.ID, err)
		}
		switch word(reply, 0) {
		case "FENCED":
			return ErrFenced
		case "CREATED":
			res.Fixes = append(res.Fixes, word(reply, 1))
		case "SKIP":
			res.Skips[word(reply, 1)]++
		case "DUP", "EXISTS":
		default:
			return fmt.Errorf("%s: ns_route_fix %s: %v", S, m.ID, reply)
		}
	}
	if len(ack) > 0 {
		if err := d.Client.XAck(ctx, ev, RouteGroup, ack...).Err(); err != nil {
			return fmt.Errorf("%s: ack: %w", S, err)
		}
	}
	return nil
}

// merging: the sprint's units are read in one pipeline (head, state, pr,
// holds) and only a unit that may be ready (open, with a PR, no open hold,
// CI receipts at head) is offered to ns_route_merging, which re-checks every
// guard in the call.
func (d *RouteDuty) merging(ctx context.Context, token, S, stream string, _ []string, res *RouteResult) error {
	units, err := d.Client.SMembers(ctx, "s:"+S+":units").Result()
	if err != nil {
		return fmt.Errorf("%s: units: %w", S, err)
	}
	sort.Strings(units)
	var pending []string
	for _, u := range units {
		if !d.done["merging/"+S+"/"+u] {
			pending = append(pending, u)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	pipe := d.Client.Pipeline()
	rows := make([]*redis.SliceCmd, len(pending))
	for i, u := range pending {
		rows[i] = pipe.HMGet(ctx, "s:"+S+":u:"+u, "head", "state", "repo", "pr", "holds_open")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%s: units: %w", S, err)
	}
	type cand struct{ unit, key string }
	var cands []cand
	pipe = d.Client.Pipeline()
	var gids []*redis.IntCmd
	for i, u := range pending {
		v := rows[i].Val()
		head, state, repo, pr, holds := str(v, 0), str(v, 1), str(v, 2), str(v, 3), str(v, 4)
		if head == "" || pr == "" || state == "landed" || state == "landing" || state == "dropped" {
			continue
		}
		if n, _ := strconv.Atoi(holds); n > 0 {
			res.Skips["holds-open"]++
			continue
		}
		cands = append(cands, cand{u, "merging/" + S + "/" + u})
		gids = append(gids, pipe.SCard(ctx, "ci:"+repo+":"+head+":gids"))
	}
	if len(cands) == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%s: ci: %w", S, err)
	}
	for i, c := range cands {
		if gids[i].Val() == 0 {
			res.Skips["no-ci"]++
			continue
		}
		reply, err := d.Client.FCall(ctx, "ns_route_merging", nil,
			token, S, c.unit, strconv.Itoa(d.bar()), stream, d.actor()).StringSlice()
		if err != nil {
			return fmt.Errorf("%s: ns_route_merging %s: %w", S, c.unit, err)
		}
		switch word(reply, 0) {
		case "FENCED":
			return ErrFenced
		case "MERGING":
			res.Merging = append(res.Merging, word(reply, 1))
			d.done[c.key] = true
		case "DUP":
			d.done[c.key] = true
		case "SKIP":
			res.Skips[word(reply, 1)]++
		default:
			return fmt.Errorf("%s: ns_route_merging %s: %v", S, c.unit, reply)
		}
	}
	return nil
}

func word(reply []string, i int) string {
	if i < len(reply) {
		return reply[i]
	}
	return ""
}
