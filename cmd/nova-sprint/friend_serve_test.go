package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// TestHelperServeDispatch is the fake harness the serve tests declare as the
// seat's dispatch: this test binary, which writes one DONE line when
// NOVA_SERVE_FAKE is set and is otherwise an empty test.
func TestHelperServeDispatch(t *testing.T) {
	if os.Getenv("NOVA_SERVE_FAKE") == "" {
		return
	}
	fmt.Printf("DONE served %s\n", os.Getenv(life.ServeEnvID))
}

// TestFriendServeUsageRefusalsOpenNoStore: every usage refusal exits 2 before
// any dial (the --redis address is unreachable on purpose).
func TestFriendServeUsageRefusalsOpenNoStore(t *testing.T) {
	const dead = "127.0.0.1:1"
	refused := func(env string, args ...string) string {
		t.Helper()
		if env != "" {
			t.Setenv(seatEnv, env)
		} else {
			t.Setenv(seatEnv, "")
		}
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (%s%s)", args, code, out.String(), errOut.String())
		}
		return errOut.String()
	}
	if got := refused("emma", "friend", "serve", "--redis", dead); !strings.Contains(got, "--as is required") {
		t.Fatalf("no --as: %s", got)
	}
	if got := refused("", "friend", "serve", "--as", "emma", "--redis", dead); !strings.Contains(got, "NOVA_FRIEND") {
		t.Fatalf("no seat: %s", got)
	}
	if got := refused("rowan", "friend", "wake", "--as", "emma", "--redis", dead); !strings.Contains(got, "equal to NOVA_FRIEND") {
		t.Fatalf("seat mismatch: %s", got)
	}
	if got := refused("emma", "friend", "serve", "--as", "emma", "--dispatch", "x", "--redis", dead, "--", "y"); !strings.Contains(got, "not both") {
		t.Fatalf("two harnesses: %s", got)
	}
	if got := refused("emma", "friend", "serve", "--as", "emma", "--width", "-1", "--redis", dead); !strings.Contains(got, "--width") {
		t.Fatalf("bad width: %s", got)
	}
}

// TestFriendServeVerb drives the seat through the CLI against a throwaway
// Redis: no declared dispatch refuses (exit 1, naming cfg:friend:<f>), a held
// seat refuses (exit 1, naming the holder), and `friend wake --as` with the
// declared dispatch takes the one ready task, runs the fake harness and
// closes the task with its DONE line.
func TestFriendServeVerb(t *testing.T) {
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	st := store.New(client)
	if err := client.HSet(ctx, "s:s1", "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "friends", "emma").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:emma:desired", "slots", "2", "machine", "studio").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "machine:studio:ceiling", "slots", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: "s1", ID: "w1", Kind: task.KindWork, Title: "build w1",
		Effects: task.EffectsNone, PayloadSHA: "w1", To: "emma",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push: %s, %v", got, err)
	}
	t.Setenv(seatEnv, "emma")
	t.Setenv("NOVA_SERVE_FAKE", "done")
	dir := t.TempDir()
	base := []string{"friend", "wake", "--as", "emma", "--redis", addr, "--sprint", "s1", "--dir", dir, "--host", "studio"}

	var out, errOut bytes.Buffer
	if code := run(base, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "cfg:friend:emma") {
		t.Fatalf("no dispatch: exit %d (%s%s), want 1 naming cfg:friend:emma", code, out.String(), errOut.String())
	}
	if state := client.HGet(ctx, task.Key("s1", "w1"), "state").Val(); state != "open" {
		t.Fatalf("a refused serve touched the task: state %s", state)
	}

	if err := client.HSet(ctx, cfgFriendKey("emma"), "dispatch", os.Args[0]+" -test.run=^TestHelperServeDispatch$").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, life.LockKey("emma"), "other-session", life.ServeLockTTL).Err(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := run(base, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "seat-held holder=other-session") {
		t.Fatalf("held seat: exit %d (%s%s), want 1 naming the holder", code, out.String(), errOut.String())
	}
	if err := client.Del(ctx, life.LockKey("emma")).Err(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if code := run(base, &out, &errOut); code != 0 {
		t.Fatalf("wake --as: exit %d: %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "SERVE emma once taken=1 closed=1 width=2") {
		t.Fatalf("wake --as line: %s", out.String())
	}
	h := client.HGetAll(ctx, task.Key("s1", "w1")).Val()
	if h["state"] != "closed" || !strings.HasPrefix(h["evidence"], "DONE served w1") {
		t.Fatalf("task after wake: state=%s evidence=%q", h["state"], h["evidence"])
	}
	if client.Exists(ctx, life.LockKey("emma")).Val() != 0 {
		t.Fatalf("seat lock held after the one-shot pass")
	}
	if n := client.XLen(ctx, life.LogKey("emma")).Val(); n == 0 {
		t.Fatalf("no receipts on friend:emma:log")
	}
}
