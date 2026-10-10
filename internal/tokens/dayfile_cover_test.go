package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dayfileRow makes one row with the named types reported and the rest dashes.
func dayfileRow(model, repo string, reported map[Type]int64, sources ...string) DayRow {
	r := DayRow{Date: "2026-09-11", Model: model, Repo: repo, Basis: UTC, Sources: sources}
	for ty, v := range reported {
		r.Counts.Set(ty, v)
	}
	return r
}

// TestDayfileCoverTotalsSumsReportedTypesKeepsDashes pins Totals: per-type sums over the
// rows that reported each type, types no row reported stay dashes, and the fold does not
// mutate the rows it totals.
func TestDayfileCoverTotalsSumsReportedTypesKeepsDashes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rows []DayRow
		want map[Type]int64 // the types expected reported, and their sum
	}{
		{
			name: "rows with disjoint reports sum and keep dashes",
			rows: []DayRow{
				dayfileRow("m1", "r1", map[Type]int64{Input: 100, Output: 20}),
				dayfileRow("m2", "r2", map[Type]int64{Input: 50, Reasoning: 7}),
			},
			want: map[Type]int64{Input: 150, Output: 20, Reasoning: 7},
		},
		{
			name: "two rows reporting one type sum into one cell",
			rows: []DayRow{
				dayfileRow("m1", "r1", map[Type]int64{CacheWrite: 3}),
				dayfileRow("m2", "r2", map[Type]int64{CacheWrite: 4, CacheRead: 40}),
			},
			want: map[Type]int64{CacheWrite: 7, CacheRead: 40},
		},
		{
			name: "a zero a row reported is a measurement, not a dash",
			rows: []DayRow{
				dayfileRow("m1", "r1", map[Type]int64{Output: 0}),
				dayfileRow("m2", "r2", map[Type]int64{}),
			},
			want: map[Type]int64{Output: 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			own := make([]Counts, len(tc.rows))
			for i, r := range tc.rows {
				own[i] = r.Counts
			}
			d := DayFile{Day: "2026-09-11", Rows: tc.rows}
			got := d.Totals()
			for ty := Type(0); ty < NTypes; ty++ {
				v, ok := got.Get(ty)
				if want, reported := tc.want[ty]; reported {
					assert.Truef(t, ok, "%s is reported by a row", TypeNames[ty])
					assert.Equalf(t, want, v, "%s sums over the rows that reported it", TypeNames[ty])
					continue
				}
				assert.Falsef(t, ok, "%s is a dash: no row reported it", TypeNames[ty])
				assert.Equalf(t, Dash, got.Cell(ty), "%s prints the dash Totals refuses to invent", TypeNames[ty])
			}
			for i := range tc.rows {
				assert.Equalf(t, own[i], tc.rows[i].Counts, "row %d still holds only its own counts", i)
			}
		})
	}
}

// TestDayfileCoverTotalsOfADayWithNoRowsRefusesToInvent pins the refusal of Totals: a day
// with no rows totals to five dashes and zero, never zeros dressed as measurements.
func TestDayfileCoverTotalsOfADayWithNoRowsRefusesToInvent(t *testing.T) {
	t.Parallel()
	d := DayFile{Day: "2026-09-11"}
	got := d.Totals()
	require.Equal(t, int(NTypes), got.Dashes(), "a day with no rows reports none of the five types")
	assert.Zero(t, got.Total(), "nothing reported totals nothing, not a measured zero")
	for ty := Type(0); ty < NTypes; ty++ {
		assert.Equalf(t, Dash, got.Cell(ty), "the %s cell of an empty day is a dash", TypeNames[ty])
	}
}

// TestDayfileCoverSourcesOfUnionsAndSortsRowSources pins SourcesOf: the union of every
// row's sources, each label once, sorted, and a fresh slice no later sort can rewrite the
// rows.
func TestDayfileCoverSourcesOfUnionsAndSortsRowSources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rows []DayRow
		want []string
	}{
		{
			name: "labels from every row union and sort",
			rows: []DayRow{
				dayfileRow("m1", "r1", map[Type]int64{Input: 1}, "swarm:reader-b"),
				dayfileRow("m2", "r2", map[Type]int64{Input: 1}, "claude", "opencode"),
			},
			want: []string{"claude", "opencode", "swarm:reader-b"},
		},
		{
			name: "a label two rows share lands once",
			rows: []DayRow{
				dayfileRow("m1", "r1", map[Type]int64{Input: 1}, "swarm:reader-b", "claude"),
				dayfileRow("m2", "r2", map[Type]int64{Input: 1}, "swarm:reader-b"),
			},
			want: []string{"claude", "swarm:reader-b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SourcesOf(tc.rows)
			assert.Equal(t, tc.want, got, "the union of the rows' sources, each once, sorted")
			if len(got) > 1 {
				got[0], got[1] = got[1], got[0]
				assert.Equal(t, tc.want, SourcesOf(tc.rows), "the rows' own source lists are untouched")
			}
		})
	}
}

// TestDayfileCoverSourcesOfRowsWithoutSourcesRefusesToNameOne pins the refusal of
// SourcesOf: rows with no sources, and no rows at all, name no source rather than a
// placeholder.
func TestDayfileCoverSourcesOfRowsWithoutSourcesRefusesToNameOne(t *testing.T) {
	t.Parallel()
	assert.Nil(t, SourcesOf(nil), "no rows name no sources")
	assert.Nil(t, SourcesOf([]DayRow{}), "an empty row list names no sources")
	assert.Nil(t, SourcesOf([]DayRow{dayfileRow("m1", "r1", map[Type]int64{Input: 1})}),
		"a row with an empty sources cell names no sources")
}
