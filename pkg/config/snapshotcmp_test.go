package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				"billing": "metered", "funded": "false", "balance_usd": "999", "balance_as_of": "2026-10-13", "beat": "2026-10-13T00:00:00Z", "console_url": "https://example",
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
