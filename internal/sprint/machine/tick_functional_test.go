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
	c, err := sprintfn.NewRedis(addr, "", "", testNames)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	w := &world{t: t, c: c, raw: raw, clk: &clock{at: time.Now().UTC().Truncate(time.Millisecond)}}
	w.seed()
	return w
}

// requireSprintProfile fails a tick's store-tier test with what it still
// owes: it never passes by doing nothing.
func requireSprintProfile(t *testing.T) {
	t.Helper()
	if _, err := fn.TSetSource(fn.TSetSprint); err != nil {
		t.Fatalf("the sprint profile does not assemble: %v", err)
	}
	t.Fatal("owed: the tick on a store world (newStoreWorld), its round trips counted " +
		"(idle 1, busy at most 3, as tick_test.go pins on the twin); it needs a way to put the counting hook " +
		"on sprintfn.Redis's own client, which has no exported seam")
}

// TestTickStoreIdleOneRoundTrip: on the store, an idle tick is one round trip
// (1.4.2, T5; E8).
func TestTickStoreIdleOneRoundTrip(t *testing.T) {
	t.Parallel()
	requireTSetLogFragment(t)
	requireSprintProfile(t)
}

// TestTickStoreBusyAtMostThree: on the store, a busy tick is at most three
// round trips (1.4.2; E8).
func TestTickStoreBusyAtMostThree(t *testing.T) {
	t.Parallel()
	requireTSetLogFragment(t)
	requireSprintProfile(t)
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
