//go:build functional

package machine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// The store tier of IT17: the round trips of a tick counted on a real store
// (E8's count, in the functional container), which the twin tests pin on the
// twin, and the real rules' steps applied through ns_sprint_step. Each runs on
// a store world: a private redis-server of the test's own, the four tables
// defined on it as newWorld defines them on the twin's Mem, the sprint
// profile (fn.TSetSprint: Layer 1, Layer 2 and the sprint's functions) loaded,
// and every step and read sent through sprintfn.Redis. They skip while Layer
// 2's fragment is absent (storetier_test.go). The limit of IT17 (a busy tick
// at most three round trips, 2 MiB each way, 150 ms of store and Go time
// beyond them, p99 over ten minutes at 128 ms) is the owner's drive through E9
// and at a far bench store, not these tests'.

// storeTables are the four tables in newWorld's order, which is the catalog's.
var storeTables = []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}

// newStoreWorld is newWorld on a real store: each table's definition written
// as Layer 1 reads one (<space>table:<t> and its :definition snapshot: engine,
// order, col:<c>=set, member_prefix, epoch_key, epoch_field; L1 1.2), the
// epoch marker at epoch 0 with the catalog of the four (the key and its @0
// snapshot), the sprint profile loaded, and the coordinator's seed step. Its
// clock starts at the wall time, the store's TIME being the store's clock.
func newStoreWorld(t *testing.T) *world {
	t.Helper()
	requireTSetLogFragment(t)
	if _, err := fn.TSetSource(fn.TSetSprint); err != nil {
		t.Fatalf("the sprint profile does not assemble: %v", err)
	}
	addr := testredis.Start(t)
	raw := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = raw.Close() })
	ctx := context.Background()
	pipe := raw.Pipeline()
	for _, table := range storeTables {
		cols := testColumns[table]
		fields := map[string]any{
			"engine":        tset.Version,
			"order":         strings.Join(cols, ","),
			"member_prefix": testNames.TSetMemberPrefix(table),
			"epoch_key":     testNames.EpochKey(),
			"epoch_field":   "n",
		}
		for _, c := range cols {
			fields["col:"+c] = "set"
		}
		base := testNames.Prefix + "table:" + table
		pipe.HSet(ctx, base, fields)
		pipe.HSet(ctx, base+":definition", fields)
	}
	catalog, err := json.Marshal(storeTables)
	if err != nil {
		t.Fatal(err)
	}
	marker := map[string]any{"engine": tset.Version, "n": "0", "tables": string(catalog)}
	pipe.HSet(ctx, testNames.EpochKey(), marker)
	pipe.HSet(ctx, testNames.EpochKey()+"@0", marker)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("define the four tables on the store: %v", err)
	}
	if err := fn.LoadTSet(ctx, raw, fn.TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	c, err := sprintfn.NewRedis(addr, "", "", testNames, storeLibrary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	w := &world{t: t, c: c, raw: raw, clk: &clock{at: time.Now().UTC().Truncate(time.Millisecond)}}
	w.seed()
	return w
}

// storeLibrary is this build's sprint library, which the store world's client
// checks the store against before its first call, as the command's does.
var storeLibrary = &sprintfn.Library{Name: fn.Library, Sum: fn.Sum,
	Source: func() (string, error) { return fn.TSetSource(fn.TSetSprint) }}

// storeTrips puts a round-trip counter on the store world's own client
// (testredis.RoundTrips, added last; sprintfn.Redis.AddHookForTest, the
// functional build's seam): what it counts is what the connection sent and
// waited for, the store's own count of E8.
func storeTrips(t *testing.T, w *world) *testredis.Counter {
	t.Helper()
	r, ok := w.c.(*sprintfn.Redis)
	if !ok {
		t.Fatalf("the store world's client is %T, not sprintfn.Redis", w.c)
	}
	trips, hook := testredis.RoundTrips(t)
	r.AddHookForTest(hook)
	return trips
}

// lastSeq is the log's last seq at epoch 0, read through the world's client.
func (w *world) lastSeq() string {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.c, &sprintfn.ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "last"}}})
	if err != nil || res.Read == nil {
		w.t.Fatalf("read the log's last seq: %v %+v", err, res.Refusal)
	}
	return string(res.Read.Tset[0].LastSeq)
}

// TestTickStoreIdleOneRoundTrip: on the store, an idle tick is one round trip
// (1.4.2, T5; E8), counted on the connection: the lease step, the page and
// the read in one pipeline, which writes no line and moves no agenda entry.
// The ticks before it are TestTickIdleOneRoundTrip's on the twin.
func TestTickStoreIdleOneRoundTrip(t *testing.T) {
	t.Parallel()
	w := newStoreWorld(t)
	trips := storeTrips(t, w)
	w.rows("s1")
	w.verb(create("s1:ready", fresh(), "p1", "p2"))
	k := &counting{c: w.c}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	if rep := w.tick(l, k); rep.Lines == 0 || rep.Applied == 0 {
		t.Fatalf("the busy tick: %+v", rep)
	}
	if rep := w.tick(l, k); rep.Lines != 1 || rep.RoundTrips != 2 {
		t.Fatalf("the tick after: %+v", rep)
	}
	for i := 0; i < 3; i++ {
		w.clk.add(TickEvery)
		last, agenda := w.lastSeq(), w.zset("agenda@0")
		var rep Report
		trips.Expect(t, 1, func() { rep = w.tick(l, k) })
		if rep.RoundTrips != 1 || !rep.Held {
			t.Fatalf("idle tick %d: %d round trips, held %v, %+v", i, rep.RoundTrips, rep.Held, rep)
		}
		if got := w.lastSeq(); got != last {
			t.Fatalf("idle tick %d wrote lines: the last seq %s, was %s", i, got, last)
		}
		if got := w.zset("agenda@0"); len(got) != len(agenda) {
			t.Fatalf("idle tick %d changed the agenda: %v -> %v", i, agenda, got)
		}
		items := k.last()
		if len(items) != 3 || items[0].Step == nil || items[0].Step.Lease == nil || items[1].Page == nil || items[2].Read == nil {
			t.Fatalf("an idle tick's round trip is not the lease step, the page and the read: %+v", items)
		}
	}
}

// TestTickStoreBusyAtMostThree: on the store, a busy tick (new lines, keys,
// two rules' reads, their steps) is at most three round trips (1.4.2; E8),
// counted on the connection, and deals and releases on the store as the twin
// does (TestTickBusyAtMostThree); what it made due is dealt the next tick,
// again in at most three.
func TestTickStoreBusyAtMostThree(t *testing.T) {
	t.Parallel()
	w := newStoreWorld(t)
	trips := storeTrips(t, w)
	w.rows("s1")
	k := &counting{c: w.c}
	l := w.loop("a", []sprint.Rule{dealRule(64), releaseRule(64)}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1", "p2"), create("s1:waiting", waiting(), "q1", "q2", "q3"))
	var rep Report
	trips.Expect(t, 3, func() { rep = w.tick(l, k) })
	if rep.RoundTrips != 3 || len(rep.Dealt) != 2 || rep.Applied != 2 || rep.Read["deal"] != 1 || rep.Read["resolve"] != 1 {
		t.Fatalf("the busy tick did not deal and release in three round trips: %+v", rep)
	}
	for _, id := range []string{"p1", "p2"} {
		if p := w.place(id); p != "s1:working" {
			t.Fatalf("%s is at %q, not dealt", id, p)
		}
	}
	for _, id := range []string{"q1", "q2", "q3"} {
		if p := w.place(id); p != "s1:ready" {
			t.Fatalf("%s is at %q, not released", id, p)
		}
	}
	w.clk.add(TickEvery)
	var n int
	trips.Expect(t, 3, func() { rep = w.tick(l, k); n = rep.RoundTrips })
	if n > 3 || w.place("q1") != "s1:working" {
		t.Fatalf("the next tick: %d round trips, q1 at %q", n, w.place("q1"))
	}
}

// The real rules' steps on the store (the integration's gaps a to e): the
// twin tier's TestRealRule* scenarios (real_rules_test.go), R3 resolve, R6
// deal, R15 done, R11 late and R17's look, each applied through
// ns_sprint_step on a store world and read back from the store.

func TestRealRuleStoreResolve(t *testing.T) {
	t.Parallel()
	realRuleResolve(t, newStoreWorld(t))
}

func TestRealRuleStoreDeal(t *testing.T) {
	t.Parallel()
	realRuleDeal(t, newStoreWorld(t))
}

func TestRealRuleStoreDone(t *testing.T) {
	t.Parallel()
	realRuleDone(t, newStoreWorld(t))
}

func TestRealRuleStoreLate(t *testing.T) {
	t.Parallel()
	realRuleLate(t, newStoreWorld(t))
}

func TestRealRuleStoreStoppedLook(t *testing.T) {
	t.Parallel()
	realRuleStoppedLook(t, newStoreWorld(t))
}
