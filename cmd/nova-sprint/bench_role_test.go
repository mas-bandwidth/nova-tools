package main

// nova-tools #3634 ("Studio is for friends. Fleet is for CI and swarms."):
// the bench registry's role column end to end through the verbs, on the
// throwaway redis-server. capacity bench --role writes it, bench ls prints it,
// and every swarm or CI path refuses a friends bench with one REFUSED line
// naming the role, exit 1: the CI legs declaration, the CI claim, the deal
// call and the CI cut's carrier check.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func benchRoleFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	return addr, client
}

func runVerb(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestBenchRoleFriendsRefusedOnEverySwarmAndCIPath(t *testing.T) {
	addr, c := benchRoleFixture(t)
	ctx := context.Background()

	if code, _, stderr := runVerb(t, "capacity", "machine", "--redis", addr, "--as", "t", "m1", "64"); code != 0 {
		t.Fatalf("capacity machine: %d %q", code, stderr)
	}
	code, stdout, stderr := runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--role", "friends", "studio", "0")
	if code != 0 || !strings.HasPrefix(stdout, "SET bench studio ") || !strings.Contains(stdout, " role=friends ") {
		t.Fatalf("capacity bench --role friends: %d %q %q", code, stdout, stderr)
	}
	if code, _, stderr := runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "hulk", "8"); code != 0 {
		t.Fatalf("capacity bench hulk: %d %q", code, stderr)
	}
	// The same write again is SAME: the role column takes part in the compare.
	code, stdout, _ = runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--role", "friends", "studio", "0")
	if code != 0 || !strings.HasPrefix(stdout, "SAME bench studio ") {
		t.Fatalf("repeat --role friends: %d %q, want SAME", code, stdout)
	}
	if code, _, stderr := runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--role", "ci", "studio", "0"); code != 2 ||
		!strings.Contains(stderr, "want friends or fleet") {
		t.Fatalf("--role ci: %d %q, want exit 2 naming friends or fleet", code, stderr)
	}
	if code, _, stderr := runVerb(t, "capacity", "friend", "--redis", addr, "--machine", "m1", "--as", "t", "--role", "fleet", "ann", "1"); code != 2 ||
		!strings.Contains(stderr, "--role is a bench flag") {
		t.Fatalf("capacity friend --role: %d %q, want exit 2", code, stderr)
	}

	// bench ls prints the column and the counts.
	code, stdout, stderr = runVerb(t, "bench", "ls", "--redis", addr)
	want := "BENCH hulk role=fleet machine=m1 slots=8 legs=- paused=0\n" +
		"BENCH studio role=friends machine=m1 slots=0 legs=- paused=0\n" +
		"BENCHES n=2 fleet=1 friends=1\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("bench ls: %d\n%s%q\nwant\n%s", code, stdout, stderr, want)
	}

	// CI runner registration: a legs declaration on a friends bench.
	code, stdout, stderr = runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--legs", "go", "studio", "0")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "REFUSED bench=studio role=friends: ") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("--legs on a friends bench: %d %q %q, want exit 1 and one REFUSED line", code, stdout, stderr)
	}
	if legs := c.HGet(ctx, "bench:studio:desired", "legs").Val(); legs != "" {
		t.Fatalf("refused legs were written: %q", legs)
	}
	// ... and --role friends with --legs in one call is the same refusal.
	if code, _, stderr := runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--legs", "go", "--role", "friends", "hulk", "8"); code != 1 ||
		!strings.HasPrefix(stderr, "REFUSED bench=hulk role=friends: ") {
		t.Fatalf("--legs --role friends: %d %q", code, stderr)
	}
	if role := c.HGet(ctx, "bench:hulk:desired", "role").Val(); role != "" {
		t.Fatalf("refused role was written on hulk: %q", role)
	}

	// CI claim: a request in the pool is refused to studio, untouched.
	sha := strings.Repeat("ab", 20)
	c.HSet(ctx, "cfg:ci:fx", "checks", "ok", "check:ok", "true")
	if code, _, stderr := runVerb(t, "ci", "request", "--redis", addr, "--repo", "fx", "--sha", sha, "--url", "file:///nonexistent"); code != 0 {
		t.Fatalf("ci request: %d %q", code, stderr)
	}
	code, stdout, stderr = runVerb(t, "ci", "run", "--redis", addr, "--bench", "studio", "--results", t.TempDir(), "--scratch", t.TempDir())
	if code != 1 || !strings.HasPrefix(stderr, "REFUSED bench=studio role=friends: no CI claim") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("ci run on a friends bench: %d %q %q, want exit 1 and one REFUSED line", code, stdout, stderr)
	}
	if rec := c.HMGet(ctx, "ci:fx:"+sha, "bench", "attempt").Val(); rec[0] != nil && rec[0] != "" {
		t.Fatalf("the refused claim wrote the record: %v", rec)
	}

	// Deal: ns_card_deal answers NONE role=friends before any card is read.
	c.HSet(ctx, "lease:reconciler", "token", "fence", "instance", "i", "host", "h")
	c.HSet(ctx, "bench:studio:state", "state", "UP")
	c.HSet(ctx, "bench:studio:desired", "slots", "4")
	reply, err := c.FCall(ctx, "ns_card_deal", nil, "studio", "fence", "t", "").StringSlice()
	if err != nil || len(reply) != 2 || reply[0] != "NONE" || reply[1] != "role=friends" {
		t.Fatalf("ns_card_deal on a friends bench: %v %v, want [NONE role=friends]", reply, err)
	}

	// CI cut: a leg carried only by a friends bench is RUNNER-ONLY.
	c.SRem(ctx, "benches", "hulk")
	c.HSet(ctx, "s:s1", "status", "open")
	reply, err = c.FCall(ctx, "ns_ci_cut", nil, "s1", "ci-1-abababab", "fx", "1", sha, "dev", sha, "go", "-", "t", "").StringSlice()
	if err != nil || len(reply) < 1 || reply[0] != "RUNNER-ONLY" {
		t.Fatalf("ns_ci_cut with only a friends bench: %v %v, want RUNNER-ONLY", reply, err)
	}

	// Back to fleet: the role column moves, and bench ls follows.
	if code, _, stderr := runVerb(t, "capacity", "bench", "--redis", addr, "--machine", "m1", "--as", "t", "--role", "fleet", "studio", "4"); code != 0 {
		t.Fatalf("--role fleet: %d %q", code, stderr)
	}
	reply, err = c.FCall(ctx, "ns_card_deal", nil, "studio", "fence", "t", "").StringSlice()
	if err != nil || len(reply) < 1 || reply[0] != "DEALT" {
		t.Fatalf("ns_card_deal on a fleet bench: %v %v, want DEALT", reply, err)
	}
}

func TestBenchLsRefusesAStoredRoleThatDoesNotParse(t *testing.T) {
	addr, c := benchRoleFixture(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "odd")
	c.HSet(ctx, "bench:odd:desired", "role", "swarm")
	code, stdout, stderr := runVerb(t, "bench", "ls", "--redis", addr)
	if code != 1 || !strings.Contains(stdout, "BENCH odd role=swarm ") || !strings.HasPrefix(stderr, "REFUSED bench ls: role not friends or fleet: odd=swarm") {
		t.Fatalf("bench ls with role=swarm: %d %q %q", code, stdout, stderr)
	}
	if code, _, _ := runVerb(t, "bench", "ls", "--redis", addr, "extra"); code != 2 {
		t.Fatalf("bench ls extra: %d, want usage exit 2", code)
	}
}
