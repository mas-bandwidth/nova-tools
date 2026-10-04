package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestReadsCoverCardIDs pins CardIDs: every card placed on a table at the
// sprint's epoch, by id, from one read of the shape and one of the cells' ids,
// with no record read; a table with nothing placed answers empty, and a table
// that does not exist is refused.
func TestReadsCoverCardIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	cases := []struct {
		name    string
		logical string
		want    []string
		code    string
	}{
		{name: "every placed card", logical: sprint.Work, want: []string{"s1-1", "s1-2"}},
		{name: "a table with nothing placed", logical: sprint.Readers},
		{name: "no such table", logical: "nope", code: "NOTABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := h.st.CardIDs(h.ctx, c.logical)
			if c.code != "" {
				require.Error(t, err, "%s: %v", c.name, got)
				assert.Equal(t, c.code, refusalCode(err), "%s: %v", c.name, err)
				assert.Nil(t, got, "%s", c.name)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, len(c.want), "%s: %v", c.name, got)
			for _, id := range c.want {
				assert.True(t, got[id], "%s: %v misses %s", c.name, got, id)
			}
		})
	}
}

// TestReadsCoverRecords pins Records: the named records of a table by identity,
// placed or kept, an id with no record left out, and a table that does not
// exist refused.
func TestReadsCoverRecords(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	cases := []struct {
		name    string
		logical string
		ids     []string
		want    []string
		code    string
	}{
		{name: "every named card", logical: sprint.Work, ids: []string{"s1-1", "s1-2"}, want: []string{"s1-1", "s1-2"}},
		{name: "an id with no record is left out", logical: sprint.Work, ids: []string{"s1-1", "nobody"}, want: []string{"s1-1"}},
		{name: "no such table", logical: "nope", ids: []string{"s1-1"}, code: "NOTABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := h.st.Records(h.ctx, c.logical, c.ids)
			if c.code != "" {
				require.Error(t, err, "%s: %v", c.name, got)
				assert.Equal(t, c.code, refusalCode(err), "%s: %v", c.name, err)
				return
			}
			require.NoError(t, err)
			var ids []string
			for _, card := range got {
				ids = append(ids, card.ID)
			}
			assert.ElementsMatch(t, c.want, ids, "%s: %v", c.name, ids)
		})
	}
}
