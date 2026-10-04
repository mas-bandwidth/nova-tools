package tokens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokens_cover_test.go reaches the functions of tokens.go that the unit tier's per-function
// table leaves at 0.0%: the five-name resolver, the Counts arithmetic, the Row basis, the
// Folder fold with its overlaps, turns and day rows, the Source stream collapse, the small
// renderers, and the one door every source file is opened through. Every test name begins
// TestTokensCover so `-run TestTokensCover` selects the set.

func TestTokensCoverTypeByName(t *testing.T) {
	t.Parallel()

	for i, name := range TypeNames {
		got, ok := TypeByName(name)
		assert.Truef(t, ok, "TypeByName(%q) did not resolve", name)
		assert.Equalf(t, Type(i), got, "TypeByName(%q) resolved to %v", name, got)
	}
	got, ok := TypeByName("no-such-type")
	assert.False(t, ok, "TypeByName resolved a name that is not one of the five")
	assert.Equal(t, Type(0), got)
}

func TestTokensCoverCountsArithmetic(t *testing.T) {
	t.Parallel()

	var c Counts
	c.Set(Input, 10)
	c.Set(Output, 20)
	c.Set(CacheWrite, 30)
	c.Set(CacheRead, 40)
	c.Set(Reasoning, 50)

	assert.Equal(t, int64(100), c.Billed(), "billed is the four priced types, never reasoning")
	assert.Equal(t, int64(150), c.Total(), "total sums all five reported types")
	assert.Equal(t, 0, c.Dashes(), "all five are reported, so none is a dash")

	var empty Counts
	assert.Equal(t, int64(0), empty.Billed(), "a dash adds nothing to billed")
	assert.Equal(t, int64(0), empty.Total(), "a dash adds nothing to total")
	assert.Equal(t, int(NTypes), empty.Dashes(), "an unreported type is a dash")
}

func TestTokensCoverRowBasis(t *testing.T) {
	t.Parallel()

	single := &Row{bases: map[string]bool{UTC: true}}
	assert.Equal(t, UTC, single.Basis(), "one basis is the row's basis")

	two := &Row{bases: map[string]bool{UTC: true, "America/New_York": true}}
	assert.Equal(t, "", two.Basis(), "two bases is no single basis")

	none := &Row{bases: map[string]bool{}}
	assert.Equal(t, "", none.Basis(), "no basis is no single basis")
}

func TestTokensCoverFolderAddOverlapsTurns(t *testing.T) {
	t.Parallel()

	f := NewFolder()
	m := Message{ID: "m1", Day: "2026-09-11", Basis: UTC, Model: "a", Repo: "r", Rough: 2, Usd: 100, Priced: true, Provider: "p", Turn: true}
	m.Counts.Set(Input, 5)
	m.Counts.Set(Reasoning, 7)

	// An id is scoped to one provider. These two labels are two providers, so the
	// shared id is not an overlap and neither message is dropped (TokenFold
	// invariant UnscopedIDNotDeduped).
	f.Add("swarm:two", m)
	f.Add("claude:one", m)

	row := f.rows[Key{Day: "2026-09-11", Model: "a", Repo: "r"}]
	require.NotNil(t, row)
	assert.Equal(t, int64(10), row.Counts.n[Input], "the same key from two sources sums per type")
	assert.Equal(t, 4, row.Rough)
	assert.Equal(t, int64(200), row.Usd)
	assert.Equal(t, int64(10), row.PricedTokens, "billed tokens of the priced messages are summed")
	assert.Equal(t, "p", row.Provider)
	assert.Equal(t, []string{"claude:one", "swarm:two"}, row.Sources())

	assert.Empty(t, f.Overlaps(), "an id shared across providers is not an overlap")
	assert.False(t, f.RefuseWrite(), "a cross-provider id is not a refusal")
	assert.Equal(t, []string{"2026-09-11"}, f.Days())

	n, ok := f.Turns("2026-09-11")
	assert.True(t, ok)
	assert.Equal(t, 2, n)
	n, ok = f.Turns("2026-09-10")
	assert.False(t, ok, "a day no source fed has no turn count")
	assert.Equal(t, 0, n)

	// One id from one label is not an overlap, and a message with no id cannot be one.
	g := NewFolder()
	g.Add("claude:one", m)
	g.Add("claude:one", m)
	g.Add("claude:one", Message{Day: "2026-09-11", Model: "a", Repo: "r"})
	assert.Empty(t, g.Overlaps())
}

func TestTokensCoverFolderDayRows(t *testing.T) {
	t.Parallel()

	f := NewFolder()
	clean := Message{ID: "c1", Day: "2026-09-11", Basis: UTC, Model: "b", Repo: "r"}
	clean.Counts.Set(Input, 1)
	f.Add("claude:one", clean)

	// A row fed two day bases is mixed: named, and in neither the rows nor the counts.
	mixed := Message{ID: "x1", Day: "2026-09-11", Basis: "America/New_York", Model: "a", Repo: "r"}
	mixed.Counts.Set(Input, 2)
	f.Add("claude:one", mixed)
	f.Add("claude:one", Message{ID: "x2", Day: "2026-09-11", Basis: UTC, Model: "a", Repo: "r"})

	rows, mixedRows := f.DayRows("2026-09-11")
	require.Len(t, rows, 1)
	assert.Equal(t, Key{Day: "2026-09-11", Model: "b", Repo: "r"}, rows[0].Key)
	require.Len(t, mixedRows, 1)
	assert.Equal(t, Key{Day: "2026-09-11", Model: "a", Repo: "r"}, mixedRows[0].Key)
	assert.ElementsMatch(t, []string{UTC, "America/New_York"}, mixedRows[0].Bases)
	assert.Equal(t, []string{"claude:one"}, mixedRows[0].Labels)

	other, otherMixed := f.DayRows("2026-09-10")
	assert.Empty(t, other)
	assert.Empty(t, otherMixed)
}

func TestTokensCoverSourceAddMessageCollapse(t *testing.T) {
	t.Parallel()

	var s Source
	s.AddMessage("", Message{Day: "2026-09-11"})
	assert.Equal(t, 1, s.Stat.NoID, "a message with no id is counted and not folded")

	s.AddMessage("m1", Message{Day: "2026-09-11", Model: "a"})
	s.AddMessage("m2", Message{Day: "2026-09-11", Model: "b"})
	s.AddMessage("m1", Message{Day: "2026-09-11", Model: "a-final"})
	assert.Equal(t, 1, s.Stat.Dup, "a repeated id is a dup, never a second count")

	s.Collapse()
	require.Len(t, s.Stream, 2, "the stream is one entry per id, in first-seen order")
	assert.Equal(t, "a-final", s.Stream[0].Model, "the last line for an id is the one kept")
	assert.Equal(t, "b", s.Stream[1].Model)
	assert.Equal(t, 2, s.Stat.Messages)
}

func TestTokensCoverSourceReportsList(t *testing.T) {
	t.Parallel()

	var none Source
	assert.Equal(t, Dash, none.ReportsList(), "a source reporting no type prints a dash")

	s := Source{Reports: []Type{Input, Output, CacheWrite, CacheRead, Reasoning}}
	assert.Equal(t, "input,output,cache_write,cache_read,reasoning", s.ReportsList())
}

func TestTokensCoverItoa64AndPercent(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "42", itoa64(42))
	assert.Equal(t, "-7", itoa64(-7))
	assert.Equal(t, "40.0", Percent(40, 100))
	assert.Equal(t, "0.0", Percent(1, 0), "there is no share over nothing")
}

func TestTokensCoverParseMicro(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want int64
		ok   bool
	}{
		{"dollars and cents", "1.23", 1230000, true},
		{"one micro-dollar", "0.000001", 1, true},
		{"whole dollars", "2", 2000000, true},
		{"seventh digit is cut, not rounded", "1.2345678", 1234567, true},
		{"dash is an absence", Dash, 0, false},
		{"empty is an absence", "", 0, false},
		{"two points is refused", "1.2.3", 0, false},
		{"an exponent is refused", "1e3", 0, false},
		{"letters are refused", "abc", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseMicro(tc.in)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTokensCoverUsd(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.234001", Usd(1234001))
	assert.Equal(t, "2", Usd(2000000), "trailing zeros after the point are trimmed")
	assert.Equal(t, "0", Usd(0))
	assert.Equal(t, "-1.234001", Usd(-1234001), "a negative cost keeps its sign")
}

func TestTokensCoverUsdPerMtok(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2.0000", UsdPerMtok(2000000, 1000000))
	assert.Equal(t, Dash, UsdPerMtok(123, 0), "there is no average over no tokens")
}

func TestTokensCoverOpensAndOpenSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "source.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	before := Opens()
	f, err := openSource(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.GreaterOrEqual(t, Opens(), before+1, "the one door counts the file it opened")

	_, err = openSource(filepath.Join(dir, "missing.txt"))
	assert.Error(t, err, "a source that cannot be opened is an error, not a silent zero")
}
