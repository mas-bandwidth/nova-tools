package task_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// redisNowMS reads Redis server TIME, the same clock the transition functions
// use. The lease controls backdate stored timestamps against it; no wall-clock
// sleep decides an expiry (spec 2.1 rule 3).
func redisNowMS(t *testing.T, ctx context.Context, client *redis.Client) int64 {
	t.Helper()
	raw, err := client.Do(ctx, "TIME").Result()
	if err != nil {
		t.Fatalf("redis TIME: %v", err)
	}
	parts, ok := raw.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("redis TIME reply = %T; want two parts", raw)
	}
	seconds, err := strconv.ParseInt(fmt.Sprint(parts[0]), 10, 64)
	if err != nil {
		t.Fatalf("redis TIME seconds %v: %v", parts[0], err)
	}
	micros, err := strconv.ParseInt(fmt.Sprint(parts[1]), 10, 64)
	if err != nil {
		t.Fatalf("redis TIME micros %v: %v", parts[1], err)
	}
	return seconds*1000 + micros/1000
}

func zcard(t *testing.T, ctx context.Context, client *redis.Client, key string) int64 {
	t.Helper()
	n, err := client.ZCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("ZCARD %s: %v", key, err)
	}
	return n
}

// TestControl01EightClaimsOneChild is #2756 control 1 with the #2745 expiry
// transition: eight claimed tasks with one first beat leave seven starting and
// one living. The seven stale leases stay visible until the reconciler expiry
// runs; with a claimed_at backdated 70 s against Redis TIME they reopen,
// release their global starting leases and emit one receipt each, leaving
// starting 0 and living 1.
func TestControl01EightClaimsOneChild(t *testing.T) {
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
	if got := zcard(t, ctx, client, "friend:ctl-a:starting"); got != 7 {
		t.Fatalf("starting = %d; want 7", got)
	}
	if got := zcard(t, ctx, client, "friend:ctl-a:living"); got != 1 {
		t.Fatalf("living = %d; want 1", got)
	}

	// The seven with no start ack are 70 s stale; run the reconciler pass.
	stale := redisNowMS(t, ctx, client) - 70_000
	for _, claim := range claims[1:] {
		if err := client.HSet(ctx, "s:"+sprint+":task:"+claim.ID, "claimed_at", stale).Err(); err != nil {
			t.Fatalf("backdate %s: %v", claim.ID, err)
		}
	}
	expired, err := task.ExpireSprint(ctx, st, sprint, "reconciler", "")
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if expired != 7 {
		t.Fatalf("expired = %d; want 7", expired)
	}
	for _, claim := range claims[1:] {
		state, err := client.HGet(ctx, "s:"+sprint+":task:"+claim.ID, "state").Result()
		if err != nil || state != "open" {
			t.Fatalf("expired %s state = %q, %v; want open", claim.ID, state, err)
		}
	}
	if got := zcard(t, ctx, client, "friend:ctl-a:starting"); got != 0 {
		t.Fatalf("starting after expiry = %d; want 0", got)
	}
	if got := zcard(t, ctx, client, "friend:ctl-a:living"); got != 1 {
		t.Fatalf("living after expiry = %d; want 1", got)
	}
	if got, err := client.XLen(ctx, "cap:log").Result(); err != nil || got != 7 {
		t.Fatalf("slot-freed events = %d, %v; want 7", got, err)
	}
	count, err := receipts(ctx, client, "s:"+sprint+":log", "task expire", claims[1].ID)
	if err != nil || count != 1 {
		t.Fatalf("expire receipts for %s = %d, %v; want 1", claims[1].ID, count, err)
	}
}

// TestControl02TakeNeverBeat is #2756 control 2 with the #2745 expiry: a task
// taken and never beaten reopens after its 60 s start-ack window (and by the
// 180 s beat window), the old token's existing Done returns exit 3 FENCED, and
// the next claim gets attempt 2.
func TestControl02TakeNeverBeat(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-02deadbe"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-b", 1)

	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "never", Kind: task.KindWork, Title: "never",
		Effects: task.EffectsNone, To: "ctl-b",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push = %s, %v; want CREATED", got, err)
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "never", As: "ctl-b"})
	if err != nil || !ok {
		t.Fatalf("take = %v, %v; want one claim", ok, err)
	}
	if claim.Attempt != 1 || !strings.HasPrefix(claim.Token, "1.") {
		t.Fatalf("claim attempt=%d token=%q; want attempt 1", claim.Attempt, claim.Token)
	}

	// No beat: once the 60 s start window has passed the reconciler reopens.
	stale := redisNowMS(t, ctx, client) - 70_000
	if err := client.HSet(ctx, "s:"+sprint+":task:never", "claimed_at", stale).Err(); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if got, err := task.Expire(ctx, st, task.ExpireRequest{Sprint: sprint, ID: "never", Actor: "reconciler"}); err != nil || got != task.ExpireReopened {
		t.Fatalf("expire = %s, %v; want REOPENED", got, err)
	}
	state, err := client.HGet(ctx, "s:"+sprint+":task:never", "state").Result()
	if err != nil || state != "open" {
		t.Fatalf("state = %q, %v; want open", state, err)
	}
	if got := zcard(t, ctx, client, "friend:ctl-b:starting"); got != 0 {
		t.Fatalf("starting = %d; want 0", got)
	}
	if got, err := client.XLen(ctx, "cap:log").Result(); err != nil || got != 1 {
		t.Fatalf("slot-freed events = %d, %v; want 1", got, err)
	}
	if beat, err := task.Beat(ctx, st, task.BeatRequest{
		Sprint: sprint, ID: "never", Token: claim.Token,
	}); err != nil || beat != task.BeatFenced || beat.ExitCode() != 3 {
		t.Fatalf("beat with superseded token = %s, %v; want FENCED exit 3", beat, err)
	}

	// The old token is fenced: #2929's Done refuses with exit 3, unchanged.
	done, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "never", Token: claim.Token, Evidence: "DONE: pass\nPR: https://github.com/mas-bandwidth/nova-tools/pull/1",
	})
	if err != nil || done != task.DoneFenced || done.ExitCode() != 3 {
		t.Fatalf("done with superseded token = %s, %v; want FENCED exit 3", done, err)
	}

	// The next claim is attempt 2 with a fresh token.
	again, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "never", As: "ctl-b"})
	if err != nil || !ok {
		t.Fatalf("second take = %v, %v; want one claim", ok, err)
	}
	if again.Attempt != 2 || !strings.HasPrefix(again.Token, "2.") {
		t.Fatalf("second claim attempt=%d token=%q; want attempt 2", again.Attempt, again.Token)
	}
}

// TestTaskWorkingStaleExpiry is the #2745 working-stale rule: a working
// task whose beat is stale for 180 s reopens only if its effects are none or
// idempotent; an external-effect task becomes reconcile-required with an
// unresolved item and nothing is replayed.
func TestTaskWorkingStaleExpiry(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-05abc123"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-c", 2)

	parts := []struct {
		id      string
		effects task.Effects
	}{
		{"w0", task.EffectsIdempotent},
		{"w1", task.EffectsExternal},
	}
	for _, p := range parts {
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: p.id, Kind: task.KindWork, Title: p.id,
			Effects: p.effects, To: "ctl-c",
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v; want CREATED", p.id, got, err)
		}
		claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: p.id, As: "ctl-c"})
		if err != nil || !ok {
			t.Fatalf("take %s = %v, %v", p.id, ok, err)
		}
		if got, err := task.Beat(ctx, st, task.BeatRequest{Sprint: sprint, ID: p.id, Token: claim.Token}); err != nil || got != task.BeatWorking {
			t.Fatalf("first beat %s = %s, %v; want WORKING", p.id, got, err)
		}
	}

	stale := redisNowMS(t, ctx, client) - 181_000
	for _, p := range parts {
		if err := client.HSet(ctx, "s:"+sprint+":task:"+p.id, "beat_at", stale).Err(); err != nil {
			t.Fatalf("backdate %s: %v", p.id, err)
		}
	}
	if got, err := task.Expire(ctx, st, task.ExpireRequest{Sprint: sprint, ID: "w0", Actor: "reconciler"}); err != nil || got != task.ExpireExpired {
		t.Fatalf("expire w0 = %s, %v; want EXPIRED", got, err)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:w0", "state").Result(); state != "open" {
		t.Fatalf("w0 state = %q; want open", state)
	}
	if got, err := task.Expire(ctx, st, task.ExpireRequest{Sprint: sprint, ID: "w1", Actor: "reconciler"}); err != nil || got != task.ExpireReconcile {
		t.Fatalf("expire w1 = %s, %v; want RECONCILE", got, err)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:w1", "state").Result(); state != "reconcile-required" {
		t.Fatalf("w1 state = %q; want reconcile-required", state)
	}
	unresolved, err := client.HExists(ctx, "s:"+sprint+":unresolved", "w1:beat-timeout:").Result()
	if err != nil || !unresolved {
		t.Fatalf("w1 unresolved evidence present = %v, %v; want true", unresolved, err)
	}
	if got := zcard(t, ctx, client, "friend:ctl-c:living"); got != 0 {
		t.Fatalf("living after expiry = %d; want 0", got)
	}
}

// TestTaskCancelExternal is the #2745 cancel rule: cancelling an
// external-effect task goes reconcile-required with unresolved evidence, not
// open, and the old token's Done refuses exit 3.
func TestTaskCancelExternal(t *testing.T) {
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
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:c0", "state").Result(); state != "open" {
		t.Fatalf("c0 state = %q; want open", state)
	}
	if got, err := task.Cancel(ctx, st, task.CancelRequest{
		Sprint: sprint, ID: "c1", Token: claims["c1"].Token, Reason: "give back",
	}); err != nil || got != task.CancelReconcile {
		t.Fatalf("cancel c1 = %s, %v; want RECONCILE", got, err)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:c1", "state").Result(); state != "reconcile-required" {
		t.Fatalf("c1 state = %q; want reconcile-required", state)
	}
	unresolved, err := client.HExists(ctx, "s:"+sprint+":unresolved", "c1:cancel-external:").Result()
	if err != nil || !unresolved {
		t.Fatalf("c1 unresolved evidence present = %v, %v; want true", unresolved, err)
	}
	done, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "c1", Token: claims["c1"].Token, Evidence: "DONE: pass\nPR: https://github.com/mas-bandwidth/nova-tools/pull/1",
	})
	if err != nil || done != task.DoneFenced || done.ExitCode() != 3 {
		t.Fatalf("done with cancelled token = %s, %v; want FENCED exit 3", done, err)
	}
	if got := zcard(t, ctx, client, "friend:ctl-d:starting"); got != 0 {
		t.Fatalf("starting after cancels = %d; want 0", got)
	}
	if got, err := client.XLen(ctx, "cap:log").Result(); err != nil || got != 2 {
		t.Fatalf("slot-freed events after cancels = %d, %v; want 2", got, err)
	}
}
