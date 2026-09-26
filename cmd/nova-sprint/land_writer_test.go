package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestLandWriterUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := runSprint("land", "writer")
	if code != 2 || !strings.Contains(errOut, "needs --repo") {
		t.Fatalf("bare land writer: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev", "extra")
	if code != 2 || !strings.Contains(errOut, "takes flags") {
		t.Fatalf("positional arg: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev", "--to", "invalid", "--redis", "127.0.0.1:6379")
	if code != 2 || !strings.Contains(errOut, "--to must be old-loop or nova-sprint") {
		t.Fatalf("invalid --to: code=%d err=%q", code, errOut)
	}

	code, _, errOut = runSprint("land", "writer", "--repo", "o/n", "--base", "dev")
	if code != 2 || !strings.Contains(errOut, "--redis <addr> is required") {
		t.Fatalf("missing --redis: code=%d err=%q", code, errOut)
	}
}

func TestLandWriterCutoverAndRollback(t *testing.T) {
	t.Parallel()

	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	repo := "mas-bandwidth/nova-tools"
	base := "dev"

	// 1. Initial query before any writer is set: defaults to gen=0 owner=old-loop
	code, out, errOut := runSprint("land", "writer", "--repo", repo, "--base", base, "--redis", addr)
	if code != 0 {
		t.Fatalf("initial get: code=%d err=%q", code, errOut)
	}
	wantInitial := "WRITER repo=mas-bandwidth/nova-tools base=dev gen=0 owner=old-loop\n"
	if out != wantInitial {
		t.Fatalf("initial get: got %q, want %q", out, wantInitial)
	}

	// 2. Cutover with inflight > 0 refused via --inflight flag
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--to", "nova-sprint", "--redis", addr, "--inflight", "2")
	if code != 2 || !strings.Contains(errOut, "REFUSED inflight count=2") {
		t.Fatalf("cutover with --inflight 2: code=%d out=%q err=%q", code, out, errOut)
	}

	// 3. Cutover with inflight in Redis key
	inflightKey := "land:" + repo + ":" + base + ":inflight"
	if err := client.Set(ctx, inflightKey, "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--to", "nova-sprint", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "REFUSED inflight count=1") {
		t.Fatalf("cutover with inflight key: code=%d out=%q err=%q", code, out, errOut)
	}
	_ = client.Del(ctx, inflightKey)

	// 4. Cutover to nova-sprint succeeds
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--to", "nova-sprint", "--redis", addr, "--by", "rowan")
	if code != 0 {
		t.Fatalf("cutover code=%d err=%q", code, errOut)
	}
	wantCutover := "WRITER repo=mas-bandwidth/nova-tools base=dev gen=1 owner=nova-sprint\n"
	if out != wantCutover {
		t.Fatalf("cutover: got %q, want %q", out, wantCutover)
	}

	// Verify Redis hash
	writerKey := "land:" + repo + ":" + base + ":writer"
	vals, err := client.HGetAll(ctx, writerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if vals["gen"] != "1" || vals["owner"] != "nova-sprint" || vals["by"] != "rowan" || vals["since"] == "" {
		t.Fatalf("writer hash mismatch: %+v", vals)
	}

	// Verify events stream
	eventsKey := "land:" + repo + ":events"
	xmsgs, err := client.XRevRange(ctx, eventsKey, "+", "-").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(xmsgs) == 0 {
		t.Fatalf("expected events in %s, got none", eventsKey)
	}
	f := xmsgs[0].Values
	if f["event"] != "WRITER" || f["gen"] != "1" || f["owner"] != "nova-sprint" || f["by"] != "rowan" {
		t.Fatalf("event record mismatch: %+v", f)
	}

	// 5. Query shows current state
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--redis", addr)
	if code != 0 || out != wantCutover {
		t.Fatalf("query after cutover: code=%d out=%q want=%q err=%q", code, out, wantCutover, errOut)
	}

	// 5b. #3485 rowan hold item 3 (#3139 10.2 "bumps gen after resolving any pub:* by 7.2"):
	// rollback is REFUSED while pub:active names an intent that is not resolved, and the
	// writer hash is unchanged; once the intent is dead the rollback goes through.
	pubActive := "land:" + repo + ":" + base + ":pub:active"
	pubB1 := "land:" + repo + ":" + base + ":pub:b1"
	if err := client.Set(ctx, pubActive, "b1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, pubB1, "state", "intent").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--to", "old-loop", "--redis", addr, "--by", "emma")
	if code != 2 || out != "" || !strings.Contains(errOut, "REFUSED pub=b1 ") {
		t.Fatalf("rollback with unresolved intent: code=%d out=%q err=%q", code, out, errOut)
	}
	vals, err = client.HGetAll(ctx, writerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if vals["gen"] != "1" || vals["owner"] != "nova-sprint" {
		t.Fatalf("refused rollback wrote the writer hash: %+v", vals)
	}
	if err := client.HSet(ctx, pubB1, "state", "dead").Err(); err != nil {
		t.Fatal(err)
	}

	// 6. Rollback to old-loop succeeds and bumps gen
	code, out, errOut = runSprint("land", "writer", "--repo", repo, "--base", base, "--to", "old-loop", "--redis", addr, "--by", "emma")
	if code != 0 {
		t.Fatalf("rollback code=%d err=%q", code, errOut)
	}
	wantRollback := "WRITER repo=mas-bandwidth/nova-tools base=dev gen=2 owner=old-loop\n"
	if out != wantRollback {
		t.Fatalf("rollback: got %q, want %q", out, wantRollback)
	}

	vals, err = client.HGetAll(ctx, writerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if vals["gen"] != "2" || vals["owner"] != "old-loop" || vals["by"] != "emma" {
		t.Fatalf("rollback hash mismatch: %+v", vals)
	}
}
