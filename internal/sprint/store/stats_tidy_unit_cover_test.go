package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreStatsTidyCoverArchiveKey(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 6, 12, 34, 56, 789, time.UTC)
	key := StatsArchiveKey(at, "deadbeef")
	assert.Equal(t, "stats:archive:2026-10-06T12:34:56.000000789Z-deadbeef", key)
}

func TestStoreStatsTidyCoverArchiveNonce(t *testing.T) {
	t.Parallel()
	nonce := archiveNonce()
	assert.Len(t, nonce, 8)
}

func TestStoreStatsTidyCoverTidyResultCount(t *testing.T) {
	t.Parallel()
	res := TidyResult{
		Record: StatsArchive{
			Rows: []sprint.TidyRow{
				{Moved: []sprint.TidyCard{{ID: "a"}, {ID: "b"}}, Kept: []sprint.TidyCard{{ID: "c"}}},
				{Moved: []sprint.TidyCard{{ID: "d"}}, Kept: []sprint.TidyCard{}},
			},
		},
	}
	res.count()
	assert.Equal(t, 3, res.Moved)
	assert.Equal(t, 1, res.Kept)
}

func TestStoreStatsTidyCoverStatsRecordOf(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// 1. empty raw / ok false
	rec := h.st.statsRecordOf("", false)
	assert.Equal(t, StatsRecord{}, rec)

	// 2. valid JSON
	validRec := StatsRecord{Epoch: 1, Reason: "test"}
	data, err := json.Marshal(validRec)
	require.NoError(t, err)
	rec2 := h.st.statsRecordOf(string(data), true)
	assert.Equal(t, validRec, rec2)

	// 3. unreadable JSON
	rec3 := h.st.statsRecordOf("not-json", true)
	assert.Equal(t, StatsRecord{}, rec3)
}

func TestStoreStatsTidyCoverStatsTidied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec, err := h.st.StatsTidied(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StatsRecord{}, rec)
}

func TestStoreStatsTidyCoverStatsSince(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Fresh store has no tidy record
	since, err := h.st.StatsSince(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, since.IsZero())

	sinceKind, err := h.st.StatsSince(context.Background(), sprint.TidyRoutes)
	require.NoError(t, err)
	assert.True(t, sinceKind.IsZero())
}

func TestStoreStatsTidyCoverTidyStats(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// 1. Dry run tidy
	dryRes, err := h.st.TidyStats(context.Background(), TidyReq{
		Kinds:  []string{sprint.TidyRoutes},
		Reason: "dry run tidy",
		DryRun: true,
	})
	require.NoError(t, err)
	assert.True(t, dryRes.DryRun)
	assert.NotEmpty(t, dryRes.Archive)

	// 2. Actual routes tidy
	tidyRes, err := h.st.TidyStats(context.Background(), TidyReq{
		Kinds:  []string{sprint.TidyRoutes},
		Reason: "routes tidy",
	})
	require.NoError(t, err)
	assert.False(t, tidyRes.DryRun)
	assert.Empty(t, tidyRes.Refused)

	// 3. Second tidy refusal within 1m0s
	refusedRes, err := h.st.TidyStats(context.Background(), TidyReq{
		Kinds:  []string{sprint.TidyRoutes},
		Reason: "second tidy",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refusedRes.Refused, "second tidy within 1m0s should be refused")
}
