//go:build functional

package reconcile_test

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/redis/go-redis/v9"
)

// countingSource counts the deal pass's reads, so a test can tell a pass
// that dealt from a pass that never ran the deal (and its forge reads).
type countingSource struct {
	inner deal.Source
	mu    sync.Mutex
	n     int
}

func (s *countingSource) Read(ctx context.Context) (deal.Input, error) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return s.inner.Read(ctx)
}

func (s *countingSource) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// frozenClock never moves, so no sweep is ever due after the first deal:
// every deal after it was woken by an event.
type frozenClock struct{ t time.Time }

func (c frozenClock) Now() time.Time { return c.t }

func seedBench(t *testing.T, c *redis.Client, bench string, slots int) {
	t.Helper()
	ctx := context.Background()
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":desired", "slots", strconv.Itoa(slots))
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// seedSprint opens a control sprint with n queued pool cards
// card-00..card-<n-1> at priority i, in the shape the card push function
// writes them (fn_test.go seedFleet).
func seedSprint(t *testing.T, c *redis.Client, sprint string, order, n int) {
	t.Helper()
	ctx := context.Background()
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", sprint)
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: float64(order), Member: sprint})
	pipe.HSet(ctx, "s:"+sprint, "status", "open")
	pipe.HSet(ctx, "s:"+sprint+":policy", "share", "1", "backpressure_missing", "open")
	for i := 0; i < n; i++ {
		label := fmt.Sprintf("card-%02d", i)
		pipe.HSet(ctx, "s:"+sprint+":card:"+label, "state", "queued", "priority", strconv.Itoa(i),
			"attempt", "0", "retries", "0", "base_sha", "0123456789abcdef", "bench", "", "leg", "", "tier", "")
		pipe.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: float64(i), Member: label})
		pipe.SAdd(ctx, "s:"+sprint+":idx:card:queued", label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// childDone ends one dealt card the way a card transition function that frees
// a slot does (spec 2.2 cap:log, 5.2 slot freed): out of the bench's one
// working set (#3998: the card's id, s:<S>:card:<label>), state done, its
// receipt on s:<S>:log and one slot-freed event on cap:log, in one atomic
// call. member is <S>/<label>/<attempt>, as starting lists it.
func childDone(t *testing.T, c *redis.Client, bench, member string) {
	t.Helper()
	ctx := context.Background()
	parts := strings.Split(member, "/")
	if len(parts) != 3 {
		t.Fatalf("dealt member %q is not <S>/<label>/<attempt>", member)
	}
	S, label, attempt := parts[0], parts[1], parts[2]
	pipe := c.TxPipeline()
	pipe.ZRem(ctx, "bench:"+bench+":cards:working", "s:"+S+":card:"+label)
	pipe.HSet(ctx, "s:"+S+":card:"+label, "state", "done", "outcome", "DONE")
	pipe.SMove(ctx, "s:"+S+":idx:card:dealt", "s:"+S+":idx:card:done", label)
	pipe.ZRem(ctx, "s:"+S+":bench:"+bench+":queue", label)
	pipe.SAdd(ctx, "s:"+S+":bench:"+bench+":ended", label)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":log", Values: []string{
		"kind", "card end", "id", label, "from", "dealt", "to", "done", "attempt", attempt, "actor", "ctl-card"}})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", Values: []string{
		"kind", "slot-freed", "target", "bench:" + bench, "slots", "1", "actor", "ctl-card"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// leased is ZCARD bench:<b>:cards:working, the one lease ledger (spec 2.2,
// #3998), over every sprint.
func leased(t *testing.T, c *redis.Client, bench string) int {
	t.Helper()
	n, err := c.ZCard(context.Background(), "bench:"+bench+":cards:working").Result()
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// starting lists the bench's dealt sprint cards as <S>/<label>/<attempt>:
// the sprint card ids in its working set, each with its record's attempt.
func starting(t *testing.T, c *redis.Client, bench string) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := c.ZRange(ctx, "bench:"+bench+":cards:working", 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	var m []string
	for _, id := range ids {
		rest, ok := strings.CutPrefix(id, "s:")
		S, label, ok2 := strings.Cut(rest, ":card:")
		if !ok || !ok2 {
			continue
		}
		attempt, err := c.HGet(ctx, id, "attempt").Result()
		if err != nil {
			t.Fatal(err)
		}
		m = append(m, S+"/"+label+"/"+attempt)
	}
	sort.Strings(m)
	return m
}

func openCards(t *testing.T, c *redis.Client, sprints ...string) int {
	t.Helper()
	n := 0
	for _, s := range sprints {
		z, err := c.ZCard(context.Background(), "s:"+s+":pool").Result()
		if err != nil {
			t.Fatal(err)
		}
		n += int(z)
	}
	return n
}

func mustPass(t *testing.T, lp *reconcile.Loop) reconcile.PassResult {
	t.Helper()
	res, err := lp.Pass(context.Background())
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if res.Err != "" {
		t.Fatalf("pass recorded an error: %s", res.Err)
	}
	return res
}

// TestRefillOnChildCompletion: a slot freed by a finishing child is refilled
// in the same reconciler pass, woken by the event and not by the sweep, and
// working = min(slots, working + open) after every pass.
func TestRefillOnChildCompletion(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const bench, S, slots = "ctl-refill", "control-29350c01", 4
	seedBench(t, c, bench, slots)
	seedSprint(t, c, S, 1, 6)

	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatal(err)
	}
	dialer := &fakeDialer{}
	src := &countingSource{inner: deal.RedisSource{Client: c}}
	var wakes []reconcile.Wake
	rf := &reconcile.Refill{
		Client:    c,
		Deal:      &deal.Pass{Source: src, Dialer: dialer},
		Now:       frozenClock{time.Unix(1_800_000_000, 0)}.Now,
		AfterDeal: func(w reconcile.Wake, _ deal.Result) { wakes = append(wakes, w) },
	}
	lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{rf.Run}}

	// Pass 1, a new instance: the restart deal fills the bench over one
	// session. working = min(4, 0 + 6) = 4, open 2.
	p := mustPass(t, lp)
	if got := leased(t, c, bench); got != slots {
		t.Fatalf("pass 1: working %d, want min(slots 4, 0 + open 6) = 4", got)
	}
	if got := openCards(t, c, S); got != 2 {
		t.Fatalf("pass 1: open %d, want 2", got)
	}
	if p.Counts.Dealt != 4 {
		t.Fatalf("pass 1: counts dealt %d, want 4", p.Counts.Dealt)
	}
	if n, _ := dialer.count(bench); n != 1 {
		t.Fatalf("pass 1: %d ssh sessions to %s, want 1 for the whole batch", n, bench)
	}
	if len(wakes) != 1 || !wakes[0].Restart {
		t.Fatalf("pass 1: wakes %+v, want one restart deal", wakes)
	}

	// Pass 2, nothing happened: the reconciler's own `card deal` receipts do
	// not wake it, the sweep is not due, and the deal (and its forge reads)
	// does not run.
	reads := src.reads()
	mustPass(t, lp)
	if src.reads() != reads {
		t.Fatalf("pass 2 with no event ran the deal pass (%d reads, want %d): a self-wake loop", src.reads(), reads)
	}

	// One child ends: its slot is refilled in the very next pass.
	// working = min(4, 3 + 2) = 4, open 1.
	childDone(t, c, bench, starting(t, c, bench)[0])
	if got := leased(t, c, bench); got != 3 {
		t.Fatalf("after one child ended: working %d, want 3", got)
	}
	p = mustPass(t, lp)
	if got := leased(t, c, bench); got != slots {
		t.Fatalf("pass 3: working %d, want min(slots 4, 3 + open 2) = 4", got)
	}
	if got := openCards(t, c, S); got != 1 {
		t.Fatalf("pass 3: open %d, want 1", got)
	}
	if p.Counts.Dealt != 1 {
		t.Fatalf("pass 3: counts dealt %d, want 1", p.Counts.Dealt)
	}

	// Three children end: working = min(4, 1 + 1) = 2, open 0.
	for _, m := range starting(t, c, bench)[:3] {
		childDone(t, c, bench, m)
	}
	p = mustPass(t, lp)
	if got := leased(t, c, bench); got != 2 {
		t.Fatalf("pass 4: working %d, want min(slots 4, 1 + open 1) = 2", got)
	}
	if got := openCards(t, c, S); got != 0 {
		t.Fatalf("pass 4: open %d, want 0", got)
	}
	if p.Counts.Dealt != 1 {
		t.Fatalf("pass 4: counts dealt %d, want 1", p.Counts.Dealt)
	}

	// Every refill after the first was woken by the completion events, never
	// by the sweep (the clock never moved), and the pass record carries it.
	if len(wakes) != 3 {
		t.Fatalf("deal passes %d, want 3 (restart, then one per completion pass): %+v", len(wakes), wakes)
	}
	for i, w := range wakes[1:] {
		if w.Sweep || w.Restart || w.Deal == 0 {
			t.Fatalf("refill %d woke by %q, want completion events only", i+2, w.Why())
		}
	}
	dealt, err := c.HGet(ctx, reconcile.ProcKey, "dealt").Result()
	if err != nil || dealt != "1" {
		t.Fatalf("proc:reconciler dealt = %q (%v), want 1 from the last pass", dealt, err)
	}
	if n, lines := dialer.count(bench); n != 3 || lines != 6 {
		t.Fatalf("sessions %d lines %d, want 3 sessions (one per deal pass) carrying all 6 cards", n, lines)
	}
}

// TestControl05TwoSprintsOneBench (#2756 control 5): the same card labels in
// two control sprints sharing one fixture bench do not collide, and leased
// never exceeds desired, with two refills racing every pass.
func TestControl05TwoSprintsOneBench(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const bench, slots, cards = "ctl-shared", 4, 6
	sprints := []string{"control-05aaaaaa", "control-05bbbbbb"}
	seedBench(t, c, bench, slots)
	for i, s := range sprints {
		seedSprint(t, c, s, i+1, cards)
	}
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatal(err)
	}
	dialer := &fakeDialer{}
	clock := frozenClock{time.Unix(1_800_000_000, 0)}
	var loops []*reconcile.Loop
	for _, name := range []string{"ctl-refill-a", "ctl-refill-b"} {
		rf := &reconcile.Refill{
			Client:   c,
			Deal:     &deal.Pass{Dialer: dialer},
			Consumer: name,
			Now:      clock.Now,
		}
		loops = append(loops, &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{rf.Run}})
	}
	// race runs both refills' passes at once and checks the ceiling after.
	race := func(step string) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make([]error, len(loops))
		for i, lp := range loops {
			wg.Add(1)
			go func(i int, lp *reconcile.Loop) {
				defer wg.Done()
				res, err := lp.Pass(ctx)
				if err == nil && res.Err != "" {
					err = fmt.Errorf("%s", res.Err)
				}
				errs[i] = err
			}(i, lp)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("%s: %v", step, err)
			}
		}
		if got := leased(t, c, bench); got > slots {
			t.Fatalf("%s: leased %d above desired %d on the shared bench", step, got, slots)
		}
	}

	race("pass 1")
	if got := leased(t, c, bench); got != slots {
		t.Fatalf("pass 1: leased %d, want %d (the bench full, not twice full)", got, slots)
	}
	// No collision: the same label is dealt in both sprints as two identities.
	members := starting(t, c, bench)
	have := map[string]bool{}
	for _, m := range members {
		have[m] = true
	}
	for _, s := range sprints {
		if !have[s+"/card-00/1"] {
			t.Fatalf("pass 1: %s/card-00/1 not starting on %s (members %v); the share split 2/2", s, bench, members)
		}
	}

	// Children end two at a time, one per sprint where it can; both refills
	// race every pass; after each, working = min(slots, working + open).
	for round := 1; ; round++ {
		members = starting(t, c, bench)
		if len(members) == 0 {
			break
		}
		if round > 3*cards {
			t.Fatalf("round %d: still %d starting", round, len(members))
		}
		ended := 0
		for _, s := range sprints {
			for _, m := range members {
				if strings.HasPrefix(m, s+"/") {
					childDone(t, c, bench, m)
					ended++
					break
				}
			}
		}
		working := len(members) - ended
		open := openCards(t, c, sprints...)
		race(fmt.Sprintf("round %d", round))
		if got, want := leased(t, c, bench), min(slots, working+open); got != want {
			t.Fatalf("round %d: working %d, want min(slots %d, working %d + open %d) = %d", round, got, slots, working, open, want)
		}
	}

	// Every card of both sprints ran exactly once on its own identity.
	ids := map[string]bool{}
	for _, s := range sprints {
		for i := 0; i < cards; i++ {
			label := fmt.Sprintf("card-%02d", i)
			v, err := c.HMGet(ctx, "s:"+s+":card:"+label, "state", "attempt", "identity").Result()
			if err != nil {
				t.Fatal(err)
			}
			if v[0] != "done" || v[1] != "1" {
				t.Fatalf("%s/%s: state %v attempt %v, want done at attempt 1 (dealt once)", s, label, v[0], v[1])
			}
			id, _ := v[2].(string)
			if !strings.HasPrefix(id, s+"/"+label+"/") || ids[id] {
				t.Fatalf("%s/%s: identity %q collides or names another sprint", s, label, id)
			}
			ids[id] = true
		}
	}
	if _, lines := dialer.count(bench); lines != 2*cards {
		t.Fatalf("launch lines %d, want %d: a card was launched twice or never", lines, 2*cards)
	}
}

// TestRefillBlockWakesOnEvent: with Block set, a pass that has nothing to do
// waits on the streams, and a completion event wakes it to deal in that same
// pass. The assertion is the event's deal, never the clock.
func TestRefillBlockWakesOnEvent(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const bench, S = "ctl-block", "control-2935b10c"
	seedBench(t, c, bench, 2)
	seedSprint(t, c, S, 1, 3)
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatal(err)
	}
	rf := &reconcile.Refill{
		Client: c,
		Deal:   &deal.Pass{Dialer: &fakeDialer{}},
		Block:  30 * time.Second,
		Now:    frozenClock{time.Unix(1_800_000_000, 0)}.Now,
	}
	if cnt, err := rf.Run(ctx, l); err != nil || cnt.Dealt != 2 {
		t.Fatalf("restart refill: dealt %d err %v, want 2", cnt.Dealt, err)
	}
	// The deal writes its own no-wake receipts. Drain them without blocking so
	// the next pass has no event available before it enters XREADGROUP.
	rf.Block = 0
	if cnt, err := rf.Run(ctx, l); err != nil || cnt.Dealt != 0 {
		t.Fatalf("drain refill receipts: dealt %d err %v, want 0", cnt.Dealt, err)
	}
	rf.Block = 30 * time.Second
	type out struct {
		c   reconcile.Counts
		err error
	}
	done := make(chan out, 1)
	member := starting(t, c, bench)[0]
	go func() {
		cnt, err := rf.Run(ctx, l)
		done <- out{cnt, err}
	}()
	waitBlockedXReadGroup(t, c)
	childDone(t, c, bench, member)
	got := <-done
	if got.err != nil || got.c.Dealt != 1 {
		t.Fatalf("blocked refill after one completion: dealt %d err %v, want 1", got.c.Dealt, got.err)
	}
	if n := leased(t, c, bench); n != 2 {
		t.Fatalf("working %d, want min(slots 2, 1 + open 1) = 2", n)
	}
}

// waitBlockedXReadGroup proves the refill connection is inside Redis's
// blocking XREADGROUP before the test writes the event that must wake it.
// CLIENT LIST is an observed server state, not a scheduler or sleep proxy.
func waitBlockedXReadGroup(t *testing.T, c *redis.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var last string
	for time.Now().Before(deadline) {
		list, err := c.ClientList(context.Background()).Result()
		if err != nil {
			t.Fatalf("client list: %v", err)
		}
		last = list
		for _, line := range strings.Split(list, "\n") {
			var blocked, reading bool
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "flags=") && strings.Contains(strings.TrimPrefix(field, "flags="), "b") {
					blocked = true
				}
				if field == "cmd=xreadgroup" {
					reading = true
				}
			}
			if blocked && reading {
				return
			}
		}
		<-tick.C
	}
	t.Fatalf("refill never blocked in XREADGROUP; CLIENT LIST:\n%s", last)
}

// routeEvent writes one task event on the sprint log: a route wake.
func routeEvent(t *testing.T, c *redis.Client, sprint, id string) {
	t.Helper()
	if err := c.XAdd(context.Background(), &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: []string{
		"kind", "task push", "id", id, "actor", "ctl-rowan"}}).Err(); err != nil {
		t.Fatal(err)
	}
}

// groupPending is the reconciler group's pending count on one stream.
func groupPending(t *testing.T, c *redis.Client, stream string) int64 {
	t.Helper()
	p, err := c.XPending(context.Background(), stream, reconcile.Group).Result()
	if err != nil {
		t.Fatal(err)
	}
	return p.Count
}

// TestRefillFailedRouteReplayedOnceDealt (Stella's hold at 30435ad2): a route
// event whose Route fails is not acknowledged, on the no-deal branch or the
// deal branch; the next pass replays it to Route and acknowledges it only
// then; and the replay never wakes the deal again, so the deal the event rode
// in with ran exactly once.
func TestRefillFailedRouteReplayedOnceDealt(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const bench, S, slots = "ctl-route", "control-2935f00d", 2
	seedBench(t, c, bench, slots)
	seedSprint(t, c, S, 1, 4)
	sprintLog := "s:" + S + ":log"
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatal(err)
	}
	dialer := &fakeDialer{}
	src := &countingSource{inner: deal.RedisSource{Client: c}}
	var routes []reconcile.Wake
	fail := false
	rf := &reconcile.Refill{
		Client: c,
		Deal:   &deal.Pass{Source: src, Dialer: dialer},
		Now:    frozenClock{time.Unix(1_800_000_000, 0)}.Now,
		Route: func(_ context.Context, _ *reconcile.Lease, w reconcile.Wake) (int, error) {
			routes = append(routes, w)
			if fail {
				return 0, fmt.Errorf("fixture route refused")
			}
			return w.Route + w.RouteReplay, nil
		},
	}
	lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{rf.Run}}
	pass := func(wantErr bool) reconcile.PassResult {
		t.Helper()
		res, err := lp.Pass(ctx)
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		if got := res.Err != ""; got != wantErr {
			t.Fatalf("pass recorded err %q, want an error: %v", res.Err, wantErr)
		}
		return res
	}

	pass(false) // restart: deals the bench full
	if n := leased(t, c, bench); n != slots {
		t.Fatalf("restart: working %d, want %d", n, slots)
	}

	// No-deal branch: one route event, Route fails. Not acknowledged.
	fail = true
	routeEvent(t, c, S, "task-a")
	reads := src.reads()
	pass(true)
	if len(routes) != 1 || routes[0].Route != 1 {
		t.Fatalf("route calls %+v, want one with route=1", routes)
	}
	if src.reads() != reads {
		t.Fatal("a route-only pass ran the deal")
	}
	if p := groupPending(t, c, sprintLog); p != 1 {
		t.Fatalf("after a failed route: %d pending on %s, want 1 (the failed route event kept)", p, sprintLog)
	}

	// Next pass, no new event: the failed route is replayed and, routed,
	// acknowledged; the replay does not wake the deal.
	fail = false
	pass(false)
	if len(routes) != 2 || routes[1].RouteReplay != 1 || routes[1].Route != 0 {
		t.Fatalf("route calls %+v, want the second a replay of 1", routes)
	}
	if src.reads() != reads {
		t.Fatal("the route replay ran the deal")
	}
	if p := groupPending(t, c, sprintLog); p != 0 {
		t.Fatalf("after the replay routed: %d pending, want 0", p)
	}
	pass(false)
	if len(routes) != 2 {
		t.Fatalf("an acknowledged route was replayed again: %+v", routes)
	}

	// Deal branch: a route event and a child's end in one pass, Route fails.
	// The deal runs once and its wakes are acknowledged; the route event is
	// kept. The next pass replays the route only: nothing is dealt twice.
	fail = true
	routeEvent(t, c, S, "task-b")
	childDone(t, c, bench, starting(t, c, bench)[0])
	_, launchedBefore := dialer.count(bench)
	p := pass(true)
	if p.Counts.Dealt != 1 || src.reads() != reads+1 {
		t.Fatalf("deal-branch pass: dealt %d, deal reads %d, want 1 and 1", p.Counts.Dealt, src.reads()-reads)
	}
	if n := groupPending(t, c, "cap:log"); n != 0 {
		t.Fatalf("the deal's cap:log wake still pending (%d) after the deal succeeded", n)
	}
	if n := groupPending(t, c, sprintLog); n != 1 {
		t.Fatalf("after a failed route on the deal branch: %d pending on %s, want 1 (the route event)", n, sprintLog)
	}
	fail = false
	p = pass(false)
	if last := routes[len(routes)-1]; last.RouteReplay != 1 {
		t.Fatalf("replay %+v, want route-replay=1", last)
	}
	if p.Counts.Dealt != 0 || src.reads() != reads+1 {
		t.Fatalf("the replay pass dealt %d (deal reads %d): the card must be dealt exactly once", p.Counts.Dealt, src.reads()-reads)
	}
	if n := groupPending(t, c, sprintLog); n != 0 {
		t.Fatalf("after the replay routed: %d pending on %s, want 0", n, sprintLog)
	}
	_, lines := dialer.count(bench)
	if lines != launchedBefore+1 || lines != slots+1 {
		t.Fatalf("launch lines %d, want %d: every card dealt exactly once", lines, slots+1)
	}
	seen := map[string]bool{}
	for _, ln := range dialer.lines[bench] {
		f := strings.Fields(ln)
		if len(f) < 3 || f[2] != "1" || seen[f[1]] {
			t.Fatalf("launch line %q: a card dealt twice or past attempt 1: %v", ln, dialer.lines[bench])
		}
		seen[f[1]] = true
	}
}
