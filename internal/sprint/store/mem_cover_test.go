package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemCoverRowsHideHidesTheRowsItHas pins Mem.RowsHide's main path: each
// named row the table holds is marked hidden and a row the table lacks is
// skipped, all in the one store exchange, as the table layer's row hide does
// under RowsAdd's epoch check.
func TestMemCoverRowsHideHidesTheRowsItHas(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hide []string
		want map[string]bool
	}{
		{"hides the rows the table has", []string{"reader-a", "reader-c"},
			map[string]bool{"reader-a": true, "reader-b": false, "reader-c": true}},
		{"skips a row the table lacks", []string{"reader-a", "reader-nobody"},
			map[string]bool{"reader-a": true, "reader-b": false, "reader-c": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			require.NoError(t, h.m.RowsHide(h.ctx, "t-readers", c.hide))
			shapes, err := h.m.Shapes(h.ctx, []string{"t-readers"})
			require.NoError(t, err)
			require.Len(t, shapes, 1)
			got := map[string]bool{}
			for _, row := range shapes[0].Rows {
				got[row.Key] = row.Hidden
			}
			assert.Equal(t, c.want, got, "the hidden flag of every row")
			assert.Equal(t, 1, h.m.Calls["rowshide"], "one store exchange")
		})
	}
}

// TestMemCoverRowsHideRefusesATableItDoesNotHave pins Mem.RowsHide's refusal:
// a table the store does not hold is refused NOTABLE and nothing is hidden.
func TestMemCoverRowsHideRefusesATableItDoesNotHave(t *testing.T) {
	t.Parallel()
	m := NewMem()
	err := m.RowsHide(context.Background(), "t-missing", []string{"r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such table t-missing")
	assert.Equal(t, 1, m.Calls["rowshide"], "the call is counted before the refusal")
}
