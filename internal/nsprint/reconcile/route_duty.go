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

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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

// consumerSweepEvery bounds the sweep to one XINFO CONSUMERS per stream per
// minute per instance, plus one when the instance first meets the stream.
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
	// Margin is the least lease time a sprint starts with; 0 is
	// DefaultWriteMargin.
	Margin time.Duration

	instance string
	groups   map[string]bool
	swept    map[string]time.Time // stream -> last consumer sweep
	// done holds the labels and units whose move is final for this instance
	// (created, duplicate, existing) so a pass calls no function for them.
	done map[string]bool
}

// RouteResult is what one pass moved, in receipt order.
type RouteResult struct {
	Reads   []string // task ids pushed
	Fixes   []string // fix, recut and close tasks pushed
	Merging []string
	Carried []string       // read tasks carried to a new head (identical diff)
	Skips   map[string]int // why -> count, for the pass line
	Left    int            // sprints not started (the lease bound, #3805)
}

// Run is one route pass over every open sprint, bounded by the lease.
func (d *RouteDuty) Run(ctx context.Context, l *Lease) (Counts, error) {
	if d.Client == nil || l == nil {
		return Counts{}, fmt.Errorf("route: client and lease are required")
	}
	if d.instance != l.Instance() {
		d.instance = l.Instance()
		d.groups, d.done = map[string]bool{}, map[string]bool{}
		d.swept = map[string]time.Time{}
	}
	margin := d.Margin
	if margin <= 0 {
		margin = DefaultWriteMargin
	}
	res, err := d.Pass(ctx, l, margin)
	c := Counts{Reads: len(res.Reads), Fixes: len(res.Fixes), Merging: len(res.Merging), Carried: len(res.Carried)}
	c.Routed = c.Reads + c.Fixes + c.Merging + c.Carried
	return c, err
}

// Pass runs the three legs over every open sprint with the given lease and
// returns what moved. It stops starting new sprints when the lease is fenced
// or less than the write margin is left.
func (d *RouteDuty) Pass(ctx context.Context, l *Lease, margin time.Duration) (RouteResult, error) {
	res := RouteResult{Skips: map[string]int{}}
	if d.groups == nil {
		d.groups, d.done = map[string]bool{}, map[string]bool{}
	}
	if d.swept == nil {
		d.swept = map[string]time.Time{}
	}
	sprints, err := openSprints(ctx, d.Client)
	if err != nil {
		return res, fmt.Errorf("route: %w", err)
	}
	readers, err := d.liveReaders(ctx)
	if err != nil {
		return res, fmt.Errorf("route: readers: %w", err)
	}
	coordinator, err := d.coordinator(ctx)
	if err != nil {
		return res, fmt.Errorf("route: coordinator: %w", err)
	}
	token := l.Token()
	var errs []string
	for i, s := range sprints {
		if DutyMustStop(l, margin) {
			res.Left = len(sprints) - i
			if l.Fenced() {
				return res, ErrDutyFenced
			}
			return res, &ErrDutyMargin{Left: res.Left, Reason: "route"}
		}
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
		err = d.prLegs(ctx, token, s, stream, readers, coordinator, &res)
		if errors.Is(err, ErrFenced) {
			return res, err
		}
		if err != nil {
			errs = append(errs, err.Error())
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
	consumer := "reconciler-" + d.instance
	if !d.groups[ev] {
		err := d.Client.XGroupCreateMkStream(ctx, ev, RouteGroup, "0").Err()
		if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("%s: group: %w", S, err)
		}
		// One consumer per instance, created once (#3808).
		if err := d.Client.XGroupCreateConsumer(ctx, ev, RouteGroup, consumer).Err(); err != nil {
			return fmt.Errorf("%s: consumer: %w", S, err)
		}
		d.groups[ev] = true
	}
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
	return d.sweepConsumers(ctx, S, ev, consumer)
}

// sweepConsumers deletes every consumer of the route group on ev, other than
// this instance's, idle past ConsumerMaxIdle with nothing pending (a dead
// instance's pending entries are claimed above first, so none is dropped).
// It runs when the instance first meets ev and then once a minute.
func (d *RouteDuty) sweepConsumers(ctx context.Context, S, ev, consumer string) error {
	now := time.Now()
	if at, ok := d.swept[ev]; ok && now.Sub(at) < consumerSweepEvery {
		return nil
	}
	max := d.ConsumerMaxIdle
	if max <= 0 {
		max = DefaultConsumerMaxIdle
	}
	cs, err := d.Client.XInfoConsumers(ctx, ev, RouteGroup).Result()
	if err != nil {
		return fmt.Errorf("%s: consumers: %w", S, err)
	}
	pipe := d.Client.Pipeline()
	n := 0
	for _, c := range cs {
		if c.Name == consumer || c.Pending > 0 || c.Idle < max {
			continue
		}
		pipe.XGroupDelConsumer(ctx, ev, RouteGroup, c.Name)
		n++
	}
	if n > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("%s: sweep consumers: %w", S, err)
		}
	}
	d.swept[ev] = now
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

// coordinator is the first friend (name order) whose friend:<f>:roles holds
// coordinator; with no roster, rowan when registered; else "". It is where a
// swarm-built PR's recut and a close-over-recut go.
func (d *RouteDuty) coordinator(ctx context.Context) (string, error) {
	names, err := d.Client.SMembers(ctx, "friends").Result()
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	pipe := d.Client.Pipeline()
	roles := make([]*redis.StringCmd, len(names))
	for i, f := range names {
		roles[i] = pipe.HGet(ctx, "friend:"+f+":roles", "roles")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	fallback := ""
	for i, f := range names {
		if f == "rowan" {
			fallback = f
		}
		for _, r := range strings.Split(roles[i].Val(), ",") {
			if strings.TrimSpace(r) == "coordinator" {
				return f, nil
			}
		}
	}
	return fallback, nil
}

// prRef reads a task's PR fields (pr as 123, #123, <repo>#123 or a pulls
// URL; repo; ref as <owner/repo>#<n>) into the pr:<repo>:<n> record's repo
// and number.
func prRef(pr, repo, ref string) (string, int, bool) {
	pr, repo, ref = strings.TrimSpace(pr), strings.TrimSpace(repo), strings.TrimSpace(ref)
	num := ""
	if i := strings.LastIndex(pr, "/pull/"); i >= 0 {
		parts := strings.Split(strings.Trim(pr[:i], "/"), "/")
		if len(parts) >= 2 {
			repo = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
		num = strings.Trim(pr[i+len("/pull/"):], "/")
	} else if left, right, ok := strings.Cut(pr, "#"); ok {
		if left != "" {
			repo = left
		}
		num = right
	} else {
		num = pr
	}
	if left, right, ok := strings.Cut(ref, "#"); ok {
		if repo == "" || !strings.Contains(repo, "/") {
			if strings.HasSuffix(left, "/"+repo) || repo == "" {
				repo = left
			}
		}
		if num == "" {
			num = right
		}
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 || repo == "" {
		return "", 0, false
	}
	return repo, n, true
}

type prCand struct {
	repo string
	n    int
}

// prCandidates is every PR of the stream's working and merging tasks, in
// two pipelined round trips, each once, sorted.
func (d *RouteDuty) prCandidates(ctx context.Context, stream string) ([]prCand, error) {
	pipe := d.Client.Pipeline()
	working := pipe.ZRange(ctx, "ws:"+stream+":working", 0, -1)
	merging := pipe.ZRange(ctx, "ws:"+stream+":merging", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	ids := append(working.Val(), merging.Val()...)
	if len(ids) == 0 {
		return nil, nil
	}
	pipe = d.Client.Pipeline()
	rows := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		rows[i] = pipe.HMGet(ctx, "task:"+id, "pr", "repo", "ref")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	seen := map[prCand]bool{}
	var out []prCand
	for _, r := range rows {
		v := r.Val()
		repo, n, ok := prRef(str(v, 0), str(v, 1), str(v, 2))
		if !ok {
			continue
		}
		c := prCand{repo, n}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].repo != out[j].repo {
			return out[i].repo < out[j].repo
		}
		return out[i].n < out[j].n
	})
	return out, nil
}

// prLegs runs ns_route_pr_read and ns_route_pr_fix over the stream's PR
// records: one pipelined read of the records, then one function call per
// decision still open for this instance. A head read at head, routed or
// held with a fix task is final for the instance; a new head is a new key.
func (d *RouteDuty) prLegs(ctx context.Context, token, S, stream string, readers []string, coordinator string, res *RouteResult) error {
	cands, err := d.prCandidates(ctx, stream)
	if err != nil {
		return fmt.Errorf("%s: pr candidates: %w", S, err)
	}
	if len(cands) == 0 {
		return nil
	}
	pipe := d.Client.Pipeline()
	rows := make([]*redis.SliceCmd, len(cands))
	for i, c := range cands {
		rows[i] = pipe.HMGet(ctx, prkey.Key(c.repo, c.n), "head", "state", "reads")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%s: pr records: %w", S, err)
	}
	for i, c := range cands {
		v := rows[i].Val()
		head, state, reads := str(v, 0), str(v, 1), str(v, 2)
		if head == "" || (state != "" && state != "open") {
			continue
		}
		n := strconv.Itoa(c.n)
		rkey := "prread/" + c.repo + "/" + n + "/" + head
		if !d.done[rkey] {
			if len(readers) == 0 {
				res.Skips["no-reader"]++
			} else {
				args := []any{token, S, c.repo, n, stream, d.actor()}
				for _, r := range readers {
					args = append(args, r)
				}
				reply, err := d.Client.FCall(ctx, "ns_route_pr_read", nil, args...).StringSlice()
				if err != nil {
					return fmt.Errorf("%s: ns_route_pr_read %s#%s: %w", S, c.repo, n, err)
				}
				switch word(reply, 0) {
				case "FENCED":
					return ErrFenced
				case "CREATED":
					res.Reads = append(res.Reads, word(reply, 1))
					d.done[rkey] = true
				case "CARRIED":
					res.Carried = append(res.Carried, word(reply, 1))
					d.done[rkey] = true
				case "DUP", "EXISTS":
					d.done[rkey] = true
				case "SKIP":
					res.Skips[word(reply, 1)]++
					if word(reply, 1) == "read-at-head" {
						d.done[rkey] = true
					}
				default:
					return fmt.Errorf("%s: ns_route_pr_read %s#%s: %v", S, c.repo, n, reply)
				}
			}
		}
		fkey := "prfix/" + c.repo + "/" + n + "/" + head
		if d.done[fkey] || !heldAt(reads, head) {
			continue
		}
		reply, err := d.Client.FCall(ctx, "ns_route_pr_fix", nil, token, S, c.repo, n, stream, d.actor(), coordinator).StringSlice()
		if err != nil {
			return fmt.Errorf("%s: ns_route_pr_fix %s#%s: %w", S, c.repo, n, err)
		}
		switch word(reply, 0) {
		case "FENCED":
			return ErrFenced
		case "FIX", "RECUT", "CLOSE":
			res.Fixes = append(res.Fixes, word(reply, 1))
			d.done[fkey] = true
		case "DUP", "EXISTS":
			d.done[fkey] = true
		case "SKIP":
			res.Skips[word(reply, 1)]++
		default:
			return fmt.Errorf("%s: ns_route_pr_fix %s#%s: %v", S, c.repo, n, reply)
		}
	}
	return nil
}

// heldAt is the Go prefilter for the fix leg: some HOLD or DISPOSITION
// verdict=HOLD line names this head. The function re-reads the lines with
// the author and the last-line-wins rule; this only spares a call. A
// DISPOSITION line goes through typedrec.ParseDisposition, the one typed
// parser (#2506), whose HOLD is lenient: a sloppily typed HOLD still holds.
func heldAt(reads, head string) bool {
	head = strings.ToLower(head)
	for _, l := range strings.Split(reads, "\n") {
		h, hold := "", false
		if c, ok := typedrec.ParseDisposition(l); ok {
			h = strings.TrimRight(c.Head, ":,;")
			hold = strings.TrimRight(c.Verdict, ":,;") == "HOLD"
		} else {
			f := strings.Fields(l)
			if len(f) == 0 || f[0] != "HOLD" {
				continue
			}
			hold = true
			for _, w := range f[1:] {
				if k, v, ok := strings.Cut(w, "="); ok && k == "head" {
					h = strings.ToLower(strings.TrimRight(v, ":,;"))
				}
			}
		}
		if hold && len(h) >= 7 && strings.HasPrefix(head, h) {
			return true
		}
	}
	return false
}

func word(reply []string, i int) string {
	if i < len(reply) {
		return reply[i]
	}
	return ""
}
