//go:build functional

package tset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
	fx := &tsetFixture{Client: client, Space: lifecycleStoreSpace, Epoch: "0", profile: fn.TSetStandalone, t: t,
		loaded: true, active: true, tables: []string{}, seededEpoch: "0"}
	return fx, newFixtureRedis(t, client), build
}

// lifecycleStoreSpace is the lifecycle tests' space, of the space grammar
// (ValidNamespace).
const lifecycleStoreSpace = "{l1}:"

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
	fixture := newTSetFixtureSpace(t, fn.TSetStandalone, lifecycleStoreSpace)
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
		fixture := newTSetFixtureSpace(t, fn.TSetStandalone, lifecycleStoreSpace)
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
			{Version, `{"space":"{l1}:","build":"` + build + `","view":"sprint","tables":[],"extra":"x"}`, "REQUEST"},
			{Version, `{"build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, "REQUEST"},
			{Version, `{"space":"cap:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, "CONFIG"},
			{Version, `{"space":"s:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, "CONFIG"},
			{Version, `{"space":"l1:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, "CONFIG"},
			{Version, `{"space":"{l1}:dev:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, "CONFIG"},
			{Version, `{"space":"{l1}:","build":"` + build + `","view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"count:c"}]}]}`, "CONFIG"},
			{Version, `{"space":"{l1}:","build":"` + build + `","view":"sprint","tables":[{"t":"a","columns":[{"name":"c","kind":"set"}]},{"t":"b","columns":[{"name":"c","kind":"set"}]},{"t":"c","columns":[{"name":"c","kind":"set"}]},{"t":"d","columns":[{"name":"c","kind":"set"}]},{"t":"e","columns":[{"name":"c","kind":"set"}]}]}`, "LIMIT"},
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
		{"running after a start", "sprint", map[string]any{"stopped_ms": "40", "stopped_since_ms": ""}, "RUNNING"},
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

// withoutPrefix is image without the keys under prefix.
func withoutPrefix(image map[string]testredis.Entry, prefix string) map[string]testredis.Entry {
	out := make(map[string]testredis.Entry, len(image))
	for k, v := range image {
		if !strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out
}

// seedLifecycleSpace defines space and gives it rows, members and an upper
// layer's keys, count of them.
func seedLifecycleSpace(t *testing.T, fx *tsetFixture, store *RedisStore, space, build string, count int) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Define(ctx, lifecycleStoreSpec(space, build)); err != nil {
		t.Fatalf("define %s: %v", space, err)
	}
	ids := make([]string, 0, count)
	scores := make([]string, 0, count)
	for i := 0; i < count; i++ {
		ids = append(ids, "card-"+strconv.Itoa(i))
		scores = append(scores, strconv.Itoa(i))
	}
	for _, step := range []Step{
		{Epoch: "0", Space: space, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}},
		{Epoch: "0", Space: space, Entries: []Entry{{Kind: "create", Table: "work", To: "r:ready", IDs: ids, Scores: scores}}},
	} {
		if r, err := store.Step(ctx, step); err != nil || r.Status != "ok" {
			t.Fatalf("setup step on %s: %+v %v", space, r, err)
		}
	}
	pipe := fx.Client.Pipeline()
	for i := 0; i < count; i++ {
		pipe.Set(ctx, space+"sprint:upper:"+strconv.Itoa(i), "x", 0)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestSpacesAreConfined: the space grammar {<name>}: (the amendment, section
// 1) keeps a teardown inside its own space. A legacy prefix is refused CONFIG
// by define and teardown with the store byte-equal; {l1}: and {l1-dev}: stand
// side by side, and tearing one down leaves the other, and every key outside
// both, byte-equal.
func TestSpacesAreConfined(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx, store, build := lifecycleStore(t)
	const other = "{l1-dev}:"
	seedLifecycleSpace(t, fx, store, lifecycleStoreSpace, build, 300)
	seedLifecycleSpace(t, fx, store, other, build, 300)
	pipe := fx.Client.Pipeline()
	for _, k := range []string{"cap:legacy-data", "s:legacy", "l1:sprint:epoch", "l1:dev:sprint:view", "{l1}", "{l1}x"} {
		pipe.Set(ctx, k, "legacy", 0)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	before := lifecycleImage(t, fx.Client, "")
	for _, space := range []string{"cap:", "s:", "l1:", "l1:dev:", "{l1}", "{L1}:", "{1l}:", "{" + strings.Repeat("a", MaxNamespaceName+1) + "}:", ""} {
		raw := fmt.Sprintf(`{"space":%q,"confirm":"sprint"}`, space)
		reply, err := fx.Client.FCall(ctx, "ns_tset_teardown", nil, Version, raw).Text()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeReply([]byte(reply)); lifecycleRefusal(t, err) != "CONFIG" {
			t.Errorf("teardown of %q: %s, want CONFIG", space, reply)
		}
		spec := lifecycleStoreSpec(space, build)
		spec.Tables = spec.Tables[:1]
		raw = fmt.Sprintf(`{"space":%q,"build":%q,"view":"sprint","tables":[{"t":"work","columns":[{"name":"c","kind":"set"}]}]}`, space, build)
		reply, err = fx.Client.FCall(ctx, "ns_tset_define", nil, Version, raw).Text()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeReply([]byte(reply)); lifecycleRefusal(t, err) != "CONFIG" {
			t.Errorf("define of %q: %s, want CONFIG", space, reply)
		}
		if _, err := store.Teardown(ctx, space, "sprint"); lifecycleRefusal(t, err) != "CONFIG" {
			t.Errorf("client teardown of %q: %v, want CONFIG", space, err)
		}
		if _, err := store.Define(ctx, spec); lifecycleRefusal(t, err) != "CONFIG" {
			t.Errorf("client define of %q: %v, want CONFIG", space, err)
		}
		if _, err := NewMem().Teardown(ctx, space, "sprint"); lifecycleRefusal(t, err) != "CONFIG" {
			t.Errorf("twin teardown of %q: %v, want CONFIG", space, err)
		}
	}
	if diff := testredis.Diff(before, lifecycleImage(t, fx.Client, "")); len(diff) != 0 {
		t.Fatalf("refused lifecycle calls wrote: %v", diff)
	}
	reply, err := store.Teardown(ctx, lifecycleStoreSpace, "sprint")
	if err != nil || !reply.Done {
		t.Fatalf("teardown of %s: %+v %v", lifecycleStoreSpace, reply, err)
	}
	after := lifecycleImage(t, fx.Client, "")
	if diff := testredis.Diff(withoutPrefix(before, lifecycleStoreSpace), withoutPrefix(after, lifecycleStoreSpace)); len(diff) != 0 {
		t.Fatalf("teardown of %s changed keys outside it: %v", lifecycleStoreSpace, diff)
	}
	for k := range after {
		if strings.HasPrefix(k, lifecycleStoreSpace) && k != lifecycleStoreSpace+"sprint:lifecycle" {
			t.Fatalf("teardown of %s left %s", lifecycleStoreSpace, k)
		}
	}
	// The twin agrees: the other space stands.
	mem := NewMem()
	for _, space := range []string{lifecycleStoreSpace, other} {
		if _, err := mem.Define(ctx, lifecycleStoreSpec(space, build)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mem.Teardown(ctx, lifecycleStoreSpace, "sprint"); err != nil {
		t.Fatal(err)
	}
	if mem.View(other) != "sprint" || mem.View(lifecycleStoreSpace) != "" {
		t.Fatalf("twin views after teardown: %q %q", mem.View(other), mem.View(lifecycleStoreSpace))
	}
}

// TestDefineRefusesLeftovers: define refuses EXISTS over any key under the
// space but its receipt stream (the amendment, section 2), found by bounded
// SCAN passes, with the store byte-equal.
func TestDefineRefusesLeftovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, leftover := range []string{"sprint:done@0", "member:work:card-1", "sprint:upper:x"} {
		t.Run(leftover, func(t *testing.T) {
			t.Parallel()
			fx, store, build := lifecycleStore(t)
			if err := fx.Client.XAdd(ctx, &redis.XAddArgs{Stream: fx.Space + "sprint:lifecycle",
				Values: []string{"fn", "teardown"}}).Err(); err != nil {
				t.Fatal(err)
			}
			if err := fx.Client.Set(ctx, fx.Space+leftover, "stale", 0).Err(); err != nil {
				t.Fatal(err)
			}
			before := lifecycleImage(t, fx.Client, "")
			if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); lifecycleRefusal(t, err) != "EXISTS" {
				t.Fatalf("define over %s: %v, want EXISTS", leftover, err)
			}
			if diff := testredis.Diff(before, lifecycleImage(t, fx.Client, "")); len(diff) != 0 {
				t.Fatalf("a refused define wrote: %v", diff)
			}
			// The receipt stream alone is no leftover.
			if err := fx.Client.Del(ctx, fx.Space+leftover).Err(); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
				t.Fatalf("define over the receipt stream alone: %v", err)
			}
		})
	}
	t.Run("twin", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		if err := m.SetActiveEpoch(lifecycleStoreSpace, "0"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Define(ctx, lifecycleStoreSpec(lifecycleStoreSpace, "b")); lifecycleRefusal(t, err) != "EXISTS" {
			t.Fatalf("twin define over a leftover epoch: %v", err)
		}
	})
}

// teardownCall is one raw ns_tset_teardown call's answer.
func teardownCall(t *testing.T, c *redis.Client, space string) string {
	t.Helper()
	reply, err := c.FCall(context.Background(), "ns_tset_teardown", nil, Version,
		fmt.Sprintf(`{"space":%q,"confirm":"sprint"}`, space)).Text()
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// TestTeardownRefusesAStartBetweenCalls: the clock goes last, with the view
// (the amendment, section 3), so a machine started between two calls of a
// teardown is refused RUNNING by the next call, with the partial byte-equal;
// stopped again, the teardown finishes.
func TestTeardownRefusesAStartBetweenCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx, store, build := lifecycleStore(t)
	seedLifecycleSpace(t, fx, store, fx.Space, build, 1500)
	clock := fx.Space + "sprint:clock"
	if err := fx.Client.HSet(ctx, clock, "stopped_ms", "0", "stopped_since_ms", "5").Err(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if reply := teardownCall(t, fx.Client, fx.Space); !strings.Contains(reply, `"done":false`) {
			t.Fatalf("call %d: %s, want a partial", i+1, reply)
		}
	}
	if n, err := fx.Client.Exists(ctx, clock).Result(); err != nil || n != 1 {
		t.Fatalf("the clock is gone from the partial (exists %d, err %v)", n, err)
	}
	// A start: stopped_since_ms "" (v2.1 1.2).
	if err := fx.Client.HSet(ctx, clock, "stopped_since_ms", "").Err(); err != nil {
		t.Fatal(err)
	}
	before := lifecycleImage(t, fx.Client, "")
	if _, err := store.Teardown(ctx, fx.Space, "sprint"); lifecycleRefusal(t, err) != "RUNNING" {
		t.Fatalf("teardown after a start: %v, want RUNNING", err)
	}
	if diff := testredis.Diff(before, lifecycleImage(t, fx.Client, "")); len(diff) != 0 {
		t.Fatalf("a RUNNING refusal on the partial wrote: %v", diff)
	}
	if err := fx.Client.HSet(ctx, clock, "stopped_since_ms", "9").Err(); err != nil {
		t.Fatal(err)
	}
	if r, err := store.Teardown(ctx, fx.Space, "sprint"); err != nil || !r.Done {
		t.Fatalf("teardown after the stop: %+v %v", r, err)
	}
	keys, err := fx.Client.Keys(ctx, "*").Result()
	if err != nil || len(keys) != 1 || keys[0] != fx.Space+"sprint:lifecycle" {
		t.Fatalf("after teardown the store holds %v (err %v)", keys, err)
	}
}

// TestTeardownReadsTheClockByTheDesign: stopped_since_ms "" is RUNNING; "0"
// and a time are STOPPED (v2.1 1.2; ClockRunning is the Go side's rule).
func TestTeardownReadsTheClockByTheDesign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		since   string
		running bool
	}{{"", true}, {"0", false}, {"1759240000000", false}} {
		t.Run("since="+tc.since, func(t *testing.T) {
			t.Parallel()
			fx, store, build := lifecycleStore(t)
			if _, err := store.Define(ctx, lifecycleStoreSpec(fx.Space, build)); err != nil {
				t.Fatal(err)
			}
			if err := fx.Client.HSet(ctx, fx.Space+"sprint:clock", "stopped_ms", "0", "stopped_since_ms", tc.since).Err(); err != nil {
				t.Fatal(err)
			}
			since := tc.since
			if ClockRunning(&since) != tc.running {
				t.Fatalf("ClockRunning(%q) = %v", tc.since, !tc.running)
			}
			_, err := store.Teardown(ctx, fx.Space, "sprint")
			if tc.running {
				if lifecycleRefusal(t, err) != "RUNNING" {
					t.Fatalf("teardown: %v, want RUNNING", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("teardown of a stopped machine: %v", err)
			}
		})
	}
}
