package reconcile_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// The rulings of nova-tools #4059 (Glenn 2026-09-25 1:40 PM ET) on the
// reconciler's two duties as production runs them: waiting-resolve, then the
// deal, in one pass.

// TestNoConsumerCardLogsOnceInTenTicks is #4059's DONE-WHEN (4): a card whose
// dependency has landed but that has no consumer (no live friend, no route)
// stays in waiting across ten ticks of waiting-resolve + deal, carries
// why=no-consumer, and ws:log holds exactly ONE entry for it: the note. Before
// the fix the resolve moved it to ready and the deal moved it back, a move a
// tick. When a friend comes live, the next tick resolves it and deals it.
func TestNoConsumerCardLogsOnceInTenTicks(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "A", "landed", 1000)
	wrTask(t, c, "build-3041", "waiting", 2000, "blocked_on", "task:A")

	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release(ctx) })
	var out bytes.Buffer
	resolve := &reconcile.WaitingResolve{Client: c, Out: &out}
	deal := &reconcile.FriendDeal{Client: c, Out: &out}
	lp := &reconcile.Loop{Lease: lease, Duties: []reconcile.Duty{resolve.Run, deal.Run}}
	for i := 0; i < 10; i++ {
		p := mustPass(t, lp)
		if p.Counts.Routed != 0 || p.Counts.Dealt != 0 {
			t.Fatalf("tick %d moved: routed %d dealt %d", i+1, p.Counts.Routed, p.Counts.Dealt)
		}
	}
	entries, err := c.XRange(ctx, "ws:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ws:log has %d entries across ten ticks, want exactly 1 (the note): %v", len(entries), entries)
	}
	e := entries[0].Values
	if e["id"] != "build-3041" || e["from"] != "waiting" || e["to"] != "waiting" || e["why"] != reconcile.NoConsumer {
		t.Fatalf("the one ws:log entry %v, want build-3041 waiting -> waiting why=%s", e, reconcile.NoConsumer)
	}
	h, _ := c.HMGet(ctx, "task:build-3041", "state", "why").Result()
	if h[0] != "waiting" || h[1] != reconcile.NoConsumer {
		t.Fatalf("build-3041 state/why %v, want waiting/%s", h, reconcile.NoConsumer)
	}
	if n := len(wrMembers(t, c, "ready")); n != 0 {
		t.Fatalf("ready holds %d, want 0", n)
	}
	want := `RESOLVE stream="nova-sprint + merge + bus" ready=0 still=1 on=- unknown=- noconsumer=build-3041 noseat=-` + "\n"
	if out.String() != want {
		t.Fatalf("receipts across ten ticks\n%q\nwant the one line\n%q", out.String(), want)
	}

	// A consumer appears: the next tick resolves the card and deals it.
	wrFriend(t, c, "emma", 2)
	out.Reset()
	p := mustPass(t, lp)
	if p.Counts.Routed != 1 || p.Counts.Dealt != 1 {
		t.Fatalf("with emma live: routed %d dealt %d, want 1 and 1; receipts %q", p.Counts.Routed, p.Counts.Dealt, out.String())
	}
	if h, _ := c.HMGet(ctx, "task:build-3041", "state", "owner").Result(); h[0] != "working" || h[1] != "emma" {
		t.Fatalf("build-3041 state/owner %v, want working/emma", h)
	}
	if err := ws.Check(ctx, c, []string{"A", "build-3041"}); err != nil {
		t.Fatalf("invariant: %v", err)
	}
}

// TestResolvedCardIsDealtTheSameTick is #4059's ruling (2) and DONE-WHEN (1):
// one pass of waiting-resolve + deal takes each released card to working
// (friend queue up to its seats, else the swarm by route) and leaves ready
// empty; a card whose only friend is full stays in waiting (no-seat) and
// nothing is written for it.
func TestResolvedCardIsDealtTheSameTick(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "A", "landed", 1000)
	wrTask(t, c, "B", "waiting", 2000, "blocked_on", "task:A")                       // any friend
	wrTask(t, c, "C", "waiting", 2001, "blocked_on", "task:A", "route", "anthropic") // swarm by route
	wrTask(t, c, "D", "waiting", 2002, "blocked_on", "task:A", "who", "only emma")   // emma, full
	wrTask(t, c, "E", "waiting", 2003, "blocked_on", "task:A", "who", "swarm")       // the swarm only
	wrFriend(t, c, "emma", 1)

	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release(ctx) })
	var out bytes.Buffer
	resolve := &reconcile.WaitingResolve{Client: c, Out: &out}
	deal := &reconcile.FriendDeal{Client: c, Out: &out}
	lp := &reconcile.Loop{Lease: lease, Duties: []reconcile.Duty{resolve.Run, deal.Run}}
	p := mustPass(t, lp)
	if p.Counts.Routed != 3 || p.Counts.Dealt != 3 {
		t.Fatalf("routed %d dealt %d, want 3 and 3; receipts:\n%s", p.Counts.Routed, p.Counts.Dealt, out.String())
	}
	for id, owner := range map[string]string{"B": "emma", "C": reconcile.Swarm, "E": reconcile.Swarm} {
		h, _ := c.HMGet(ctx, "task:"+id, "state", "owner").Result()
		if h[0] != "working" || h[1] != owner {
			t.Errorf("task %s state/owner %v, want working/%s", id, h, owner)
		}
	}
	if n := len(wrMembers(t, c, "ready")); n != 0 {
		t.Fatalf("ready holds %d after the pass, want 0: ready is never a resting state", n)
	}
	if h, _ := c.HMGet(ctx, "task:D", "state", "why").Result(); h[0] != "waiting" || h[1] != nil {
		t.Fatalf("task D state/why %v, want waiting with no why (no-seat writes nothing)", h)
	}
	if got := strings.Join(mustMembers(t, c, "friend:emma:cards:working"), ","); got != "B" {
		t.Fatalf("friend:emma:cards:working %q, want B", got)
	}
	if got := strings.Join(mustMembers(t, c, "friend:swarm:cards:working"), ","); got != "C,E" {
		t.Fatalf("friend:swarm:cards:working %q, want C,E", got)
	}
	if n, _ := c.XLen(ctx, "ws:log").Result(); n != 6 {
		t.Fatalf("ws:log %d entries, want 6 (three cards, waiting -> ready -> working)", n)
	}
	for _, want := range []string{"noseat=D", "DEAL emma took=1 open=1", "DEAL swarm took=2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("receipts %q, want %q", out.String(), want)
		}
	}
	if err := ws.Check(ctx, c, []string{"A", "B", "C", "D", "E"}); err != nil {
		t.Fatalf("invariant: %v", err)
	}

	// B lands: the move takes it out of emma's working view, her seat opens,
	// and the next pass releases D to her.
	if _, err := ws.Move(ctx, c, "B", "merging", "test", "pr"); err != nil {
		t.Fatal(err)
	}
	// landed needs the merge sha (the one task move, #3778): ns_ws_move's
	// fifth argument.
	if r, err := c.FCall(ctx, ws.FnMove, nil, "B", "landed", "test", "pr", "0123456789abcdef0123456789abcdef01234567").StringSlice(); err != nil || len(r) == 0 || r[0] != "MOVED" {
		t.Fatalf("B merging -> landed: %v %v", r, err)
	}
	if n, _ := c.ZCard(ctx, "friend:emma:cards:working").Result(); n != 0 {
		t.Fatalf("friend:emma:cards:working %d after B left working, want 0", n)
	}
	p = mustPass(t, lp)
	if p.Counts.Routed != 1 || p.Counts.Dealt != 1 {
		t.Fatalf("after B landed: routed %d dealt %d, want D to emma", p.Counts.Routed, p.Counts.Dealt)
	}
	if h, _ := c.HMGet(ctx, "task:D", "state", "owner").Result(); h[0] != "working" || h[1] != "emma" {
		t.Fatalf("task D state/owner %v, want working/emma", h)
	}
}

// TestNoMoveTakesReadyBackToWaiting is ruling (1) at the duties: no
// function in the library the reconciler calls returns a card
// (ns_deal_return is gone), and a pass of waiting-resolve + deal leaves a
// ready card that has no consumer in ready, never back in waiting. The card
// model's graph keeps ready -> waiting for the `task block` verb (#3778); a
// duty never makes it (internal/ci's dutymoves class test).
func TestNoMoveTakesReadyBackToWaiting(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	wrTask(t, c, "R", "ready", 3000)
	if err := c.FCall(ctx, "ns_deal_return", nil, "x", "test", "no-consumer", "R").Err(); err == nil || !strings.Contains(err.Error(), "Function not found") {
		t.Fatalf("ns_deal_return: %v, want Function not found", err)
	}
	lease, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release(ctx) })
	var out bytes.Buffer
	resolve := &reconcile.WaitingResolve{Client: c, Out: &out}
	deal := &reconcile.FriendDeal{Client: c, Out: &out}
	lp := &reconcile.Loop{Lease: lease, Duties: []reconcile.Duty{resolve.Run, deal.Run}}
	for i := 0; i < 3; i++ {
		mustPass(t, lp)
	}
	if s, _ := c.HGet(ctx, "task:R", "state").Result(); s != "ready" {
		t.Fatalf("task R state %q, want ready", s)
	}
	if n, _ := c.XLen(ctx, "ws:log").Result(); n != 0 {
		t.Fatalf("ws:log %d entries, want 0: no duty moved R", n)
	}
}

func mustMembers(t *testing.T, c *redis.Client, key string) []string {
	t.Helper()
	m, err := c.ZRange(context.Background(), key, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}
