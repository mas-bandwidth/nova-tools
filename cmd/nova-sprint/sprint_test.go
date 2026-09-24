package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// sprintRedis starts a throwaway redis-server with the function library
// loaded, the same shape as capacity_test.go.
func sprintRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.UserEnv, "")
	return addr, client
}

// TestSprintOpenLetsTaskTakeClaim is the #2939 DONE-WHEN: task take on an
// unopened sprint prints NONE; after sprint open the same take claims; a
// second open prints the same line; close and status round-trip.
func TestSprintOpenLetsTaskTakeClaim(t *testing.T) {
	addr, client := sprintRedis(t)
	ctx := context.Background()
	const s = "control-2939"
	t.Setenv("NOVA_FRIEND", "ctl-open") // #2929: push and take run from a seat
	client.SAdd(ctx, "friends", "ctl-open")
	client.HSet(ctx, "friend:ctl-open:desired", "slots", 2, "paused", "0")
	client.HSet(ctx, "friend:ctl-open:beat", "host", "fixture")

	step := func(want string, args ...string) {
		t.Helper()
		code, out, errOut := runSprint(args...)
		if code != 0 || out != want+"\n" {
			t.Fatalf("%s: code=%d out=%q stderr=%q; want 0 %q", strings.Join(args, " "), code, out, errOut, want)
		}
	}
	step("PUSH CREATED id=t1", "task", "push", "--redis", addr, "--sprint", s, "--id", "t1",
		"--title", "first", "--payload-sha", "p1", "--to", "ctl-open")
	step(s+" status=absent", "sprint", "status", "--redis", addr, "--sprint", s)
	step("NONE", "task", "take", "--redis", addr, "--sprint", s, "--as", "ctl-open")

	step(s+" status=open", "sprint", "open", "--redis", addr, "--sprint", s)
	step(s+" status=open", "sprint", "open", "--redis", addr, "--sprint", s)
	if st, _ := client.HGet(ctx, "s:"+s, "status").Result(); st != "open" {
		t.Fatalf("s:%s status=%q want open", s, st)
	}
	if ok, _ := client.SIsMember(ctx, "sprints", s).Result(); !ok {
		t.Fatalf("sprints does not hold %s after open", s)
	}
	if n, _ := client.ZCard(ctx, "sprint:order").Result(); n != 1 {
		t.Fatalf("sprint:order has %d members after two opens, want 1", n)
	}
	step(s+" status=open", "sprint", "status", "--redis", addr, "--sprint", s)

	code, out, errOut := runSprint("task", "take", "--redis", addr, "--as", "ctl-open")
	if code != 0 || !strings.HasPrefix(out, "CLAIMED "+s+"/t1 attempt=1 ") {
		t.Fatalf("take after open: code=%d out=%q stderr=%q; want CLAIMED %s/t1", code, out, errOut, s)
	}

	step(s+" status=closed", "sprint", "close", "--redis", addr, "--sprint", s)
	step(s+" status=closed", "sprint", "close", "--redis", addr, "--sprint", s)
	if ok, _ := client.SIsMember(ctx, "sprints", s).Result(); ok {
		t.Fatalf("sprints still holds %s after close", s)
	}
	step(s+" status=closed", "sprint", "status", "--redis", addr, "--sprint", s)
}

func TestSprintRefusesABadName(t *testing.T) {
	for _, args := range [][]string{
		{"sprint"},
		{"sprint", "reopen", "--sprint", "x"},
		{"sprint", "open", "--redis", "127.0.0.1:1"},
		{"sprint", "open", "--redis", "127.0.0.1:1", "--sprint", "Bad Name"},
	} {
		code, out, errOut := runSprint(args...)
		if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint sprint") {
			t.Errorf("%v: code=%d out=%q stderr=%q; want exit 2 and one refusal", args, code, out, errOut)
		}
	}
}
