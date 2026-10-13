package cairn

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCairnIndexSinceCursor(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	stamp := time.Date(2026, 10, 13, 12, 0, 0, 123, time.UTC)
	for _, session := range []string{"a", "b"} {
		require.NoError(t, Open(store, session, "fixture", stamp, "manual"))
	}
	for _, row := range []struct {
		session, id string
		stamp       time.Time
	}{
		{"a", "first", stamp.Add(-time.Second)}, {"a", "middle", stamp}, {"a", "next", stamp}, {"b", "last", stamp}, {"b", "later", stamp.Add(time.Second)},
	} {
		_, err := Append(store, row.session, row.id, row.id, "fixture", row.stamp, "manual")
		require.NoError(t, err)
	}
	all, names, _, err := Index(store, "")
	require.NoError(t, err)
	rows, gotNames, total, err := IndexSince(store, "", "middle")
	require.NoError(t, err)
	require.Equal(t, all[2:], rows, "equal-stamp siblings survive an ID cursor")
	require.Equal(t, names, gotNames)
	require.Equal(t, 3, total)
	rows, _, total, err = IndexSince(store, "", stamp.Format(time.RFC3339Nano))
	require.NoError(t, err)
	require.Equal(t, all[4:], rows, "an instant excludes every equal-stamp sibling")
	require.Equal(t, 1, total)
	rows, _, _, err = IndexSince(store, "", stamp.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano))
	require.NoError(t, err)
	require.Equal(t, all[4:], rows)
	_, err = Append(store, "b", "middle", "duplicate cursor ID", "fixture", stamp, "manual")
	require.NoError(t, err)
	_, _, _, err = IndexSince(store, "", "middle")
	require.ErrorContains(t, err, "ambiguous")
	rows, _, _, err = IndexSince(store, "a", "middle")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "next", rows[0].ID)
	for _, cursor := range []string{"", "missing", "invalid cursor"} {
		_, _, _, err = IndexSince(store, "", cursor)
		require.Error(t, err)
	}
}
