//go:build functional

package task_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

func zcard(t *testing.T, ctx context.Context, client *redis.Client, key string) int64 {
	t.Helper()
	n, err := client.ZCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("ZCARD %s: %v", key, err)
	}
	return n
}

// TestControl01EightClaimsOneChild is #2756 control 1: eight claimed tasks
// with one first beat hold eight leases in the friend's one working set
// (#3998; the beat moves nothing). The #2745 expiry half of the control went
// with task.Expire (2026-09-26): no verb ran that reconciler pass any more.
func TestControl01EightClaimsOneChild(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-01c0ffee"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-a", 8)

	const tasks = 8
	for i := 0; i < tasks; i++ {
		id := fmt.Sprintf("t%d", i)
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: id,
			Effects: task.EffectsNone, To: "ctl-a",
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v; want CREATED", id, got, err)
		}
	}
	claims, err := task.TakeAvailable(ctx, st, "ctl-a", sprint, "", 0, "ctl-a", "")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(claims) != tasks {
		t.Fatalf("claims = %d; want %d", len(claims), tasks)
	}

	// One child beats: its start ack moves its lease from starting to living.
	first := claims[0]
	if got, err := task.Beat(ctx, st, task.BeatRequest{
		Sprint: sprint, ID: first.ID, Token: first.Token, Actor: "ctl-a",
	}); err != nil || got != task.BeatWorking {
		t.Fatalf("first beat = %s, %v; want WORKING", got, err)
	}
	if got := zcard(t, ctx, client, "friend:ctl-a:cards:working"); got != 8 {
		t.Fatalf("working = %d; want 8", got)
	}
}

// TestTaskCancelExternal is the #2745 cancel rule: cancelling an
// external-effect task goes reconcile-required with unresolved evidence, not
// open, and the old token's Done refuses exit 3.
func TestTaskCancelExternal(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-06beef01"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-d", 2)

	claims := map[string]task.Claim{}
	for _, p := range []struct {
		id      string
		effects task.Effects
	}{
		{"c0", task.EffectsNone},
		{"c1", task.EffectsExternal},
	} {
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: p.id, Kind: task.KindWork, Title: p.id,
			Effects: p.effects, To: "ctl-d",
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v; want CREATED", p.id, got, err)
		}
		claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: p.id, As: "ctl-d"})
		if err != nil || !ok {
			t.Fatalf("take %s = %v, %v", p.id, ok, err)
		}
		claims[p.id] = claim
	}

	if got, err := task.Cancel(ctx, st, task.CancelRequest{
		Sprint: sprint, ID: "c0", Token: claims["c0"].Token, Reason: "give back",
	}); err != nil || got != task.CancelOpen {
		t.Fatalf("cancel c0 = %s, %v; want OPEN", got, err)
	}
	if state, _ := client.HGet(ctx, "task:c0", "state").Result(); state != "open" {
		t.Fatalf("c0 state = %q; want open", state)
	}
	if got, err := task.Cancel(ctx, st, task.CancelRequest{
		Sprint: sprint, ID: "c1", Token: claims["c1"].Token, Reason: "give back",
	}); err != nil || got != task.CancelReconcile {
		t.Fatalf("cancel c1 = %s, %v; want RECONCILE", got, err)
	}
	if state, _ := client.HGet(ctx, "task:c1", "state").Result(); state != "reconcile-required" {
		t.Fatalf("c1 state = %q; want reconcile-required", state)
	}
	unresolved, err := client.HExists(ctx, "s:"+sprint+":unresolved", "c1:cancel-external:").Result()
	if err != nil || !unresolved {
		t.Fatalf("c1 unresolved evidence present = %v, %v; want true", unresolved, err)
	}
	done, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "c1", Token: claims["c1"].Token, Evidence: "https://example.test/e",
	})
	if err != nil || done != task.DoneFenced || done.ExitCode() != 3 {
		t.Fatalf("done with cancelled token = %s, %v; want FENCED exit 3", done, err)
	}
	if got := zcard(t, ctx, client, "friend:ctl-d:cards:working"); got != 0 {
		t.Fatalf("starting after cancels = %d; want 0", got)
	}
	if got, err := client.XLen(ctx, "cap:log").Result(); err != nil || got != 2 {
		t.Fatalf("slot-freed events after cancels = %d, %v; want 2", got, err)
	}
}
