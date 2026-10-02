package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PlanWrite is the dry run of a write: the change the store would record,
// the refusal it would make, and nothing written.
func TestPlanWriteIsTheChangeAndRefusalOfTheWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	m1, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "8", "width": "4"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m1, "a1")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m1"}, "a1")
	require.NoError(t, err)
	m2, err := machine.NewRow("m2", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)

	cases := []struct {
		name    string
		op      string
		kind    string
		row     Row
		changes map[string]string
		want    error  // the refusal's sentinel, nil when the write would land
		before  string // the width before, for a set or remove
		after   string // the width after, for an add or set
	}{
		{name: "add a new row", op: OpAdd, kind: KindMachine, row: m2, after: "0"},
		{name: "add a name taken", op: OpAdd, kind: KindMachine, row: m1, want: ErrExists},
		{name: "set a field", op: OpSet, kind: KindMachine, row: Row{Name: "m1"}, changes: map[string]string{"width": "6"}, before: "4", after: "6"},
		{name: "set a row not there", op: OpSet, kind: KindMachine, row: Row{Name: "m9"}, changes: map[string]string{"width": "6"}, want: ErrNotFound},
		{name: "set a ref to no row", op: OpSet, kind: KindFleet, row: Row{Name: KindFleet}, changes: map[string]string{"store": "m9"}, want: ErrNoRef},
		{name: "remove a row another names", op: OpRemove, kind: KindMachine, row: Row{Name: "m1"}, want: ErrReferenced},
		{name: "remove a row not there", op: OpRemove, kind: KindMachine, row: Row{Name: "m9"}, want: ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := PlanWrite(ctx, st, tc.op, tc.kind, tc.row, tc.changes)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
				assert.True(t, Refused(err), "a plan's refusal is the store's refusal (exit 1)")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.op, c.Op)
			assert.Zero(t, c.ID, "a plan records nothing")
			assert.Equal(t, tc.before, c.Before["width"])
			assert.Equal(t, tc.after, c.After["width"])
		})
	}
	rev, err := st.Rev(ctx, KindMachine)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev, "no plan wrote a history row")
	_, found, err := st.Get(ctx, KindMachine, "m2")
	require.NoError(t, err)
	assert.False(t, found, "no plan wrote a row")
}
