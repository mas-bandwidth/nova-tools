//go:build functional

package main

import (
	"bytes"
	"context"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestStage1CapacityIsTheOneWidthWriter: a friend's desired slots have one
// writer, ns_capacity_desired, which nova-config apply calls
// (capacity.SetFriendWith is the same call); `capacity friend` is gone and
// says so. The read-only width verb prints the desired slots (#3591).
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
		for _, line := range preflight.StoreChecks(ctx, client, preflight.Options{}) {
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

	// the verb is gone: exit 2 naming nova-config, nothing written
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "ops", "--machine", "m", "f", "32"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "nova-config") {
		t.Fatalf("capacity friend code=%d stderr=%q; want 2 naming nova-config", code, errOut.String())
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "" {
		t.Fatalf("the retired verb wrote desired slots=%q", got)
	}

	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	res, err := capacity.SetFriendWith(ctx, st, "f", "m", 32, "ops", "", capacity.DesiredOpts{})
	if err != nil || res.Slots != 32 || res.Sum != 40 || res.Ceiling != 40 {
		t.Fatalf("SetFriendWith f 32: %+v %v", res, err)
	}
	if !client.SIsMember(ctx, "friends", "f").Val() {
		t.Fatal("the desired write did not register f")
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "32" {
		t.Fatalf("desired slots=%q", got)
	}
	if client.Exists(ctx, "friend:f:slots").Val() != 0 {
		t.Fatal("the desired write left legacy width")
	}
	entries := client.XRange(ctx, "cap:log", "-", "+").Val()
	if len(entries) != 1 || entries[0].Values["actor"] != "ops" || entries[0].Values["legacy"] != "32" {
		t.Fatalf("cap:log=%v", entries)
	}
	if line := ceiling(); line.Red {
		t.Fatalf("post-migration preflight=%s", line.String())
	}

	// Reader: the read-only width verb prints the desired slots; the
	// desired write is the one writer (#3591).
	out.Reset()
	errOut.Reset()
	if code := runWidth(ctx, []string{"--redis", addr, "--as", "f"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "WIDTH f ") || !strings.Contains(out.String(), "slots=32") {
		t.Fatalf("width code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	// Setter: a 33 is refused at the machine ceiling.
	if _, err := capacity.SetFriendWith(ctx, st, "f", "m", 33, "ops", "", capacity.DesiredOpts{}); err == nil || !strings.Contains(err.Error(), "CEILING m 41/40") {
		t.Fatalf("SetFriendWith f 33: %v; want CEILING m 41/40", err)
	}
	if got := client.HGet(ctx, "friend:f:desired", "slots").Val(); got != "32" {
		t.Fatalf("refused 33 changed desired=%q", got)
	}
}

// TestCapacityAsActorControlReceipt exercises the specified --as control flag
// through the CLI, not merely through the Go capacity API. The actor must be
// preserved in the server-timed cap:log receipt.
func TestCapacityAsActorControlReceipt(t *testing.T) {
	t.Parallel()

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
	client.SAdd(ctx, "benches", "alice")
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"bench", "--redis", addr, "--as", "operator", "--machine", "ctl-machine", "alice", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity bench code=%d stderr=%q", code, errOut.String())
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
	t.Parallel()

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
// login alias and never registers as a friend, so the desired write
// (ns_capacity_desired, what nova-config apply calls) refuses it by name
// (#3593). The refusal must hold even when the ceiling would allow the
// raise, and must leave the name out of the friends set.
func TestCapacityRefusesMappedLogin(t *testing.T) {
	t.Parallel()

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
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := capacity.SetFriendWith(ctx, st, "rowan-claude", "m", 8, "ops", "", capacity.DesiredOpts{}); err == nil || !strings.Contains(err.Error(), "NAME-IS-LOGIN rowan-claude") {
		t.Fatalf("desired write on a mapped login: %v, want NAME-IS-LOGIN", err)
	}
	if client.SIsMember(ctx, "friends", "rowan-claude").Val() {
		t.Fatalf("a mapped login registered as a friend")
	}
	if got := client.Exists(ctx, "friend:rowan-claude:desired").Val(); got != 0 {
		t.Fatalf("a refused mapped login left friend:rowan-claude:desired")
	}
	// A plain friend name on the same machine still registers: the login guard
	// refuses aliases, not friends.
	if _, err := capacity.SetFriendWith(ctx, st, "alice", "m", 8, "ops", "", capacity.DesiredOpts{}); err != nil {
		t.Fatalf("desired write alice: %v", err)
	}
	if !client.SIsMember(ctx, "friends", "alice").Val() {
		t.Fatalf("alice did not register")
	}
}

// TestCapacityWritesKindsAndTiers (#4270): `capacity bench --kinds
// work,read --tiers pro <b> <slots>` writes the copy filters the card moves
// read (TM.may) on bench:<b>:desired; a write without the flags keeps them,
// the same again is SAME, an empty value clears, and a kind that is not
// work, read or fix is refused. A friend takes the same flags.
func TestCapacityWritesKindsAndTiers(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.HSet(ctx, "machine:m:ceiling", "slots", 40)
	run := func(kind string, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runCapacity(ctx, append([]string{kind, "--redis", addr, "--as", "ops", "--machine", "m"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	desired := func() (string, string) {
		v := client.HMGet(ctx, "bench:b:desired", "kinds", "tiers").Val()
		s := func(x any) string {
			if x == nil {
				return "<none>"
			}
			return x.(string)
		}
		return s(v[0]), s(v[1])
	}
	if code, out, errOut := run("bench", "--kinds", "work, read", "--tiers", "pro", "b", "8"); code != 0 ||
		!strings.HasPrefix(out, "SET bench b ") || !strings.Contains(out, " kinds=work,read tiers=pro ") {
		t.Fatalf("capacity bench --kinds --tiers code=%d out=%q err=%q", code, out, errOut)
	}
	if k, tr := desired(); k != "work,read" || tr != "pro" {
		t.Fatalf("bench:b:desired kinds=%q tiers=%q; want work,read and pro", k, tr)
	}
	if code, out, _ := run("bench", "--kinds", "work,read", "--tiers", "pro", "b", "8"); code != 0 || !strings.HasPrefix(out, "SAME bench b ") {
		t.Fatalf("the same filters again code=%d out=%q; want SAME", code, out)
	}
	if code, _, _ := run("bench", "b", "6"); code != 0 {
		t.Fatalf("capacity bench without the flags code=%d", code)
	}
	if k, tr := desired(); k != "work,read" || tr != "pro" {
		t.Fatalf("a write without the flags changed kinds=%q tiers=%q; want kept", k, tr)
	}
	if code, out, _ := run("bench", "--kinds", "work", "b", "6"); code != 0 || !strings.Contains(out, " kinds=work ") || strings.Contains(out, "tiers=") {
		t.Fatalf("narrowed kinds code=%d out=%q", code, out)
	}
	if k, tr := desired(); k != "work" || tr != "pro" {
		t.Fatalf("kinds=%q tiers=%q; want work and pro kept", k, tr)
	}
	if code, out, _ := run("bench", "--kinds", "", "--tiers", "", "b", "6"); code != 0 || !strings.Contains(out, " kinds=- tiers=- ") {
		t.Fatalf("clear code=%d out=%q", code, out)
	}
	if k, tr := desired(); k != "<none>" || tr != "<none>" {
		t.Fatalf("after clearing: kinds=%q tiers=%q; want neither field", k, tr)
	}
	if code, _, errOut := run("bench", "--kinds", "work,report", "b", "6"); code != 2 || !strings.Contains(errOut, "--kinds") {
		t.Fatalf("a kind that is not work, read or fix: code=%d err=%q; want a refusal naming --kinds", code, errOut)
	}
	if code, _, errOut := run("bench", "--tiers", "pro;rm", "b", "6"); code != 2 || !strings.Contains(errOut, "--tiers") {
		t.Fatalf("malformed --tiers: code=%d err=%q; want a refusal naming --tiers", code, errOut)
	}
	// the three model types (Glenn 2026-09-26): frontier joins pro and flash;
	// any other word is refused naming the three, and the Lua refuses it too
	if code, _, errOut := run("bench", "--tiers", "turbo", "b", "6"); code != 2 || !strings.Contains(errOut, "--tiers") ||
		!strings.Contains(errOut, "frontier, pro or flash") {
		t.Fatalf("--tiers turbo: code=%d err=%q; want a refusal naming --tiers and the three types", code, errOut)
	}
	if code, out, errOut := run("bench", "--tiers", "frontier, pro", "b", "6"); code != 0 || !strings.Contains(out, " tiers=frontier,pro ") {
		t.Fatalf("--tiers frontier,pro: code=%d out=%q err=%q", code, out, errOut)
	}
	if _, tr := desired(); tr != "frontier,pro" {
		t.Fatalf("bench:b:desired tiers=%q; want frontier,pro", tr)
	}
	if r, err := client.FCall(ctx, "ns_capacity_desired", nil, "bench", "b", "6", "m", "ops", "idem-turbo", "", "", "", "", "", "turbo").Slice(); err != nil ||
		len(r) == 0 || r[0] != "INVALID" {
		t.Fatalf("ns_capacity_desired tiers=turbo: %v %v; want INVALID", r, err)
	}
	if _, tr := desired(); tr != "frontier,pro" {
		t.Fatalf("after the refused Lua write tiers=%q; want frontier,pro kept", tr)
	}
}
