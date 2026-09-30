//go:build functional

package tset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// The lifecycle on a store (the L1 contract amendment, lifecycle,
// 2026-09-30): ns_tset_define writes exactly the fixture initializer's state
// (L1 1.2) plus the view and a receipt; ns_tset_teardown deletes every key of
// the space in bounded atomic batches and leaves only the receipt stream; the
// refusals write nothing and agree with the twin.

// lifecycleStore is a store of its own with the tset library loaded and no
// key written: a fixture's server with nothing seeded.
func lifecycleStore(t *testing.T) (*tsetFixture, *RedisStore, string) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.LoadTSet(ctx, client, fn.TSetStandalone); err != nil {
		t.Fatal(err)
	}
	build, err := fn.TSetBuild(fn.TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	fx := &tsetFixture{Client: client, Space: "l1:", Epoch: "0", profile: fn.TSetStandalone, t: t,
		loaded: true, active: true, tables: []string{}, seededEpoch: "0"}
	return fx, newFixtureRedis(t, client), build
}

func lifecycleStoreSpec(space, build string) DefineSpec {
	return DefineSpec{Space: space, Build: build, View: "sprint", Tables: []TableSpec{
		{Name: "work", Columns: []ColumnSpec{{Name: "ready", Kind: ColumnKindSet}, {Name: "done", Kind: ColumnKindSet}}},
		{Name: "readers", Columns: []ColumnSpec{{Name: "asked", Kind: ColumnKindSet}}},
	}}
}

func lifecycleImage(t *testing.T, c *redis.Client, prefix string) map[string]testredis.Entry {
	t.Helper()
	image, err := testredis.Image(context.Background(), c, prefix)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func lifecycleRefusal(t *testing.T, err error) string {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("error %v is not a refusal", err)
	}
	return r.Code
}

// lifecycleReceiptFns are the fn fields of the space's receipt stream.
func lifecycleReceiptFns(t *testing.T, c *redis.Client, space string) []string {
	t.Helper()
	entries, err := c.XRange(context.Background(), space+"sprint:lifecycle", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var fns []string
	for _, e := range entries {
		fns = append(fns, fmt.Sprint(e.Values["fn"]))
	}
	return fns
}

func TestDefineEqualsTheFixtureState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newTSetFixture(t)
	fixture.Define(t, "work", "ready", "done")
	fixture.Define(t, "readers", "asked")
	fixture.Activate(t)
	want := lifecycleImage(t, fixture.Client, fixture.Space)

	fx, store, build := lifecycleStore(t)
	reply, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Status != "ok" || reply.Epoch != "0" || reply.View != "sprint" || !reflect.DeepEqual(reply.Tables, []string{"work", "readers"}) {
		t.Fatalf("define reply %+v", reply)
	}
	got := lifecycleImage(t, fx.Client, fx.Space)
	view, receipts := fx.Space+"sprint:view", fx.Space+"sprint:lifecycle"
	if got[view].Type != "hash" || got[receipts].Type != "stream" {
		t.Fatalf("define wrote no view or receipt stream: %v", got)
	}
	delete(got, view)
	delete(got, receipts)
	if diff := testredis.Diff(want, got); len(diff) != 0 {
		t.Fatalf("define's image differs from the fixture's, apart from the view and the receipts: %v", diff)
	}
	if h, err := fx.Client.HGetAll(ctx, view).Result(); err != nil ||
		!reflect.DeepEqual(h, map[string]string{"engine": Version, "name": "sprint", "tables": `["work","readers"]`}) {
		t.Fatalf("view %v (err %v)", h, err)
	}
	if fns := lifecycleReceiptFns(t, fx.Client, fx.Space); !reflect.DeepEqual(fns, []string{"define"}) {
		t.Fatalf("receipts %v", fns)
	}

	// The twin's Define is the same state.
	mem := NewMem()
	if _, err := mem.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatal(err)
	}
	model, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	if lua := fx.SemanticSnapshot(t); !reflect.DeepEqual(model, lua) {
		t.Fatalf("Mem/Lua define differs: %s", compareJSON("state", model, lua))
	}
	// The defined space takes the same public step on both.
	step := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}}
	luaReply, luaErr := store.Step(ctx, step)
	memReply, memErr := mem.Step(ctx, step)
	if luaErr != nil || memErr != nil || luaReply.Status != "ok" || memReply.Status != "ok" {
		t.Fatalf("a step on the defined space: Lua %+v %v, Mem %+v %v", luaReply, luaErr, memReply, memErr)
	}
	model, _ = mem.Snapshot(fx.Space)
	if lua := fx.SemanticSnapshot(t); !reflect.DeepEqual(model, lua) {
		t.Fatalf("Mem/Lua after a step differs: %s", compareJSON("state", model, lua))
	}
}

func TestDefineRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("EXISTS", func(t *testing.T) {
		t.Parallel()
		fx, store, build := lifecycleStore(t)
		if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
			t.Fatal(err)
		}
		before := lifecycleImage(t, fx.Client, "")
		_, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build))
		if code := lifecycleRefusal(t, err); code != "EXISTS" {
			t.Fatalf("second define: %s", code)
		}
		if diff := testredis.Diff(before, lifecycleImage(t, fx.Client, "")); len(diff) != 0 {
			t.Fatalf("a refused define wrote: %v", diff)
		}
		mem := NewMem()
		_, _ = mem.Define(ctx, lifecycleStoreSpec(fx.Space, build))
		if _, err := mem.Define(ctx, lifecycleStoreSpec(fx.Space, build)); lifecycleRefusal(t, err) != "EXISTS" {
			t.Fatalf("twin second define: %v", err)
		}
	})
	t.Run("EXISTS over the fixture", func(t *testing.T) {
		t.Parallel()
		fixture := newTSetFixture(t)
		fixture.Define(t, "work", "ready")
		fixture.Activate(t)
		build, err := fn.TSetBuild(fn.TSetStandalone)
		if err != nil {
			t.Fatal(err)
		}
		before := lifecycleImage(t, fixture.Client, "")
		_, err = newFixtureRedis(t, fixture.Client).Define(ctx, lifecycleStoreSpec(fixture.Space, build))
		if code := lifecycleRefusal(t, err); code != "EXISTS" {
			t.Fatalf("define over the fixture: %s", code)
		}
		if diff := testredis.Diff(before, lifecycleImage(t, fixture.Client, "")); len(diff) != 0 {
			t.Fatalf("a refused define wrote: %v", diff)
		}
	})
	t.Run("BUILD", func(t *testing.T) {
		t.Parallel()
		fx, store, build := lifecycleStore(t)
		spec := lifecycleStoreSpec(fx.Space, build+"x")
		if _, err := store.Define(ctx, spec); lifecycleRefusal(t, err) != "BUILD" {
			t.Fatalf("define of another build: %v", err)
		}
		if n, err := fx.Client.DBSize(ctx).Result(); err != nil || n != 0 {
			t.Fatalf("a BUILD refusal wrote %d keys (err %v)", n, err)
		}
		mem := NewMem()
		mem.SetBuild(build)
		if _, err := mem.Define(ctx, spec); lifecycleRefusal(t, err) != "BUILD" {
			t.Fatalf("twin define of another build: %v", err)
		}
	})
	t.Run("a composed library is another build", func(t *testing.T) {
		t.Parallel()
		fx, store, _ := lifecycleStore(t)
		requireTSetLogFragment(t)
		composed, err := fn.TSetBuild(fn.TSetComposed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, composed)); lifecycleRefusal(t, err) != "BUILD" {
			t.Fatalf("define naming the composed build on a standalone store: %v", err)
		}
	})
	t.Run("the wire's refusals", func(t *testing.T) {
		t.Parallel()
		fx, _, build := lifecycleStore(t)
		for _, tc := range []struct{ version, raw, code string }{
			{"tset/0", `{}`, "VERSION"},
			{Version, `[]`, "REQUEST"},
			{Version, `{"space":"l1:","build":"` + build + `","view":"sprint","tables":[],"extra":"x"}`, "REQUEST"},
			{Version, `{"space":"l1:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"count:c"}]}]}`, "CONFIG"},
			{Version, `{"space":"l1:","build":"` + build + `","view":"sprint","tables":[{"t":"a","columns":[{"name":"c","kind":"set"}]},{"t":"b","columns":[{"name":"c","kind":"set"}]},{"t":"c","columns":[{"name":"c","kind":"set"}]},{"t":"d","columns":[{"name":"c","kind":"set"}]},{"t":"e","columns":[{"name":"c","kind":"set"}]}]}`, "LIMIT"},
		} {
			reply, err := fx.Client.FCall(ctx, "ns_tset_define", nil, tc.version, tc.raw).Text()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeReply([]byte(reply)); lifecycleRefusal(t, err) != tc.code {
				t.Errorf("%s: %s, want %s", tc.raw, reply, tc.code)
			}
		}
		if n, err := fx.Client.DBSize(ctx).Result(); err != nil || n != 0 {
			t.Fatalf("refused defines wrote %d keys (err %v)", n, err)
		}
	})
}

func TestTeardownLeavesTheStoreEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx, store, build := lifecycleStore(t)
	if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatal(err)
	}
	// Table state through public steps: rows, members and an advance.
	ids := make([]string, 0, 1500)
	scores := make([]string, 0, 1500)
	for i := 0; i < 1500; i++ {
		ids = append(ids, "card-"+strconv.Itoa(i))
		scores = append(scores, strconv.Itoa(i))
	}
	for _, step := range []Step{
		{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}},
		{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create", Table: "work", To: "r:ready", IDs: ids, Scores: scores}}},
	} {
		if r, err := store.Step(ctx, step); err != nil || r.Status != "ok" {
			t.Fatalf("setup step: %+v %v", r, err)
		}
	}
	// Keys of the space no table owns (an upper layer's), and one outside it.
	pipe := fx.Client.Pipeline()
	for i := 0; i < 1200; i++ {
		pipe.Set(ctx, fx.Space+"sprint:upper:"+strconv.Itoa(i), "x", 0)
	}
	pipe.HSet(ctx, fx.Space+"sprint:clock", "stopped_ms", "0", "stopped_since_ms", "5")
	pipe.Set(ctx, "elsewhere:keep", "x", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	total, err := fx.Client.DBSize(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	reply, err := store.Teardown(ctx, fx.Space, "sprint")
	if err != nil {
		t.Fatalf("teardown: %+v %v", reply, err)
	}
	// Everything but the view (deleted last, not counted), the receipt stream
	// and the key outside the space.
	if !reply.Done || reply.Calls < 3 || reply.Deleted != total-3 {
		t.Fatalf("teardown %+v of %d keys", reply, total)
	}
	keys, err := fx.Client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"elsewhere:keep", fx.Space + "sprint:lifecycle"}) {
		t.Fatalf("after teardown the store holds %v", keys)
	}
	fns := lifecycleReceiptFns(t, fx.Client, fx.Space)
	if len(fns) < 3 || fns[0] != "define" || fns[len(fns)-1] != "teardown" {
		t.Fatalf("receipts %v", fns)
	}
	// Teardown again is NOSPACE, on the store and the twin; define again
	// succeeds, and the state is the fixture's again.
	if _, err := store.Teardown(ctx, fx.Space, "sprint"); lifecycleRefusal(t, err) != "NOSPACE" {
		t.Fatalf("second teardown: %v", err)
	}
	if _, err := NewMem().Teardown(ctx, fx.Space, "sprint"); lifecycleRefusal(t, err) != "NOSPACE" {
		t.Fatalf("twin teardown of no space: %v", err)
	}
	if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatalf("define after teardown: %v", err)
	}
	mem := NewMem()
	if _, err := mem.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatal(err)
	}
	model, _ := mem.Snapshot(fx.Space)
	if lua := fx.SemanticSnapshot(t); !reflect.DeepEqual(model, lua) {
		t.Fatalf("Mem/Lua after define, teardown, define: %s", compareJSON("state", model, lua))
	}
}

func TestTeardownRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx, store, build := lifecycleStore(t)
	if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatal(err)
	}
	mem := NewMem()
	if _, err := mem.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
		t.Fatal(err)
	}
	clock := fx.Space + "sprint:clock"
	for _, tc := range []struct {
		name, confirm string
		clock         map[string]any // the clock hash; nil for none
		code          string
	}{
		{"another name", "work", nil, "CONFIRM"},
		{"running, never stopped", "sprint", map[string]any{"stopped_ms": "0"}, "RUNNING"},
		{"running after a start", "sprint", map[string]any{"stopped_ms": "40", "stopped_since_ms": "0"}, "RUNNING"},
	} {
		if err := fx.Client.Del(ctx, clock).Err(); err != nil {
			t.Fatal(err)
		}
		if tc.clock != nil {
			if err := fx.Client.HSet(ctx, clock, tc.clock).Err(); err != nil {
				t.Fatal(err)
			}
		}
		mem.SetRunning(fx.Space, tc.code == "RUNNING")
		before := lifecycleImage(t, fx.Client, "")
		if _, err := store.Teardown(ctx, fx.Space, tc.confirm); lifecycleRefusal(t, err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
		if diff := testredis.Diff(before, lifecycleImage(t, fx.Client, "")); len(diff) != 0 {
			t.Errorf("%s: a refused teardown wrote %v", tc.name, diff)
		}
		if _, err := mem.Teardown(ctx, fx.Space, tc.confirm); lifecycleRefusal(t, err) != tc.code {
			t.Errorf("%s: twin %v, want %s", tc.name, err, tc.code)
		}
	}
	// Stopped: the teardown runs, on both.
	if err := fx.Client.HSet(ctx, clock, "stopped_since_ms", "77").Err(); err != nil {
		t.Fatal(err)
	}
	mem.SetRunning(fx.Space, false)
	if r, err := store.Teardown(ctx, fx.Space, "sprint"); err != nil || !r.Done {
		t.Fatalf("teardown while stopped: %+v %v", r, err)
	}
	if r, err := mem.Teardown(ctx, fx.Space, "sprint"); err != nil || !r.Done {
		t.Fatalf("twin teardown while stopped: %+v %v", r, err)
	}
	if keys, err := fx.Client.Keys(ctx, "*").Result(); err != nil || len(keys) != 1 {
		t.Fatalf("after teardown %v (err %v)", keys, err)
	}
}
