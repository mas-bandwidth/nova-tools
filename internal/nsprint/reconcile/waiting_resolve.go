package reconcile

// The waiting-resolve duty (nova-tools #3872): waiting -> ready is automatic.
//
// THE HURT (2026-09-25). Rowan walked the waiting sets by hand three times in
// one morning, checking each card's DEPENDS-ON against what had landed and
// moving the released ones to ready. Glenn 09:50 AM ET: "This activity of
// checking 'waiting' cards and for ones that aren't blocked on dependencies
// distributing them to friends (or swarms!) should be an automatic thing."
//
// THE RULE. Every reconciler pass, under the lease, this duty reads
// ws:<s>:waiting for every stream in ws:order and each waiting task's
// blocked_on (its DEPENDS-ON, the one form of #3409: task ids and
// owner/repo#n issues or PRs, joined by ',' or ';'), and checks every entry
// against the records:
//
//   - task:<id> (or a bare id) is met when task:<id> is landed (state landed,
//     or where landed, or where done with where_ok not fail); a stream's
//     sentinel, <slug>:sentinel (nova-tools #4318, ws.SentinelID), is a
//     task id like any other, so a stream that must wait for another whole
//     stream puts DEPENDS-ON <slug>:sentinel on its first card and that is
//     the one kind of edge there is;
//   - owner/repo#n is met when a task in a ws set whose pr, ref or origin
//     names it is landed;
//   - an entry with no record (no task:<id>; no task in any ws set naming the
//     repo#n; any other form) is NOT met and is reported in unknown=, never
//     guessed: no evidence is not negative evidence;
//   - blocked_on "none" or "-" has nothing to wait on and is met; an empty
//     blocked_on is no evidence either way and the task stays waiting.
//
// A stream's own sentinel is never a waiter here: it waits in its stream
// for every other card to land and lands by structure with the last one
// (TK.land_stop in 02_card_move.lua; task land by hand when the last card
// was cancelled instead). The duty leaves it out of the counts, and when a
// waiting sentinel has no live card left it prints the remedy:
//
//	SENTINEL stream=<s> id=<slug>:sentinel live=0 ready-to-land: nova-sprint task land --id <slug>:sentinel --sha <merge sha>
//
// A sentinel edge is met by landed alone (never by done: a sentinel's done
// is a rename's), so a dependent stream is released only by the stop.
//
// A task whose every entry is met moves waiting -> ready through
// ns_ws_move_many (ws.MoveMany, the ws index's one writer), one call per
// stream per distinct blocked_on, so the ws:log entry's why names the
// dependencies that released it; its score in ready is its created_at,
// unchanged. Reads are pipelined rounds over the sets and the named records,
// never a SCAN or KEYS. The duty prints one receipt line per stream with
// waiting tasks when it moves something or what is still waiting changed:
//
//	RESOLVE stream=<s> ready=<k> still=<n> on=<unmet deps> unknown=<deps>
//
// Every write is bounded by the lease (#3322, #3805): no move starts with
// less than the write margin of the lease left, and a fenced lease stops the
// duty with ErrFenced.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pipeerr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// WaitingResolve is the reconciler's waiting-resolve duty. Its Run is a Duty.
type WaitingResolve struct {
	Client *redis.Client
	// Actor is the ws:log `by`; "reconciler" when empty.
	Actor string
	// Margin is the lease time kept back from the moves; DefaultWriteMargin
	// capped at TTL/6 when zero.
	Margin time.Duration
	// Out receives the RESOLVE receipt lines; nil prints nothing.
	Out io.Writer

	last map[string]string // stream -> what its last printed line held waiting
}

// ResolveLine is one stream's receipt for one pass.
type ResolveLine struct {
	Stream  string
	Ready   []string   // ids moved to ready, oldest first
	Still   int        // tasks left waiting
	On      []string   // unmet dependencies that have a record
	Unknown []string   // dependencies with no record
	Refused []ws.IDWhy // ids ns_ws_move_many refused (left waiting, counted in Still)
	// Stop is the stream's waiting sentinel with no live card left (its
	// landing by structure had no last landing: the last card was cancelled).
	Stop string
}

// StopLine is the remedy line for a sentinel ready to land.
func (r ResolveLine) StopLine() string {
	return fmt.Sprintf("SENTINEL stream=%s id=%s live=0 ready-to-land: nova-sprint task land --id %s --sha <merge sha>",
		wrField(r.Stream), r.Stop, r.Stop)
}

// String is the receipt line.
func (r ResolveLine) String() string {
	return fmt.Sprintf("RESOLVE stream=%s ready=%d still=%d on=%s unknown=%s",
		wrField(r.Stream), len(r.Ready), r.Still, wrList(r.On), wrList(r.Unknown))
}

func wrField(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"=") {
		return strconv.Quote(s)
	}
	return s
}

func wrList(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ",")
}

// Run is one duty pass: Counts.Routed is the tasks moved to ready,
// Counts.Refused the moves ns_ws_move_many refused (each also named in the
// duty's error), so they reach the DUTY line and proc:progress once.
func (d *WaitingResolve) Run(ctx context.Context, l *Lease) (Counts, error) {
	lines, err := d.Pass(ctx, l)
	var c Counts
	for _, r := range lines {
		c.Routed += len(r.Ready)
		c.Refused += len(r.Refused)
	}
	d.print(lines)
	return c, err
}

func (d *WaitingResolve) print(lines []ResolveLine) {
	if d.Out == nil {
		return
	}
	if d.last == nil {
		d.last = map[string]string{}
	}
	for _, r := range lines {
		// What is still waiting, and on what: an idle pass over the same
		// waiting set prints nothing.
		held := fmt.Sprintf("%d %s %s %s", r.Still, wrList(r.On), wrList(r.Unknown), r.Stop)
		if len(r.Ready) == 0 && d.last[r.Stream] == held {
			continue
		}
		d.last[r.Stream] = held
		if r.Still > 0 || len(r.Ready) > 0 {
			_, _ = fmt.Fprintln(d.Out, r.String())
		}
		if r.Stop != "" {
			_, _ = fmt.Fprintln(d.Out, r.StopLine())
		}
	}
}

func (d *WaitingResolve) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return "reconciler"
}

func (d *WaitingResolve) margin(l *Lease) time.Duration {
	if d.Margin > 0 {
		return d.Margin
	}
	return min(DefaultWriteMargin, l.TTL()/6)
}

// wrDep is one parsed DEPENDS-ON entry.
type wrDep struct {
	raw  string
	kind int    // wrTask, wrRef or wrBad
	key  string // the task id, or owner/repo#n lower-cased
}

const (
	wrBad = iota
	wrTask
	wrRef
)

var (
	wrRefRE = regexp.MustCompile(`^(?:([A-Za-z0-9_.-]+)/)?([A-Za-z0-9_.-]+)#([0-9]+)$`)
	wrURLRE = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/(?:issues|pull)/([0-9]+)(?:[/?#].*)?$`)
	// a task id, or a stream sentinel's (<slug>:sentinel, ws.IsSentinel)
	wrIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$|^[a-z0-9][a-z0-9-]*:sentinel$`)
)

// wrRefKey is owner/repo#n (the owner defaulting to prkey.DefaultOwner),
// lower-cased, for an owner/repo#n, repo#n or GitHub issue/PR URL; "" for
// anything else.
func wrRefKey(s string) string {
	s = strings.TrimSpace(s)
	m := wrRefRE.FindStringSubmatch(s)
	if m == nil {
		m = wrURLRE.FindStringSubmatch(s)
	}
	if m == nil {
		return ""
	}
	owner := m[1]
	if owner == "" {
		owner = prkey.DefaultOwner
	}
	return strings.ToLower(owner + "/" + m[2] + "#" + m[3])
}

// wrParse splits a blocked_on value (ws.SplitDeps, the one splitter). none
// is true for "none" or "-" alone; an empty value returns no deps and none
// false.
func wrParse(text string) (deps []wrDep, none bool) {
	parts := ws.SplitDeps(text)
	if len(parts) == 0 {
		t := strings.TrimSpace(text)
		return nil, t == "none" || t == "-"
	}
	for _, p := range parts {
		dep := wrDep{raw: p}
		if id, ok := strings.CutPrefix(p, "task:"); ok {
			if wrIDRE.MatchString(id) {
				dep.kind, dep.key = wrTask, id
			}
		} else if k := wrRefKey(p); k != "" {
			dep.kind, dep.key = wrRef, k
		} else if wrIDRE.MatchString(p) && p != "none" {
			dep.kind, dep.key = wrTask, p
		}
		deps = append(deps, dep)
	}
	return deps, false
}

// wrLanded is whether a task record's fields say landed.
func wrLanded(state, where, whereOK string) bool {
	return state == "landed" || where == "landed" || (where == "done" && whereOK != "fail")
}

// wrStatus is what the records say of one dependency.
type wrStatus int

const (
	wrUnknown wrStatus = iota // no record
	wrUnmet                   // a record, not landed
	wrMet
)

type wrWaiter struct {
	id, stream, blockedOn string
	deps                  []wrDep
	none                  bool
	stitch                bool // a plan's stitch (#4317): its brief is rewritten on release
}

// Pass resolves every stream's waiting set once and returns one line per
// stream that had waiting tasks, in ws:order.
func (d *WaitingResolve) Pass(ctx context.Context, l *Lease) ([]ResolveLine, error) {
	if d.Client == nil || l == nil {
		return nil, errors.New("waiting-resolve: client and lease are required")
	}
	if err := l.fencedErr(); err != nil {
		return nil, err
	}
	c := d.Client

	// Round 1: the streams, in rank order.
	streams, err := c.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: ws:order: %w", err)
	}
	// A stream a pit stop holds keeps its waiting tasks where they are.
	if streams, err = unheld(ctx, c, streams); err != nil {
		return nil, fmt.Errorf("waiting-resolve: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil
	}

	// Round 2: every stream's waiting set, oldest first.
	pipe := c.Pipeline()
	waitCmds := make([]*redis.StringSliceCmd, len(streams))
	for i, s := range streams {
		waitCmds[i] = pipe.ZRange(ctx, ws.Key(s, "waiting"), 0, -1)
	}
	if err := pipeerr.Exec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("waiting-resolve: waiting sets: %w", err)
	}
	var waiters []*wrWaiter
	stops := map[string]string{} // stream -> its waiting sentinel
	for i, s := range streams {
		for _, id := range waitCmds[i].Val() {
			if ws.IsSentinel(id) {
				stops[s] = id // the stream's stop: it lands by structure, not by this duty
				continue
			}
			waiters = append(waiters, &wrWaiter{id: id, stream: s})
		}
	}
	// A waiting stop with no live card left is named with its remedy.
	ready, err := wrStopsReady(ctx, c, streams, stops)
	if err != nil {
		return nil, err
	}
	if len(waiters) == 0 && len(ready) == 0 {
		return nil, nil
	}

	// Round 3: each waiting task's blocked_on (and its phase: a stitch's
	// brief is regenerated when it is released).
	pipe = c.Pipeline()
	boCmds := make([]*redis.SliceCmd, len(waiters))
	for i, w := range waiters {
		boCmds[i] = pipe.HMGet(ctx, "task:"+w.id, "blocked_on", taskcard.FieldPhase, "kind")
	}
	if err := pipeerr.Exec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("waiting-resolve: blocked_on: %w", err)
	}
	taskDeps, refDeps := map[string]wrStatus{}, map[string]wrStatus{}
	// A plan (kind plan, #4317) is never released: it waits on its stitch
	// and lands with it, so it is not a waiter here.
	kept := waiters[:0]
	keptCmds := boCmds[:0]
	for i, w := range waiters {
		if wrStr(boCmds[i].Val(), 2) == taskcard.KindPlan {
			continue
		}
		kept = append(kept, w)
		keptCmds = append(keptCmds, boCmds[i])
	}
	waiters, boCmds = kept, keptCmds
	if len(waiters) == 0 {
		return nil, nil
	}
	for i, w := range waiters {
		w.blockedOn = strings.TrimSpace(wrStr(boCmds[i].Val(), 0))
		w.stitch = wrStr(boCmds[i].Val(), 1) == taskcard.PhaseStitch
		w.deps, w.none = wrParse(w.blockedOn)
		for _, dep := range w.deps {
			switch dep.kind {
			case wrTask:
				taskDeps[dep.key] = wrUnknown
			case wrRef:
				refDeps[dep.key] = wrUnknown
			}
		}
	}

	// Round 4: the task records named, and, when a repo#n is named, every
	// ws set's members (the tasks that can name it).
	pipe = c.Pipeline()
	taskIDs := wrKeys(taskDeps)
	taskCmds := make([]*redis.SliceCmd, len(taskIDs))
	for i, id := range taskIDs {
		taskCmds[i] = pipe.HMGet(ctx, "task:"+id, "state", "where", "where_ok")
	}
	var setCmds []*redis.StringSliceCmd
	if len(refDeps) > 0 {
		for _, s := range streams {
			for _, st := range ws.States {
				setCmds = append(setCmds, pipe.ZRange(ctx, ws.Key(s, st), 0, -1))
			}
		}
	}
	if err := pipeerr.Exec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("waiting-resolve: dependencies: %w", err)
	}
	for i, id := range taskIDs {
		v := taskCmds[i].Val()
		taskDeps[id] = wrTaskStatus(id, wrStr(v, 0), wrStr(v, 1), wrStr(v, 2))
	}

	// Round 5: the members' names (pr, ref, origin) and whether each landed.
	if len(refDeps) > 0 {
		seen := map[string]bool{}
		var members []string
		for _, cmd := range setCmds {
			for _, m := range cmd.Val() {
				if !seen[m] {
					seen[m] = true
					members = append(members, m)
				}
			}
		}
		if err := wrMembersStatus(ctx, c, members, refDeps); err != nil {
			return nil, err
		}
	}

	// The verdicts, per stream in rank order.
	// lines never grows past len(streams), so the pointers into it hold.
	lines := make([]ResolveLine, 0, len(streams))
	byStream := map[string]*ResolveLine{}
	for _, s := range streams {
		if ready[s] {
			lines = append(lines, ResolveLine{Stream: s, Stop: stops[s]})
			byStream[s] = &lines[len(lines)-1]
		}
	}
	type group struct {
		stream, why   string
		ids, stitches []string
	}
	var groups []*group
	groupOf := map[string]*group{}
	for _, w := range waiters {
		r := byStream[w.stream]
		if r == nil {
			lines = append(lines, ResolveLine{Stream: w.stream})
			r = &lines[len(lines)-1]
			byStream[w.stream] = r
		}
		met := w.none
		if !w.none && len(w.deps) > 0 {
			met = true
			for _, dep := range w.deps {
				st := wrUnknown
				switch dep.kind {
				case wrTask:
					st = taskDeps[dep.key]
				case wrRef:
					st = refDeps[dep.key]
				}
				switch st {
				case wrMet:
					continue
				case wrUnmet:
					r.On = wrAdd(r.On, dep.raw)
				default:
					r.Unknown = wrAdd(r.Unknown, dep.raw)
				}
				met = false
			}
		}
		if !met {
			r.Still++
			continue
		}
		gk := w.stream + "\x00" + w.blockedOn
		g := groupOf[gk]
		if g == nil {
			why := "depends-on met: none"
			if !w.none {
				raws := make([]string, len(w.deps))
				for i, dep := range w.deps {
					raws[i] = dep.raw
				}
				why = "depends-on met: " + strings.Join(raws, ",")
			}
			g = &group{stream: w.stream, why: why}
			groupOf[gk] = g
			groups = append(groups, g)
		}
		g.ids = append(g.ids, w.id)
		if w.stitch {
			g.stitches = append(g.stitches, w.id)
		}
	}

	// The moves, each bounded by the lease.
	var errs []string
	for gi, g := range groups {
		if err := l.fencedErr(); err != nil {
			return lines, err
		}
		if left, m := l.Remaining(), d.margin(l); left < m {
			skipped := 0
			for _, rest := range groups[gi:] {
				skipped += len(rest.ids)
				byStream[rest.stream].Still += len(rest.ids)
			}
			errs = append(errs, fmt.Sprintf("%d of %d move(s) not started, %d task(s) left waiting: LEASE-MARGIN: %s of the lease left, below the %s write margin",
				len(groups)-gi, len(groups), skipped, left.Round(time.Millisecond), m))
			break
		}
		res, err := ws.MoveMany(ctx, c, "ready", d.actor(), g.why, g.ids)
		r := byStream[g.stream]
		if err != nil {
			r.Still += len(g.ids)
			errs = append(errs, fmt.Sprintf("stream %s: %v", g.stream, err))
			continue
		}
		refused := map[string]bool{}
		for _, x := range res.Refused {
			refused[x.ID] = true
			r.Refused = append(r.Refused, x)
			errs = append(errs, fmt.Sprintf("stream %s: %s refused: %s", g.stream, x.ID, x.Why))
		}
		for _, id := range g.ids {
			if refused[id] {
				r.Still++
				continue
			}
			r.Ready = append(r.Ready, id)
		}
		// A released stitch starts with the whole picture (#4317): its body's
		// generated section is every child's PR, RESULT.md summary and read
		// score as the records hold them now, after every child landed.
		for _, id := range g.stitches {
			if refused[id] {
				continue
			}
			if _, _, err := taskcard.WriteStitchBrief(ctx, c, id); err != nil {
				errs = append(errs, fmt.Sprintf("stream %s: stitch %s brief: %v", g.stream, id, err))
			}
		}
	}
	for i := range lines {
		sort.Strings(lines[i].On)
		sort.Strings(lines[i].Unknown)
	}
	if len(errs) > 0 {
		return lines, fmt.Errorf("waiting-resolve: %s", strings.Join(errs, "; "))
	}
	return lines, nil
}

// wrStopsReady reads, for every stream with a waiting sentinel, whether any
// other card of the stream is live (waiting, ready, working, review,
// merging or parked), one pipelined round; ready[s] is true when none is.
func wrStopsReady(ctx context.Context, c *redis.Client, streams []string, stops map[string]string) (map[string]bool, error) {
	ready := map[string]bool{}
	if len(stops) == 0 {
		return ready, nil
	}
	live := []string{ws.Waiting, ws.Ready, ws.Working, ws.Review, ws.Merging, ws.Parked}
	pipe := c.Pipeline()
	cmds := map[string][]*ws.CardCountCmd{}
	for _, s := range streams {
		if stops[s] == "" {
			continue
		}
		for _, w := range live {
			cmds[s] = append(cmds[s], ws.QueueCardCount(ctx, pipe, s, w))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: stops: %w", err)
	}
	for s, cs := range cmds {
		n := int64(0)
		for _, cmd := range cs {
			n += cmd.Val()
		}
		ready[s] = n == 0
	}
	return ready, nil
}

// wrNames is every owner/repo#n a task's pr, ref and origin name.
func wrNames(pr, ref, origin, repo string) []string {
	var out []string
	for _, s := range []string{ref, origin, pr} {
		if k := wrRefKey(s); k != "" {
			out = append(out, k)
		}
	}
	n := strings.TrimPrefix(strings.TrimSpace(pr), "#")
	if _, err := strconv.Atoi(n); err == nil && n != "" {
		r := repo
		if r == "" {
			// The ref's repo when the task carries no repo field.
			if k := wrRefKey(ref); k != "" {
				r = k[:strings.LastIndex(k, "#")]
			}
		}
		if r != "" {
			if full, err := prkey.Full(r); err == nil {
				out = append(out, strings.ToLower(full+"#"+n))
			}
		}
	}
	return out
}

// wrTaskStatus is what a task record's state, where and where_ok say of a
// task-id dependency.
func wrTaskStatus(id, state, where, ok string) wrStatus {
	switch {
	case state == "" && where == "":
		return wrUnknown
	case ws.IsSentinel(id) && (state == "landed" || where == "landed"):
		return wrMet // a stop is met by its landing alone
	case ws.IsSentinel(id):
		return wrUnmet
	case wrLanded(state, where, ok):
		return wrMet
	}
	return wrUnmet
}

// wrMembersStatus reads the members' names (pr, ref, origin) and whether each
// landed, one pipelined round, and settles each owner/repo#n in refDeps.
func wrMembersStatus(ctx context.Context, c redis.Cmdable, members []string, refDeps map[string]wrStatus) error {
	if len(members) == 0 {
		return nil
	}
	pipe := c.Pipeline()
	memCmds := make([]*redis.SliceCmd, len(members))
	for i, m := range members {
		memCmds[i] = pipe.HMGet(ctx, "task:"+m, "state", "where", "where_ok", "pr", "ref", "origin", "repo")
	}
	if err := pipeerr.Exec(ctx, pipe); err != nil {
		return fmt.Errorf("waiting-resolve: members: %w", err)
	}
	for i := range members {
		v := memCmds[i].Val()
		landed := wrLanded(wrStr(v, 0), wrStr(v, 1), wrStr(v, 2))
		for _, k := range wrNames(wrStr(v, 3), wrStr(v, 4), wrStr(v, 5), wrStr(v, 6)) {
			st, named := refDeps[k]
			if !named || st == wrMet {
				continue
			}
			if landed {
				refDeps[k] = wrMet
			} else {
				refDeps[k] = wrUnmet
			}
		}
	}
	return nil
}

// Why is one task card as the duty's next pass reads it: where it is and,
// when it waits, each DEPENDS-ON entry met, unmet (On) or with no record
// (Unknown). Empty is a waiting card with no blocked_on at all: no evidence,
// which the duty leaves waiting.
type Why struct {
	ID, Stream, Where, BlockedOn string
	None, Empty                  bool
	Met, On, Unknown             []string
}

// Explain reads task:<id> (a bare id or task:<id>) and, when it waits, each
// of its dependencies the way Pass does (nova-tools#4399: `ready --why` on a
// waiting card). found is false when there is no such task record. It only
// reads.
func Explain(ctx context.Context, c redis.Cmdable, id string) (w Why, found bool, err error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "task:")
	w.ID = id
	v, err := c.HMGet(ctx, "task:"+id, "where", "stream", "blocked_on").Result()
	if err != nil {
		return w, false, fmt.Errorf("why %s: %w", id, err)
	}
	w.Where, w.Stream, w.BlockedOn = wrStr(v, 0), wrStr(v, 1), strings.TrimSpace(wrStr(v, 2))
	if w.Where == "" {
		return w, false, nil
	}
	if w.Where != ws.Waiting {
		return w, true, nil
	}
	deps, none := wrParse(w.BlockedOn)
	w.None, w.Empty = none, !none && len(deps) == 0
	taskDeps, refDeps := map[string]wrStatus{}, map[string]wrStatus{}
	for _, d := range deps {
		switch d.kind {
		case wrTask:
			taskDeps[d.key] = wrUnknown
		case wrRef:
			refDeps[d.key] = wrUnknown
		}
	}
	pipe := c.Pipeline()
	taskIDs := wrKeys(taskDeps)
	taskCmds := make([]*redis.SliceCmd, len(taskIDs))
	for i, t := range taskIDs {
		taskCmds[i] = pipe.HMGet(ctx, "task:"+t, "state", "where", "where_ok")
	}
	var order *redis.StringSliceCmd
	if len(refDeps) > 0 {
		order = pipe.ZRange(ctx, "ws:order", 0, -1)
	}
	if len(taskIDs) > 0 || order != nil {
		if err := pipeerr.Exec(ctx, pipe); err != nil {
			return w, true, fmt.Errorf("why %s: %w", id, err)
		}
	}
	for i, t := range taskIDs {
		tv := taskCmds[i].Val()
		taskDeps[t] = wrTaskStatus(t, wrStr(tv, 0), wrStr(tv, 1), wrStr(tv, 2))
	}
	if order != nil {
		pipe = c.Pipeline()
		var setCmds []*redis.StringSliceCmd
		for _, s := range order.Val() {
			for _, st := range ws.States {
				setCmds = append(setCmds, pipe.ZRange(ctx, ws.Key(s, st), 0, -1))
			}
		}
		if len(setCmds) > 0 {
			if err := pipeerr.Exec(ctx, pipe); err != nil {
				return w, true, fmt.Errorf("why %s: %w", id, err)
			}
		}
		seen := map[string]bool{}
		var members []string
		for _, cmd := range setCmds {
			for _, m := range cmd.Val() {
				if !seen[m] {
					seen[m] = true
					members = append(members, m)
				}
			}
		}
		if err := wrMembersStatus(ctx, c, members, refDeps); err != nil {
			return w, true, err
		}
	}
	for _, d := range deps {
		st := wrUnknown
		switch d.kind {
		case wrTask:
			st = taskDeps[d.key]
		case wrRef:
			st = refDeps[d.key]
		}
		switch st {
		case wrMet:
			w.Met = wrAdd(w.Met, d.raw)
		case wrUnmet:
			w.On = wrAdd(w.On, d.raw)
		default:
			w.Unknown = wrAdd(w.Unknown, d.raw)
		}
	}
	return w, true, nil
}

// Released is whether the duty's next pass moves the card to ready.
func (w Why) Released() bool {
	return w.Where == ws.Waiting && !w.Empty && len(w.On) == 0 && len(w.Unknown) == 0
}

func wrStr(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

func wrAdd(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func wrKeys(m map[string]wrStatus) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
