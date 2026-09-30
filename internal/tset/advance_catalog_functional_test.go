//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// catalogWitnessHarness installs every definition before activating the writer.
// All subsequent rows and records enter through the public Step path.
func catalogWitnessHarness(t *testing.T) *namedStateHarness {
	t.Helper()
	fx := newTSetFixture(t)
	mem := NewMem()
	for _, table := range []struct {
		name string
		cols []string
	}{
		{"work", []string{"ready", "done"}},
		{"fleet", []string{"waiting"}},
		{"untouched", []string{"open"}},
		{"empty", []string{"slot"}},
	} {
		fx.Define(t, table.name, table.cols...)
		if err := mem.DefineTable(fx.Space, table.name, TableDefinition{
			Columns: table.cols, MemberPrefix: fx.Space + "member:" + table.name + ":",
			EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
		}); err != nil {
			t.Fatalf("define Mem table %q: %v", table.name, err)
		}
	}
	fx.Activate(t)
	return &namedStateHarness{fx: fx, mem: mem}
}

func catalogWitnessEpoch(t *testing.T, fx *tsetFixture, epoch string) {
	t.Helper()
	ctx := context.Background()
	marker, err := fx.Client.HGetAll(ctx, fx.Space+"sprint:epoch@"+epoch).Result()
	if err != nil {
		t.Fatal(err)
	}
	if marker["engine"] != Version || marker["n"] != epoch {
		t.Fatalf("epoch %s marker absent or invalid: %v", epoch, marker)
	}
	var catalog []string
	if err := json.Unmarshal([]byte(marker["tables"]), &catalog); err != nil {
		t.Fatalf("epoch %s catalog %q: %v", epoch, marker["tables"], err)
	}
	if !reflect.DeepEqual(catalog, fx.tables) {
		t.Fatalf("epoch %s catalog=%v, want %v", epoch, catalog, fx.tables)
	}
	for _, table := range fx.tables {
		live, err := fx.Client.HGetAll(ctx, fx.Space+"table:"+table).Result()
		if err != nil {
			t.Fatal(err)
		}
		saved, err := fx.Client.HGetAll(ctx, fixtureDefinitionKey(fx.Space, table, epoch)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(saved) == 0 || !reflect.DeepEqual(saved, live) {
			t.Fatalf("epoch %s definition %q differs from live: saved=%v live=%v", epoch, table, saved, live)
		}
	}
}

func catalogWitnessRead(t *testing.T, h *namedStateHarness, epoch Decimal, tables ...string) ReadReply {
	t.Helper()
	queries := make([]ReadQuery, 0, len(tables))
	for _, table := range tables {
		queries = append(queries, ReadQuery{Kind: "rows", Table: table})
	}
	plan := ReadPlan{Space: h.fx.Space, Epoch: epoch, Mode: "atomic", Queries: queries}
	model, modelErr := h.mem.Read(context.Background(), plan)
	lua, luaErr := newFixtureRedis(t, h.fx.Client).Read(context.Background(), plan)
	if modelErr != nil || luaErr != nil {
		t.Fatalf("epoch %s rows read: Mem=%v Lua=%v", epoch, modelErr, luaErr)
	}
	differentialReadAnswers(t, model, lua)
	return lua
}

func TestAdvanceCatalogSnapshotsAllDefinitions(t *testing.T) {
	t.Parallel()
	h := catalogWitnessHarness(t)
	space := h.fx.Space
	catalogWitnessEpoch(t, h.fx, "0")
	h.apply(t, stateStep(space, "0",
		Entry{Kind: "rows", Table: "work", Add: []string{"old"}},
		Entry{Kind: "rows", Table: "fleet", Add: []string{"garage"}},
		Entry{Kind: "create", Table: "work", To: "old:ready", IDs: []string{"historical"}, Scores: []string{"1"}}))
	got := h.apply(t, stateNamed(space, "0", "catalog-advance-one", "catalog advance with work restore", "advanced",
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "work", Add: []string{"new"}}))
	if got.EpochAfter != "1" {
		t.Fatalf("advance epoch after=%q, want 1", got.EpochAfter)
	}
	// This checks the catalog contract directly, including tables which were
	// neither named by the advance step nor occupied in either epoch.
	catalogWitnessEpoch(t, h.fx, "0")
	catalogWitnessEpoch(t, h.fx, "1")
	snapshot := h.snapshot(t)
	if snapshot.ActiveEpoch != "1" {
		t.Fatalf("active epoch=%q, want 1", snapshot.ActiveEpoch)
	}
	for _, table := range h.fx.tables {
		for _, epoch := range []Decimal{"0", "1"} {
			if _, ok := snapshot.Epochs[epoch].Tables[table]; !ok {
				t.Fatalf("epoch %s lost catalog table %q", epoch, table)
			}
		}
	}
	if len(snapshot.Epochs["1"].Tables["untouched"].Rows) != 0 ||
		len(snapshot.Epochs["1"].Tables["empty"].Rows) != 0 ||
		len(snapshot.Epochs["1"].Tables["fleet"].Rows) != 0 {
		t.Fatalf("successor copied untouched rows: %+v", snapshot.Epochs["1"])
	}
	old := catalogWitnessRead(t, h, "0", h.fx.tables...)
	new := catalogWitnessRead(t, h, "1", h.fx.tables...)
	if !reflect.DeepEqual(old.Answers[0].Rows, []RowRank{{Row: "old", Rank: "0"}}) ||
		!reflect.DeepEqual(old.Answers[1].Rows, []RowRank{{Row: "garage", Rank: "0"}}) ||
		!reflect.DeepEqual(new.Answers[0].Rows, []RowRank{{Row: "new", Rank: "0"}}) {
		t.Fatalf("historical/successor rows differ: old=%+v new=%+v", old.Answers, new.Answers)
	}
	for _, answer := range append(old.Answers[2:], new.Answers[1:]...) {
		if len(answer.Rows) != 0 {
			t.Fatalf("untouched or successor table has rows: %+v", answer)
		}
	}
}

func TestEmptyEpochMarkerAndHistoricalRead(t *testing.T) {
	t.Parallel()
	h := catalogWitnessHarness(t)
	if reply := h.apply(t, stateNamed(h.fx.Space, "0", "empty-advance-one", "empty epoch advance", "advanced",
		Entry{Kind: "advance", AdvanceFrom: "0"})); reply.EpochAfter != "1" || reply.Changed != 0 {
		t.Fatalf("empty advance reply=%+v", reply)
	}
	for _, epoch := range []Decimal{"0", "1"} {
		catalogWitnessEpoch(t, h.fx, string(epoch))
		read := catalogWitnessRead(t, h, epoch, h.fx.tables...)
		if len(read.Answers) != len(h.fx.tables) {
			t.Fatalf("epoch %s answers=%d, want %d", epoch, len(read.Answers), len(h.fx.tables))
		}
		for _, answer := range read.Answers {
			if len(answer.Rows) != 0 {
				t.Fatalf("epoch %s empty table returned rows: %+v", epoch, answer)
			}
		}
	}
	h.snapshot(t)
}

func TestCellReferenceWireRoundTrip(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	const row = "zone:west"
	h.apply(t, stateStep(space, "0",
		Entry{Kind: "rows", Table: "work", Add: []string{row}},
		Entry{Kind: "create", Table: "work", To: row + ":c", IDs: []string{"card"}, Scores: []string{"1"}}))
	stored, err := h.fx.Client.HGet(context.Background(), fixtureRecordKey(space, "work", "card"), "place:work").Result()
	if err != nil || stored != row+":c" {
		t.Fatalf("stored placement=%q err=%v", stored, err)
	}
	plan := ReadPlan{Space: space, Epoch: "0", Mode: "atomic", Queries: []ReadQuery{
		{Kind: "ids", Table: "work", IDs: []string{"card"}, Fields: []string{}},
		{Kind: "range", Table: "work", Cell: row + ":c", Min: "-inf", Max: "+inf", Limit: 10, Records: true, Fields: []string{}},
	}}
	model, modelErr := h.mem.Read(context.Background(), plan)
	lua, luaErr := newFixtureRedis(t, h.fx.Client).Read(context.Background(), plan)
	if modelErr != nil || luaErr != nil {
		t.Fatalf("colon-row read: Mem=%v Lua=%v", modelErr, luaErr)
	}
	differentialReadAnswers(t, model, lua)
	for i, answer := range lua.Answers {
		if len(answer.Records) != 1 || answer.Records[0].Place == nil ||
			answer.Records[0].Place.Row != row || answer.Records[0].Place.Col != "c" {
			t.Fatalf("answer %d split cell at wrong colon: %+v", i, answer)
		}
	}
	if rec := h.snapshot(t).Epochs["0"].Tables["work"].Records["card"]; rec.Row != row || rec.Column != "c" {
		t.Fatalf("snapshot split cell at wrong colon: %+v", rec)
	}
}

func TestAdvanceCatalogDefinitionCorruptionRefusesUnchanged(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "corrupt"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "ready")
			fx.Define(t, "untouched", "slot")
			ctx := context.Background()
			key := fx.Space + "table:untouched"
			var err error
			switch fault {
			case "missing":
				err = fx.Client.Del(ctx, key).Err()
			case "corrupt":
				// A wrong engine is ENGINE by contract. Corrupt the column
				// definition instead so this case isolates CONFIG.
				err = fx.Client.HSet(ctx, key, "col:slot", "unsupported-kind").Err()
			}
			if err != nil {
				t.Fatalf("create %s catalog fault: %v", fault, err)
			}
			fx.Activate(t)
			before := commitProbeImage(t, fx.Client)
			_, err = newFixtureRedis(t, fx.Client).Step(ctx, stateNamed(fx.Space, "0",
				"catalog-fault-advance-"+fault, "advance with invalid catalog definition", "unapplied",
				Entry{Kind: "advance", AdvanceFrom: "0"}))
			requireRefusal(t, err, "CONFIG")
			if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s catalog CONFIG refusal changed Redis image", fault)
			}
			if n, err := fx.Client.Exists(ctx, fx.Space+"sprint:epoch@1",
				fixtureDefinitionKey(fx.Space, "work", "1"),
				fixtureDefinitionKey(fx.Space, "untouched", "1")).Result(); err != nil || n != 0 {
				t.Fatalf("%s catalog fault left successor keys: n=%d err=%v", fault, n, err)
			}
		})
	}
}

func TestAdvanceCreateRetainedGlobalIDRefusesUnchanged(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	h.apply(t, stateStep(space, "0",
		Entry{Kind: "rows", Table: "work", Add: []string{"old"}},
		Entry{Kind: "create", Table: "work", To: "old:c", IDs: []string{"same-id"}, Scores: []string{"1"}}))
	h.refuse(t, stateNamed(space, "0", "retained-id-advance", "advance and recreate retained id", "unapplied",
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "work", Add: []string{"new"}},
		Entry{Kind: "create", Table: "work", To: "new:d", IDs: []string{"same-id"}, Scores: []string{"2"}}), "EXISTS")
	if got := h.snapshot(t); got.ActiveEpoch != "0" || got.Epochs["0"].Tables["work"].Records["same-id"].Row != "old" {
		t.Fatalf("EXISTS refusal changed retained ID ownership: %+v", got)
	}
}
