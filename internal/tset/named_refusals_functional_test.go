//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Each named refusal uses the same single-fault request against the in-memory
// model and the installed Redis Function. The raw Lua call intentionally
// bypasses Go client validation so an early Go refusal cannot impersonate a
// tested Lua refusal. Both full states are compared before and after.
type namedRefusalCase struct {
	code     string
	revision Decimal
	step     func(space string) Step
	arrange  func(t *testing.T, fx *tsetFixture, mem *Mem)
}

func runNamedRefusal(t *testing.T, tc namedRefusalCase) {
	t.Helper()
	fx, mem := namedRefusalFixture(t, tc.revision)
	if tc.arrange != nil {
		tc.arrange(t, fx, mem)
	}
	step := tc.step(fx.Space)
	beforeMem, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatalf("model snapshot before %s: %v", tc.code, err)
	}
	beforeRedis := commitProbeImage(t, fx.Client)
	_, memErr := mem.Step(context.Background(), step)
	memRef := requireRefusal(t, memErr, tc.code)
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal %s request: %v", tc.code, err)
	}
	wire, err := fx.Step(string(raw))
	if err != nil {
		t.Fatalf("Lua %s call returned a Redis error: %v", tc.code, err)
	}
	var encoded []byte
	switch value := wire.(type) {
	case string:
		encoded = []byte(value)
	case []byte:
		encoded = value
	default:
		t.Fatalf("Lua %s reply has type %T, want JSON bulk string", tc.code, wire)
	}
	var luaRef Refusal
	if err := json.Unmarshal(encoded, &luaRef); err != nil {
		t.Fatalf("Lua %s reply %q is not JSON: %v", tc.code, encoded, err)
	}
	requireRefusal(t, &luaRef, tc.code)
	if md, ld := comparableDetail(memRef.Detail), comparableDetail(luaRef.Detail); !reflect.DeepEqual(md, ld) {
		t.Errorf("%s structured detail differs: Mem=%+v Lua=%+v", tc.code, md, ld)
	}
	afterMem, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatalf("model snapshot after %s: %v", tc.code, err)
	}
	if !reflect.DeepEqual(beforeMem, afterMem) {
		t.Errorf("%s refusal changed Mem full state: before=%#v after=%#v", tc.code, beforeMem, afterMem)
	}
	if afterRedis := commitProbeImage(t, fx.Client); !reflect.DeepEqual(beforeRedis, afterRedis) {
		t.Errorf("%s refusal changed Redis whole-key image: before=%#v after=%#v", tc.code, beforeRedis, afterRedis)
	}
}

func comparableDetail(d RefusalDetail) RefusalDetail {
	if len(d.IDs) == 0 {
		d.IDs = nil
	}
	if len(d.Cells) == 0 {
		d.Cells = nil
	}
	if len(d.Rows) == 0 {
		d.Rows = nil
	}
	return d
}

func namedRefusalFixture(t *testing.T, revision Decimal) (*tsetFixture, *Mem) {
	t.Helper()
	if revision == "" {
		revision = "3"
	}
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.AddRow(t, "work", "r", 0)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c", "d"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatalf("model definition: %v", err)
	}
	if err := mem.SeedRow(fx.Space, "work", "0", "r", "0"); err != nil {
		t.Fatalf("model row: %v", err)
	}
	if err := mem.SeedMember(fx.Space, "work", "0", "existing", MemRecord{
		Epoch: "0", Revision: revision, Row: "r", Column: "c", Score: "1",
		Fields: map[string]string{"state": "live"},
	}); err != nil {
		t.Fatalf("model member: %v", err)
	}
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "existing"), map[string]any{
		"epoch": "0", "revision": string(revision), "place:work": "r:c", "state": "live",
	}).Err(); err != nil {
		t.Fatalf("Redis member fixture: %v", err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"),
		redis.Z{Score: 1, Member: "existing"}).Err(); err != nil {
		t.Fatalf("Redis cell fixture: %v", err)
	}
	fx.Activate(t)
	return fx, mem
}

func namedStep(space string, entries ...Entry) Step {
	return Step{Epoch: "0", Space: space, Entries: append([]Entry{}, entries...)}
}

func namedCreate(table, id, cell, score string) Entry {
	return Entry{Kind: "create", Table: table, To: cell, IDs: []string{id}, Scores: []string{score}}
}

func namedMove(id, cell string) Entry {
	return Entry{Kind: "move", Table: "work", From: cell, IDs: []string{id}}
}

func namedAdvance(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	op, intent := "named-refusal-advance", "named refusal fixture successor setup"
	step := namedStep(fx.Space, Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "work", Add: []string{"r"}})
	step.Op, step.Intent = &op, &intent
	model, modelErr := mem.Step(context.Background(), step)
	lua, luaErr := newFixtureRedis(t, fx.Client).Step(context.Background(), step)
	if modelErr != nil || luaErr != nil || model.EpochAfter != "1" || lua.EpochAfter != "1" {
		t.Fatalf("advance refusal fixture: Mem=%+v/%v Lua=%+v/%v", model, modelErr, lua, luaErr)
	}
}

func namedReceipt(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	op, intent := "part-7", "stable semantic arguments"
	step := namedStep(fx.Space)
	step.Op, step.Intent = &op, &intent
	model, modelErr := mem.Step(context.Background(), step)
	lua, luaErr := newFixtureRedis(t, fx.Client).Step(context.Background(), step)
	if modelErr != nil || luaErr != nil || model.Status != "ok" || lua.Status != "ok" {
		t.Fatalf("receipt refusal fixture: Mem=%+v/%v Lua=%+v/%v", model, modelErr, lua, luaErr)
	}
}

func namedDrift(t *testing.T, fx *tsetFixture, mem *Mem) {
	t.Helper()
	if err := fx.Client.ZRem(context.Background(), fixtureCellKey(fx.Space, "work", "0", "r", "c"), "existing").Err(); err != nil {
		t.Fatalf("corrupt Redis cell: %v", err)
	}
	mem.mu.Lock()
	delete(mem.spaces[fx.Space].epochs["0"].tables["work"].cells["r"]["c"], "existing")
	mem.mu.Unlock()
}

// ARGS and VERSION are transport-envelope refusals; the Mem.Step API has no
// KEYS or protocol-version parameter. Exercise the registered Lua entrypoint
// directly and compare the entire Redis key image around every call.
func TestRefuseARGS(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.Activate(t)
	raw, err := json.Marshal(namedStep(fx.Space))
	if err != nil {
		t.Fatal(err)
	}
	profileRefusalUnchanged(t, fx, "ARGS", nil, Version)
	profileRefusalUnchanged(t, fx, "ARGS", nil, Version, string(raw), "extra")
	profileRefusalUnchanged(t, fx, "ARGS", []string{fx.Space + "unexpected"}, Version, string(raw))
}

func TestRefuseVERSION(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c")
	fx.Activate(t)
	raw, err := json.Marshal(namedStep(fx.Space))
	if err != nil {
		t.Fatal(err)
	}
	profileRefusalUnchanged(t, fx, "VERSION", nil, "tset/0", string(raw))
}

func TestRefuseLIMIT(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "LIMIT", step: func(space string) Step {
		entries := make([]Entry, MaxEntries+1)
		for i := range entries {
			entries[i] = Entry{Kind: "rows", Table: "work", Add: []string{"r"}}
		}
		return namedStep(space, entries...)
	}})
}

func TestRefuseREQUEST(t *testing.T) {
	t.Parallel()
	t.Run("invalid score", func(t *testing.T) {
		t.Parallel()
		runNamedRefusal(t, namedRefusalCase{code: "REQUEST", step: func(space string) Step {
			return namedStep(space, namedCreate("work", "new", "r:c", "not-a-score"))
		}})
	})
	t.Run("caller notes require operation identity", func(t *testing.T) {
		t.Parallel()
		runNamedRefusal(t, namedRefusalCase{code: "REQUEST", step: func(space string) Step {
			step := namedStep(space)
			step.Notes = []Note{{
				Line:  NoteLine{Kind: "note", Meta: json.RawMessage(`{}`)},
				About: []string{"primary"},
			}}
			return step
		}})
	})
}

func TestRefuseFIELDNAME(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "FIELDNAME", step: func(space string) Step {
		entry := namedCreate("work", "new", "r:c", "1")
		entry.Set = map[string]string{"revision": "forbidden"}
		return namedStep(space, entry)
	}})
}

func TestRefuseFIELDOVERLAP(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "FIELDOVERLAP", step: func(space string) Step {
		entry := namedMove("existing", "r:c")
		entry.Set, entry.Unset = map[string]string{"state": "new"}, []string{"state"}
		return namedStep(space, entry)
	}})
}

func TestRefuseOPCONFLICT(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "OPCONFLICT", arrange: namedReceipt,
		step: func(space string) Step {
			op, otherIntent := "part-7", "different semantic arguments"
			step := namedStep(space)
			step.Op, step.Intent = &op, &otherIntent
			return step
		}})
}

func TestRefuseNOTABLE(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "NOTABLE", step: func(space string) Step {
		return namedStep(space, namedCreate("absent", "new", "r:c", "1"))
	}})
}

func TestRefuseSTALE(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "STALE", arrange: namedAdvance,
		step: func(space string) Step { return namedStep(space) }})
}

func TestRefuseEPOCHAHEAD(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "EPOCHAHEAD", step: func(space string) Step {
		step := namedStep(space)
		step.Epoch = "1"
		return step
	}})
}

func TestRefuseADVANCE(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "ADVANCE", step: func(space string) Step {
		step := namedStep(space, Entry{Kind: "advance", AdvanceFrom: "1"})
		op, intent := "refuse-advance-from", "request epoch zero with mismatched advance from one"
		step.Op, step.Intent = &op, &intent
		return step
	}})
}

func TestRefuseNOROW(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "NOROW", step: func(space string) Step {
		return namedStep(space, namedCreate("work", "new", "missing:c", "1"))
	}})
}

func TestRefuseROWCONFLICT(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "ROWCONFLICT", step: func(space string) Step {
		return namedStep(space, Entry{Kind: "rows", Table: "work", Add: []string{"r"}, Del: []string{"r"}})
	}})
}

func TestRefuseNOCOL(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "NOCOL", step: func(space string) Step {
		return namedStep(space, namedCreate("work", "new", "r:unknown", "1"))
	}})
}

func TestRefuseTWICE(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "TWICE", step: func(space string) Step {
		return namedStep(space, Entry{Kind: "create", Table: "work", To: "r:c",
			IDs: []string{"new", "new"}, Scores: []string{"1", "2"}})
	}})
}

func TestRefuseEXISTS(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "EXISTS", step: func(space string) Step {
		return namedStep(space, namedCreate("work", "existing", "r:c", "1"))
	}})
}

func TestRefuseMISSING(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "MISSING", step: func(space string) Step {
		return namedStep(space, namedMove("ghost", "r:c"))
	}})
}

func TestRefuseMEMBEREPOCH(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "MEMBEREPOCH", arrange: namedAdvance,
		step: func(space string) Step {
			step := namedStep(space, namedMove("existing", "r:c"))
			step.Epoch = "1"
			return step
		}})
}

func TestRefusePLACE(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "PLACE", step: func(space string) Step {
		return namedStep(space, namedMove("existing", "r:d"))
	}})
}

func TestRefuseREVISION(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "REVISION", step: func(space string) Step {
		entry := namedMove("existing", "r:c")
		entry.Revs = []Decimal{"2"}
		return namedStep(space, entry)
	}})
}

func TestRefuseDRIFT(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "DRIFT", arrange: namedDrift,
		step: func(space string) Step { return namedStep(space, namedMove("existing", "r:c")) }})
}

func TestRefuseCELLFULL(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "CELLFULL", step: func(space string) Step {
		return namedStep(space, Entry{Kind: "count", Table: "work", Cells: []string{"r:c"}, CountMax: []uint64{0}})
	}})
}

func TestRefuseRANGECOUNT(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "RANGECOUNT", step: func(space string) Step {
		two := uint64(2)
		return namedStep(space, Entry{Kind: "rcount", Table: "work", Cells: []string{"r:c"},
			ScoreMin: "0", ScoreMax: "2", AtLeast: &two})
	}})
}

func TestRefuseOCCUPIED(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "OCCUPIED", step: func(space string) Step {
		return namedStep(space, Entry{Kind: "rows", Table: "work", Del: []string{"r"}})
	}})
}

func TestRefuseOVERFLOW(t *testing.T) {
	t.Parallel()
	runNamedRefusal(t, namedRefusalCase{code: "OVERFLOW", revision: "18446744073709551615",
		step: func(space string) Step {
			entry := namedMove("existing", "r:c")
			entry.Scores = []string{"2"}
			return namedStep(space, entry)
		}})
}
