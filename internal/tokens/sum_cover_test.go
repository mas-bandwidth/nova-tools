package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveDay writes one day file into out through the package's own writer, so the
// month tests read exactly the bytes a fold would have written.
func saveDay(t *testing.T, out, day, turns string, rows ...DayRow) {
	t.Helper()
	d := DayFile{Day: day, At: day + "T00:00:00Z", Build: "cover-test", Turns: turns,
		Sources: []string{"cover-test"}, Rows: rows}
	require.NoError(t, d.Save(out))
}

func coverRow(date, model, repo string, basis string, counts func(c *Counts), rough int) DayRow {
	r := DayRow{Date: date, Model: model, Repo: repo, Basis: basis, Rough: rough,
		Sources: []string{"cover-test"}}
	if counts != nil {
		counts(&r.Counts)
	}
	return r
}

// TestSumCoverNewAggStartsEmpty pins newAgg: a fresh grouping holds no days, no keys,
// no rows, and every cell is the dash a zero-row month must print.
func TestSumCoverNewAggStartsEmpty(t *testing.T) {
	t.Parallel()
	a := newAgg()
	require.NotNil(t, a, "newAgg hands back a grouping")
	require.NotNil(t, a.days, "the day set is ready for adds")
	require.NotNil(t, a.keys, "the key set is ready for adds")
	assert.Equal(t, 0, a.Days(), "a fresh grouping has seen no days")
	assert.Equal(t, 0, a.Keys(), "a fresh grouping has seen no keys")
	assert.Equal(t, 0, a.Rows, "a fresh grouping has no rows")
	for tt := Type(0); tt < NTypes; tt++ {
		assert.Equalf(t, Dash, a.Cell(tt), "%s cell of a fresh grouping is a dash", TypeNames[tt])
	}
}

// TestSumCoverAggAddSumsPerTypeAndCountsRows pins add: reported types sum, types no row
// reported land as dashes (never zeros), rough and rows count up, and a row whose basis
// is not UTC is named.
func TestSumCoverAggAddSumsPerTypeAndCountsRows(t *testing.T) {
	t.Parallel()
	a := newAgg()
	a.add(coverRow("2026-03-01", "m1", "r1", UTC, func(c *Counts) {
		c.Set(Input, 100)
		c.Set(Output, 20)
	}, 5), "m1\tr1")
	a.add(coverRow("2026-03-02", "m2", "r2", "America/New_York", func(c *Counts) {
		c.Set(Input, 30)
	}, 2), "m2\tr2")

	assert.Equal(t, int64(130), a.Totals[Input], "input sums over both rows")
	assert.Equal(t, int64(20), a.Totals[Output], "output sums over the one row that reported it")
	assert.Equal(t, 2, a.Rows, "both rows count")
	assert.Equal(t, 7, a.Rough, "rough sums over both rows")
	assert.Equal(t, 1, a.NonUTC, "the row dated from a zone that is not UTC is named")
	assert.Equal(t, 0, a.Dashes[Input], "input was reported by both rows")
	assert.Equal(t, 1, a.Dashes[Output], "only the second row left output unreported")
	assert.Equal(t, 2, a.Dashes[Reasoning], "neither row reported reasoning")

	empty := newAgg()
	empty.add(coverRow("2026-03-01", "m1", "r1", UTC, nil, 0), "m1\tr1")
	assert.Equal(t, int(NTypes), len(empty.Dashes), "a row with no measurement dashes every type")
	assert.Equal(t, Dash, empty.Cell(Input), "a type no row reported prints the dash, never a zero")
}

// TestSumCoverAggDaysAndKeysCountDistinct pins Days and Keys: a date or a key fed
// twice is one day or one key.
func TestSumCoverAggDaysAndKeysCountDistinct(t *testing.T) {
	t.Parallel()
	a := newAgg()
	a.add(coverRow("2026-03-01", "m1", "r1", UTC, nil, 0), "m1\tr1")
	a.add(coverRow("2026-03-01", "m1", "r1", UTC, nil, 0), "m1\tr1")
	a.add(coverRow("2026-03-02", "m1", "r1", UTC, nil, 0), "m1\tr1")
	a.add(coverRow("2026-03-02", "m1", "r1", UTC, nil, 0), "m1\tr1x")
	assert.Equal(t, 2, a.Days(), "two calendar days, each fed twice")
	assert.Equal(t, 2, a.Keys(), "two grouping keys, one fed under two keys")
	assert.Equal(t, 4, a.Rows, "every add is a row, distinct or not")
}

// TestSumCoverCellPrintsNumberAndRefusesAbsentType pins Cell: the sum when any row
// reported the type, the dash when every row left it absent, and the dash for a
// grouping with no rows at all.
func TestSumCoverCellPrintsNumberAndRefusesAbsentType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		add  func(a *Agg)
		t    Type
		want string
	}{
		{"number over rows that reported it", func(a *Agg) {
			a.add(coverRow("2026-03-01", "m1", "r1", UTC, func(c *Counts) { c.Set(Input, 30) }, 0), "k")
			a.add(coverRow("2026-03-02", "m1", "r1", UTC, nil, 0), "k")
		}, Input, "30"},
		{"every row a dash", func(a *Agg) {
			a.add(coverRow("2026-03-01", "m1", "r1", UTC, nil, 0), "k")
		}, Output, Dash},
		{"no rows at all", func(a *Agg) {}, Input, Dash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newAgg()
			c.add(a)
			assert.Equal(t, c.want, a.Cell(c.t))
		})
	}
}

// TestSumCoverSumMonthWalksDayFilesInOrder pins SumMonth's main path: the two day
// files' rows fold into total, pair and model groupings, the gap between the days is
// named, turns sum, and the listings sort biggest spend first.
func TestSumCoverSumMonthWalksDayFilesInOrder(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	saveDay(t, out, "2026-03-01", "12",
		coverRow("2026-03-01", "m1", "r1", UTC, func(c *Counts) {
			c.Set(Input, 100)
			c.Set(Output, 20)
		}, 5),
		coverRow("2026-03-01", "m2", "r2", "America/New_York", func(c *Counts) {
			c.Set(Input, 30)
		}, 2),
	)
	saveDay(t, out, "2026-03-03", Dash,
		coverRow("2026-03-03", "m1", "r1", UTC, func(c *Counts) {
			c.Set(Input, 40)
			c.Set(Output, 10)
		}, 3),
	)
	// Stray entries the walk must step over: a directory named like a day file, a
	// file without the suffix, a file of another month, and a day that is not a
	// calendar day. None of them is a day file, and none may enter the sum.
	require.NoError(t, os.MkdirAll(filepath.Join(out, "2026-03-05.tsv"), 0o755))
	for _, stray := range []string{"notes.txt", "2026-04-01.tsv", "2026-03-00.tsv"} {
		require.NoError(t, os.WriteFile(filepath.Join(out, stray), []byte("junk\n"), 0o644))
	}
	s, err := SumMonth(out, "2026-03")
	require.NoError(t, err)
	require.NotNil(t, s)

	assert.Equal(t, "2026-03", s.Month)
	assert.Equal(t, []string{"2026-03-01", "2026-03-03"}, s.Days, "the days walk in name order and the strays stepped over")
	assert.Equal(t, []string{"2026-03-02"}, s.Missing, "the day between the two files is named, not filled")
	assert.Equal(t, 3, s.Rows)
	assert.Equal(t, 12, s.Turns, "turns sum over the files that counted any")
	assert.True(t, s.HaveTurn, "a dash turns= adds nothing but the month did count turns")

	totalAgg := s.Total
	require.NotNil(t, totalAgg)
	assert.Equal(t, int64(170), totalAgg.Totals[Input], "input folds over both days")
	assert.Equal(t, "170", totalAgg.Cell(Input))
	assert.Equal(t, Dash, totalAgg.Cell(Reasoning), "no row reported reasoning: the cell is the dash")
	assert.Equal(t, 2, totalAgg.Days(), "two days fed the month total")
	assert.Equal(t, 2, totalAgg.Keys(), "two (model, repo) pairs fed it")
	assert.Equal(t, 1, totalAgg.NonUTC, "one row was dated from a zone that is not UTC")

	require.Len(t, s.Pairs, 2)
	assert.Equal(t, "m1", s.Pairs[0].Model, "the biggest spend leads the pair listing")
	assert.Equal(t, "r1", s.Pairs[0].Repo)
	assert.Equal(t, "m2", s.Pairs[1].Model)
	require.Len(t, s.Models, 2)
	assert.Equal(t, "m1", s.Models[0].Model, "the biggest spend leads the model listing")
	assert.Equal(t, "m2", s.Models[1].Model)
}

// TestSumCoverSumMonthRefusesFileThatIsNotADayFile pins the refusal: a file whose
// stamp line is not nova-tokens v1 is an error named by BadDayFile, never a finding to
// step over.
func TestSumCoverSumMonthRefusesFileThatIsNotADayFile(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	saveDay(t, out, "2026-03-01", "1",
		coverRow("2026-03-01", "m1", "r1", UTC, func(c *Counts) { c.Set(Input, 5) }, 1))
	require.NoError(t, os.WriteFile(filepath.Join(out, "2026-03-02.tsv"), []byte("not a day file\n"), 0o644))
	_, sumErr := SumMonth(out, "2026-03")
	require.Error(t, sumErr, "a file that is not a day file is not a thing sum can answer about")
	var bad *BadDayFile
	require.ErrorAs(t, sumErr, &bad)
	assert.Equal(t, filepath.Join(out, "2026-03-02.tsv"), bad.Path)
	assert.Contains(t, bad.Error(), "2026-03-02.tsv: ", "Error is path: reason")
}

// TestSumCoverSumMonthRefusesMissingDirectory pins the read error: an out directory
// that does not exist is an error, not an empty month.
func TestSumCoverSumMonthRefusesMissingDirectory(t *testing.T) {
	t.Parallel()
	s, err := SumMonth(t.TempDir()+"/nowhere", "2026-03")
	require.Error(t, err)
	assert.Nil(t, s, "a month that cannot be read is not answered with an empty sum")
}

// TestSumCoverTotalSumsEveryType pins total: the five types sum over every reported
// type, and an empty grouping totals zero.
func TestSumCoverTotalSumsEveryType(t *testing.T) {
	t.Parallel()
	a := newAgg()
	a.add(coverRow("2026-03-01", "m1", "r1", UTC, func(c *Counts) {
		c.Set(Input, 3)
		c.Set(Output, 4)
		c.Set(Reasoning, 10)
	}, 0), "k")
	assert.EqualValues(t, 17, total(a), "the three reported types sum")
	assert.EqualValues(t, 0, total(newAgg()), "a grouping with no rows totals zero")
}

// TestSumCoverAtoiSafeParsesDigitsOnly pins atoiSafe: digits parse, anything else
// answers the -1 that SumMonth's n>=0 gate reads as no turn count.
func TestSumCoverAtoiSafeParsesDigitsOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"zero", "0", 0},
		{"whole number", "42", 42},
		{"trailing letter", "12a", -1},
		{"leading sign", "-1", -1},
		{"space", " 7", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, atoiSafe(c.in))
		})
	}
}
