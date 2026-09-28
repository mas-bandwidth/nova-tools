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
// The pass is ONE Redis Function call, ns_route_pass (2026-09-27, Glenn:
// "You always need to batch redis. This is standard."): the store is 128 ms
// from the Studio and the Go pass chained 17 round trips, one per read. The
// function reads the open sprints, the live readers and the coordinator and
// runs the four legs below in the server, calling the same five functions
// the Go legs called over the wire; Go sends the token, the actor, the bar,
// the instance, the sweep flag, its clock and the --readers, and reads the
// moves back as pairs. Every function checks the fence itself, so a pass
// that lost its lease writes nothing past the first refusal.
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
// Bounded by the reconciler lease (#3805): no sprint, leg or move starts
// with less than the write margin of the lease left; the pass returns what
// it moved and an error wrapping ErrLeaseMargin naming where it stopped and
// how many sprints it did not start, which proc:reconciler err records.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pipeerr"
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

	instance string
	lease    *Lease    // the pass's bound, set by Run; nil under Pass alone
	swept    time.Time // the last consumer sweep this instance asked for
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

// Pass runs the four legs over every open sprint with the given fence
// token, one function call, and returns what moved.
func (d *RouteDuty) Pass(ctx context.Context, token string) (RouteResult, error) {
	res := RouteResult{Skips: map[string]int{}}
	if err := d.bounded(); errors.Is(err, ErrFenced) {
		return res, err
	} else if err != nil {
		// The refusal says what it left (#3805): the open sprints are read
		// only on this path, never on a pass that runs.
		sprints, rerr := openSprints(ctx, d.Client)
		if rerr != nil {
			return res, fmt.Errorf("route: stopped before the pass, sprints unread (%v): %w", rerr, err)
		}
		return res, fmt.Errorf("route: stopped before the pass, %d of %d sprint(s) not started: %w", len(sprints), len(sprints), err)
	}
	now := time.Now()
	sweep := "0"
	if d.swept.IsZero() || now.Sub(d.swept) >= consumerSweepEvery {
		sweep = "1"
	}
	maxIdle := d.ConsumerMaxIdle
	if maxIdle <= 0 {
		maxIdle = DefaultConsumerMaxIdle
	}
	args := []any{token, d.actor(), strconv.Itoa(d.bar()), d.instance, sweep,
		strconv.FormatInt(maxIdle.Milliseconds(), 10), strconv.FormatInt(now.UnixMilli(), 10)}
	for _, r := range d.Readers {
		args = append(args, r)
	}
	reply, err := d.Client.FCall(ctx, "ns_route_pass", nil, args...).StringSlice()
	if err != nil {
		return res, fmt.Errorf("route: ns_route_pass: %w", err)
	}
	if word(reply, 0) == "FENCED" {
		return res, ErrFenced
	}
	if sweep == "1" {
		d.swept = now
	}
	var errs []string
	for i := 0; i+1 < len(reply); i += 2 {
		k, v := reply[i], reply[i+1]
		switch k {
		case "read":
			res.Reads = append(res.Reads, v)
		case "fix":
			res.Fixes = append(res.Fixes, v)
		case "merging":
			res.Merging = append(res.Merging, v)
		case "carried":
			res.Carried = append(res.Carried, v)
		case "skip":
			res.Skips[v]++
		case "err":
			errs = append(errs, v)
		default:
			return res, fmt.Errorf("route: ns_route_pass: unexpected reply %q %q", k, v)
		}
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("route: %s", strings.Join(errs, "; "))
	}
	return res, nil
}

// bounded is the lease bound before a move (Lease.Bounded; nil without a
// lease).
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
	if err := pipeerr.Exec(ctx, pipe); err != nil {
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
	if err := pipeerr.Exec(ctx, pipe); err != nil {
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
