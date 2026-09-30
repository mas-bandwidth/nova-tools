//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// rowWitnessLuaRefuse bypasses Go's request validator so the installed Lua
// function must make the refusal itself. The whole-key image catches writes
// outside the semantic table snapshot, including receipt and metadata keys.
func rowWitnessLuaRefuse(t *testing.T, fx *tsetFixture, step Step, code string) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal witness request: %v", err)
	}
	wire, err := fx.Step(string(raw))
	if err != nil {
		t.Fatalf("Lua witness FCALL: %v", err)
	}
	encoded, ok := wire.(string)
	if !ok {
		t.Fatalf("Lua witness reply type %T, want JSON string", wire)
	}
	var refusal Refusal
	if err := json.Unmarshal([]byte(encoded), &refusal); err != nil {
		t.Fatalf("decode Lua refusal %q: %v", encoded, err)
	}
	requireRefusal(t, &refusal, code)
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed the complete Redis key image", code)
	}
}

func rowWitnessTwinRefuse(t *testing.T, h *namedStateHarness, step Step, code string) {
	t.Helper()
	before := h.snapshot(t)
	_, err := h.mem.Step(context.Background(), step)
	requireRefusal(t, err, code)
	rowWitnessLuaRefuse(t, h.fx, step, code)
	if after := h.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed the complete Mem/Lua semantic state: %s", code, compareJSON("state", before, after))
	}
}

func TestRowsetStaticSingleFaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		code string
		edit func(Step) Step
	}{
		{
			name: "duplicate row name despite matching cardinality", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries[0].Rows = []RowRank{{Row: "r0000", Rank: "0"}, {Row: "r0000", Rank: "0"}}
				return s
			},
		},
		{
			name: "second rowset for one table", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries = append([]Entry{s.Entries[0], s.Entries[0]}, s.Entries[1:]...)
				return s
			},
		},
		{
			name: "rank above exact integer maximum", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries[0].Rows[0].Rank = "9007199254740992"
				return s
			},
		},
		{
			name: "rowset without advance", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries = s.Entries[:1]
				return s
			},
		},
		{
			name: "late rowset after advance", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries[0], s.Entries[1] = s.Entries[1], s.Entries[0]
				return s
			},
		},
		{
			name: "late rowset after rows entry", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries[0], s.Entries[1], s.Entries[2] = s.Entries[2], s.Entries[0], s.Entries[1]
				return s
			},
		},
		{
			name: "advance not first after rowset prefix", code: "REQUEST",
			edit: func(s Step) Step {
				s.Entries = []Entry{s.Entries[0], {Kind: "rows", Table: "work", Add: []string{"fresh"}}, s.Entries[1]}
				return s
			},
		},
		{
			name: "reserved before_fields", code: "FIELDNAME",
			edit: func(s Step) Step {
				s.Entries = []Entry{{Kind: "create", Table: "work", To: "r0000:c", IDs: []string{"new"}, Scores: []string{"1"}, BeforeFields: []string{"place:work"}}}
				return s
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Two stored rows make the duplicate-name witness material: a
			// cardinality-only implementation would otherwise accept it.
			h := newRowsetFunctionalHarness(t, rowsetRanks(2))
			step := tc.edit(rowsetAdvance(h.fx.Space, rowsetRanks(2), []string{"r0000"}))
			rowWitnessTwinRefuse(t, h, step, tc.code)
		})
	}
}
