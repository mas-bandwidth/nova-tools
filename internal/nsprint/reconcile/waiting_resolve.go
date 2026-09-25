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
//     or where landed, or where done with where_ok not fail);
//   - owner/repo#n is met when a task in a ws set whose pr, ref or origin
//     names it is landed;
//   - an entry with no record (no task:<id>; no task in any ws set naming the
//     repo#n; any other form) is NOT met and is reported in unknown=, never
//     guessed: no evidence is not negative evidence;
//   - blocked_on "none" or "-" has nothing to wait on and is met; an empty
//     blocked_on is no evidence either way and the task stays waiting.
//
// A task whose every entry is met moves waiting -> ready only when it has a
// consumer this tick (nova-tools #4059; consumer.go): a live friend WHO
// admits with a seat, or a route that names the swarm. Glenn 2026-09-25
// 1:40 PM ET: waiting -> ready is ONE WAY, and a ready card is dealt at once
// (the deal duty runs next in the same pass, with the same answer), so ready
// is never a resting state. The plan walks the ready cards and the released
// ones together in the deal's order (ws:order rank, then oldest first), so a
// seat the deal will give an older ready card is not promised twice.
//
// The move goes through ns_ws_move_many (ws.MoveMany, the ws index's one
// writer), one call per stream per distinct blocked_on, so the ws:log entry's
// why names the dependencies that released it; its score in ready is its
// created_at, unchanged. A released task with no consumer stays in waiting
// with why=no-consumer, written once through ns_ws_note (one ws:log entry the
// first time, nothing after); one whose friends are full (no-seat) stays in
// waiting and nothing is written. Reads are pipelined rounds over the sets
// and the named records, never a SCAN or KEYS. The duty prints one receipt
// line per stream with waiting tasks when it moves something or what is
// still waiting changed:
//
//	RESOLVE stream=<s> ready=<k> still=<n> on=<unmet deps> unknown=<deps> noconsumer=<ids> noseat=<ids>
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
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
	// NoConsumer and NoSeat are the released ids left waiting (counted in
	// Still): no consumer at all, or every admitted friend full this tick.
	NoConsumer []string
	NoSeat     []string
}

// String is the receipt line.
func (r ResolveLine) String() string {
	return fmt.Sprintf("RESOLVE stream=%s ready=%d still=%d on=%s unknown=%s noconsumer=%s noseat=%s",
		wrField(r.Stream), len(r.Ready), r.Still, wrList(r.On), wrList(r.Unknown), wrList(r.NoConsumer), wrList(r.NoSeat))
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

// Run is one duty pass: Counts.Routed is the tasks moved to ready.
func (d *WaitingResolve) Run(ctx context.Context, l *Lease) (Counts, error) {
	lines, err := d.Pass(ctx, l)
	n := 0
	for _, r := range lines {
		n += len(r.Ready)
	}
	d.print(lines)
	return Counts{Routed: n}, err
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
		held := fmt.Sprintf("%d %s %s %s %s", r.Still, wrList(r.On), wrList(r.Unknown), wrList(r.NoConsumer), wrList(r.NoSeat))
		if len(r.Ready) == 0 && d.last[r.Stream] == held {
			continue
		}
		d.last[r.Stream] = held
		_, _ = fmt.Fprintln(d.Out, r.String())
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
	wrIDRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
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

// wrParse splits a blocked_on value. none is true for "none" or "-" alone;
// an empty value returns no deps and none false.
func wrParse(text string) (deps []wrDep, none bool) {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(parts) == 1 && (parts[0] == "none" || parts[0] == "-") {
		return nil, true
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p] {
			continue
		}
		seen[p] = true
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
	score                 float64
	deps                  []wrDep
	none                  bool
	card                  planCard
	record                bool // task:<id> exists (a card-model id has none)
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
	if len(streams) == 0 {
		return nil, nil
	}

	// Round 2: every stream's waiting set, oldest first.
	pipe := c.Pipeline()
	waitCmds := make([]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		waitCmds[i] = pipe.ZRangeWithScores(ctx, ws.Key(s, "waiting"), 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: waiting sets: %w", err)
	}
	var waiters []*wrWaiter
	for i, s := range streams {
		for _, z := range waitCmds[i].Val() {
			id, _ := z.Member.(string)
			waiters = append(waiters, &wrWaiter{id: id, stream: s, score: z.Score})
		}
	}
	if len(waiters) == 0 {
		return nil, nil
	}

	// Round 3: each waiting task's blocked_on and the fields its consumer
	// is read from.
	pipe = c.Pipeline()
	boCmds := make([]*redis.SliceCmd, len(waiters))
	for i, w := range waiters {
		boCmds[i] = pipe.HMGet(ctx, "task:"+w.id, append([]string{"blocked_on"}, planFields...)...)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: blocked_on: %w", err)
	}
	taskDeps, refDeps := map[string]wrStatus{}, map[string]wrStatus{}
	for i, w := range waiters {
		v := boCmds[i].Val()
		w.card = planCard{id: w.id, stream: w.stream, score: w.score, waiting: true}
		if len(v) > 1 {
			w.record = w.card.setFields(v[1:])
		}
		w.blockedOn = strings.TrimSpace(wrStr(v, 0))
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
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: dependencies: %w", err)
	}
	for i, id := range taskIDs {
		v := taskCmds[i].Val()
		state, where, ok := wrStr(v, 0), wrStr(v, 1), wrStr(v, 2)
		switch {
		case state == "" && where == "":
			taskDeps[id] = wrUnknown
		case wrLanded(state, where, ok):
			taskDeps[id] = wrMet
		default:
			taskDeps[id] = wrUnmet
		}
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
		pipe = c.Pipeline()
		memCmds := make([]*redis.SliceCmd, len(members))
		for i, m := range members {
			memCmds[i] = pipe.HMGet(ctx, "task:"+m, "state", "where", "where_ok", "pr", "ref", "origin", "repo")
		}
		if len(members) > 0 {
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("waiting-resolve: members: %w", err)
			}
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
	}

	// The verdicts, per stream in rank order.
	// lines never grows past len(streams), so the pointers into it hold.
	lines := make([]ResolveLine, 0, len(streams))
	byStream := map[string]*ResolveLine{}
	var released []*wrWaiter
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
		if !met || !w.record {
			r.Still++
			continue
		}
		released = append(released, w)
	}

	// The consumers (#4059): a released task moves to ready only when the
	// deal, next in this pass, will take it; else it stays waiting.
	placed, err := d.place(ctx, released)
	if err != nil {
		return lines, err
	}
	type group struct {
		stream, why string
		ids         []string
	}
	var groups []*group
	groupOf := map[string]*group{}
	var notes []string
	for _, w := range released {
		r := byStream[w.stream]
		switch placed[w.id] {
		case NoConsumer:
			r.Still++
			r.NoConsumer = append(r.NoConsumer, w.id)
			if w.card.why != NoConsumer {
				notes = append(notes, w.id)
			}
			continue
		case NoSeat:
			r.Still++
			r.NoSeat = append(r.NoSeat, w.id)
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
	}
	// The no-consumer notes, one fenced call, written once per card.
	if len(notes) > 0 {
		if err := d.note(ctx, l, notes); errors.Is(err, ErrFenced) {
			return lines, err
		} else if err != nil {
			errs = append(errs, err.Error())
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

// place answers, for each released task, who takes it this tick (consumer.go):
// the plan walks every ws ready set and the released tasks together in the
// deal's order, so the seats the ready cards will take are taken first. It
// returns id -> NoConsumer, NoSeat, or the consumer.
func (d *WaitingResolve) place(ctx context.Context, released []*wrWaiter) (map[string]string, error) {
	out := map[string]string{}
	if len(released) == 0 {
		return out, nil
	}
	c := d.Client
	cs, err := readConsumers(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("waiting-resolve: %w", err)
	}
	p1 := c.Pipeline()
	order := p1.ZRange(ctx, "ws:order", 0, -1)
	names := p1.SMembers(ctx, "ws:names")
	if _, err := p1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: streams: %w", err)
	}
	streams := streamOrder(order.Val(), names.Val())
	p2 := c.Pipeline()
	ready := make([]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		ready[i] = p2.ZRangeWithScores(ctx, ws.Key(s, "ready"), 0, -1)
	}
	if _, err := p2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("waiting-resolve: ready sets: %w", err)
	}
	var readyCards []planCard
	for i, s := range streams {
		for _, z := range ready[i].Val() {
			id, _ := z.Member.(string)
			readyCards = append(readyCards, planCard{id: id, stream: s, score: z.Score})
		}
	}
	p3 := c.Pipeline()
	fields := make([]*redis.SliceCmd, len(readyCards))
	for i, k := range readyCards {
		fields[i] = p3.HMGet(ctx, "task:"+k.id, planFields...)
	}
	if len(readyCards) > 0 {
		if _, err := p3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("waiting-resolve: ready cards: %w", err)
		}
	}
	byStream := map[string][]planCard{}
	for i := range readyCards {
		if readyCards[i].setFields(fields[i].Val()) {
			byStream[readyCards[i].stream] = append(byStream[readyCards[i].stream], readyCards[i])
		}
	}
	for _, w := range released {
		byStream[w.stream] = append(byStream[w.stream], w.card)
	}
	for _, s := range streams {
		cards := byStream[s]
		planOrder(cards)
		for _, k := range cards {
			to, why := cs.place(k)
			if k.waiting {
				if to == "" {
					out[k.id] = why
				} else {
					out[k.id] = to
				}
			}
		}
	}
	return out, nil
}

// note writes why=no-consumer on the released tasks that have no consumer,
// through ns_ws_note (fenced; a card already carrying it writes nothing).
func (d *WaitingResolve) note(ctx context.Context, l *Lease, ids []string) error {
	if left, m := l.Remaining(), d.margin(l); left < m {
		return fmt.Errorf("%d no-consumer note(s) not written: LEASE-MARGIN: %s of the lease left, below the %s write margin",
			len(ids), left.Round(time.Millisecond), m)
	}
	args := []any{l.Token(), d.actor(), NoConsumer}
	for _, id := range ids {
		args = append(args, id)
	}
	reply, err := d.Client.FCall(ctx, "ns_ws_note", nil, args...).Slice()
	if err != nil {
		return fmt.Errorf("ns_ws_note: %w", err)
	}
	if len(reply) > 0 && fmt.Sprint(reply[0]) == "FENCED" {
		return fmt.Errorf("ns_ws_note: %w", ErrFenced)
	}
	if len(reply) < 4 || fmt.Sprint(reply[0]) != "NOTED" {
		return fmt.Errorf("ns_ws_note: %v", reply)
	}
	var bad []string
	for i := 4; i+1 < len(reply); i += 2 {
		bad = append(bad, fmt.Sprintf("%v refused: %v", reply[i], reply[i+1]))
	}
	if len(bad) > 0 {
		return fmt.Errorf("ns_ws_note: %s", strings.Join(bad, "; "))
	}
	return nil
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
