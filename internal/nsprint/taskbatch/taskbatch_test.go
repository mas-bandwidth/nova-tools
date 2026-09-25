package taskbatch_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskbatch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

const sprint = "sp"

// wheres are the sets a task card can be in (nova-tools #3778).
var wheres = []string{"waiting", "ready", "working", "merging", "landed", "done", "parked"}

// evalCaller FCALLs the loaded nova_sprint library (task_batch.lua reaches
// the one task move as NS.task, so the whole library is loaded on a
// throwaway redis-server); calls counts the round trips.
type evalCaller struct {
	c     *redis.Client
	calls int
}

func newEval(t *testing.T, c *redis.Client) *evalCaller {
	t.Helper()
	return &evalCaller{c: c}
}

func (e *evalCaller) call() taskbatch.Caller {
	f := taskbatch.FCall(e.c)
	return func(ctx context.Context, fn string, args []any) (any, error) {
		e.calls++
		return f(ctx, fn, args)
	}
}

type row struct {
	id, stream, state, owner string
	order                    int
	extra                    []string
}

func newStore(t *testing.T) (string, *redis.Client) {
	t.Helper()
	return wstest.Start(t)
}

// mirror is the friend-queue state a where is written as for one release.
var mirror = map[string]string{"ready": "open", "done": "closed"}

// seed writes rows in the task card shape (where, the stream and friend
// sets scored by created_at = the row's order) plus the legacy friend-queue
// shape (idx sets, the ready entry on q:<owner> with its queue and xid).
func seed(t *testing.T, c *redis.Client, rows []row) {
	t.Helper()
	ctx := context.Background()
	for _, r := range rows {
		st := r.state
		if m, ok := mirror[st]; ok {
			st = m
		}
		created := 1700000000000 + int64(r.order)
		rs := sprint
		for i := 0; i+1 < len(r.extra); i += 2 {
			if r.extra[i] == "sprint" {
				rs = r.extra[i+1]
			}
		}
		fields := []any{"stream", r.stream, "where", r.state, "where_ok", "-", "state", st, "order", r.order, "owner", r.owner,
			"friend", r.owner, "kind", "fix", "created_at", created, "sprint", sprint}
		for _, x := range r.extra {
			fields = append(fields, x)
		}
		p := c.Pipeline()
		p.SAdd(ctx, "ws:names", r.stream)
		p.ZAdd(ctx, "ws:"+r.stream+":"+r.state, redis.Z{Score: float64(created), Member: r.id})
		p.ZAdd(ctx, "friend:"+r.owner+":cards:"+r.state, redis.Z{Score: float64(created), Member: r.id})
		switch r.state {
		case "ready":
			p.SAdd(ctx, "sprint:"+rs+":idx:"+r.owner+":open", r.id)
			xid := c.XAdd(ctx, &redis.XAddArgs{Stream: "q:" + r.owner, Values: []any{"id", r.id}}).Val()
			fields = append(fields, "queue", "q:"+r.owner, "xid", xid)
		case "working":
			p.SAdd(ctx, "sprint:"+rs+":idx:"+r.owner+":working", r.id)
		case "merging", "landed":
			p.SAdd(ctx, "sprint:"+rs+":idx:"+r.owner+":closed", r.id)
		case "waiting", "parked":
			p.ZAdd(ctx, "q:blocked", redis.Z{Score: 1, Member: r.id})
		}
		p.HSet(ctx, "task:"+r.id, fields...)
		if _, err := p.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// check is the spec's invariant for every id: in exactly the one ws ZSET its
// stream/where fields name, the friend set agrees, and the legacy shapes
// agree (done: idx closed; ready: idx open; waiting/parked: q:blocked).
func check(t *testing.T, c *redis.Client, ids []string) {
	t.Helper()
	ctx := context.Background()
	names, err := c.SMembers(ctx, "ws:names").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		v := c.HMGet(ctx, "task:"+id, "stream", "where", "owner", "sprint").Val()
		stream, where, owner, rs := fmt.Sprint(v[0]), fmt.Sprint(v[1]), fmt.Sprint(v[2]), fmt.Sprint(v[3])
		var in []string
		for _, s := range names {
			for _, w := range wheres {
				if c.ZScore(ctx, "ws:"+s+":"+w, id).Err() == nil {
					in = append(in, s+":"+w)
				}
			}
		}
		if len(in) != 1 || in[0] != stream+":"+where {
			t.Fatalf("%s fields %s:%s but in %v", id, stream, where, in)
		}
		if c.ZScore(ctx, "friend:"+owner+":cards:"+where, id).Err() != nil {
			t.Fatalf("%s not in friend:%s:cards:%s", id, owner, where)
		}
		open := c.SIsMember(ctx, "sprint:"+rs+":idx:"+owner+":open", id).Val()
		closed := c.SIsMember(ctx, "sprint:"+rs+":idx:"+owner+":closed", id).Val()
		blocked := c.ZScore(ctx, "q:blocked", id).Err() == nil
		if (where == "ready") != open || (where == "done" || where == "merging" || where == "landed") != closed ||
			(where == "waiting" || where == "parked") != blocked {
			t.Fatalf("%s %s: legacy open=%v closed=%v blocked=%v", id, where, open, closed, blocked)
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
	t.Parallel()

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
	t.Parallel()

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
	if st := c.HGet(ctx, "task:a", "where").Val(); st != "ready" {
		t.Fatalf("a refused batch moved a to %s", st)
	}
}

func TestMoveStreamKeepsOrder(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	if res := run(taskbatch.Request{Verb: taskbatch.MoveState, Param: "merging", IDs: []string{"r"}}); res.OK || res.ID != "r" || !strings.HasPrefix(res.Why, "OFFGRAPH ready -> merging") {
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
	if s := c.ZScore(ctx, "ws:s:ready", "r").Val(); s != 1700000000007 {
		t.Fatalf("front rescored r to %v; every score is the task's age", s)
	}
	if o := c.HGet(ctx, "task:r", "order").Val(); o != "0" {
		t.Fatalf("front order %q", o)
	}
	if f := c.HGet(ctx, "task:r", "front").Val(); f != "1" {
		t.Fatalf("front field %q", f)
	}
	if a, b := c.XLen(ctx, "q:g").Val(), c.XLen(ctx, "q:g:front").Val(); a != 0 || b != 1 {
		t.Fatalf("q:g=%d q:g:front=%d", a, b)
	}
}

func TestRefusalChangesNothingAndNamesFirstBadID(t *testing.T) {
	t.Parallel()

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
	if res.OK || res.ID != "b" || !strings.HasPrefix(res.Why, "DRIFT unlinked ws:s:ready task:b") {
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
	t.Parallel()

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
	// p1 and o1 are sprint P's (their record's sprint names their idx sets):
	// one sweep per sprint.
	res, err := taskbatch.Sweep(ctx, e.call(), taskbatch.SweepRequest{Sprint: sprint, By: "rowan", Friend: "f"})
	if err != nil || !res.OK || res.N != 1 || res.Cancelled != 1 || res.Stream != "s" {
		t.Fatalf("sweep = %+v %v", res, err)
	}
	res, err = taskbatch.Sweep(ctx, e.call(), taskbatch.SweepRequest{Sprint: "P", By: "rowan", Friend: "f"})
	if err != nil || !res.OK || res.N != 1 || res.Waiting != 1 || res.Stream != "t" {
		t.Fatalf("sweep P = %+v %v", res, err)
	}
	check(t, c, ids(rows))
	want := map[string]string{"r1": "done", "r2": "ready", "r3": "ready", "p1": "waiting", "o1": "ready"}
	for id, st := range want {
		if got := c.HGet(ctx, "task:"+id, "where").Val(); got != st {
			t.Fatalf("%s where %s, want %s", id, got, st)
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
	t.Parallel()

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
