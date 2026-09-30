//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// The supported writer boundary is a property of the library actually loaded
// on a private server. These checks invoke it, rather than inspecting Lua
// source or assuming that a function's declared flag limits its commands.
func TestLoadedWriterSurface(t *testing.T) {
	t.Parallel()
	profileLoadedSurface(t, newTSetFixture(t))
}

// The composed source has its own gate so an absent Layer 2 fragment cannot
// prevent the standalone registered surface from being checked.
func TestLoadedComposedWriterSurface(t *testing.T) {
	t.Parallel()
	profileLoadedSurface(t, newComposedTSetFixture(t))
}

// The legacy library is still available on its own server, while the tset
// profile owns the new writer. Neither profile may silently take over the
// other's writer surface.
func TestEngineIsolation(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	ctx := context.Background()
	if err := fn.Load(ctx, fx.Client); err != nil {
		t.Fatal(err)
	}
	libraries, err := fx.Client.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil || len(libraries) != 1 {
		t.Fatalf("legacy FUNCTION LIST: libraries=%+v err=%v", libraries, err)
	}
	foundOld, foundNew := false, false
	for _, function := range libraries[0].Functions {
		if function.Name == "ns_oset_move" {
			foundOld = true
		}
		if function.Name == "ns_tset_step" {
			foundNew = true
		}
	}
	if !foundOld || foundNew {
		t.Fatalf("legacy writer surface: ns_oset_move=%v ns_tset_step=%v", foundOld, foundNew)
	}
	before := commitProbeImage(t, fx.Client)
	_, err = newFixtureRedis(t, fx.Client).Step(ctx, profileCreateStep(fx.Space))
	var mapped *ClientError
	if !errors.As(err, &mapped) || mapped.Code != "FUNCTIONMISSING" {
		t.Fatalf("tset Step on legacy library = %v, want FUNCTIONMISSING", err)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("tset Step changed legacy store")
	}
}

func TestLegacyPreludeLoadsNoTsetFunctions(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	if err := fn.Load(context.Background(), fx.Client); err != nil {
		t.Fatal(err)
	}
	functions := profileFunctions(t, fx.Client)
	for _, name := range []string{"ns_tset_step", "ns_tset_read"} {
		if _, present := functions[name]; present {
			t.Errorf("legacy library registered %s", name)
		}
		_, err := fx.Client.FCall(context.Background(), name, []string{}).Result()
		if err == nil || !profileMissingFunctionError(err) {
			t.Errorf("legacy FCALL %s = %v, want absent function", name, err)
		}
	}
}

func TestRefuseENGINE(t *testing.T) {
	t.Parallel()
	op, intent := "engine-replay", "stable semantic intent"
	runNamedRefusal(t, namedRefusalCase{
		code: "ENGINE",
		step: func(space string) Step {
			step := namedStep(space, namedCreate("work", "new", "r:c", "1"))
			step.Op, step.Intent = &op, &intent
			step.Result = "different dynamic result"
			return step
		},
		arrange: func(t *testing.T, fx *tsetFixture, mem *Mem) {
			t.Helper()
			seed := namedStep(fx.Space)
			seed.Op, seed.Intent = &op, &intent
			modelReply, err := mem.Step(context.Background(), seed)
			if err != nil || modelReply.Status != "ok" || modelReply.Replay {
				t.Fatalf("seed Mem receipt: reply=%+v err=%v", modelReply, err)
			}
			raw, err := EncodeStep(seed)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := fx.Step(string(raw))
			if err != nil {
				t.Fatalf("seed Lua receipt FCALL: %v", err)
			}
			text, ok := wire.(string)
			if !ok {
				t.Fatalf("seed Lua receipt reply type %T", wire)
			}
			luaReply, err := DecodeReply([]byte(text))
			if err != nil || luaReply.Status != "ok" || luaReply.Replay {
				t.Fatalf("seed Lua receipt: reply=%+v err=%v", luaReply, err)
			}
			modelImage, err := mem.Snapshot(fx.Space)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := modelImage.Receipts["0"][op]; !ok {
				t.Fatal("Mem seed did not persist named receipt")
			}
			if exists, err := fx.Client.HExists(context.Background(), fixtureDoneKey(fx.Space, "0"), op).Result(); err != nil || !exists {
				t.Fatalf("Lua seed did not persist named receipt: exists=%v err=%v", exists, err)
			}
			if err := mem.SetEngine(fx.Space, "legacy"); err != nil {
				t.Fatal(err)
			}
			if err := fx.Client.HSet(context.Background(), fx.Space+"sprint:epoch", "engine", "legacy").Err(); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func TestOOMStartNoWrites(t *testing.T) {
	t.Parallel()
	profileOOMStart(t)
}

func TestMissingLibraryNoImplicitLoad(t *testing.T) {
	t.Parallel()
	profileMissingFunction(t)
}

func TestStandaloneIsolatedLoadOnly(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	ctx := context.Background()
	if err := fn.Load(ctx, fx.Client); err != nil {
		t.Fatal(err)
	}
	before, err := fx.Client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library, WithCode: true}).Result()
	if err != nil || len(before) != 1 {
		t.Fatalf("legacy library before tset load: %+v err=%v", before, err)
	}
	if err := fn.LoadTSet(ctx, fx.Client, fn.TSetStandalone); err == nil {
		t.Fatal("tset loader replaced legacy library")
	}
	after, err := fx.Client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library, WithCode: true}).Result()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("tset load changed legacy function library: before=%+v after=%+v err=%v", before, after, err)
	}
}

var profileAllowedFunctions = map[string][]string{
	"ns_tset_step": nil,
	"ns_tset_read": {"no-writes"},
}

// Inventory includes every old table/view callback, the old primitive, and
// lifecycle names that would expose an unsupported tset client surface.
var profileForbiddenFunctions = []string{
	"ns_oset_move", "ns_table_apply", "ns_table_apply_multi",
	"ns_table_bind", "ns_table_cell_add", "ns_table_cell_move", "ns_table_cell_remove",
	"ns_table_check", "ns_table_clear", "ns_table_create", "ns_table_drop", "ns_table_drop_definition",
	"ns_table_list", "ns_table_member_create", "ns_table_member_find", "ns_table_members",
	"ns_table_read", "ns_table_read_set", "ns_table_row_add", "ns_table_row_del", "ns_table_row_set",
	"ns_table_rows_add", "ns_table_rows_hide", "ns_table_set",
	"ns_view_del", "ns_view_get", "ns_view_list", "ns_view_set", "ns_view_state",
	"ns_sprint_step", "ns_tset_init", "ns_tset_teardown",
}

func profileFunctions(t *testing.T, c *redis.Client) map[string][]string {
	t.Helper()
	libraries, err := c.FunctionList(context.Background(), redis.FunctionListQuery{}).Result()
	if err != nil {
		t.Fatalf("FUNCTION LIST: %v", err)
	}
	if len(libraries) != 1 || libraries[0].Name != fn.Library {
		t.Fatalf("loaded libraries = %+v, want only %s", libraries, fn.Library)
	}
	functions := make(map[string][]string, len(libraries[0].Functions))
	for _, f := range libraries[0].Functions {
		if _, exists := functions[f.Name]; exists {
			t.Errorf("duplicate registered function %q", f.Name)
		}
		flags := append([]string(nil), f.Flags...)
		sort.Strings(flags)
		functions[f.Name] = flags
	}
	return functions
}

func profileLoadedSurface(t *testing.T, fx *tsetFixture) {
	t.Helper()
	fx.Activate(t)
	got := profileFunctions(t, fx.Client)
	if !reflect.DeepEqual(got, profileAllowedFunctions) {
		t.Errorf("%s FUNCTION LIST surface = %+v, want %+v", fx.profile, got, profileAllowedFunctions)
	}
	before := commitProbeImage(t, fx.Client)
	for _, name := range profileForbiddenFunctions {
		if _, present := got[name]; present {
			t.Errorf("forbidden callback %q registered in %s profile", name, fx.profile)
		}
		_, err := fx.Client.FCall(context.Background(), name, []string{}, "l1:probe").Result()
		if err == nil || !profileMissingFunctionError(err) {
			t.Errorf("FCALL %s in %s profile = %v, want absent function", name, fx.profile, err)
		}
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("calling forbidden callbacks changed the private store")
	}
}

func profileMissingFunctionError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "function not found") || strings.Contains(message, "unknown function") || strings.Contains(message, "nosuchfunction")
}

func profileCreateStep(space string) Step {
	return Step{Epoch: "0", Space: space, Entries: []Entry{{
		Kind: "create", Table: "work", To: "r:ready",
		IDs: []string{"card-1"}, Scores: []string{"1"},
	}}}
}

func profileDefineWork(t *testing.T, fx *tsetFixture) {
	t.Helper()
	fx.Define(t, "work", "ready")
	fx.AddRow(t, "work", "r", 0)
}

func profileMissingFunction(t *testing.T) {
	t.Helper()
	fx := newTSetFixture(t)
	profileDefineWork(t, fx)
	before := commitProbeImage(t, fx.Client)
	if libraries, err := fx.Client.FunctionList(context.Background(), redis.FunctionListQuery{}).Result(); err != nil || len(libraries) != 0 {
		t.Fatalf("fresh private server unexpectedly has functions: %+v err=%v", libraries, err)
	}
	_, err := newFixtureRedis(t, fx.Client).Step(context.Background(), profileCreateStep(fx.Space))
	var mapped *ClientError
	if !errors.As(err, &mapped) || mapped.Code != "FUNCTIONMISSING" {
		t.Fatalf("missing function Step error = %v, want FUNCTIONMISSING", err)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("missing function changed the store")
	}
	if libraries, err := fx.Client.FunctionList(context.Background(), redis.FunctionListQuery{}).Result(); err != nil || len(libraries) != 0 {
		t.Fatalf("Step implicitly loaded a function library: %+v err=%v", libraries, err)
	}
	commitProbeNoKeys(t, fx.Client, []string{fixtureRecordKey(fx.Space, "work", "card-1"), fixtureCellKey(fx.Space, "work", "0", "r", "ready")})
}

func profileCall(t *testing.T, c *redis.Client, name string, keys []string, args ...any) Refusal {
	t.Helper()
	value, err := c.FCall(context.Background(), name, keys, args...).Result()
	if err != nil {
		t.Fatalf("FCALL %s returned Redis error: %v", name, err)
	}
	encoded, ok := value.(string)
	if !ok {
		t.Fatalf("FCALL %s reply type %T, want JSON bulk string", name, value)
	}
	var reply Refusal
	if err := json.Unmarshal([]byte(encoded), &reply); err != nil {
		t.Fatalf("FCALL %s malformed JSON %q: %v", name, encoded, err)
	}
	return reply
}

func profileRefusalUnchanged(t *testing.T, fx *tsetFixture, code string, keys []string, args ...any) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	reply := profileCall(t, fx.Client, "ns_tset_step", keys, args...)
	if reply.Status != "refused" || reply.Code != code || !strings.HasSuffix(reply.Message, "; nothing was changed") {
		t.Errorf("want %s no-change refusal; got %+v", code, reply)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("refused FCALL changed the whole store")
	}
}

func profileOOMStart(t *testing.T) {
	t.Helper()
	fx := newTSetFixture(t)
	profileDefineWork(t, fx)
	fx.Activate(t)
	ctx := context.Background()
	max, err := fx.Client.ConfigGet(ctx, "maxmemory").Result()
	if err != nil {
		t.Fatalf("read private server maxmemory: %v", err)
	}
	old := max["maxmemory"]
	if old == "" {
		t.Fatal("private server did not report maxmemory; cannot restore it")
	}
	if err := fx.Client.ConfigSet(ctx, "maxmemory", "1").Err(); err != nil {
		t.Fatalf("set private server maxmemory: %v", err)
	}
	t.Cleanup(func() {
		if err := fx.Client.ConfigSet(context.Background(), "maxmemory", old).Err(); err != nil {
			t.Errorf("restore private server maxmemory: %v", err)
		}
	})
	before := commitProbeImage(t, fx.Client)
	_, err = newFixtureRedis(t, fx.Client).Step(ctx, profileCreateStep(fx.Space))
	var mapped *ClientError
	if !errors.As(err, &mapped) || mapped.Code != "OOMSTART" {
		t.Fatalf("private server OOM entry error = %v, want OOMSTART", err)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Error("preexecution OOM changed the whole store")
	}
	commitProbeNoKeys(t, fx.Client, []string{fixtureRecordKey(fx.Space, "work", "card-1"), fixtureCellKey(fx.Space, "work", "0", "r", "ready")})
}
