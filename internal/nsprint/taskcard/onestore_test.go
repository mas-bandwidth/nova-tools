package taskcard_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// TestMigrateFoldsTheSprintStore is the one-store ruling (Glenn 2026-09-25
// 09:35 ET): task migrate moves every s:<S>:task:<id> record to task:<id>
// (sprint a field), places it through the one move, fences a stale claim and
// leaves a key whose id the one store already holds, named, untouched.
func TestMigrateFoldsTheSprintStore(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	old := func(id string) string { return "s:" + sprint + ":task:" + id }
	hour := strconv.FormatInt(time.Now().Add(-time.Hour).UnixMilli(), 10)
	p := c.Pipeline()
	p.HSet(ctx, old("o1"), "kind", "work", "title", "open work", "state", "open", "owner", "", "dest", "rowan",
		"priority", "3", "attempt", "0", "token", "0", "payload_sha", "x", "pushed_at", hour)
	p.ZAdd(ctx, "s:"+sprint+":open:rowan", redis.Z{Score: 3, Member: "o1"})
	p.SAdd(ctx, "s:"+sprint+":idx:task:open", "o1")
	p.HSet(ctx, old("w1"), "kind", "work", "title", "stale claim", "state", "working", "owner", "rowan", "dest", "rowan",
		"attempt", "1", "token", "1.abc", "beat_at", hour, "claimed_at", hour, "payload_sha", "y")
	p.SAdd(ctx, "s:"+sprint+":idx:task:working", "w1")
	p.HSet(ctx, old("h1"), "kind", "work", "title", "held", "state", "open", "payload_sha", "z")
	p.HSet(ctx, "task:h1", "kind", "read", "title", "a card with the same id", "owner", "stella", "state", "open")
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := taskcard.Migrate(ctx, c, sprint, "rowan", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Folded["ready"] != 2 || len(res.Held) != 1 || res.Held[0] != old("h1") {
		t.Fatalf("fold %+v", res)
	}
	if c.Exists(ctx, old("o1"), old("w1")).Val() != 0 || c.Exists(ctx, old("h1")).Val() != 1 {
		t.Fatal("the folded records did not move, or the held one moved")
	}
	h := c.HGetAll(ctx, "task:o1").Val()
	if h["where"] != "ready" || h["friend"] != "rowan" || h["sprint"] != sprint || h["payload_sha"] != "x" {
		t.Fatalf("o1 %v", h)
	}
	if s := c.ZScore(ctx, "s:"+sprint+":open:rowan", "o1").Val(); s != 3 {
		t.Fatalf("o1 left its queue score: %v", s)
	}
	if h := c.HGetAll(ctx, "task:w1").Val(); h["where"] != "ready" || h["token"] != "fenced" {
		t.Fatalf("stale claim %v", h)
	}
	clean(t, c, "after fold")
}

// TestFriendServeTakesAPushedCard: a task pushed as a card (task push
// --actor, friend-queue's push, a route) is on the friend's sprint queue, so
// friend serve's take (task.TakeAvailable, the sprint store's guarded take)
// claims it through the one move: one store, zero hand steps.
func TestFriendServeTakesAPushedCard(t *testing.T) {
	t.Parallel()

	addr, c := wstest.Start(t)
	ctx := context.Background()
	p := c.Pipeline()
	p.SAdd(ctx, "friends", "rowan")
	p.HSet(ctx, "friend:rowan:desired", "slots", "4", "paused", "0")
	p.HSet(ctx, "friend:rowan:beat", "host", "fixture")
	p.HSet(ctx, "s:"+sprint, "status", "open")
	p.SAdd(ctx, "sprints", sprint)
	p.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "rowan-1", Stream: stream, Friend: "rowan", Sprint: sprint,
		Kind: "work", Title: "rowan's queue", By: "coordinator"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	claims, err := task.TakeAvailable(ctx, st, "rowan", sprint, "", 1, "rowan", "")
	if err != nil || len(claims) != 1 || claims[0].ID != "rowan-1" {
		t.Fatalf("friend serve's take %+v %v", claims, err)
	}
	h := c.HGetAll(ctx, "task:rowan-1").Val()
	if h["where"] != "working" || h["state"] != "claimed" || c.ZCard(ctx, taskcard.FriendKey("rowan", "working")).Val() != 1 {
		t.Fatalf("taken card %v", h)
	}
	if r, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: "rowan-1", Token: claims[0].Token, Evidence: "done"}); err != nil || r != task.DoneClosed {
		t.Fatalf("done %v %v", r, err)
	}
	if w := c.HGet(ctx, "task:rowan-1", "where").Val(); w != "done" {
		t.Fatalf("where after done %q", w)
	}
	clean(t, c, "serve")
}
