//go:build functional

package tset

import (
	"context"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestAdvanceTouchedNamespaceMustBeEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		seed func(*testing.T, *tsetFixture)
	}{
		{
			name: "successor cell",
			seed: func(t *testing.T, fx *tsetFixture) {
				t.Helper()
				key := fixtureCellKey(fx.Space, "work", "1", "r0000", "c")
				if err := fx.Client.ZAdd(context.Background(), key, redis.Z{Score: 1, Member: "intruder"}).Err(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "successor definition",
			seed: func(t *testing.T, fx *tsetFixture) {
				t.Helper()
				if err := fx.Client.HSet(context.Background(), fixtureDefinitionKey(fx.Space, "work", "1"), "intruder", "1").Err(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "successor marker",
			seed: func(t *testing.T, fx *tsetFixture) {
				t.Helper()
				if err := fx.Client.HSet(context.Background(), fx.Space+"sprint:epoch@1", "intruder", "1").Err(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "successor rows",
			seed: func(t *testing.T, fx *tsetFixture) {
				t.Helper()
				if err := fx.Client.ZAdd(context.Background(), fixtureRowsKey(fx.Space, "work", "1"), redis.Z{Score: 1, Member: "intruder"}).Err(); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			fx.Define(t, "work", "c", "d")
			fx.AddRow(t, "work", "r0000", 0)
			// Direct setup is confined to the fixture initialization interval.
			// Only one successor key is occupied in each subtest.
			tc.seed(t, fx)
			fx.Activate(t)
			step := rowsetAdvance(fx.Space, rowsetRanks(1), []string{"r0000"})
			step.Entries = append(step.Entries, Entry{Kind: "create", Table: "work", To: "r0000:c", IDs: []string{"new"}, Scores: []string{"1"}})
			rowWitnessLuaRefuse(t, fx, step, "DRIFT")
		})
	}
	// Mem's public epoch fixture represents a populated successor row. It
	// does not expose independent successor definition, marker, or cell keys.
	t.Run("Mem successor rows", func(t *testing.T) {
		t.Parallel()
		const space = "row-namespace:"
		m := newRowsetMem(t, space, rowsetRanks(1))
		if err := m.SetActiveEpoch(space, "1"); err != nil {
			t.Fatal(err)
		}
		if err := m.SeedRow(space, "work", "1", "intruder", "1"); err != nil {
			t.Fatal(err)
		}
		if err := m.SetActiveEpoch(space, "0"); err != nil {
			t.Fatal(err)
		}
		before := rowsetMemSnapshot(t, m, space)
		step := rowsetAdvance(space, rowsetRanks(1), []string{"r0000"})
		step.Entries = append(step.Entries, Entry{Kind: "create", Table: "work", To: "r0000:c", IDs: []string{"new"}, Scores: []string{"1"}})
		_, err := m.Step(context.Background(), step)
		requireRefusal(t, err, "DRIFT")
		if after := rowsetMemSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
			t.Fatalf("DRIFT changed complete Mem state: %s", compareJSON("state", before, after))
		}
	})
}

func TestRowRankOverflow(t *testing.T) {
	t.Parallel()
	const maxRank = Decimal("9007199254740991")
	h := newRowsetFunctionalHarness(t, []RowRank{{Row: "last", Rank: maxRank}})
	step := Step{Epoch: "0", Space: h.fx.Space, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"next"}}}}
	rowWitnessTwinRefuse(t, h, step, "OVERFLOW")
}

func TestCountOnNewlyAddedRowRefusesNOROW(t *testing.T) {
	t.Parallel()
	h := newRowsetFunctionalHarness(t, nil)
	step := Step{Epoch: "0", Space: h.fx.Space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"fresh"}},
		{Kind: "count", Table: "work", Cells: []string{"fresh:c"}, CountMax: []uint64{0}},
	}}
	rowWitnessTwinRefuse(t, h, step, "NOROW")
}
