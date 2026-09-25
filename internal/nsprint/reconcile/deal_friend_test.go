package reconcile_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The friend deal duty (nova-tools #3873) on a throwaway redis-server.

type dealFixture struct {
	ctx context.Context
	c   *redis.Client
	l   *reconcile.Lease
}

func newDealFixture(t *testing.T) *dealFixture {
	t.Helper()
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	return &dealFixture{ctx: ctx, c: c, l: l}
}

// friend registers f with slots and working ids; live writes a beat now.
func (f *dealFixture) friend(t *testing.T, name string, slots int, live bool, working ...string) {
	t.Helper()
	pipe := f.c.TxPipeline()
	pipe.SAdd(f.ctx, "friends", name)
	pipe.Set(f.ctx, "friend:"+name+":slots", slots, 0)
	if live {
		pipe.HSet(f.ctx, "friend:"+name, "at", time.Now().UTC().Format(time.RFC3339))
	}
	for i, id := range working {
		pipe.ZAdd(f.ctx, "friend:"+name+":cards:working", redis.Z{Score: float64(i + 1), Member: id})
	}
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

// cardEpoch is the fixtures' created_at origin: a real epoch-ms value, so
// the one move (#3778), which reads a created_at under 1e11 as seconds,
// keeps each card's age as its score.
const cardEpoch int64 = 1_758_800_000_000

// ready writes one ready task on stream with age (created_at ms after
// cardEpoch) and extra fields.
func (f *dealFixture) ready(t *testing.T, id, stream string, age int64, extra ...any) {
	t.Helper()
	age += cardEpoch
	pipe := f.c.TxPipeline()
	pipe.SAdd(f.ctx, "ws:names", stream)
	pipe.HSet(f.ctx, "task:"+id, append([]any{"stream", stream, "state", "ready", "created_at", age, "kind", "build"}, extra...)...)
	pipe.ZAdd(f.ctx, "ws:"+stream+":ready", redis.Z{Score: float64(age), Member: id})
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *dealFixture) zcard(t *testing.T, key string) int64 {
	t.Helper()
	n, err := f.c.ZCard(f.ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *dealFixture) members(t *testing.T, key string) []string {
	t.Helper()
	m, err := f.c.ZRange(f.ctx, key, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDealFillsEveryOpenSlotInOneTick is #3873's DONE-WHEN: two friends
// (slots 4 and 2, one working each) and 20 ready cards across two streams,
// plus one card only stella (down) may take. One pass moves 3 + 1 cards to
// the friends' working sets, oldest first by ws:order rank (the second
// stream's cards are older, and still wait for the first stream's), the
// ready ZCARDs drop by the 4 dealt, the stella card goes back to waiting with
// why=no-consumer, and a second pass moves nothing.
func TestDealFillsEveryOpenSlotInOneTick(t *testing.T) {
	f := newDealFixture(t)
	const s1, s2 = "nova-sprint", "swarm: cards"
	must(t, f.c.ZAdd(f.ctx, "ws:order", redis.Z{Score: 1, Member: s1}, redis.Z{Score: 2, Member: s2}).Err())
	f.friend(t, "emma", 4, true, "pre-emma")
	f.friend(t, "rowan", 2, true, "pre-rowan")
	f.friend(t, "stella", 4, false)
	for i := 0; i < 10; i++ {
		f.ready(t, fmt.Sprintf("a%02d", i), s1, int64(2000+i))
		f.ready(t, fmt.Sprintf("b%02d", i), s2, int64(1000+i))
	}
	f.ready(t, "only-stella", s1, 1500, "who", "only stella")

	var out bytes.Buffer
	duty := &reconcile.FriendDeal{Client: f.c, Out: &out}
	lp := &reconcile.Loop{Lease: f.l, Duties: []reconcile.Duty{duty.Run}}
	p := mustPass(t, lp)
	if p.Counts.Dealt != 4 {
		t.Fatalf("dealt %d, want 4 (3 open on emma + 1 on rowan); receipts:\n%s", p.Counts.Dealt, out.String())
	}
	emma := f.members(t, "friend:emma:cards:working")
	rowan := f.members(t, "friend:rowan:cards:working")
	if len(emma) != 4 || len(rowan) != 2 {
		t.Fatalf("working emma %v rowan %v, want 1+3 and 1+1", emma, rowan)
	}
	got := map[string]string{}
	for _, id := range append(emma, rowan...) {
		if !strings.HasPrefix(id, "pre-") {
			got[id] = "?"
		}
	}
	for _, id := range []string{"a00", "a01", "a02", "a03"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("dealt %v, want the four oldest of the rank-1 stream a00..a03", got)
		}
	}
	for _, id := range []string{"a00", "a01", "a02"} {
		h, _ := f.c.HMGet(f.ctx, "task:"+id, "state", "owner").Result()
		if h[0] != "working" || h[1] != "emma" {
			t.Fatalf("task %s state/owner %v, want working/emma", id, h)
		}
	}
	if h, _ := f.c.HMGet(f.ctx, "task:a03", "state", "owner").Result(); h[0] != "working" || h[1] != "rowan" {
		t.Fatalf("task a03 state/owner %v, want working/rowan", h)
	}
	if n := f.zcard(t, "ws:"+s1+":ready"); n != 6 {
		t.Fatalf("ws:%s:ready %d, want 11 - 4 dealt - 1 returned = 6", s1, n)
	}
	if n := f.zcard(t, "ws:"+s2+":ready"); n != 10 {
		t.Fatalf("ws:%s:ready %d, want 10: rank 2 waits for rank 1", s2, n)
	}
	if n := f.zcard(t, "ws:"+s1+":working"); n != 4 {
		t.Fatalf("ws:%s:working %d, want 4", s1, n)
	}
	if sc, err := f.c.ZScore(f.ctx, "ws:"+s1+":working", "a00").Result(); err != nil || sc != float64(cardEpoch+2000) {
		t.Fatalf("a00 working score %v %v, want its created_at 2000", sc, err)
	}
	h, _ := f.c.HMGet(f.ctx, "task:only-stella", "state", "why").Result()
	if h[0] != "waiting" || h[1] != reconcile.NoConsumer {
		t.Fatalf("only-stella state/why %v, want waiting/%s", h, reconcile.NoConsumer)
	}
	if sc, err := f.c.ZScore(f.ctx, "ws:"+s1+":waiting", "only-stella").Result(); err != nil || sc != float64(cardEpoch+1500) {
		t.Fatalf("only-stella waiting score %v %v, want 1500", sc, err)
	}
	for _, want := range []string{"DEAL emma took=3 open=3 from=" + s1, "DEAL rowan took=1 open=1 from=" + s1,
		"DEAL waiting returned=1 why=no-consumer ids=only-stella"} {
		if !strings.Contains(out.String(), want+"\n") {
			t.Fatalf("receipts %q, want the line %q", out.String(), want)
		}
	}
	if n, _ := f.c.XLen(f.ctx, "ws:log").Result(); n != 5 {
		t.Fatalf("ws:log %d entries, want one per move (5)", n)
	}

	out.Reset()
	p = mustPass(t, lp)
	if p.Counts.Dealt != 0 || out.Len() != 0 {
		t.Fatalf("second pass dealt %d, receipts %q; want nothing", p.Counts.Dealt, out.String())
	}
	if n := f.zcard(t, "ws:"+s1+":ready") + f.zcard(t, "ws:"+s2+":ready"); n != 16 {
		t.Fatalf("ready %d after the second pass, want 16", n)
	}
}

// TestDealHonoursWhoKindAndOwner: WHO except sends a card past a friend, a
// read card never reaches its author, an owned card and a swarm kind are
// left in ready, and a fenced token moves nothing.
func TestDealHonoursWhoKindAndOwner(t *testing.T) {
	f := newDealFixture(t)
	const s = "nova-sprint"
	must(t, f.c.ZAdd(f.ctx, "ws:order", redis.Z{Score: 1, Member: s}).Err())
	must(t, f.c.HSet(f.ctx, "cfg:deal:kind", "swarm-build", "swarm").Err())
	f.friend(t, "emma", 5, true)
	f.friend(t, "rowan", 1, true)
	f.ready(t, "not-emma", s, 1, "who", "except emma")
	f.ready(t, "owned", s, 2, "owner", "stella")
	f.ready(t, "swarmed", s, 3, "kind", "swarm-build")
	f.ready(t, "read-1", s, 4, "kind", "read", "author", "emma")
	f.ready(t, "plain", s, 5)

	if _, err := (&reconcile.FriendDeal{Client: f.c}).Pass(f.ctx, "not-the-token"); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("pass with a stale token: %v, want fenced", err)
	}
	if n := f.zcard(t, "ws:"+s+":ready"); n != 5 {
		t.Fatalf("a fenced pass moved cards: ready %d", n)
	}
	res, err := (&reconcile.FriendDeal{Client: f.c}).Pass(f.ctx, f.l.Token())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Took["rowan"], ","); got != "not-emma" {
		t.Fatalf("rowan took %q, want not-emma (WHO except emma)", got)
	}
	if got := strings.Join(res.Took["emma"], ","); got != "plain" {
		t.Fatalf("emma took %q, want plain only (read-1 is hers, rowan is full)", got)
	}
	if got := f.members(t, "ws:"+s+":ready"); strings.Join(got, ",") != "owned,swarmed,read-1" {
		t.Fatalf("ready %v, want owned,swarmed,read-1 left", got)
	}
	if len(res.Returned) != 0 {
		t.Fatalf("returned %v, want none: every card has a live consumer", res.Returned)
	}
}

func TestWhoAdmits(t *testing.T) {
	for _, c := range []struct {
		who, f string
		want   bool
	}{
		{"", "emma", true}, {"any", "emma", true}, {"ANY", "emma", true},
		{"only stella", "emma", false}, {"only stella,emma", "Emma", true},
		{"except emma", "emma", false}, {"except emma, rowan", "stella", true},
		{"nobody", "emma", false},
	} {
		if got := reconcile.WhoAdmits(c.who, c.f); got != c.want {
			t.Errorf("WhoAdmits(%q, %q) = %v, want %v", c.who, c.f, got, c.want)
		}
	}
}
