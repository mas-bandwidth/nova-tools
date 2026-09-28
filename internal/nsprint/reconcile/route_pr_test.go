//go:build functional

package reconcile_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The route duty's pr:<repo>:<n> legs (nova-tools #3579, #3580) on a
// throwaway redis-server: no GitHub, every guard a record.

const (
	prRepo   = "mas-bandwidth/nova-tools"
	prStream = "nova-sprint"
)

var (
	headA = strings.Repeat("a", 40)
	headB = strings.Repeat("b", 40)
	headC = strings.Repeat("c", 40)
)

type prFixture struct {
	ctx  context.Context
	c    *redis.Client
	S    string
	duty *reconcile.RouteDuty
	l    *reconcile.Lease
}

// newPRFixture is one open sprint on the nova-sprint stream with three live
// friends: rowan (coordinator), emma and stella.
func newPRFixture(t *testing.T, S string) *prFixture {
	t.Helper()
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open", "stream", prStream)
	pipe.SAdd(ctx, "ws:names", prStream)
	for _, f := range []string{"rowan", "emma", "stella"} {
		pipe.SAdd(ctx, "friends", f)
		pipe.HSet(ctx, "friend:"+f+":beat", "harness", "ctl", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	pipe.HSet(ctx, "friend:rowan:roles", "roles", "coordinator,may-hold,builder")
	pipe.SAdd(ctx, "readers", "rowan", "emma", "stella")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	return &prFixture{ctx: ctx, c: c, S: S, duty: &reconcile.RouteDuty{Client: c}, l: l}
}

// addPR is one working build task of owner (empty: swarm-built) with its PR
// record at head and the typed lines given.
func (f *prFixture) addPR(t *testing.T, task, owner, n, head string, lines ...string) {
	t.Helper()
	pipe := f.c.TxPipeline()
	pipe.HSet(f.ctx, "task:"+task, "stream", prStream, "state", "working", "owner", owner,
		"pr", prRepo+"#"+n, "kind", "build", "branch", "codex/"+n+"-anything")
	pipe.ZAdd(f.ctx, "ws:"+prStream+":working", redis.Z{Score: 1, Member: task})
	pipe.HSet(f.ctx, prkey.KeyText(prRepo, n), "repo", prRepo, "n", n, "head", head, "base", "dev",
		"stream", prStream, "task", task, "state", "open", "ci", "green", "reads", strings.Join(lines, "\n"))
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *prFixture) run(t *testing.T) reconcile.Counts {
	t.Helper()
	c, err := f.duty.Run(f.ctx, f.l)
	if err != nil {
		t.Fatalf("route pass: %v", err)
	}
	return c
}

func (f *prFixture) task(t *testing.T, id string) map[string]string {
	t.Helper()
	m, err := f.c.HGetAll(f.ctx, "task:"+id).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *prFixture) qlen(name string) int64 { return f.c.XLen(f.ctx, "q:"+name).Val() }

func (f *prFixture) record(t *testing.T, n string) map[string]string {
	t.Helper()
	m, err := f.c.HGetAll(f.ctx, prkey.KeyText(prRepo, n)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// wsLogWhy is every ws:log why containing s.
func (f *prFixture) wsLogWhy(s string) int {
	n := 0
	for _, m := range f.c.XRange(f.ctx, "ws:log", "-", "+").Val() {
		if w, _ := m.Values["why"].(string); strings.Contains(w, s) {
			n++
		}
	}
	return n
}

// TestHeldHeadWithoutFixTaskPushesOneFixToAuthor (#3579): a HOLD at head by
// emma on stella's PR with no fix task pushes exactly one fix-<n>-<head8>
// task to stella naming the HOLD line and the head; the record carries
// fix_task; a second pass pushes nothing more.
func TestHeldHeadWithoutFixTaskPushesOneFixToAuthor(t *testing.T) {
	f := newPRFixture(t, "ctl-3579a")
	hold := "HOLD who=emma head=" + headA + " gates=scope:fail reason=touches-a-path-outside-PATHS"
	f.addPR(t, "build-3572", "stella", "3572", headA, "SCORE who=jev head="+headA+" score=8/10", hold)

	c := f.run(t)
	if c.Fixes != 1 || c.Reads != 0 {
		t.Fatalf("first pass counts %s; want fixes=1 reads=0", c.Line())
	}
	id := "fix-3572-" + headA[:8]
	task := f.task(t, id)
	if task["owner"] != "stella" || task["kind"] != "fix" || task["where"] != "ready" || task["head"] != headA {
		t.Fatalf("task:%s = %v; want a ready fix task owned by stella at head", id, task)
	}
	if !strings.Contains(task["title"], hold) || !strings.Contains(task["title"], headA[:8]) || task["holder"] != "emma" {
		t.Fatalf("fix task title %q holder %q: want the HOLD line, the head and the holder", task["title"], task["holder"])
	}
	if f.qlen("stella") != 1 || f.qlen("rowan") != 0 || f.qlen("emma") != 0 {
		t.Fatalf("queues stella=%d rowan=%d emma=%d; want the fix on stella's alone", f.qlen("stella"), f.qlen("rowan"), f.qlen("emma"))
	}
	if f.c.ZScore(f.ctx, "ws:"+prStream+":ready", id).Err() != nil {
		t.Fatalf("%s is not in ws:%s:ready", id, prStream)
	}
	rec := f.record(t, "3572")
	if rec["fix_task"] != id || rec["fix_task_head"] != headA || rec["fix_task_to"] != "stella" || rec["fix_task_kind"] != "fix" {
		t.Fatalf("record fix fields = %v", rec)
	}
	if f.wsLogWhy("route pr fix") != 1 {
		t.Fatalf("ws:log has %d 'route pr fix' receipts, want 1", f.wsLogWhy("route pr fix"))
	}
	// The second pass, on a fresh instance too, pushes nothing.
	for i, d := range []*reconcile.RouteDuty{f.duty, {Client: f.c}} {
		c, err := d.Run(f.ctx, f.l)
		if err != nil || c.Fixes != 0 || c.Reads != 0 {
			t.Fatalf("pass %d: %s (%v); want nothing pushed", i+2, c.Line(), err)
		}
	}
	if f.qlen("stella") != 1 {
		t.Fatalf("q:stella has %d entries after the second pass, want still 1", f.qlen("stella"))
	}
}

// TestThirdHeldHeadIsCloseOverRecut (#3579, one-friend-read-lands): a PR
// held at two earlier heads and now at a third gets no fix task; one
// close-<n>-<head8> task goes to the coordinator with the finding kept.
func TestThirdHeldHeadIsCloseOverRecut(t *testing.T) {
	f := newPRFixture(t, "ctl-3579c")
	f.addPR(t, "build-3233", "stella", "3233", headC,
		"HOLD who=emma head="+headA+" reason=first",
		"HOLD who=rowan head="+headB+" reason=second",
		"DISPOSITION who=emma head="+headC+" verdict=HOLD score=5 reason=third")
	c := f.run(t)
	if c.Fixes != 1 {
		t.Fatalf("counts %s; want fixes=1 (the close task)", c.Line())
	}
	id := "close-3233-" + headC[:8]
	task := f.task(t, id)
	if task["owner"] != "rowan" || task["kind"] != "close" || task["held_heads"] != "3" || !strings.Contains(task["title"], "held at 3 heads") {
		t.Fatalf("task:%s = %v; want a close task on the coordinator naming 3 heads", id, task)
	}
	if f.qlen("stella") != 0 || f.c.Exists(f.ctx, "task:fix-3233-"+headC[:8]).Val() != 0 {
		t.Fatalf("the author got a fix task on the third held head")
	}
	if rec := f.record(t, "3233"); rec["fix_task"] != id || rec["fix_task_kind"] != "close" {
		t.Fatalf("record fix fields = %v", rec)
	}
	if c := f.run(t); c.Fixes != 0 {
		t.Fatalf("second pass %s; want nothing", c.Line())
	}
}

// TestSwarmBuiltHoldIsRecutOnTheCoordinator (#3579): the author is the
// builder task's owner; a swarm-built PR has none, so the HOLD becomes one
// recut-<n>-<head8> task on the coordinator's queue, never a fix to
// whatever the branch name says.
func TestSwarmBuiltHoldIsRecutOnTheCoordinator(t *testing.T) {
	f := newPRFixture(t, "ctl-3579s")
	f.addPR(t, "card-3484", "", "3484", headA, "HOLD who=emma head="+headA+" reason=no-control")
	if c := f.run(t); c.Fixes != 1 {
		t.Fatalf("counts %s; want fixes=1", c.Line())
	}
	id := "recut-3484-" + headA[:8]
	task := f.task(t, id)
	if task["owner"] != "rowan" || task["kind"] != "recut" || task["author"] != "" {
		t.Fatalf("task:%s = %v; want a recut on rowan's queue with no author", id, task)
	}
	if f.qlen("emma") != 0 || f.qlen("stella") != 0 {
		t.Fatalf("a recut went to a friend's queue")
	}
}

// TestEveryUnreadHeadGetsOneRead (#3580): a head with only a JEV score and a
// cold score (COLD-GATES) is unread; one read-<n>-<head8> task goes to the
// least-loaded live reader, never the author; the record carries read_task;
// a second pass pushes nothing.
func TestEveryUnreadHeadGetsOneRead(t *testing.T) {
	f := newPRFixture(t, "ctl-3580r")
	// rowan carries two ready entries, emma one, stella (the author) none.
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:rowan", Values: []any{"id", "other-1"}})
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:rowan:front", Values: []any{"id", "other-2"}})
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:emma", Values: []any{"id", "other-3"}})
	f.addPR(t, "build-3542", "stella", "3542", headA,
		"SCORE who=jev head="+headA+" score=9/10",
		"SCORE who=cold-opus head="+headA+" score=8/10 ci=pending")
	c := f.run(t)
	if c.Reads != 1 || c.Fixes != 0 {
		t.Fatalf("counts %s; want reads=1 fixes=0", c.Line())
	}
	id := "read-3542-" + headA[:8]
	task := f.task(t, id)
	if task["owner"] != "emma" || task["kind"] != "read" || task["where"] != "ready" || task["head"] != headA || task["author"] != "stella" {
		t.Fatalf("task:%s = %v; want a ready read task on emma (least loaded, not the author)", id, task)
	}
	if f.qlen("stella") != 0 || f.qlen("emma") != 2 {
		t.Fatalf("queues stella=%d emma=%d; want 0 and 2", f.qlen("stella"), f.qlen("emma"))
	}
	rec := f.record(t, "3542")
	if rec["read_task"] != id || rec["read_task_head"] != headA || rec["read_task_to"] != "emma" {
		t.Fatalf("record read fields = %v", rec)
	}
	if f.wsLogWhy("route pr read") != 1 {
		t.Fatalf("ws:log has %d 'route pr read' receipts, want 1", f.wsLogWhy("route pr read"))
	}
	for i, d := range []*reconcile.RouteDuty{f.duty, {Client: f.c}} {
		c, err := d.Run(f.ctx, f.l)
		if err != nil || c.Reads != 0 || c.Fixes != 0 {
			t.Fatalf("pass %d: %s (%v); want nothing pushed", i+2, c.Line(), err)
		}
	}
	if f.qlen("emma") != 2 {
		t.Fatalf("q:emma has %d entries after the second pass, want still 2", f.qlen("emma"))
	}
	// A counting read at head (emma's SCORE) ends the need: no further task.
	f.c.HSet(f.ctx, prkey.Key(prRepo, 3542), "reads", "SCORE who=emma head="+headA+" score=8/10")
	if c := f.run(t); c.Reads != 0 {
		t.Fatalf("a read at head still pushed: %s", c.Line())
	}
}

// TestAuthorFromBuilderTaskNotBranch (#3580): the author is the builder
// task's owner. stella owns the task, is the least-loaded reader and even
// has a SCORE at head; the branch name says nothing. The read goes to
// another friend and stella's own line does not count as the read.
func TestAuthorFromBuilderTaskNotBranch(t *testing.T) {
	f := newPRFixture(t, "ctl-3580a")
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:emma", Values: []any{"id", "other-1"}})
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:rowan", Values: []any{"id", "other-2"}})
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: "q:rowan", Values: []any{"id", "other-3"}})
	f.addPR(t, "build-3553", "stella", "3553", headA, "SCORE who=stella head="+headA+" score=10/10")
	f.c.HSet(f.ctx, "task:build-3553", "branch", "gafferongames:fix/3553-something")
	if c := f.run(t); c.Reads != 1 {
		t.Fatalf("counts %s; want reads=1", c.Line())
	}
	task := f.task(t, "read-3553-"+headA[:8])
	if task["owner"] != "emma" || task["author"] != "stella" {
		t.Fatalf("read task = %v; want emma (stella is the author, from the task, not the branch)", task)
	}
	if f.qlen("stella") != 0 {
		t.Fatalf("the author's queue got the read")
	}
}

// TestNewHeadSupersedesOldRead (#3580): a head move cancels the old head's
// unstarted read task `superseded by <head>` and pushes one read at the new
// head; the cancelled task leaves its ws set, its owner's open index and
// its queue.
func TestNewHeadSupersedesOldRead(t *testing.T) {
	f := newPRFixture(t, "ctl-3580s")
	f.addPR(t, "build-3552", "stella", "3552", headA)
	if c := f.run(t); c.Reads != 1 {
		t.Fatalf("counts %s; want reads=1", c.Line())
	}
	old := "read-3552-" + headA[:8]
	owner := f.task(t, old)["owner"]
	before := f.qlen(owner)
	f.c.HSet(f.ctx, prkey.Key(prRepo, 3552), "head", headB, "ci", "pending")
	c := f.run(t)
	if c.Reads != 1 || c.Carried != 0 {
		t.Fatalf("after the head move %s; want reads=1 carried=0", c.Line())
	}
	got := f.task(t, old)
	if got["where"] != "done" || got["where_ok"] != "fail" || got["cancelled"] != "1" || got["evidence"] != "superseded by "+headB {
		t.Fatalf("old read task = %v; want closed, cancelled, superseded by the new head", got)
	}
	if f.c.ZScore(f.ctx, "ws:"+prStream+":ready", old).Err() == nil {
		t.Fatalf("%s is still in ws:%s:ready", old, prStream)
	}
	if f.c.SIsMember(f.ctx, "sprint:"+f.S+":idx:"+owner+":open", old).Val() {
		t.Fatalf("%s is still in %s's open index", old, owner)
	}
	if f.qlen(owner) != before {
		t.Fatalf("q:%s has %d entries, want %d (old entry deleted, new one added)", owner, f.qlen(owner), before)
	}
	fresh := f.task(t, "read-3552-"+headB[:8])
	if fresh["where"] != "ready" || fresh["head"] != headB {
		t.Fatalf("new read task = %v", fresh)
	}
	if f.wsLogWhy("superseded by "+headB) != 1 {
		t.Fatalf("ws:log has %d supersede receipts, want 1", f.wsLogWhy("superseded by "+headB))
	}
	if rec := f.record(t, "3552"); rec["read_task"] != "read-3552-"+headB[:8] || rec["read_task_head"] != headB {
		t.Fatalf("record read fields = %v", rec)
	}
}

// TestIdenticalDiffCarriesTheRead (#3580, #3687's carry rule): when the
// record's diff_sha256 at the new head equals the one the read task was
// pushed with, the task is carried to the new head (carried_from set, a
// `read carry` note in ws:log) and nothing new is pushed or cancelled.
func TestIdenticalDiffCarriesTheRead(t *testing.T) {
	f := newPRFixture(t, "ctl-3580c")
	f.addPR(t, "build-3556", "stella", "3556", headA)
	f.c.HSet(f.ctx, prkey.Key(prRepo, 3556), "diff_sha256", "d1")
	if c := f.run(t); c.Reads != 1 {
		t.Fatalf("counts %s; want reads=1", c.Line())
	}
	id := "read-3556-" + headA[:8]
	if f.task(t, id)["diff_sha256"] != "d1" {
		t.Fatalf("the read task did not record the diff digest")
	}
	f.c.HSet(f.ctx, prkey.Key(prRepo, 3556), "head", headB)
	c := f.run(t)
	if c.Carried != 1 || c.Reads != 0 {
		t.Fatalf("after a rebase with the same diff %s; want carried=1 reads=0", c.Line())
	}
	got := f.task(t, id)
	if got["where"] != "ready" || got["head"] != headB || got["carried_from"] != headA {
		t.Fatalf("carried task = %v; want ready at the new head with carried_from", got)
	}
	if f.c.Exists(f.ctx, "task:read-3556-"+headB[:8]).Val() != 0 {
		t.Fatalf("a second read task was pushed despite the identical diff")
	}
	if f.wsLogWhy("read carry") != 1 {
		t.Fatalf("ws:log has %d 'read carry' notes, want 1", f.wsLogWhy("read carry"))
	}
	if rec := f.record(t, "3556"); rec["read_task"] != id || rec["read_task_head"] != headB {
		t.Fatalf("record read fields = %v", rec)
	}
	if c := f.run(t); c.Carried != 0 || c.Reads != 0 {
		t.Fatalf("third pass %s; want nothing", c.Line())
	}
}
