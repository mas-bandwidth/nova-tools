package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func TestWhereBackupCountsEveryActiveStream(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		counts []int64
		want   string
	}{
		{"none", []int64{2, 1, 1}, "none"},
		{"reads", []int64{1, 2, 0}, "reads"},
		{"merges", []int64{1, 1, 3}, "merges"},
		{"both", []int64{0, 2, 3}, "merges"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := ntable.Table{Columns: []ntable.Column{{Name: sprint.Working}, {Name: sprint.Review}, {Name: sprint.Merging}}}
			for i, n := range tc.counts {
				cells := make([]ntable.Cell, 3)
				cells[i].Count = n
				work.Rows = append(work.Rows, ntable.Row{Cells: cells, Hidden: true})
			}
			assert.Equal(t, tc.want, whereBackup(work))
		})
	}
}

func TestWhereJSONAlwaysCarriesBackup(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b")
	var view map[string]any
	ta.json("where", &view)
	assert.Equal(t, "none", view["backup"])
}
