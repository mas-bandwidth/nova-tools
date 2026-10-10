package sprint_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A tidy keeps each provider's reconciliation baseline: the day, read and provider figure of
// its last read before the tidy, so a cost reconcile after it counts the provider and the
// records over one window (sprint.ReconcileBase, sprint.CostReconcileSince). The reader's
// finding, 2026-10-06: stats_reconcile.go compared a full day's provider figure with only the
// window's records because the baseline the tidy opened was not kept.
func TestATidyKeepsEachProvidersReconcileBaseline(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	day := r.now.UTC().Format(time.DateOnly)

	bases, err := r.st.StatsReconciles(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, bases, "no tidy recorded: no baseline")

	r.must(store.CostReconcileStep(sprint.CostReconcileReq{Reads: []sprint.UsageRead{{Provider: "prov-flash-a", Known: true, Day: day, Used: 12.5}}}))
	bases, err = r.st.StatsReconciles(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, bases, "the read alone is no tidy: no window opened")

	res, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyStreams}, Reason: "midday"})
	require.NoError(t, err)
	require.Empty(t, res.Refused)

	want := sprint.ReconcileBase{Day: day, At: r.st.Now().UTC(), Provider: 12.5}
	assert.Equal(t, map[string]sprint.ReconcileBase{"prov-flash-a": want}, res.Record.Reconciles, "the archive names the baseline")
	bases, err = r.st.StatsReconciles(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]sprint.ReconcileBase{"prov-flash-a": want}, bases, "the record keeps the baseline a reconcile after the tidy reads")
}
