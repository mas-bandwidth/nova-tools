package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A cost reconcile after a midday tidy sets the provider's count and the sprint's records
// over the same window: the provider's figure since its last read before the tidy beside the
// records since that read. A full day's provider count set beside only the window's records
// reports the whole morning's spend as a gap (the reader's finding, 2026-10-06:
// stats_reconcile.go compared a full day's provider figure with only the window's records).
func TestACostReconcileAfterAMiddayTidyCountsTheSameWindow(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Routes = []Route{{Name: "flash-or", Provider: "openrouter", Model: "m"}}
	w.s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(pr, Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g1", Route: "flash-or", End: "no result",
		At: stamp(w.s.Now.Add(-time.Hour)), Usage: cardcost.ParseUsage("input=100 actual_usd=10 actual_by=harness")})
	w.s.Work.Put(pr)

	// midday, before the tidy: the provider counted $10 over the day and the records hold it
	w.must(CostReconcile(w.s, openrouterRead(w, 10)))
	bases := ReconcileBases(w.s)
	require.Equal(t, ReconcileBase{Day: w.s.Now.UTC().Format(time.DateOnly), At: w.s.Now.UTC(), Provider: 10}, bases["openrouter"])

	// after the tidy the sprint does $5 more work on the same provider and day
	pr = w.s.Work.Card("s1-1")
	book(pr, Consumer{Kind: "work", Card: "s1-1", Key: "s1-1#g2", Route: "flash-or", End: "ok",
		At: stamp(w.s.Now), Usage: cardcost.ParseUsage("input=100 actual_usd=5 actual_by=harness")})

	// the provider has counted $15 over the day: $10 before its last read and $5 since
	p := w.must(CostReconcileSince(w.s, openrouterRead(w, 15), bases))
	rec, ok := CostReconcileOf(w.s.Fleet, "openrouter")
	require.True(t, ok)
	assert.InDelta(t, 5.0, rec.Used, 1e-9, "the provider's count since the baseline read, not the whole day's $15")
	assert.InDelta(t, 5.0, rec.Internal, 1e-9, "the records since the baseline read, not the whole day's $15")
	assert.InDelta(t, 0.0, rec.Gap, 1e-9)
	assert.Empty(t, p.Notes, "the windows match: no gap judgment")
	assert.InDelta(t, 15.0, rec.Days[rec.Day].Provider, 1e-9, "the day's stored gap stays the whole day's, for the unreconciled line")
	assert.InDelta(t, 15.0, rec.Days[rec.Day].Internal, 1e-9)
}
