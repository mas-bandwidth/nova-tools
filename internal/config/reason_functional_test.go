//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reason against the package's throwaway Postgres: it rides in the history
// row's own JSON (there is no reason column and the migration chain is full),
// History hands it back as Change.Reason, and a reader of the row sees only
// the fields the row carries.

func TestPostgresRecordsTheReasonAndHandsBackCleanFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := migrated(t)
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("box", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	_, err = st.Insert(WithReason(ctx, "the only 64-core box left"), KindMachine, m, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(WithReason(ctx, "held for the measured load"), KindMachine, "box", map[string]string{"note": "held"}, "rowan")
	require.NoError(t, err)
	_, err = st.Delete(WithReason(ctx, "returned to the pool"), KindMachine, "box", "rowan")
	require.NoError(t, err)

	hist, err := st.History(ctx, KindMachine, "box")
	require.NoError(t, err)
	require.Len(t, hist, 3, "an add, a set and a remove")
	assert.Equal(t, "the only 64-core box left", hist[0].Reason)
	assert.Equal(t, "held for the measured load", hist[1].Reason)
	assert.Equal(t, "returned to the pool", hist[2].Reason)
	assert.NotContains(t, hist[0].After, historyReasonKey, "the reason is not a row field")
	assert.NotContains(t, hist[1].After, historyReasonKey)
	assert.NotContains(t, hist[2].Before, historyReasonKey)
	assert.Equal(t, "held", hist[1].After["note"], "the row's own field is untouched")
	assert.Contains(t, HistoryLine(hist[1]), `reason=held\x20for\x20the\x20measured\x20load`)

	// a write that named no reason reads back with none, and its fields are
	// what they were
	_, err = st.Insert(ctx, KindMachine, m, "rowan")
	require.NoError(t, err)
	hist, err = st.History(ctx, KindMachine, "box")
	require.NoError(t, err)
	require.Len(t, hist, 4)
	assert.Equal(t, "", hist[3].Reason)
	assert.NotContains(t, HistoryLine(hist[3]), "reason=")
	assert.Equal(t, "8", hist[3].After["slots"], "the add's own fields are there without the reason")
}
