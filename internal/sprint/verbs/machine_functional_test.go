//go:build functional

package verbs

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// storeNames are the store tier's names: the new path's namespace.
var storeNames = sprint.Names{Prefix: "{sprint}:"}

// TestMachineVerbsOnAStore: the store tier of the machine's verbs. On a store
// of the test's own holding this build's sprint library, Layer 1's lifecycle
// defines the namespace (DefineSpec), then Init, Start, Stop and Clear run
// through sprintfn.Redis, each two round trips on the connection (its read and
// its step, 1.5.3; clear of an empty sprint is one part), as on the twin; the
// clock reads STOPPED, RUNNING, STOPPED, and clear moves the epoch to 1.
func TestMachineVerbsOnAStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testredis.Start(t)
	raw := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = raw.Close() })
	if err := fn.LoadTSet(ctx, raw, fn.TSetSprint); err != nil {
		t.Fatalf("FUNCTION LOAD of the sprint profile: %v", err)
	}
	build, err := fn.TSetBuild(fn.TSetSprint)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := tset.NewRedis(addr, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	if defined, err := Define(ctx, lc, storeNames, build); err != nil || !defined {
		t.Fatalf("define: %v, defined %v", err, defined)
	}
	if defined, err := Define(ctx, lc, storeNames, build); err != nil || defined {
		t.Fatalf("define again: %v, defined %v; want EXISTS, taken as defined", err, defined)
	}
	c, err := sprintfn.NewRedis(addr, "", "", storeNames, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	trips, hook := testredis.RoundTrips(t)
	c.AddHookForTest(hook)
	e := &Env{C: c, Names: storeNames, Actor: "coordinator"}
	clock := func() string {
		t.Helper()
		h, err := raw.HGetAll(ctx, storeNames.Key("clock")).Result()
		if err != nil {
			t.Fatal(err)
		}
		if h["stopped_since_ms"] != "" {
			return "STOPPED"
		}
		return "RUNNING"
	}
	for _, v := range []struct {
		name  string
		run   func() (Result, error)
		state string
	}{
		{"init", func() (Result, error) { return Init(ctx, e, InitReq{Coordinator: "coordinator"}) }, "STOPPED"},
		{"start", func() (Result, error) { return Start(ctx, e, ClockReq{}) }, "RUNNING"},
		{"stop", func() (Result, error) { return Stop(ctx, e, ClockReq{}) }, "STOPPED"},
		{"clear", func() (Result, error) { return Clear(ctx, e, ClearReq{Confirm: storeNames.Prefix}) }, ""},
	} {
		var res Result
		var err error
		trips.Expect(t, 2, func() { res, err = v.run() })
		if err != nil || res.Trips != 2 {
			t.Fatalf("%s on the store: %v, %d round trips", v.name, err, res.Trips)
		}
		if v.state != "" && clock() != v.state {
			t.Fatalf("after %s the clock is %s, want %s", v.name, clock(), v.state)
		}
		if v.name == "clear" && (res.EpochAfter != 1 || e.Epoch != 1) {
			t.Fatalf("clear: the epoch after is %d (env %d), want 1", res.EpochAfter, e.Epoch)
		}
	}
}
