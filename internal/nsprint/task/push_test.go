package task_test

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func TestPushEstDomain(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "domain-abcdef01"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-a", 4)

	// 1. --est 45 -> est 45
	push45 := task.PushRequest{
		Sprint: sprint, ID: "t-est-45", Kind: task.KindWork, Title: "task 45",
		Effects: task.EffectsNone, To: "ctl-a", Est: "45",
	}
	got, err := task.Push(ctx, st, push45)
	if err != nil || got != task.PushCreated {
		t.Fatalf("--est 45 push = %s, %v; want CREATED", got, err)
	}
	if v := client.HGet(ctx, "task:t-est-45", "est").Val(); v != "45" {
		t.Fatalf("stored est = %q; want 45", v)
	}

	// 2. title est: 30 min -> est 30
	pushTitle := task.PushRequest{
		Sprint: sprint, ID: "t-title-30", Kind: task.KindWork, Title: "task est: 30 min",
		Effects: task.EffectsNone, To: "ctl-a",
	}
	got, err = task.Push(ctx, st, pushTitle)
	if err != nil || got != task.PushCreated {
		t.Fatalf("title est 30 push = %s, %v; want CREATED", got, err)
	}
	if v := client.HGet(ctx, "task:t-title-30", "est").Val(); v != "30" {
		t.Fatalf("stored est = %q; want 30", v)
	}

	// 3. neither -> est ""
	pushNeither := task.PushRequest{
		Sprint: sprint, ID: "t-neither", Kind: task.KindWork, Title: "plain task",
		Effects: task.EffectsNone, To: "ctl-a",
	}
	got, err = task.Push(ctx, st, pushNeither)
	if err != nil || got != task.PushCreated {
		t.Fatalf("neither push = %s, %v; want CREATED", got, err)
	}
	if v := client.HGet(ctx, "task:t-neither", "est").Val(); v != "" {
		t.Fatalf("stored est = %q; want empty", v)
	}

	// Boundary: --est 1 accepted and stored as-is
	push1 := task.PushRequest{
		Sprint: sprint, ID: "t-est-1", Kind: task.KindWork, Title: "task 1",
		Effects: task.EffectsNone, To: "ctl-a", Est: "1",
	}
	got, err = task.Push(ctx, st, push1)
	if err != nil || got != task.PushCreated {
		t.Fatalf("--est 1 push = %s, %v; want CREATED", got, err)
	}
	if v := client.HGet(ctx, "task:t-est-1", "est").Val(); v != "1" {
		t.Fatalf("stored est = %q; want 1", v)
	}

	// Boundary: --est 10080 accepted and stored as-is
	push10080 := task.PushRequest{
		Sprint: sprint, ID: "t-est-10080", Kind: task.KindWork, Title: "task 10080",
		Effects: task.EffectsNone, To: "ctl-a", Est: "10080",
	}
	got, err = task.Push(ctx, st, push10080)
	if err != nil || got != task.PushCreated {
		t.Fatalf("--est 10080 push = %s, %v; want CREATED", got, err)
	}
	if v := client.HGet(ctx, "task:t-est-10080", "est").Val(); v != "10080" {
		t.Fatalf("stored est = %q; want 10080", v)
	}

	// 4. --est 0, -5, 1.5, abc, 007, 10081 -> INVALID, nothing written (EXISTS = 0)
	for _, bad := range []string{"0", "-5", "1.5", "abc", "007", "10081"} {
		id := "t-bad-" + bad
		var errBuf bytes.Buffer
		got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: "bad task",
			Effects: task.EffectsNone, To: "ctl-a", Est: bad, ErrOut: &errBuf,
		})
		if err != nil {
			t.Fatalf("--est %s returned error: %v", bad, err)
		}
		if got != task.PushInvalid {
			t.Fatalf("--est %s got = %s; want INVALID", bad, got)
		}
		if client.Exists(ctx, "task:"+id).Val() != 0 {
			t.Fatalf("--est %s wrote task hash; want EXISTS = 0", bad)
		}
		wantErr := fmt.Sprintf("INVALID est=%s: whole minutes 1..10080", bad)
		if !strings.Contains(errBuf.String(), wantErr) {
			t.Fatalf("--est %s output %q does not contain %q", bad, errBuf.String(), wantErr)
		}
	}

	// 5. title est: 0 min, est: -5 min -> est "" plus the WARN line
	for _, badTitle := range []string{"0", "-5"} {
		id := "t-title-bad-" + badTitle
		var warnBuf bytes.Buffer
		got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: fmt.Sprintf("title est: %s min", badTitle),
			Effects: task.EffectsNone, To: "ctl-a", ErrOut: &warnBuf,
		})
		if err != nil || got != task.PushCreated {
			t.Fatalf("title est %s push = %s, %v; want CREATED", badTitle, got, err)
		}
		if v := client.HGet(ctx, "task:"+id, "est").Val(); v != "" {
			t.Fatalf("stored est for title %s = %q; want empty", badTitle, v)
		}
		wantWarn := fmt.Sprintf("WARN est: title value %s outside 1..10080, stored empty", badTitle)
		if !strings.Contains(warnBuf.String(), wantWarn) {
			t.Fatalf("warn output %q does not contain %q", warnBuf.String(), wantWarn)
		}
	}

	// 6. Direct ns_task_push FCALL with est 0 or -5 returns INVALID
	for _, bad := range []string{"0", "-5"} {
		id := "t-fcall-bad-" + bad
		reply, err := client.FCall(ctx, "ns_task_push", nil,
			sprint, id, "work", "title", "none", "", "0", "", "", "ctl-a", "0", "0", "sha-"+id, "actor", "idem", bad).Result()
		if err != nil {
			t.Fatalf("direct FCALL with est %s failed: %v", bad, err)
		}
		status, ok := reply.([]any)
		if !ok || len(status) == 0 || status[0] != "INVALID" {
			t.Fatalf("direct FCALL with est %s reply = %v; want [INVALID]", bad, reply)
		}
		if client.Exists(ctx, "task:"+id).Val() != 0 {
			t.Fatalf("direct FCALL with est %s wrote task hash; want EXISTS = 0", bad)
		}
	}
}

func TestPushWritesEstAndPushedAt(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "est-pushed-at-01"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-a", 4)

	// --est 45 stores est=45 and pushed_at ms timestamp
	push := task.PushRequest{
		Sprint: sprint, ID: "t1", Kind: task.KindWork, Title: "task 1",
		Effects: task.EffectsNone, To: "ctl-a", Est: "45",
	}
	got, err := task.Push(ctx, st, push)
	if err != nil || got != task.PushCreated {
		t.Fatalf("push t1 = %s, %v; want CREATED", got, err)
	}
	hash := client.HGetAll(ctx, "task:t1").Val()
	if hash["est"] != "45" {
		t.Fatalf("hash[est] = %q; want 45", hash["est"])
	}
	pushedAt, err := strconv.ParseInt(hash["pushed_at"], 10, 64)
	if err != nil || pushedAt <= 0 {
		t.Fatalf("hash[pushed_at] = %q; want positive ms timestamp", hash["pushed_at"])
	}

	// Re-push with identical est returns PushExists
	again, err := task.Push(ctx, st, push)
	if err != nil || again != task.PushExists {
		t.Fatalf("re-push with same est = %s, %v; want EXISTS", again, err)
	}

	// Est is part of payload_sha, so re-push with different est returns CONFLICT
	pushDiffEst := push
	pushDiffEst.Est = "50"
	pushDiffEst.PayloadSHA = "" // recompute SHA with new Est
	conflict, err := task.Push(ctx, st, pushDiffEst)
	if err != nil || conflict != task.PushConflict {
		t.Fatalf("re-push with different est = %s, %v; want CONFLICT", conflict, err)
	}

	// A --front push with priority 0 (or no priority) stores priority=1 and score -1
	frontPush := task.PushRequest{
		Sprint: sprint, ID: "t-front", Kind: task.KindWork, Title: "front task",
		Effects: task.EffectsNone, To: "ctl-a", Front: true, Priority: 0, Est: "10",
	}
	got, err = task.Push(ctx, st, frontPush)
	if err != nil || got != task.PushCreated {
		t.Fatalf("front push = %s, %v; want CREATED", got, err)
	}
	frontHash := client.HGetAll(ctx, "task:t-front").Val()
	if frontHash["priority"] != "1" {
		t.Fatalf("front task priority = %q; want 1", frontHash["priority"])
	}
	score, err := client.ZScore(ctx, "s:"+sprint+":open:ctl-a", "t-front").Result()
	if err != nil || score != -1.0 {
		t.Fatalf("front task zscore = %v, %v; want -1", score, err)
	}

	// Direct FCALL bypassing Go with front and priority 0 also stores priority 1 and score -1
	reply, err := client.FCall(ctx, "ns_task_push", nil,
		sprint, "t-front-raw", "work", "raw front", "none", "", "0", "", "", "ctl-a", "1", "0", "sha-raw", "actor", "idem", "15").Result()
	if err != nil {
		t.Fatalf("direct front FCALL failed: %v", err)
	}
	status, ok := reply.([]any)
	if !ok || len(status) == 0 || status[0] != "CREATED" {
		t.Fatalf("direct front FCALL reply = %v; want [CREATED]", reply)
	}
	rawHash := client.HGetAll(ctx, "task:t-front-raw").Val()
	if rawHash["priority"] != "1" {
		t.Fatalf("direct front priority = %q; want 1", rawHash["priority"])
	}
	rawScore, err := client.ZScore(ctx, "s:"+sprint+":open:ctl-a", "t-front-raw").Result()
	if err != nil || rawScore != -1.0 {
		t.Fatalf("direct front zscore = %v, %v; want -1", rawScore, err)
	}
}
