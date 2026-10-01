package cairn

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlatReadMetadataOrderingAndNestedPrecedence(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	raw := "# Own heading\n\n## 2026-09-28T02:00:00Z — late\n\n  late prose  \n\n## 2026-09-28T01:00:00Z — early\n\nearly\n"
	require.NoError(t, os.WriteFile(benchFile(store, "flat"), []byte(raw), 0600))
	now := time.Date(2026, 9, 28, 1, 30, 0, 0, time.UTC)
	require.NoError(t, Open(store, "nested", "src", now, PublishNever))
	_, err := Append(store, "nested", "middle", "nested prose", "", now, PublishNever)
	require.NoError(t, err)
	rows, total, err := Index(store, "", 0)
	require.NoError(t, err, "index=%+v total=%d", rows, total)
	require.Equal(t, 3, total, "index=%+v", rows)
	require.Len(t, rows, 3)
	require.Equal(t, []string{"early", "middle", "late"}, []string{rows[0].ID, rows[1].ID, rows[2].ID}, "order=%+v", rows)
	rc, err := Receipt(store, "flat", "late")
	require.NoError(t, err, "flat metadata=%+v", rc)
	require.Equal(t, len("late prose"), rc.Bytes, "flat metadata=%+v", rc)
	require.Equal(t, "unknown", rc.Policy, "flat metadata=%+v", rc)
	require.Empty(t, rc.Source, "flat metadata=%+v", rc)
	require.Equal(t, Ledger{Sessions: 2, Entries: 3}, Coverage(store), "coverage")
	// A flat duplicate of a nested session is not a second record and cannot
	// replace its entry metadata, even if its own contents are malformed.
	require.NoError(t, os.WriteFile(benchFile(store, "nested"), []byte("## 2026-99-28T01:00:00Z — bad\n"), 0600))
	rows, total, err = Index(store, "nested", 0)
	require.NoError(t, err, "nested precedence=%+v total=%d", rows, total)
	require.Equal(t, 1, total, "nested precedence=%+v", rows)
	require.NotEmpty(t, rows, "nested precedence total=%d", total)
	require.Equal(t, "src", rows[0].Source, "nested precedence=%+v", rows)
	require.Equal(t, Ledger{Sessions: 2, Entries: 3}, Coverage(store), "duplicate coverage")
	rc, err = Receipt(store, "nested", "middle")
	require.NoError(t, err, "nested receipt=%+v", rc)
	require.Equal(t, "src", rc.Source, "nested receipt=%+v", rc)
	require.Equal(t, PublishNever, rc.Policy, "nested receipt=%+v", rc)
}

func TestFlatReadersRefuseCorruptAndAmbiguousHeadings(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"## 2026-99-28T01:00:00Z — e\n\nnote\n",
		"## 2026-09-28T01:00:00Z — ../e\n\nnote\n",
		"## 2026-09-28T01:00:00Z — e\n\na\n\n## 2026-09-28T02:00:00Z — e\n\nb\n",
	} {
		store := t.TempDir()
		require.NoError(t, os.WriteFile(benchFile(store, "s"), []byte(raw), 0600))
		_, _, err := Index(store, "", 0)
		assert.Error(t, err, "index accepted %q", raw)
		_, err = Receipt(store, "s", "e")
		assert.Error(t, err, "receipt accepted %q", raw)
	}
}

func TestFlatReadersKeepUnstructuredProseAndMissingEntriesDistinct(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	raw := "# My record\n\n## A manually dated note\n\nwords\n"
	require.NoError(t, os.WriteFile(benchFile(store, "s"), []byte(raw), 0600))
	rows, total, err := Index(store, "s", 0)
	require.NoError(t, err, "unstructured prose")
	require.Zero(t, total, "unstructured prose=%v", rows)
	require.Empty(t, rows, "unstructured prose")
	_, err = Receipt(store, "s", "e")
	require.ErrorContains(t, err, "no such entry", "missing entry")
}
