package main

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchretire"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// fakeUnits is the unit half's fake runner: it records each call and answers
// every loop stopped, or fails like an unreachable bench.
type fakeUnits struct {
	calls []string
	fail  bool
}

func (f *fakeUnits) run(_ context.Context, t benchsh.Target, _ string, args ...string) (benchsh.Result, error) {
	f.calls = append(f.calls, t.Dest()+" "+strings.Join(args, ","))
	if f.fail {
		return benchsh.Result{Exit: -1}, errors.New("ssh " + t.Dest() + ": connection refused")
	}
	var b strings.Builder
	b.WriteString("warning: host key\nUNITS-REPLY\n")
	for _, n := range args {
		b.WriteString("UNIT " + n + " stopped\n")
	}
	return benchsh.Result{Output: b.String()}, nil
}

func retireFixture(t *testing.T) (*redis.Client, string, *fakeUnits) {
	t.Helper()
	ctx := context.Background()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	f := &fakeUnits{}
	old := benchRetireRunner
	benchRetireRunner = f.run
	t.Cleanup(func() { benchRetireRunner = old })
	t.Setenv(seatEnv, "rowan")
	return c, addr, f
}

// seedBench registers bench b with its row, beat and conform record, one
// ready card pinned to it and one done card it ran, both linked by the card
// model's one-time adoption (ns_card_repair).
func seedBench(t *testing.T, c *redis.Client, b string) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	pipe.SAdd(ctx, "benches", b, "other")
	pipe.HSet(ctx, "bench:"+b, "state", "UP")
	pipe.HSet(ctx, "bench:"+b+":beat", "host", b+".example.invalid", "user", "nova", "at", "1")
	pipe.HSet(ctx, "bench:"+b+":conform", "ok", "1")
	pipe.HSet(ctx, "bench:"+b+":desired", "slots", "8")
	pipe.HSet(ctx, "bench:other:beat", "host", "other.example.invalid")
	pipe.HSet(ctx, "s:s1:card:c1", "state", "queued", "bench", b, "pin", b, "priority", "3", "created_at", "1000")
	pipe.SAdd(ctx, "s:s1:idx:card:queued", "c1")
	pipe.ZAdd(ctx, "s:s1:pool", redis.Z{Score: 3, Member: "c1"})
	pipe.ZAdd(ctx, "s:s1:bench:"+b+":queue", redis.Z{Score: 3, Member: "c1"})
	pipe.HSet(ctx, "s:s1:card:c2", "state", "ended", "outcome", "DONE", "bench", b, "attempt", "1", "created_at", "2000")
	pipe.SAdd(ctx, "s:s1:idx:card:ended", "c2")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FCall(ctx, "ns_card_repair", nil, "s1").Result(); err != nil {
		t.Fatal(err)
	}
	if n := c.ZCard(ctx, "bench:"+b+":cards:ready").Val(); n != 1 {
		t.Fatalf("fixture: bench:%s:cards:ready = %d, want the adopted pinned card", b, n)
	}
	if n := c.ZCard(ctx, "bench:"+b+":cards:done").Val(); n != 1 {
		t.Fatalf("fixture: bench:%s:cards:done = %d, want the adopted done card", b, n)
	}
}

func retireEvents(t *testing.T, c *redis.Client) int {
	t.Helper()
	msgs, err := c.XRange(context.Background(), "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Values["kind"] == "bench-retire" {
			n++
		}
	}
	return n
}

// dump is every key and its type, for "nothing changed".
func dump(t *testing.T, c *redis.Client) string {
	t.Helper()
	ctx := context.Background()
	keys := c.Keys(ctx, "*").Val()
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + " " + c.Type(ctx, k).Val() + "\n")
	}
	b.WriteString(strings.Join(c.SMembers(ctx, "benches").Val(), ","))
	return b.String()
}

func retire(t *testing.T, addr string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runBench(context.Background(), append([]string{"retire", "--redis", addr}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestBenchRetire is nova-tools#3645's DONE-WHEN: `bench retire --bench x
// --why test` leaves zero keys matching bench:x*, x out of the benches set,
// one retire event, the bench's loops stopped through the unit seam, every
// card that pointed at x moved (not lost) with fsck clean, and exit 0 on a
// second run; with a leased card it exits 2 and changes nothing.
func TestBenchRetire(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, addr, units := retireFixture(t)
	seedBench(t, c, "x")

	code, out, errOut := retire(t, addr, "--bench", "x", "--why", "test")
	if code != 0 {
		t.Fatalf("retire exit %d, stdout %q stderr %q", code, out, errOut)
	}
	if !strings.Contains(out, "BENCH RETIRE bench=x status=RETIRED") || !strings.Contains(out, "requeued=1 repointed=1") {
		t.Fatalf("receipt %q", out)
	}
	if keys := c.Keys(ctx, "bench:x*").Val(); len(keys) != 0 {
		t.Fatalf("keys left matching bench:x*: %v", keys)
	}
	if c.SIsMember(ctx, "benches", "x").Val() || !c.SIsMember(ctx, "benches", "other").Val() {
		t.Fatalf("benches = %v, want other only", c.SMembers(ctx, "benches").Val())
	}
	if c.Exists(ctx, "bench:other:beat").Val() != 1 {
		t.Fatal("another bench's row was deleted")
	}
	if n := retireEvents(t, c); n != 1 {
		t.Fatalf("retire events = %d, want 1", n)
	}
	if len(units.calls) != 1 || units.calls[0] != "nova@x.example.invalid "+strings.Join(benchretire.Loops, ",") {
		t.Fatalf("unit calls = %q, want one to the beat's user@host naming every loop", units.calls)
	}
	c1 := c.HGetAll(ctx, "s:s1:card:c1").Val()
	if c1["where"] != "ready" || c1["bench"] != "" || c1["pin"] != "" {
		t.Fatalf("pinned ready card = %v, want back in the pool, unpinned", c1)
	}
	if c.ZScore(ctx, "bench:_pool:cards:ready", "s:s1:card:c1").Err() != nil || c.ZScore(ctx, "s:s1:pool", "c1").Err() != nil {
		t.Fatal("the requeued card is not in the pool views")
	}
	if c.Exists(ctx, "s:s1:bench:x:queue").Val() != 0 {
		t.Fatal("the sprint's queue for x still holds the card")
	}
	c2 := c.HGetAll(ctx, "s:s1:card:c2").Val()
	if c2["where"] != "done" || c2["bench"] != "_retired" || c2["retired_bench"] != "x" {
		t.Fatalf("done card = %v, want re-pointed to _retired with retired_bench=x", c2)
	}
	fsck, err := c.FCall(ctx, "ns_card_fsck", nil, "s1").StringSlice()
	if err != nil || len(fsck) < 13 || fsck[2] != "2" || fsck[11] != "0" {
		t.Fatalf("fsck = %v (%v), want 2 cards and zero drift", fsck, err)
	}

	code, out, errOut = retire(t, addr, "--bench", "x", "--why", "test")
	if code != 0 || !strings.Contains(out, "status=ALREADY") {
		t.Fatalf("second run exit %d, stdout %q stderr %q; want 0 ALREADY", code, out, errOut)
	}
	if n := retireEvents(t, c); n != 1 || len(units.calls) != 1 {
		t.Fatalf("second run wrote %d events and %d unit calls; want 1 and 1", n, len(units.calls))
	}

	// A leased card: exit 2, nothing changed, no unit stopped.
	seedBench(t, c, "y")
	c.ZAdd(ctx, "bench:y:living", redis.Z{Score: 1, Member: "s1/c9/1"})
	before := dump(t, c)
	code, _, errOut = retire(t, addr, "--bench", "y", "--why", "test")
	if code != 2 || !strings.Contains(errOut, "status=LEASED") || !strings.Contains(errOut, "bench reset --bench y") {
		t.Fatalf("leased: exit %d stderr %q; want 2 LEASED naming bench reset", code, errOut)
	}
	if after := dump(t, c); after != before || len(units.calls) != 1 {
		t.Fatalf("leased retire changed the store or ran the unit half:\n%s\nvs\n%s", before, after)
	}
}

// TestBenchRetireUnreachable: a unit half that cannot reach the bench exits 1
// with Redis untouched and names --offline; --offline then retires.
func TestBenchRetireUnreachable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, addr, units := retireFixture(t)
	seedBench(t, c, "z")
	units.fail = true
	before := dump(t, c)
	code, _, errOut := retire(t, addr, "--bench", "z", "--why", "gone")
	if code != 1 || !strings.Contains(errOut, "status=UNREACHABLE") || !strings.Contains(errOut, "--offline") {
		t.Fatalf("unreachable: exit %d stderr %q", code, errOut)
	}
	if dump(t, c) != before {
		t.Fatal("an unreachable retire changed the store")
	}
	code, out, errOut := retire(t, addr, "--bench", "z", "--why", "gone", "--offline")
	if code != 0 || !strings.Contains(out, "units=skipped") || len(units.calls) != 1 {
		t.Fatalf("offline: exit %d stdout %q stderr %q calls %d", code, out, errOut, len(units.calls))
	}
	if keys := c.Keys(ctx, "bench:z*").Val(); len(keys) != 0 {
		t.Fatalf("offline retire left %v", keys)
	}
	if code, _, _ := retire(t, addr, "--bench", "z"); code != 2 {
		t.Fatalf("no --why: exit %d, want 2", code)
	}
}
