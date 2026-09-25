package taskcard_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const (
	stream = "nova-sprint + merge + bus"
	sprint = "tc-3778"
)

// table is the stream and friend rows the sprint table prints: ZCARDs.
func table(t *testing.T, c *redis.Client, friend string) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	m := map[string]int64{}
	for _, w := range taskcard.Wheres {
		m["ws:"+w] = c.ZCard(ctx, taskcard.StreamKey(stream, w)).Val()
		m["friend:"+w] = c.ZCard(ctx, taskcard.FriendKey(friend, w)).Val()
	}
	return m
}

// step asserts the one move changed the table by exactly -1 at from and +1
// at to, in both the stream and the friend rows, and nothing else.
func step(t *testing.T, name string, before, after map[string]int64, from, to string) {
	t.Helper()
	for k, v := range after {
		want := before[k]
		if strings.HasSuffix(k, ":"+from) {
			want--
		}
		if strings.HasSuffix(k, ":"+to) {
			want++
		}
		if v != want {
			t.Errorf("%s: %s is %d, want %d (before %d)", name, k, v, want, before[k])
		}
	}
}

func clean(t *testing.T, c *redis.Client, when string) taskcard.FsckResult {
	t.Helper()
	r, err := taskcard.Fsck(context.Background(), c, sprint)
	if err != nil {
		t.Fatal(err)
	}
	if r.Drift != 0 {
		t.Fatalf("%s: fsck drift %d: %v", when, r.Drift, r.Lines)
	}
	return r
}

func start(t *testing.T) *redis.Client {
	t.Helper()
	_, c := wstest.Start(t)
	c.SAdd(context.Background(), "friends", "rowan", "stella")
	return c
}

// TestPushTakeDoneLandWalksTheSets is the #3778 DONE-WHEN: a task pushed,
// taken, done with a PR and landed moves ready -> working -> merging ->
// landed, each ws:<stream>:<where> and friend:<f>:cards:<where> ZCARD
// changing by exactly one per step, the record's where naming the one set,
// the friend-queue shapes following, and fsck clean after every step.
func TestPushTakeDoneLandWalksTheSets(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	before := table(t, c, "rowan")
	r, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "build-3778-a", Stream: stream, Friend: "rowan",
		Sprint: sprint, Kind: "build", Ref: "nova-tools#3778", Origin: "issue:nova-tools#3778",
		Title: "tasks are cards", PR: "3790", By: "rowan"})
	if err != nil || r.Where != "ready" || r.XID == "" {
		t.Fatalf("push %+v %v", r, err)
	}
	after := table(t, c, "rowan")
	for k, v := range after {
		want := before[k]
		if strings.HasSuffix(k, ":ready") {
			want++
		}
		if v != want {
			t.Fatalf("push: %s is %d, want %d", k, v, want)
		}
	}
	h := c.HGetAll(ctx, "task:build-3778-a").Val()
	if h["where"] != "ready" || h["state"] != "open" || h["friend"] != "rowan" || h["owner"] != "rowan" || h["stream"] != stream ||
		h["origin"] == "" || h["xid"] != r.XID || h["queue"] != "q:rowan" {
		t.Fatalf("pushed record %v", h)
	}
	if !c.SIsMember(ctx, "sprint:"+sprint+":idx:rowan:open", "build-3778-a").Val() || c.XLen(ctx, "q:rowan").Val() != 1 {
		t.Fatal("push did not keep the friend-queue shape (idx open, q:rowan entry)")
	}
	clean(t, c, "push")

	before = after
	ids, err := taskcard.Take(ctx, c, "rowan", 1, "rowan")
	if err != nil || len(ids) != 1 || ids[0] != "build-3778-a" {
		t.Fatalf("take %v %v", ids, err)
	}
	after = table(t, c, "rowan")
	step(t, "take", before, after, "ready", "working")
	h = c.HGetAll(ctx, "task:build-3778-a").Val()
	if h["where"] != "working" || h["state"] != "working" || h["lease_until"] == "" || h["xid"] != "" {
		t.Fatalf("taken record %v", h)
	}
	if c.XLen(ctx, "q:rowan").Val() != 0 || !c.SIsMember(ctx, "sprint:"+sprint+":idx:rowan:working", "build-3778-a").Val() {
		t.Fatal("take left the ready entry or missed idx working")
	}
	clean(t, c, "take")

	before = after
	m, err := taskcard.Done(ctx, c, "build-3778-a", "rowan", "PR #3790 opened", "")
	if err != nil || m != (taskcard.Result{Status: "MOVED", From: "working", To: "merging"}) {
		t.Fatalf("done %+v %v", m, err)
	}
	after = table(t, c, "rowan")
	step(t, "done", before, after, "working", "merging")
	clean(t, c, "done")

	before = after
	m, err = taskcard.Land(ctx, c, "build-3778-a", "lander", "0123abcd", "")
	if err != nil || m.To != "landed" {
		t.Fatalf("land %+v %v", m, err)
	}
	after = table(t, c, "rowan")
	step(t, "land", before, after, "merging", "landed")
	h = c.HGetAll(ctx, "task:build-3778-a").Val()
	if h["where_ok"] != "ok" || h["merge_sha"] != "0123abcd" || h["state"] != "landed" {
		t.Fatalf("landed record %v", h)
	}
	if !c.SIsMember(ctx, "sprint:"+sprint+":idx:rowan:closed", "build-3778-a").Val() {
		t.Fatal("landed task not in idx closed")
	}
	res := clean(t, c, "land")
	if res.Counts["landed"] != 1 || res.Tasks != 1 {
		t.Fatalf("fsck counts %+v", res)
	}
	if n := c.XLen(ctx, "ws:log").Val(); n != 4 {
		t.Fatalf("ws:log has %d entries, want 4 (push, take, done, land)", n)
	}

	// A task that names no PR: done is done/ok.
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "fix-b", Stream: stream, Friend: "rowan", Sprint: sprint, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "fix-b"); err != nil {
		t.Fatal(err)
	}
	if m, err := taskcard.Done(ctx, c, "fix-b", "rowan", "done", ""); err != nil || m.To != "done" {
		t.Fatalf("done without a PR %+v %v", m, err)
	}
	if ok := c.HGet(ctx, "task:fix-b", "where_ok").Val(); ok != "ok" {
		t.Fatalf("done without a PR where_ok %q", ok)
	}
	clean(t, c, "done without a PR")
}

// TestOffGraphIsRefusedAndWritesNothing: every move off the graph, and a
// landed without its merge sha, is REFUSED with the record, the sets and
// ws:log untouched.
func TestOffGraphIsRefusedAndWritesNothing(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	for _, p := range []taskcard.PushRequest{
		{ID: "r1", Stream: stream, Friend: "rowan", Sprint: sprint},
		{ID: "w1", Stream: stream, Friend: "rowan", Sprint: sprint, DependsOn: "task:r1"},
	} {
		if _, err := taskcard.Push(ctx, c, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := taskcard.Take(ctx, c, "stella", 1, "stella", "r1"); err == nil {
		t.Fatal("stella took rowan's task")
	}
	for _, tc := range []struct {
		id, to string
		o      taskcard.Opts
		want   string
	}{
		{"r1", "landed", taskcard.Opts{Sha: "abc"}, "OFFGRAPH ready -> landed"},
		{"r1", "merging", taskcard.Opts{}, "OFFGRAPH ready -> merging"},
		{"w1", "working", taskcard.Opts{}, "OFFGRAPH waiting -> working"},
		{"r1", "done", taskcard.Opts{OK: "fail"}, "WHY ready -> done needs a why"},
		{"r1", "limbo", taskcard.Opts{}, "WHERE limbo"},
		{"nope", "ready", taskcard.Opts{}, "NOTASK task:nope"},
		{"r1", "ready", taskcard.Opts{Fields: []string{"where", "landed"}}, "FIELD where is the pointer"},
	} {
		dump := c.Dump(ctx, "task:"+tc.id).Val()
		log := c.XLen(ctx, "ws:log").Val()
		_, err := taskcard.Move(ctx, c, tc.id, tc.to, tc.o)
		why, ok := taskcard.IsRefused(err)
		if !ok || !strings.HasPrefix(why, tc.want) {
			t.Errorf("%s -> %s: %v, want REFUSED %s", tc.id, tc.to, err, tc.want)
		}
		if c.Dump(ctx, "task:"+tc.id).Val() != dump || c.XLen(ctx, "ws:log").Val() != log {
			t.Errorf("%s -> %s: a refusal wrote", tc.id, tc.to)
		}
	}
	// merging -> landed needs the sha.
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Done(ctx, c, "r1", "rowan", "pr", "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "r1", "landed", taskcard.Opts{}); err == nil || !strings.Contains(err.Error(), "SHA") {
		t.Fatalf("landed without a sha: %v", err)
	}
	if _, err := taskcard.Land(ctx, c, "r1", "lander", "abcdef12", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "r1", "ready", taskcard.Opts{Why: "again"}); err == nil {
		t.Fatal("landed -> ready was not refused")
	}
	clean(t, c, "off graph")
}

// TestFsckFindsDriftInjectedByHand: a set written around the one writer is
// found by fsck (one line per drift) and refused by the next move.
func TestFsckFindsDriftInjectedByHand(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	for _, id := range []string{"d1", "d2", "d3"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Stream: stream, Friend: "rowan", Sprint: sprint}); err != nil {
			t.Fatal(err)
		}
	}
	clean(t, c, "before")
	c.ZAdd(ctx, taskcard.StreamKey(stream, "working"), redis.Z{Score: 1, Member: "d1"}) // d1 in two places
	c.ZRem(ctx, taskcard.FriendKey("rowan", "ready"), "d2")                             // d2 unlinked
	c.SRem(ctx, "sprint:"+sprint+":idx:rowan:open", "d3")                               // d3 unindexed
	r, err := taskcard.Fsck(ctx, c, sprint)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.Lines, "\n")
	for _, want := range []string{"stray ws:" + stream + ":working d1", "unlinked friend:rowan:cards:ready d2",
		"unindexed sprint:" + sprint + ":idx:rowan:open d3"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fsck did not find %q in %v", want, r.Lines)
		}
	}
	if r.Drift != 3 {
		t.Errorf("drift %d, want 3: %v", r.Drift, r.Lines)
	}
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "d1"); err == nil || !strings.Contains(err.Error(), "DRIFT twice") {
		t.Fatalf("a move over drift: %v", err)
	}
}

// TestLeaseLapsedGoesBackToReady is Glenn 08:15 AM (rowan working=65 with two
// children alive): a take carries a lease the beat renews; expire moves a
// working task whose lease lapsed back to ready, so the friend's working
// ZCARD counts only held tasks.
func TestLeaseLapsedGoesBackToReady(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	for _, id := range []string{"l1", "l2"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Stream: stream, Friend: "rowan", Sprint: sprint}); err != nil {
			t.Fatal(err)
		}
	}
	if ids, err := taskcard.Take(ctx, c, "rowan", 2, "rowan"); err != nil || len(ids) != 2 {
		t.Fatalf("take %v %v", ids, err)
	}
	if _, err := taskcard.Beat(ctx, c, "l1", "stella"); err == nil {
		t.Fatal("stella beat rowan's task")
	}
	until, err := taskcard.Beat(ctx, c, "l1", "rowan")
	if err != nil || until <= time.Now().UnixMilli() {
		t.Fatalf("beat %d %v", until, err)
	}
	// l2's child died: its lease is in the past.
	c.HSet(ctx, "task:l2", "lease_until", time.Now().Add(-time.Minute).UnixMilli())
	ids, err := taskcard.Expire(ctx, c, "reconciler")
	if err != nil || len(ids) != 1 || ids[0] != "l2" {
		t.Fatalf("expire %v %v", ids, err)
	}
	if n := c.ZCard(ctx, taskcard.FriendKey("rowan", "working")).Val(); n != 1 {
		t.Fatalf("rowan working %d, want 1", n)
	}
	h := c.HGetAll(ctx, "task:l2").Val()
	if h["where"] != "ready" || h["why"] != "lease lapsed" || h["lease_until"] != "" || h["queue"] != "q:rowan" {
		t.Fatalf("expired record %v", h)
	}
	clean(t, c, "expire")
}

// TestLeaseLapsedSwarmGoesBackToReady is #4074: ns_deal_swarm parks a
// swarm-dealt task in friend:swarm:cards:working, but swarm is not a member
// of the friends set, so expire's default sweep (no friend named) must
// still visit it, or a task the swarm takes and then drops (its child dies
// mid-lease) is never reclaimed.
func TestLeaseLapsedSwarmGoesBackToReady(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "s1", Stream: stream, Sprint: sprint}); err != nil {
		t.Fatal(err)
	}
	// ns_deal_swarm takes an unowned ready card for the swarm (as=swarm),
	// into friend:swarm:cards:working, lease_until = now + 3m.
	if ids, err := taskcard.Take(ctx, c, "swarm", 1, "reconciler", "s1"); err != nil || len(ids) != 1 {
		t.Fatalf("take %v %v", ids, err)
	}
	if n := c.ZCard(ctx, taskcard.FriendKey("swarm", "working")).Val(); n != 1 {
		t.Fatalf("swarm working %d, want 1", n)
	}
	// swarm's child died: its lease is in the past.
	c.HSet(ctx, "task:s1", "lease_until", time.Now().Add(-time.Minute).UnixMilli())
	// The default sweep (no friend named, as the reconciler's task-lease
	// duty calls it) must still reach s1 even though "swarm" is never in
	// SMEMBERS friends.
	ids, err := taskcard.Expire(ctx, c, "reconciler")
	if err != nil || len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("expire %v %v, want [s1]", ids, err)
	}
	if n := c.ZCard(ctx, taskcard.FriendKey("swarm", "working")).Val(); n != 0 {
		t.Fatalf("swarm working %d, want 0", n)
	}
	h := c.HGetAll(ctx, "task:s1").Val()
	if h["where"] != "ready" || h["why"] != "lease lapsed" || h["lease_until"] != "" {
		t.Fatalf("expired record %v", h)
	}
	clean(t, c, "expire swarm")
}

// TestLandStreamLandsEveryMergingMember is the lander's step (Glenn 08:50 ET):
// every member of ws:<stream>:merging moves to landed at the merge sha in one
// call, and the reply names each member's PR and origin for the CLOSE line.
func TestLandStreamLandsEveryMergingMember(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	for i, id := range []string{"m1", "m2", "m3"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Stream: stream, Friend: "rowan", Sprint: sprint,
			Repo: "mas-bandwidth/nova-tools", PR: strconv.Itoa(3800 + i), Origin: "issue:" + id}); err != nil {
			t.Fatal(err)
		}
		if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", id); err != nil {
			t.Fatal(err)
		}
		if id != "m3" {
			if _, err := taskcard.Done(ctx, c, id, "rowan", "pr", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := taskcard.LandStream(ctx, c, stream, "abcdef12", "lander", "")
	if err != nil || len(r.Landed) != 2 || len(r.Refused) != 0 ||
		r.Landed[0] != (taskcard.Member{ID: "m1", Ref: "mas-bandwidth/nova-tools#3800", Origin: "issue:m1"}) {
		t.Fatalf("land stream %+v %v", r, err)
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(stream, "landed")).Val(); n != 2 || c.ZCard(ctx, taskcard.StreamKey(stream, "merging")).Val() != 0 {
		t.Fatalf("landed %d", n)
	}
	if w := c.HGet(ctx, "task:m3", "where").Val(); w != "working" {
		t.Fatalf("m3 (not merging) moved to %s", w)
	}
	clean(t, c, "land stream")
}
