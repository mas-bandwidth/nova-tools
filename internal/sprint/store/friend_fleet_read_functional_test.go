//go:build functional

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exact totals and fields are always checked against the full-record oracle on
// actual isolated Redis. The dedicated functional,perf gate measures the budget.
func TestRedisFriendUsageProjectionPreservesTotalsAndFields(t *testing.T) {
	t.Parallel()
	redisFriendUsageProjectionReadSamples(t, 1)
}

// The measured source footprint is 4,479 friend lifecycle records carrying
// roughly 9 MB of full fields (SPEC-SPRINT-DASHBOARD, source cost). Each sample
// makes a fresh table read; the fixed oracle is calculated once, not cached by
// the implementation. Performance numbers apply only to this isolated footprint.
func redisFriendUsageProjectionReadSamples(t *testing.T, samples int) []time.Duration {
	t.Helper()
	st, _ := liveStore(t)
	ctx := context.Background()
	rows := []FriendRow{{Name: "alpha"}, {Name: "beta"}}
	require.NoError(t, st.B.RowsAdd(ctx, st.Names.Table(sprint.Fleet), []string{"m1", "control", sprint.FriendRow("alpha"), sprint.FriendRow("beta")}))
	const selected = 4479
	cols := []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed}
	members := make([]ntable.BatchMemberEntry, 0, selected+129)
	for i := 0; i < selected+129; i++ {
		row, col := sprint.FriendRow(rows[i%len(rows)].Name), cols[(i/len(rows))%len(cols)]
		if i >= selected {
			row, col = "m1", sprint.DoneOK
		}
		if i == selected+128 {
			row, col = "control", sprint.Ready
		}
		members = append(members, ntable.BatchMemberEntry{ID: fmt.Sprintf("usage-%d", i), Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: row, Col: col}, Set: map[string]string{sprint.FieldUsage: "input=11 output=7 actual_usd=0.125 actual_by=harness cost=actual", "brief": strings.Repeat("b", 2048)}})
	}
	for start := 0; start < len(members); start += ntable.LimitChangedEntries {
		shape, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
		require.NoError(t, err)
		_, err = st.B.Apply(ctx, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Fleet), Epoch: fmt.Sprint(shape[0].Epoch), ExpectedTableRevision: fmt.Sprint(shape[0].Revision), OperationID: fmt.Sprintf("usage-fixture-%d", start), Members: members[start:min(start+ntable.LimitChangedEntries, len(members))]})
		require.NoError(t, err)
	}
	pinned, err := st.Pinned(ctx)
	require.NoError(t, err)
	full, err := pinned.Load(ctx, []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	var expected []*sprint.Card
	for _, row := range rows {
		for _, col := range cols {
			expected = append(expected, full.Fleet.Cell(sprint.FriendRow(row.Name), col)...)
		}
	}
	require.Len(t, expected, selected)
	expectedTokens := sprint.FriendTokensFromCards(expected)
	expectedCharged := sprint.FriendChargedFromCards(expected)
	var durations []time.Duration
	var got []*sprint.Card
	for sample := 0; sample < samples; sample++ {
		start := time.Now()
		fleet, err := pinned.friendFleet(ctx, rows)
		require.NoError(t, err)
		durations = append(durations, time.Since(start))
		got = got[:0]
		for _, row := range rows {
			for _, col := range cols {
				got = append(got, fleet.Cell(sprint.FriendRow(row.Name), col)...)
			}
		}
		require.Len(t, got, selected)
		require.Equal(t, expectedTokens, sprint.FriendTokensFromCards(got))
		require.Equal(t, expectedCharged, sprint.FriendChargedFromCards(got))
	}
	for _, card := range got {
		assert.Len(t, card.Fields, 1)
		assert.Contains(t, card.Fields, sprint.FieldUsage)
	}
	fullBytes, err := json.Marshal(expected)
	require.NoError(t, err)
	projectedBytes, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Less(t, len(projectedBytes)*5, len(fullBytes), "wire field projection materially bounds the decoded payload")
	slices.Sort(durations)
	n := len(durations)
	p50, p95, p99 := durations[(n-1)/2], durations[(n*95+99)/100-1], durations[(n*99+99)/100-1]
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	t.Logf("isolated Redis friend usage records=%d samples=%d full_json_bytes=%d projected_json_bytes=%d mean=%s p50=%s p95=%s p99=%s max=%s target_p99=100ms", selected, n, len(fullBytes), len(projectedBytes), sum/time.Duration(n), p50, p95, p99, durations[n-1])
	return durations
}
