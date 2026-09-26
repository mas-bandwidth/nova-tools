package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// TestFnLoadThenPing is the #3196 control: the fleet Redis had no nova_sprint
// library and no verb loaded it. fn load installs the embedded library into a
// throwaway Redis, FCALL ns_ping then answers PONG, a second load is a no-op
// with the same sha, and fn check exits 1 on a missing or stale library.
func TestFnLoadThenPing(t *testing.T) {
	t.Parallel()

	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	sha := fn.Sum(source)

	code, out, errOut := runSprint("fn", "check", "--redis", addr)
	if code != 1 || !strings.HasPrefix(out, "MISSING nova_sprint ") {
		t.Fatalf("check before load: code=%d out=%q err=%q, want 1 MISSING", code, out, errOut)
	}

	code, out, errOut = runSprint("fn", "load", "--redis", addr)
	if code != 0 {
		t.Fatalf("fn load code=%d err=%q", code, errOut)
	}
	if want := "LOADED nova_sprint sha=" + sha + "\n"; out != want {
		t.Fatalf("fn load printed %q, want %q", out, want)
	}
	got, err := client.FCall(ctx, "ns_ping", nil).Result()
	if err != nil {
		t.Fatalf("FCALL ns_ping 0 after fn load: %v", err)
	}
	if got != "PONG" {
		t.Fatalf("FCALL ns_ping 0 = %v, want PONG", got)
	}

	code, out, errOut = runSprint("fn", "load", "--redis", addr)
	if code != 0 || out != "UNCHANGED nova_sprint sha="+sha+"\n" {
		t.Fatalf("second load: code=%d out=%q err=%q, want UNCHANGED at the same sha", code, out, errOut)
	}

	code, out, errOut = runSprint("fn", "check", "--redis", addr)
	if code != 0 || out != "OK nova_sprint sha="+sha+" ping=PONG\n" {
		t.Fatalf("check after load: code=%d out=%q err=%q", code, out, errOut)
	}

	// A stale ns_ping that writes: fn check must report STALE without calling
	// it, so the marker key stays absent.
	const marker = "fn-check-test:stale-ping-ran"
	stale := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() redis.call('SET', '" + marker + "', '1'); return 'PONG' end)\n"
	if err := client.FunctionLoadReplace(ctx, stale).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runSprint("fn", "check", "--redis", addr)
	if code != 1 || out != "STALE nova_sprint loaded="+fn.Sum(stale)+" want="+sha+" ping=skipped\n" {
		t.Fatalf("check on a stale library: code=%d out=%q, want 1 STALE ping=skipped", code, out)
	}
	if n, err := client.Exists(ctx, marker).Result(); err != nil || n != 0 {
		t.Fatalf("fn check ran the stale ns_ping: EXISTS %s = %d err=%v, want 0", marker, n, err)
	}

	// A different library that registers ns_ping (nova_sprint missing): fn
	// check must report MISSING without calling it.
	if err := client.FunctionDelete(ctx, fn.Library).Err(); err != nil {
		t.Fatal(err)
	}
	other := "#!lua name=other_lib\nredis.register_function('ns_ping', function() redis.call('SET', '" + marker + "', '1'); return 'PONG' end)\n"
	if err := client.FunctionLoadReplace(ctx, other).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runSprint("fn", "check", "--redis", addr)
	if code != 1 || out != "MISSING nova_sprint want="+sha+" ping=skipped\n" {
		t.Fatalf("check with nova_sprint missing: code=%d out=%q, want 1 MISSING ping=skipped", code, out)
	}
	if n, err := client.Exists(ctx, marker).Result(); err != nil || n != 0 {
		t.Fatalf("fn check ran another library's ns_ping: EXISTS %s = %d err=%v, want 0", marker, n, err)
	}
	if err := client.FunctionDelete(ctx, "other_lib").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runSprint("fn", "load", "--redis", addr)
	if code != 0 || out != "LOADED nova_sprint sha="+sha+"\n" {
		t.Fatalf("load over a stale library: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestFnRefusesWithoutRedis(t *testing.T) {
	t.Parallel()

	for _, sub := range []string{"load", "check"} {
		code, _, errOut := runSprint("fn", sub)
		if code != 2 || !strings.Contains(errOut, "--redis") {
			t.Errorf("fn %s without --redis: code=%d err=%q, want 2 naming --redis", sub, code, errOut)
		}
	}
	code, _, errOut := runSprint("fn")
	if code != 2 || !strings.Contains(errOut, "load or check") {
		t.Errorf("bare fn: code=%d err=%q", code, errOut)
	}
}
