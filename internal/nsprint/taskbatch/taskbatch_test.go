package taskbatch_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskbatch"
)

const sprint = "sp"

var states = []string{"waiting", "ready", "working", "merging", "landed", "parked"}

// evalCaller runs fn/lua/task_batch.lua on miniredis, which has EVAL but no
// FUNCTION/FCALL: the file is wrapped so its register_function calls fill a
// table and the named function is called with the args. calls counts the
// round trips.
type evalCaller struct {
	c      *redis.Client
	script string
	calls  int
}

func newEval(t *testing.T, c *redis.Client) *evalCaller {
	t.Helper()
	src, err := os.ReadFile("../fn/lua/task_batch.lua")
	if err != nil {
		t.Fatal(err)
	}
	script := "local NS = {}\nlocal fns = {}\nredis.register_function = function(name, fn) fns[name] = fn end\ndo\n" +
		string(src) + "\nend\nlocal a = {}\nfor i = 2, #ARGV do a[i - 1] = ARGV[i] end\nreturn fns[ARGV[1]](KEYS, a)\n"
	return &evalCaller{c: c, script: script}
}

func (e *evalCaller) call() taskbatch.Caller {
	return func(ctx context.Context, fn string, args []any) (any, error) {
		e.calls++
		return e.c.Eval(ctx, e.script, nil, append([]any{fn}, args...)...).Result()
	}
}

type row struct {
	id, stream, state, owner string
	order                    int
	extra                    []string
}

func newStore(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return m, c
}

// seed writes rows in the ws shape plus the legacy friend-queue shape.
func seed(t *testing.T, c *redis.Client, rows []row) {
	t.Helper()
	ctx := context.Background()
	p := c.Pipeline()
	for _, r := range rows {
		p.SAdd(ctx, "ws:names", r.stream)
		fields := []any{"stream", r.stream, "state", r.state, "order", r.order, "owner", r.owner, "kind", "fix"}
		for _, x := range r.extra {
			fields = append(fields, x)
		}
		p.HSet(ctx, "task:"+r.id, fields...)
		p.ZAdd(ctx, "ws:"+r.stream+":"+r.state, redis.Z{Score: float64(r.order), Member: r.id})
		switch r.state {
		case "ready":
			p.SAdd(ctx, "sprint:"+sprint+":idx:"+r.owner+":open", r.id)
			p.XAdd(ctx, &redis.XAddArgs{Stream: "q:" + r.owner, Values: []any{"id", r.id}})
		case "working":
			p.SAdd(ctx, "sprint:"+sprint+":idx:"+r.owner+":working", r.id)
		case "waiting", "parked":
			p.ZAdd(ctx, "q:blocked", redis.Z{Score: 1, Member: r.id})
		}
	}
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// check is the spec's invariant for every id: in exactly the one ws ZSET its
// stream/state fields name (none when closed), and the legacy shapes agree.
func check(t *testing.T, c *redis.Client, ids []string) {
	t.Helper()
	ctx := context.Background()
	names, err := c.SMembers(ctx, "ws:names").Result()
	if err != nil {
		t.Fatal(err)
	}
	p := c.Pipeline()
	hm := make([]*redis.SliceCmd, len(ids))
	sc := make([][]*redis.FloatCmd, len(ids))
	blocked := make([]*redis.FloatCmd, len(ids))
	for i, id := range ids {
		hm[i] = p.HMGet(ctx, "task:"+id, "stream", "state", "owner")
		blocked[i] = p.ZScore(ctx, "q:blocked", id)
		for _, s := range names {
			for _, st := range states {
				sc[i] = append(sc[i], p.ZScore(ctx, "ws:"+s+":"+st, id))
			}
		}
	}
	_, _ = p.Exec(ctx)
	p2 := c.Pipeline()
	open := make([]*redis.BoolCmd, len(ids))
	closed := make([]*redis.BoolCmd, len(ids))
	for i, id := range ids {
		owner := fmt.Sprint(hm[i].Val()[2])
		open[i] = p2.SIsMember(ctx, "sprint:"+sprint+":idx:"+owner+":open", id)
		closed[i] = p2.SIsMember(ctx, "sprint:"+sprint+":idx:"+owner+":closed", id)
	}
	_, _ = p2.Exec(ctx)
	for i, id := range ids {
		v := hm[i].Val()
		stream, state := fmt.Sprint(v[0]), fmt.Sprint(v[1])
		var in []string
		k := 0
		for _, s := range names {
			for _, st := range states {
				if sc[i][k].Err() == nil {
					in = append(in, s+":"+st)
				}
				k++
			}
		}
		if state == "closed" {
			if len(in) != 0 {
				t.Fatalf("%s closed but in %v", id, in)
			}
			if !closed[i].Val() || open[i].Val() || blocked[i].Err() == nil {
				t.Fatalf("%s closed: legacy closed=%v open=%v blocked=%v", id, closed[i].Val(), open[i].Val(), blocked[i].Err() == nil)
			}
			continue
		}
		if len(in) != 1 || in[0] != stream+":"+state {
			t.Fatalf("%s fields %s:%s but in %v", id, stream, state, in)
		}
		isBlocked := blocked[i].Err() == nil
		if (state == "waiting" || state == "parked") != isBlocked {
			t.Fatalf("%s %s: q:blocked=%v", id, state, isBlocked)
		}
		if (state == "ready") != open[i].Val() {
			t.Fatalf("%s %s: legacy open=%v", id, state, open[i].Val())
		}
	}
}

func ids(rows []row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.id
	}
	return out
}

func thousand() []row {
	var rows []row
	for i := 0; i < 1000; i++ {
		rows = append(rows, row{
			id: fmt.Sprintf("t%04d", i), stream: fmt.Sprintf("s%d: work", i%10),
			state: "ready", owner: fmt.Sprintf("f%d", i%4), order: i,
		})
	}
	return rows
}

// TestCancelThousandInOneCall is the bar: 1,000 ids across 10 streams cancel
// in one function call in under a second, every invariant held after.
func TestCancelThousandInOneCall(t *testing.T) {
	_, c := newStore(t)
	rows := thousand()
	seed(t, c, rows)
	e := newEval(t, c)
	ctx := context.Background()
	start := time.Now()
	res, err := taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.Cancel, Sprint: sprint, By: "rowan", Why: "superseded", IDs: ids(rows),
	})
	ms := time.Since(start).Milliseconds()
	t.Logf("cancel n=%d ms=%d calls=%d", res.N, ms, e.calls)
	if err != nil || !res.OK || res.N != 1000 || res.Stream != "*" {
		t.Fatalf("cancel = %+v %v", res, err)
	}
	if e.calls != 1 {
		t.Fatalf("calls = %d, want 1", e.calls)
	}
	if ms >= 1000 {
		t.Fatalf("1,000 cancels took %d ms, want under 1000", ms)
	}
	check(t, c, ids(rows))
	if n := c.XLen(ctx, "ws:log").Val(); n != 1000 {
		t.Fatalf("ws:log has %d entries, want 1000", n)
	}
	for f := 0; f < 4; f++ {
		if n := c.XLen(ctx, fmt.Sprintf("q:f%d", f)).Val(); n != 0 {
			t.Fatalf("q:f%d still holds %d entries", f, n)
		}
	}
	if ev := c.HGet(ctx, "task:t0500", "evidence").Val(); ev != "superseded" {
		t.Fatalf("evidence %q", ev)
	}
}

func TestBlockUnblockRoundTrip(t *testing.T) {
	_, c := newStore(t)
	rows := []row{
		{id: "a", stream: "s", state: "ready", owner: "f", order: 3},
		{id: "b", stream: "s", state: "ready", owner: "f", order: 1},
		{id: "c", stream: "s", state: "ready", owner: "g", order: 2},
	}
	seed(t, c, rows)
	e := newEval(t, c)
	ctx := context.Background()
	res, err := taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.Block, Sprint: sprint, By: "rowan", Param: "task:x", IDs: []string{"a", "b"},
	})
	if err != nil || !res.OK || res.N != 2 || res.Stream != "s" {
		t.Fatalf("block = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	if on := c.HGet(ctx, "task:a", "blocked_on").Val(); on != "task:x" {
		t.Fatalf("blocked_on %q", on)
	}
	if n := c.XLen(ctx, "q:f").Val(); n != 0 {
		t.Fatalf("q:f holds %d after block", n)
	}
	// unblock by stream: every waiting row of s.
	res, err = taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.Unblock, Sprint: sprint, By: "rowan", Stream: "s",
	})
	if err != nil || !res.OK || res.N != 2 {
		t.Fatalf("unblock = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	got := c.ZRange(ctx, "ws:s:ready", 0, -1).Val()
	if strings.Join(got, ",") != "b,c,a" {
		t.Fatalf("ready order %v, want b,c,a", got)
	}
	if on := c.HGet(ctx, "task:a", "blocked_on").Val(); on != "" {
		t.Fatalf("blocked_on kept %q", on)
	}
	if n := c.XLen(ctx, "q:f").Val(); n != 2 {
		t.Fatalf("q:f holds %d after unblock, want 2", n)
	}
	// block a waiting-only verb on a landed row refuses and names it.
	seed(t, c, []row{{id: "d", stream: "s", state: "landed", owner: "f", order: 9}})
	res, err = taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.Block, Sprint: sprint, By: "rowan", Why: "wait", IDs: []string{"a", "d"},
	})
	if err != nil || res.OK || res.ID != "d" || res.Why != "block-from-landed" {
		t.Fatalf("block landed = %+v %v", res, err)
	}
	if st := c.HGet(ctx, "task:a", "state").Val(); st != "ready" {
		t.Fatalf("a refused batch moved a to %s", st)
	}
}

func TestMoveStreamKeepsOrder(t *testing.T) {
	_, c := newStore(t)
	var rows []row
	for i := 1; i <= 5; i++ {
		rows = append(rows, row{id: fmt.Sprintf("a%d", i), stream: "A", state: "waiting", owner: "f", order: i * 10})
	}
	for i := 1; i <= 3; i++ {
		rows = append(rows, row{id: fmt.Sprintf("b%d", i), stream: "B", state: "waiting", owner: "f", order: i})
	}
	seed(t, c, rows)
	e := newEval(t, c)
	ctx := context.Background()
	// The file lists A's rows out of order; they keep their own order and
	// queue after B's tail.
	res, err := taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.MoveStream, Sprint: sprint, By: "rowan", Param: "B",
		IDs: []string{"a5", "a2", "a4", "a1", "a3"},
	})
	if err != nil || !res.OK || res.N != 5 || res.Stream != "B" {
		t.Fatalf("move = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	got := strings.Join(c.ZRange(ctx, "ws:B:waiting", 0, -1).Val(), ",")
	if got != "b1,b2,b3,a1,a2,a3,a4,a5" {
		t.Fatalf("B waiting %s", got)
	}
	if n := c.ZCard(ctx, "ws:A:waiting").Val(); n != 0 {
		t.Fatalf("A kept %d", n)
	}
	// Back by --stream: the whole of B moves to A, order kept.
	res, err = taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.MoveStream, Sprint: sprint, By: "rowan", Param: "A", Stream: "B",
	})
	if err != nil || !res.OK || res.N != 8 {
		t.Fatalf("move back = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	if got := strings.Join(c.ZRange(ctx, "ws:A:waiting", 0, -1).Val(), ","); got != "b1,b2,b3,a1,a2,a3,a4,a5" {
		t.Fatalf("A waiting %s", got)
	}
	// An unknown destination refuses.
	res, _ = taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.MoveStream, Sprint: sprint, By: "rowan", Param: "nope", IDs: []string{"a1"},
	})
	if res.OK || res.ID != "nope" || res.Why != "unknown-stream" {
		t.Fatalf("unknown stream = %+v", res)
	}
}

func TestMoveStateFriendAndFront(t *testing.T) {
	_, c := newStore(t)
	rows := []row{
		{id: "w", stream: "s", state: "waiting", owner: "f", order: 5},
		{id: "r", stream: "s", state: "ready", owner: "f", order: 7},
		{id: "k", stream: "s", state: "working", owner: "f", order: 1},
	}
	seed(t, c, rows)
	e := newEval(t, c)
	ctx := context.Background()
	run := func(r taskbatch.Request) taskbatch.Result {
		t.Helper()
		r.Sprint, r.By = sprint, "rowan"
		res, err := taskbatch.Batch(ctx, e.call(), r)
		if err != nil {
			t.Fatal(err)
		}
		check(t, c, ids(rows))
		return res
	}
	if res := run(taskbatch.Request{Verb: taskbatch.MoveState, Param: "merging", IDs: []string{"r"}}); res.OK || res.ID != "r" || res.Why != "ready-to-merging" {
		t.Fatalf("graph refusal = %+v", res)
	}
	if res := run(taskbatch.Request{Verb: taskbatch.MoveState, Param: "ready", IDs: []string{"w"}}); !res.OK || res.N != 1 {
		t.Fatalf("waiting->ready = %+v", res)
	}
	if res := run(taskbatch.Request{Verb: taskbatch.MoveState, Param: "merging", IDs: []string{"k"}}); !res.OK {
		t.Fatalf("working->merging = %+v", res)
	}
	if res := run(taskbatch.Request{Verb: taskbatch.MoveFriend, Param: "g", IDs: []string{"k"}}); res.OK || res.Why != "move-friend-from-merging" {
		t.Fatalf("move-friend merging = %+v", res)
	}
	if res := run(taskbatch.Request{Verb: taskbatch.MoveFriend, Param: "g", IDs: []string{"r"}}); !res.OK {
		t.Fatalf("move-friend = %+v", res)
	}
	if o := c.HGet(ctx, "task:r", "owner").Val(); o != "g" {
		t.Fatalf("owner %q", o)
	}
	if n := c.XLen(ctx, "q:g").Val(); n != 1 {
		t.Fatalf("q:g holds %d", n)
	}
	if res := run(taskbatch.Request{Verb: taskbatch.Front, IDs: []string{"r"}}); !res.OK {
		t.Fatalf("front = %+v", res)
	}
	if s := c.ZScore(ctx, "ws:s:ready", "r").Val(); s != 0 {
		t.Fatalf("front score %v", s)
	}
	if f := c.HGet(ctx, "task:r", "front").Val(); f != "1" {
		t.Fatalf("front field %q", f)
	}
	if a, b := c.XLen(ctx, "q:g").Val(), c.XLen(ctx, "q:g:front").Val(); a != 0 || b != 1 {
		t.Fatalf("q:g=%d q:g:front=%d", a, b)
	}
}

func TestRefusalChangesNothingAndNamesFirstBadID(t *testing.T) {
	_, c := newStore(t)
	rows := []row{
		{id: "a", stream: "s", state: "ready", owner: "f", order: 1},
		{id: "b", stream: "s", state: "ready", owner: "f", order: 2},
	}
	seed(t, c, rows)
	e := newEval(t, c)
	ctx := context.Background()
	res, err := taskbatch.Batch(ctx, e.call(), taskbatch.Request{
		Verb: taskbatch.Cancel, Sprint: sprint, By: "rowan", IDs: []string{"a", "ghost", "b", "ghost2"},
	})
	if err != nil || res.OK || res.ID != "ghost" || res.Why != "no-stream" {
		t.Fatalf("refusal = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	if n := c.XLen(ctx, "ws:log").Val(); n != 0 {
		t.Fatalf("a refused batch logged %d", n)
	}
	// A row whose ZSET does not hold it is a broken invariant: refused.
	c.ZRem(ctx, "ws:s:ready", "b")
	res, _ = taskbatch.Batch(ctx, e.call(), taskbatch.Request{Verb: taskbatch.Cancel, Sprint: sprint, By: "rowan", IDs: []string{"a", "b"}})
	if res.OK || res.ID != "b" || res.Why != "not-in-ws-ready" {
		t.Fatalf("unindexed = %+v", res)
	}
	// --set takes a ZSET's members.
	res, _ = taskbatch.Batch(ctx, e.call(), taskbatch.Request{Verb: taskbatch.Cancel, Sprint: sprint, By: "rowan", Set: "ws:s:ready"})
	if !res.OK || res.N != 1 {
		t.Fatalf("set = %+v", res)
	}
	res, _ = taskbatch.Batch(ctx, e.call(), taskbatch.Request{Verb: taskbatch.Cancel, Sprint: sprint, By: "rowan", Set: "no:such"})
	if res.OK || res.ID != "no:such" || res.Why != "not-a-set" {
		t.Fatalf("missing set = %+v", res)
	}
}

func TestSweepCancelsMovedHeadAndParksPitstop(t *testing.T) {
	_, c := newStore(t)
	rows := []row{
		{id: "r1", stream: "s", state: "ready", owner: "f", order: 1, extra: []string{"kind", "read", "head", "aaaa111", "ref", "mas-bandwidth/nova-tools#10"}},
		{id: "r2", stream: "s", state: "ready", owner: "f", order: 2, extra: []string{"kind", "read", "head", "cccc", "ref", "nova-tools#11"}},
		{id: "r3", stream: "s", state: "ready", owner: "f", order: 3, extra: []string{"kind", "read", "head", "dddd", "ref", "nova-tools#12"}},
		{id: "p1", stream: "t", state: "ready", owner: "f", order: 4, extra: []string{"sprint", "P"}},
		{id: "o1", stream: "t", state: "ready", owner: "g", order: 5, extra: []string{"sprint", "P"}},
	}
	seed(t, c, rows)
	ctx := context.Background()
	c.HSet(ctx, "pr:nova-tools:10", "head", "bbbb222")
	c.HSet(ctx, "pr:nova-tools:11", "head", "cccc999")
	c.Set(ctx, "s:P:pitstop", "1", 0)
	e := newEval(t, c)
	res, err := taskbatch.Sweep(ctx, e.call(), taskbatch.SweepRequest{Sprint: sprint, By: "rowan", Friend: "f"})
	if err != nil || !res.OK || res.N != 2 || res.Cancelled != 1 || res.Waiting != 1 || res.Stream != "*" {
		t.Fatalf("sweep = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	want := map[string]string{"r1": "closed", "r2": "ready", "r3": "ready", "p1": "waiting", "o1": "ready"}
	for id, st := range want {
		if got := c.HGet(ctx, "task:"+id, "state").Val(); got != st {
			t.Fatalf("%s state %s, want %s", id, got, st)
		}
	}
	if ev := c.HGet(ctx, "task:r1", "evidence").Val(); !strings.Contains(ev, "aaaa111 -> bbbb222") {
		t.Fatalf("evidence %q", ev)
	}
	if r := c.HGet(ctx, "task:p1", "blocked_reason").Val(); r != "sweep: pitstop P" {
		t.Fatalf("blocked_reason %q", r)
	}
	// A second sweep finds nothing to do.
	res, _ = taskbatch.Sweep(ctx, e.call(), taskbatch.SweepRequest{Sprint: sprint, By: "rowan", Friend: "f"})
	if !res.OK || res.N != 0 {
		t.Fatalf("second sweep = %+v", res)
	}
}

func TestRequestCheck(t *testing.T) {
	base := taskbatch.Request{Verb: taskbatch.Cancel, Sprint: "s", By: "b", IDs: []string{"x"}}
	if err := base.Check(); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]taskbatch.Request{
		"no source":   {Verb: taskbatch.Cancel, Sprint: "s", By: "b"},
		"two sources": {Verb: taskbatch.Cancel, Sprint: "s", By: "b", IDs: []string{"x"}, Stream: "y"},
		"bad state":   {Verb: taskbatch.MoveState, Param: "done", Sprint: "s", By: "b", IDs: []string{"x"}},
		"bad verb":    {Verb: "drop", Sprint: "s", By: "b", IDs: []string{"x"}},
		"no actor":    {Verb: taskbatch.Cancel, Sprint: "s", IDs: []string{"x"}},
	} {
		if err := r.Check(); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
	got, err := taskbatch.ReadIDs(strings.NewReader("a\n\n# c\n b \n"))
	sort.Strings(got)
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Fatalf("ReadIDs = %v %v", got, err)
	}
}
