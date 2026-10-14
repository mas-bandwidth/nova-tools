package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotComparisonRejectsUnexplainedHistory pins field-level history
// attribution (docs/SPEC-CONFIG.md, "Snapshot and compare"): the recorded
// transitions must account for the actual snapshot delta, including its final
// value; an unrelated operator write or a later ignored write cannot explain it.
func TestSnapshotComparisonRejectsUnexplainedHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		field    string
		before   string
		after    string
		operator map[string]string
		ignored  map[string]string
	}{
		{name: "field absent in both history maps", field: "external", before: "old", after: "new", operator: map[string]string{"funded": "true"}},
		{name: "out of band overwrite after operator", field: "billing", before: "metered", after: "plan", operator: map[string]string{"billing": "both"}},
		{name: "ignored overwrite after operator", field: "billing", before: "metered", after: "plan", operator: map[string]string{"billing": "both"}, ignored: map[string]string{"billing": "plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m := NewMem()
			m.Now = func() time.Time { return time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC) }
			_, err := m.Insert(ctx, KindProvider, Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
			require.NoError(t, err)
			before := CmpSnap{At: "2026-10-13T20:00:00Z", Kinds: map[string]CmpKind{KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{tc.field: tc.before}}}}}}
			m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
			_, _, err = m.Update(ctx, KindProvider, "p", tc.operator, "operator")
			require.NoError(t, err)
			if tc.ignored != nil {
				m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 30, 0, 0, time.UTC) }
				_, _, err = m.Update(ctx, KindProvider, "p", tc.ignored, MigrationActor)
				require.NoError(t, err)
			}
			after := CmpSnap{At: "2026-10-13T22:00:00Z", Kinds: map[string]CmpKind{KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{tc.field: tc.after}}}}}}
			res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
			require.NoError(t, err)
			assert.Equal(t, CompareCrit, res.Class)
			assert.Equal(t, 1, res.Exit())
			assert.Equal(t, []string{tc.field}, findRow(t, res, KindProvider, "p").Unexplained)
		})
	}
}

// TestSnapshotFilesRequireCompleteMetadata pins the eight kind files and the
// finite, ordered capture window (docs/SPEC-CONFIG.md, "Snapshot and compare").
func TestSnapshotFilesRequireCompleteMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, file, content, want string }{
		{name: "missing provider", file: "provider.json", want: "provider.json"},
		{name: "missing timestamp", file: "when.json", content: `{}`, want: "at"},
		{name: "blank timestamp", file: "when.json", content: `{"at":""}`, want: "at"},
		{name: "invalid timestamp", file: "when.json", content: `{"at":"invalid"}`, want: "RFC 3339"},
		{name: "missing tool", file: "when.json", content: `{"at":"2026-10-13T20:00:00Z"}`, want: "tool"},
		{name: "empty kind", file: "provider.json", content: `{}`, want: "kind"},
		{name: "missing rows", file: "provider.json", content: `{"kind":"provider"}`, want: "rows"},
		{name: "wrong kind", file: "provider.json", content: `{"kind":"route","rows":[]}`, want: "kind"},
		{name: "nameless row", file: "provider.json", content: `{"kind":"provider","rows":[{}]}`, want: "row names"},
		{name: "duplicate row", file: "provider.json", content: `{"kind":"provider","rows":[{"name":"p","fields":{}},{"name":"p","fields":{}}]}`, want: "row names"},
		{name: "missing fields", file: "provider.json", content: `{"kind":"provider","rows":[{"name":"p"}]}`, want: "fields"},
		{name: "null fields", file: "provider.json", content: `{"kind":"provider","rows":[{"name":"p","fields":null}]}`, want: "fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snap, err := TakeSnapshot(context.Background(), NewMem(), "2026-10-13T20:00:00Z", "test")
			require.NoError(t, err)
			dir := t.TempDir()
			require.NoError(t, WriteSnapshot(dir, snap))
			if tc.content == "" {
				require.NoError(t, os.Remove(filepath.Join(dir, tc.file)))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.content), 0o600))
			}
			_, err = LoadSnapshot(dir)
			assert.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("reversed window", func(t *testing.T) {
		t.Parallel()
		_, err := CompareSnapshots(context.Background(), NewMem(), CmpSnap{At: "2026-10-13T22:00:00Z"}, CmpSnap{At: "2026-10-13T20:00:00Z"}, CompareOptions{})
		assert.ErrorContains(t, err, "before")
	})
}

// TestSnapshotComparisonDistinguishesAbsentFromEmpty catches an omitted old
// field even when its previous value was the empty string.
func TestSnapshotComparisonDistinguishesAbsentFromEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	before := CmpSnap{At: "2026-10-13T20:00:00Z", Kinds: map[string]CmpKind{KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false", "console_url": ""}}}}}}
	after := CmpSnap{At: "2026-10-13T22:00:00Z", Kinds: map[string]CmpKind{KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}}}}}
	res, err := CompareSnapshots(ctx, NewMem(), before, after, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, CompareCrit, res.Class)
	assert.Equal(t, 1, res.Exit())
	row := findRow(t, res, KindProvider, "p")
	assert.Equal(t, []string{"console_url"}, row.Unexplained)
	require.Len(t, row.Fields, 1)
	assert.True(t, row.Fields[0].AfterMissing)

	entry := Change{ID: 1, Op: OpSet, Before: map[string]string{"console_url": ""}, After: map[string]string{}, Actor: "operator", At: "2026-10-13T21:00:00Z"}
	_, explained := explainedBy(FieldDiff{Field: "console_url", AfterMissing: true}, []Change{entry}, map[string]bool{MigrationActor: true})
	assert.True(t, explained, "a recorded operator field removal remains attributable")
}

// TestSnapshotComparisonReportsRemovedRows pins removals as row differences,
// rather than allowing a before-only row to disappear from the comparison.
func TestSnapshotComparisonReportsRemovedRows(t *testing.T) {
	t.Parallel()
	for _, actor := range []string{"operator", MigrationActor, ""} {
		t.Run(actor, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			m := NewMem()
			m.Now = func() time.Time { return time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC) }
			_, err := m.Insert(ctx, KindProvider, Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
			require.NoError(t, err)
			before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "test")
			require.NoError(t, err)
			m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
			if actor != "" {
				_, err = m.Delete(ctx, KindProvider, "p", actor)
				require.NoError(t, err)
			} else {
				delete(m.rows[KindProvider], "p")
			}
			after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "test")
			require.NoError(t, err)
			res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
			require.NoError(t, err)
			if actor == "operator" {
				assert.Equal(t, CompareOperator, res.Class)
				assert.Equal(t, "operator", findRow(t, res, KindProvider, "p").Actor)
			} else {
				assert.Equal(t, CompareCrit, res.Class)
			}
			assert.True(t, findRow(t, res, KindProvider, "p").Removed)
			assert.Equal(t, len(Kinds), len(res.Kinds))
		})
	}
}

// TestSnapshotComparisonDoesNotCreditAnIgnoredPartOfAnAddedRow keeps a later
// unrelated operator write from masking an ignored change to the new row.
func TestSnapshotComparisonDoesNotCreditAnIgnoredPartOfAnAddedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "test")
	require.NoError(t, err)
	m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
	_, err = m.Insert(ctx, KindProvider, Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, "operator")
	require.NoError(t, err)
	m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 10, 0, 0, time.UTC) }
	_, _, err = m.Update(ctx, KindProvider, "p", map[string]string{"funded": "true"}, MigrationActor)
	require.NoError(t, err)
	m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 20, 0, 0, time.UTC) }
	_, _, err = m.Update(ctx, KindProvider, "p", map[string]string{"billing": "both"}, "operator")
	require.NoError(t, err)
	after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "test")
	require.NoError(t, err)
	res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
	require.NoError(t, err)
	assert.Equal(t, CompareCrit, res.Class)
	assert.Equal(t, CompareCrit, findRow(t, res, KindProvider, "p").Class)
}

// TestSnapshotAddedRowNamesCreator pins the add act's actor and reason even
// when a later operator edit helps produce the final recorded row.
func TestSnapshotAddedRowNamesCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "test")
	require.NoError(t, err)
	m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
	_, err = m.Insert(WithReason(ctx, "created provider"), KindProvider, Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, "creator")
	require.NoError(t, err)
	m.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 30, 0, 0, time.UTC) }
	_, _, err = m.Update(WithReason(ctx, "changed billing"), KindProvider, "p", map[string]string{"billing": "both"}, "editor")
	require.NoError(t, err)
	after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "test")
	require.NoError(t, err)
	res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
	require.NoError(t, err)
	row := findRow(t, res, KindProvider, "p")
	assert.Equal(t, CompareAdded, row.Class)
	assert.Equal(t, "creator", row.Actor)
	assert.Equal(t, "2026-10-13T21:00:00Z", row.At)
	assert.Equal(t, "created provider", row.Reason)
}

// TestSnapshotCompareNamesTheActBehindAChangedRow pins compare's rule: a row
// that changed and whose change an operator's recorded history explains is
// OPERATOR and never reverts, while a change only the migration actor
// explains, or no history at all, is CRIT. The store's history rows are written
// here and the clock is the Mem's own (docs/SPEC-CONFIG.md, "History").
func TestSnapshotCompareNamesTheActBehindAChangedRow(t *testing.T) {
	t.Parallel()

	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return ts
	}
	ctx := context.Background()

	// (1) the OpenRouter case: funded false -> true with a history entry by
	// the owner inside the window is OPERATOR, exit 0.
	t.Run("operator act is reported and never a failure", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.Now = func() time.Time { return at("2026-10-13T20:00:00Z") }
		_, err := m.Insert(ctx, KindProvider, Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
		require.NoError(t, err)
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		m.Now = func() time.Time { return at("2026-10-13T21:40:12Z") }
		_, _, err = m.Update(WithReason(ctx, "top-up (funded false -> true)"), KindProvider, "openrouter", map[string]string{"funded": "true"}, "the-owner")
		require.NoError(t, err)
		after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "1.2.24")
		require.NoError(t, err)

		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareOperator, res.Class)
		assert.Equal(t, 0, res.Exit())
		row := findRow(t, res, KindProvider, "openrouter")
		assert.Equal(t, CompareOperator, row.Class)
		assert.Equal(t, "the-owner", row.Actor)
		assert.Equal(t, "2026-10-13T21:40:12Z", row.At)
		assert.Equal(t, "top-up (funded false -> true)", row.Reason)
	})

	// (2) the same row differing with only the migration actor in the window
	// is CRIT.
	t.Run("only the migration actor in the window is CRIT", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.Now = func() time.Time { return at("2026-10-13T20:00:00Z") }
		_, err := m.Insert(ctx, KindProvider, Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
		require.NoError(t, err)
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		m.Now = func() time.Time { return at("2026-10-13T21:00:00Z") }
		_, _, err = m.Update(ctx, KindProvider, "openrouter", map[string]string{"funded": "true"}, MigrationActor)
		require.NoError(t, err)
		after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "1.2.24")
		require.NoError(t, err)

		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareCrit, res.Class)
		assert.Equal(t, 1, res.Exit())
		row := findRow(t, res, KindProvider, "openrouter")
		assert.Equal(t, CompareCrit, row.Class)
		assert.Equal(t, []string{"funded"}, row.Unexplained)
	})

	// (3) a difference with no history at all is CRIT (the store was edited
	// out of band).
	t.Run("a difference with no history is CRIT", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		before := CmpSnap{At: "2026-10-13T20:00:00Z", Kinds: map[string]CmpKind{
			KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}}},
		}}
		after := CmpSnap{At: "2026-10-13T22:00:00Z", Kinds: map[string]CmpKind{
			KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "true"}}}},
		}}
		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareCrit, res.Class)
		assert.Equal(t, []string{"funded"}, findRow(t, res, KindProvider, "p").Unexplained)
	})

	// (4) a row differing in two fields, one explained by an operator act and
	// one not, is CRIT naming the unexplained field.
	t.Run("partial explanation names the unexplained field", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.Now = func() time.Time { return at("2026-10-13T20:00:00Z") }
		_, err := m.Insert(ctx, KindProvider, Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
		require.NoError(t, err)
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		m.Now = func() time.Time { return at("2026-10-13T21:00:00Z") }
		_, _, err = m.Update(ctx, KindProvider, "openrouter", map[string]string{"funded": "true"}, "the-owner")
		require.NoError(t, err)
		// billing changed out of band: the after snapshot shows it, the
		// history has no entry for it.
		after := CmpSnap{At: "2026-10-13T22:00:00Z", Kinds: map[string]CmpKind{
			KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "openrouter", Fields: map[string]string{"billing": "both", "funded": "true"}}}},
		}}
		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		row := findRow(t, res, KindProvider, "openrouter")
		assert.Equal(t, CompareCrit, row.Class)
		assert.Equal(t, []string{"billing"}, row.Unexplained)
	})

	// (5) live fields are never compared; a field the new build added is
	// listed apart.
	t.Run("live fields are ignored and added fields listed apart", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		before := CmpSnap{At: "2026-10-13T20:00:00Z", Kinds: map[string]CmpKind{
			KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{
				"billing": "metered", "funded": "false", "balance_usd": "10", "balance_as_of": "2026-10-01", "beat": "2026-10-01T00:00:00Z",
			}}}},
		}}
		after := CmpSnap{At: "2026-10-13T22:00:00Z", Kinds: map[string]CmpKind{
			KindProvider: {Kind: KindProvider, Rows: []CmpRow{{Name: "p", Fields: map[string]string{
				"billing": "metered", "funded": "false", "balance_usd": "999", "balance_as_of": "2026-10-13", "beat": "2026-10-13T00:00:00Z", "console_url": "https://example.test",
			}}}},
		}}
		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareSame, res.Class)
		kd := findKind(t, res, KindProvider)
		assert.Contains(t, kd.Added, "console_url")
		row := findRow(t, res, KindProvider, "p")
		assert.Equal(t, CompareSame, row.Class)
		assert.Equal(t, []string{"console_url"}, row.Added)
	})

	// (6) identical snapshots are SAME.
	t.Run("identical snapshots are SAME", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "1.2.24")
		require.NoError(t, err)
		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareSame, res.Class)
		assert.Equal(t, 0, res.Exit())
	})

	// A row the operator added between the snapshots (present after, absent
	// before) is an OPERATOR-ADD at the row level, named with the actor, time
	// and reason of the creation entry, and never a failure; the kind and
	// result carry OPERATOR-DIFF.
	t.Run("an operator-added row is OPERATOR-ADD", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.Now = func() time.Time { return at("2026-10-13T20:00:00Z") }
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		m.Now = func() time.Time { return at("2026-10-13T21:40:12Z") }
		_, err = m.Insert(WithReason(ctx, "add openrouter"), KindProvider, Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, "the-owner")
		require.NoError(t, err)
		after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "1.2.24")
		require.NoError(t, err)

		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareOperator, res.Class)
		assert.Equal(t, 0, res.Exit())
		row := findRow(t, res, KindProvider, "openrouter")
		assert.Equal(t, CompareAdded, row.Class)
		assert.Equal(t, "the-owner", row.Actor)
		assert.Equal(t, "2026-10-13T21:40:12Z", row.At)
		assert.Equal(t, "add openrouter", row.Reason)
	})

	// A row only the migration actor added, or with no history, is CRIT.
	t.Run("an added row with only the migration actor is CRIT", func(t *testing.T) {
		t.Parallel()
		m := NewMem()
		m.Now = func() time.Time { return at("2026-10-13T20:00:00Z") }
		before, err := TakeSnapshot(ctx, m, "2026-10-13T20:00:00Z", "1.2.24")
		require.NoError(t, err)
		m.Now = func() time.Time { return at("2026-10-13T21:00:00Z") }
		_, err = m.Insert(ctx, KindProvider, Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, MigrationActor)
		require.NoError(t, err)
		after, err := TakeSnapshot(ctx, m, "2026-10-13T22:00:00Z", "1.2.24")
		require.NoError(t, err)

		res, err := CompareSnapshots(ctx, m, before, after, CompareOptions{})
		require.NoError(t, err)
		assert.Equal(t, CompareCrit, res.Class)
		assert.Equal(t, 1, res.Exit())
		row := findRow(t, res, KindProvider, "openrouter")
		assert.Equal(t, CompareCrit, row.Class)
	})
}

func findRow(t *testing.T, res CompareResult, kind, name string) RowDiff {
	t.Helper()
	for _, kd := range res.Kinds {
		if kd.Kind != kind {
			continue
		}
		for _, rd := range kd.Rows {
			if rd.Name == name {
				return rd
			}
		}
	}
	require.Failf(t, "row not found", "kind=%s name=%s", kind, name)
	return RowDiff{}
}

func findKind(t *testing.T, res CompareResult, kind string) KindDiff {
	t.Helper()
	for _, kd := range res.Kinds {
		if kd.Kind == kind {
			return kd
		}
	}
	require.Failf(t, "kind not found", "kind=%s", kind)
	return KindDiff{}
}
