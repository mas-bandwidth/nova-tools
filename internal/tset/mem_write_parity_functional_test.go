//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

func writeParityFixture(t *testing.T, destinationMember string) (*tsetFixture, *Mem) {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.AddRow(t, "work", "r", 0)
	fx.AddRow(t, "work", "s", 1)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{Columns: []string{"c", "d"}, MemberPrefix: fx.Space + "member:work:", EpochKey: fx.Space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, rank string }{{"r", "0"}, {"s", "1"}} {
		if err := mem.SeedRow(fx.Space, "work", "0", row.name, Decimal(row.rank)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mem.SeedMember(fx.Space, "work", "0", "p", MemRecord{Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1", Fields: map[string]string{"state": "live"}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "p"), map[string]any{"epoch": "0", "revision": "1", "place:work": "r:c", "state": "live"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "r", "c"), redis.Z{Score: 1, Member: "p"}).Err(); err != nil {
		t.Fatal(err)
	}
	if destinationMember != "" {
		if err := mem.SeedMember(fx.Space, "work", "0", "q", MemRecord{Epoch: "0", Revision: "1", Row: "s", Column: "d", Score: "3"}); err != nil {
			t.Fatal(err)
		}
		if err := fx.Client.HSet(ctx, fixtureRecordKey(fx.Space, "work", "q"), map[string]any{"epoch": "0", "revision": "1", "place:work": "s:d"}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := fx.Client.ZAdd(ctx, fixtureCellKey(fx.Space, "work", "0", "s", "d"), redis.Z{Score: 3, Member: "q"}, redis.Z{Score: 2, Member: destinationMember}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := mem.CorruptCell(fx.Space, "work", "0", "s", "d", destinationMember, "2"); err != nil {
			t.Fatal(err)
		}
	}
	fx.Activate(t)
	return fx, mem
}

func assertWriteParityRefusal(t *testing.T, fx *tsetFixture, mem *Mem, step Step, code string, entry int, rows, ids []string) {
	t.Helper()
	beforeMem, err := mem.Snapshot(fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	beforeRedis := commitProbeImage(t, fx.Client)
	_, memErr := mem.Step(context.Background(), step)
	memRef := requireRefusal(t, memErr, code)
	encoded, err := EncodeStep(step)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := fx.Step(string(encoded))
	if err != nil {
		t.Fatalf("Lua transport: %v", err)
	}
	var raw []byte
	switch v := wire.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		t.Fatalf("Lua response type %T", wire)
	}
	var luaRef Refusal
	if err := json.Unmarshal(raw, &luaRef); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, &luaRef, code)
	for name, detail := range map[string]RefusalDetail{"Mem": memRef.Detail, "Lua": luaRef.Detail} {
		if detail.EntryIndex == nil || *detail.EntryIndex != entry || detail.Table != "work" ||
			!reflect.DeepEqual(comparableDetail(detail).Rows, rows) || !reflect.DeepEqual(comparableDetail(detail).IDs, ids) || len(detail.Cells) != 0 {
			t.Errorf("%s %s detail = %+v", code, name, detail)
		}
	}
	if !reflect.DeepEqual(comparableDetail(memRef.Detail), comparableDetail(luaRef.Detail)) {
		t.Errorf("%s detail mismatch: Mem=%+v Lua=%+v", code, memRef.Detail, luaRef.Detail)
	}
	afterMem, err := mem.Snapshot(fx.Space)
	if err != nil || !reflect.DeepEqual(beforeMem, afterMem) {
		t.Errorf("%s changed Mem whole state: err=%v", code, err)
	}
	if afterRedis := commitProbeImage(t, fx.Client); !reflect.DeepEqual(beforeRedis, afterRedis) {
		t.Errorf("%s changed Redis whole-key image", code)
	}
}

func TestWriteParityDeletedRowStays(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, to, score, state, code string
		index                        int
	}{
		{"changed implicit", "", "2", "", "ROWCONFLICT", 1},
		{"changed explicit", "r:c", "", "ready", "ROWCONFLICT", 1},
		{"unchanged implicit", "", "", "", "OCCUPIED", 0},
		{"unchanged explicit", "r:c", "", "", "OCCUPIED", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, mem := writeParityFixture(t, "")
			move := Entry{Kind: "move", Table: "work", From: "r:c", To: tc.to, IDs: []string{"p"}}
			if tc.score != "" {
				move.Scores = []string{tc.score}
			}
			if tc.state != "" {
				move.Set = map[string]string{"state": tc.state}
			}
			step := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "rows", Table: "work", Del: []string{"r"}}, move}}
			assertWriteParityRefusal(t, fx, mem, step, tc.code, tc.index, []string{"r"}, nil)
		})
	}
}

func TestWriteParityDestinationDuplicateIsDrift(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, kind, id string }{{"create", "create", "new"}, {"move", "move", "p"}} {
		t.Run(tc.name, func(t *testing.T) {
			fx, mem := writeParityFixture(t, tc.id)
			entry := Entry{Kind: tc.kind, Table: "work", To: "s:d", IDs: []string{tc.id}, Scores: []string{"2"}}
			if tc.kind == "move" {
				entry.From = "r:c"
			}
			assertWriteParityRefusal(t, fx, mem, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{entry}}, "DRIFT", 0, nil, []string{tc.id})
		})
	}
}
