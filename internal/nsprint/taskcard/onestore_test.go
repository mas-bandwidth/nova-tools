//go:build functional

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
	p.HSet(ctx, "friend:rowan:beat", "host", "fixture", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
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
	if h["where"] != "working" || h["state"] != "claimed" || c.ZCard(ctx, taskcard.FriendKeyAt(0, "rowan", "working")).Val() != 1 {
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
