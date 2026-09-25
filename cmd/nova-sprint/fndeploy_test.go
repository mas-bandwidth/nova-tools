package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// TestFnDeployReceiptAndDigestRefusal is the #2937 control: the deploy path
// (the rowan-tools fn-load play, the last of `make -C fleet tools`)
// loads the nova_sprint library with one verb. fn deploy --want <sha> loads the
// embedded library only when <sha> is this binary's library digest, reads it
// back, calls ns_ping and prints one FN RECEIPT line; the rerun is UNCHANGED.
// A --want that is not this binary's digest (the coordinator runs another
// build than the declared one) exits 1 and loads nothing; --dry-run changes
// nothing and prints WOULD-LOAD while the store is missing or stale.
func TestFnDeployReceiptAndDigestRefusal(t *testing.T) {
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	sha := fn.Sum(source)

	code, out, errOut := runSprint("fn", "sum")
	if code != 0 || out != "SUM nova_sprint sha="+sha+"\n" {
		t.Fatalf("fn sum: code=%d out=%q err=%q", code, out, errOut)
	}

	// A digest mismatch refuses before any write, live and dry.
	const other = "0123456789abcdef"
	for _, dry := range []bool{false, true} {
		args := []string{"fn", "deploy", "--redis", addr, "--want", other}
		if dry {
			args = append(args, "--dry-run")
		}
		code, out, errOut = runSprint(args...)
		want := "FN REFUSED store=" + addr + " reason=digest-mismatch have=" + sha + " want=" + other
		if code != 1 || !strings.HasPrefix(out, want+" remedy=") || strings.Count(out, "\n") != 1 {
			t.Fatalf("deploy dry=%v with a foreign digest: code=%d out=%q err=%q, want 1 %q", dry, code, out, errOut, want)
		}
		if _, found, err := fn.Loaded(ctx, client); err != nil || found {
			t.Fatalf("deploy dry=%v with a foreign digest loaded a library (found=%v err=%v)", dry, found, err)
		}
	}

	code, out, errOut = runSprint("fn", "deploy", "--redis", addr, "--want", sha, "--dry-run")
	if code != 0 || out != "FN WOULD-LOAD store="+addr+" got=MISSING sha="+sha+"\n" {
		t.Fatalf("dry deploy on an empty store: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, found, _ := fn.Loaded(ctx, client); found {
		t.Fatal("dry deploy loaded the library")
	}

	receipt := regexp.MustCompile(`^FN RECEIPT at=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ store=` + regexp.QuoteMeta(addr) +
		` load=(LOADED|UNCHANGED) sha=` + sha + ` version=\S+ ping=PONG\n$`)
	code, out, errOut = runSprint("fn", "deploy", "--redis", addr, "--want", sha)
	if m := receipt.FindStringSubmatch(out); code != 0 || m == nil || m[1] != "LOADED" {
		t.Fatalf("first deploy: code=%d out=%q err=%q, want one FN RECEIPT load=LOADED", code, out, errOut)
	}
	if got, err := client.FCall(ctx, "ns_ping", nil).Result(); err != nil || got != "PONG" {
		t.Fatalf("ns_ping after deploy = %v, %v", got, err)
	}
	code, out, errOut = runSprint("fn", "deploy", "--redis", addr, "--want", sha)
	if m := receipt.FindStringSubmatch(out); code != 0 || m == nil || m[1] != "UNCHANGED" {
		t.Fatalf("rerun: code=%d out=%q err=%q, want one FN RECEIPT load=UNCHANGED", code, out, errOut)
	}
	code, out, errOut = runSprint("fn", "deploy", "--redis", addr, "--dry-run")
	if code != 0 || out != "FN OK store="+addr+" sha="+sha+" ping=PONG\n" {
		t.Fatalf("dry deploy on a current store: code=%d out=%q err=%q", code, out, errOut)
	}

	// A stale library: dry-run names it and changes nothing; the live deploy replaces it.
	stale := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	if err := client.FunctionLoadReplace(ctx, stale).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runSprint("fn", "deploy", "--redis", addr, "--dry-run")
	if code != 0 || out != "FN WOULD-LOAD store="+addr+" got=STALE loaded="+fn.Sum(stale)+" sha="+sha+"\n" {
		t.Fatalf("dry deploy on a stale store: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, loaded, _ := fnLoadedSum(ctx, client); code != fn.Sum(stale) || !loaded {
		t.Fatalf("dry deploy changed the stale library: now %s", code)
	}
	code, out, errOut = runSprint("fn", "deploy", "--redis", addr)
	if m := receipt.FindStringSubmatch(out); code != 0 || m == nil || m[1] != "LOADED" {
		t.Fatalf("deploy over a stale library: code=%d out=%q err=%q", code, out, errOut)
	}
}

// TestFnDeployRefusesAReadBackMismatch: the library the store holds after the
// load is not the embedded one (another deployer replaced it in between);
// deploy must exit 1 with no receipt. The race is forced by a server whose
// FUNCTION LOAD is renamed away, so the store keeps the stale library.
func TestFnDeployRefusesAReadBackMismatch(t *testing.T) {
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	sha := fn.Sum(source)
	stale := "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'PONG' end)\n"
	if err := client.FunctionLoadReplace(ctx, stale).Err(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code := fnDeployReadBack(ctx, client, addr, sha, false, &out)
	want := "FN REFUSED store=" + addr + " reason=digest-mismatch loaded=" + fn.Sum(stale) + " want=" + sha
	if code != 1 || !strings.HasPrefix(out.String(), want+" remedy=") {
		t.Fatalf("read-back mismatch: code=%d out=%q, want 1 %q", code, out.String(), want)
	}
	if strings.Contains(out.String(), "FN RECEIPT") {
		t.Fatalf("read-back mismatch printed a receipt: %q", out.String())
	}
}

func TestFnDeployUsage(t *testing.T) {
	for _, args := range [][]string{
		{"fn", "deploy"},
		{"fn", "deploy", "--redis", "127.0.0.1:1", "--want", "short"},
		{"fn", "deploy", "--redis", "127.0.0.1:1", "extra"},
		{"fn", "sum", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || errOut == "" {
			t.Errorf("%v: code=%d err=%q, want 2 with a reason", args, code, errOut)
		}
	}
}

func fnLoadedSum(ctx context.Context, client *redis.Client) (string, bool, error) {
	code, found, err := fn.Loaded(ctx, client)
	if !found || err != nil {
		return "", found, err
	}
	return fn.Sum(code), true, nil
}
