package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

func TestStage1CapacityIsTheOneWidthWriter(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.Set(ctx, "friend:f:slots", 32, 0)
	client.SAdd(ctx, "friends", "f")
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)
	client.SAdd(ctx, "benches", "b")
	client.HSet(ctx, "bench:b:desired", "slots", 8, "machine", "m", "paused", 0)

	ceiling := func() preflight.Line {
		for _, line := range preflight.Run(ctx, client, preflight.Options{}) {
			if line.Name == "machine-ceiling" {
				return line
			}
		}
		t.Fatal("machine-ceiling preflight line missing")
		return preflight.Line{}
	}
	if line := ceiling(); !line.Red || !strings.Contains(line.Why, "friend:f:slots") {
		t.Fatalf("legacy preflight=%s", line.String())
	}

	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "--machine", "m", "f", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend code=%d stderr=%q", code, errOut.String())
	}
	// #3265: the receipt names its round trips, one pipeline and one FCALL.
	if got := out.String(); got != "SET friend f machine=m slots=32 desired=40/40 trips=2\n" {
		t.Fatalf("capacity friend receipt=%q", got)
	}
	if !client.SIsMember(ctx, "friends", "f").Val() {
		t.Fatal("capacity friend did not register f")
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "32" {
		t.Fatalf("desired slots=%q", got)
	}
	if client.Exists(ctx, "friend:f:slots").Val() != 0 {
		t.Fatal("capacity friend left legacy width")
	}
	entries := client.XRange(ctx, "cap:log", "-", "+").Val()
	if len(entries) != 1 || entries[0].Values["actor"] != "ops" || entries[0].Values["legacy"] != "32" {
		t.Fatalf("cap:log=%v", entries)
	}
	if line := ceiling(); line.Red {
		t.Fatalf("post-migration preflight=%s", line.String())
	}

	t.Setenv(seatEnv, "f")
	out.Reset()
	errOut.Reset()
	if code := runFriendHello(ctx, []string{"--redis", addr, "--as", "f", "--slots", "8", "--host", "m", "--once"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "nova-sprint capacity friend") {
		t.Fatalf("hello --slots code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "32" {
		t.Fatalf("refused hello changed desired=%q", got)
	}

	// Reader: the width duty folds friend:f:desired into the fillstate and the
	// read-only width verb prints it; capacity is the one writer (#3591).
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "capacity"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&width.Duty{Store: st}).Run(ctx, lease); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runWidth(ctx, []string{"--redis", addr, "--as", "f"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "WIDTH f slots=32 ") {
		t.Fatalf("width code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	// Setter: a 33 through capacity friend is refused at the machine ceiling.
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "f", "33"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "CEILING m 41/40") {
		t.Fatalf("capacity friend 33 code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "32" {
		t.Fatalf("refused capacity friend 33 changed desired=%q", got)
	}
}

// TestCapacityAsActorControlReceipt exercises the specified --as control flag
// through the CLI, not merely through the Go capacity API. The actor must be
// preserved in the server-timed cap:log receipt.
func TestCapacityAsActorControlReceipt(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}
	client.SAdd(ctx, "friends", "alice")
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "operator", "--machine", "ctl-machine", "alice", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend code=%d stderr=%q", code, errOut.String())
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("receipts=%d want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Values["actor"] != "operator" {
			t.Fatalf("receipt actor=%v", entry.Values["actor"])
		}
	}
}

// TestL20bRunnerHooks exercises the CI runner hooks ACTIONS_RUNNER_HOOK_JOB_STARTED
// and ACTIONS_RUNNER_HOOK_JOB_COMPLETED through the CLI verb `capacity hook`.
func TestL20bRunnerHooks(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	// Set machine with cores and mem-gb to establish budget (spec 5.1: 90%)
	// 32 cores -> 28800 cpu_milli; 64 GB -> 58982 mem_mb
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "--cores", "32", "--mem-gb", "64", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}

	// 1. Hook job-started takes budget for CI job
	out.Reset()
	errOut.Reset()
	code := runCapacity(ctx, []string{
		"hook", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "ci-runner-job-123",
		"--cpu-milli", "4000",
		"--mem-mb", "8192",
		"job-started",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("hook job-started code=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "HOOK started ci-runner-job-123") {
		t.Fatalf("unexpected hook started output: %q", out.String())
	}

	// Verify debit in Redis
	debitState := client.HGet(ctx, "machine:ctl-machine:debit:ci-runner-job-123", "state").Val()
	if debitState != "live" {
		t.Fatalf("debit state=%q want live", debitState)
	}

	// 2. An overcommitting take is refused
	out.Reset()
	errOut.Reset()
	code = runCapacity(ctx, []string{
		"take", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "huge-job",
		"--cpu-milli", "30000", // exceeds remaining ~24800
		"--mem-mb", "10000",
	}, &out, &errOut)
	if code != 2 {
		t.Fatalf("expected exit code 2 for overcommit, got %d", code)
	}
	if !strings.Contains(errOut.String(), "NOBUDGET") {
		t.Fatalf("expected NOBUDGET error, got %q", errOut.String())
	}

	// 3. Hook job-completed gives budget back
	out.Reset()
	errOut.Reset()
	code = runCapacity(ctx, []string{
		"hook", "--redis", addr,
		"--machine", "ctl-machine",
		"--consumer", "ci-runner-job-123",
		"job-completed",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("hook job-completed code=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "HOOK completed ci-runner-job-123") {
		t.Fatalf("unexpected hook completed output: %q", out.String())
	}

	// Verify debit removed
	exists := client.Exists(ctx, "machine:ctl-machine:debit:ci-runner-job-123").Val()
	if exists != 0 {
		t.Fatalf("debit still exists after completed hook")
	}
}

// TestCapacityBenchWritesLegs (#3349): `capacity bench --legs go,schema <b>
// <slots>` writes legs on bench:<b>:desired through ns_capacity_desired, a
// write without --legs keeps the stored list, the same list again is SAME,
// and a friend or a malformed list is refused. With the legs declared by
// verb, `ci cut` for a go leg is CREATED with no hand-written bench hash.
func TestCapacityBenchWritesLegs(t *testing.T) {
	t.Setenv("NOVA_TEST_NO_HOST", "1")
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)

	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runCapacity(ctx, append([]string{"bench", "--redis", addr, "--as", "ops", "--machine", "m"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, out, errOut := run("--legs", "go, schema", "b", "8"); code != 0 || !strings.HasPrefix(out, "SET bench b ") ||
		!strings.Contains(out, " legs=go,schema ") {
		t.Fatalf("capacity bench --legs code=%d out=%q err=%q", code, out, errOut)
	}
	if got := client.HGet(ctx, "bench:b:desired", "legs").Val(); got != "go,schema" {
		t.Fatalf("bench:b:desired legs=%q; want go,schema", got)
	}
	if code, out, _ := run("--legs", "go,schema", "b", "8"); code != 0 || !strings.HasPrefix(out, "SAME bench b ") {
		t.Fatalf("same legs again code=%d out=%q; want SAME", code, out)
	}
	if code, out, _ := run("--legs", "go", "b", "8"); code != 0 || !strings.HasPrefix(out, "SET bench b ") {
		t.Fatalf("changed legs code=%d out=%q; want SET", code, out)
	}
	if code, _, _ := run("b", "6"); code != 0 {
		t.Fatalf("capacity bench without --legs code=%d", code)
	}
	if got := client.HGet(ctx, "bench:b:desired", "legs").Val(); got != "go" {
		t.Fatalf("a write without --legs changed legs to %q; want go kept", got)
	}
	if code, _, errOut := run("--legs", "go;rm", "b", "8"); code != 2 || !strings.Contains(errOut, "--legs") {
		t.Fatalf("malformed --legs code=%d err=%q; want a refusal naming --legs", code, errOut)
	}
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "--machine", "m", "--legs", "go", "f", "4"}, &out, &errOut); code != 2 {
		t.Fatalf("capacity friend --legs code=%d; want a refusal (legs is a bench flag)", code)
	}

	// The legs reach ci cut: the bench is UP and declares go, so the cut is
	// CREATED, never RUNNER-ONLY, with no hand HSET of the desired hash.
	client.HSet(ctx, "bench:b:state", "state", "UP")
	client.HSet(ctx, "s:ctl", "status", "open")
	reply, err := client.FCall(ctx, "ns_ci_cut", nil, "ctl", "ci-1-abc", "nova-tools", "1",
		"abc", "dev", "def", "go", "", "ops", "").StringSlice()
	if err != nil || len(reply) == 0 || reply[0] != "CREATED" {
		t.Fatalf("ci cut on a verb-declared bench = %v %v; want CREATED", reply, err)
	}
}

// TestCapacityRefusesMappedLogin (#3604): a name bound in friends:login is a
// login alias and never registers as a friend, so `capacity friend` refuses it
// with exit 2 NAME-IS-LOGIN <name> before any ceiling check or write, exactly
// as friend hello does (#3593). The refusal must hold even when the ceiling
// would allow the raise, and must leave the name out of the friends set.
func TestCapacityRefusesMappedLogin(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.HSet(ctx, "machine:m:ceiling", "slots", 64)
	client.HSet(ctx, "friends:login", "rowan-claude", "rowan")
	client.SAdd(ctx, "friends", "rowan")
	client.HSet(ctx, "friend:rowan:desired", "slots", 32, "machine", "m")

	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "--machine", "m", "rowan-claude", "8"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "NAME-IS-LOGIN rowan-claude") {
		t.Fatalf("capacity friend on a mapped login: exit %d %q, want 2 NAME-IS-LOGIN", code, errOut.String())
	}
	if client.SIsMember(ctx, "friends", "rowan-claude").Val() {
		t.Fatalf("a mapped login registered as a friend")
	}
	if got := client.Exists(ctx, "friend:rowan-claude:desired").Val(); got != 0 {
		t.Fatalf("a refused mapped login left friend:rowan-claude:desired")
	}

	// A plain friend name on the same machine still registers: the login guard
	// refuses by name only.
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "--machine", "m", "alice", "8"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend alice: exit %d %q", code, errOut.String())
	}
}
