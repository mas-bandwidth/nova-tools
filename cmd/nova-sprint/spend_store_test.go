package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// spendSnapshot is the store's read: an openrouter route and one card whose records over the
// window (since the previous tag's day, 2026-09-17) hold $836 of openrouter in a take, a
// take with no result and a read, $50 of it before the window, and a subscription friend's
// run of 1000 tokens.
func spendSnapshot(now time.Time) *sprint.Snapshot {
	s := &sprint.Snapshot{Now: now, Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet)}
	s.Routes = []sprint.Route{{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m"}}
	s.Work.SetRows([]string{"s1"})
	pr := &sprint.Card{ID: "s1-1", Row: "s1", Col: sprint.Working, Fields: map[string]string{}}
	in, before := "2026-09-17T18:00:00Z", "2026-09-16T18:00:00Z"
	for _, c := range []sprint.Consumer{
		{Kind: "work", Card: "s1-1", Key: "a", Route: "pro-or", End: "ok", At: in, Usage: cardcost.ParseUsage("input=10 actual_usd=500 actual_by=harness")},
		{Kind: "work", Card: "s1-1", Key: "b", Route: "pro-or", End: "no result", At: in, Usage: cardcost.ParseUsage("input=10 actual_usd=300 actual_by=harness")},
		{Kind: "read", Card: "s1-1.r1", Key: "c", Model: "openrouter/m", End: "ok", At: in, Usage: cardcost.ParseUsage("input=10 predicted_usd=36")},
		{Kind: "work", Card: "s1-1", Key: "d", Route: "pro-or", End: "failed", At: before, Usage: cardcost.ParseUsage("input=10 actual_usd=50 actual_by=harness")},
		{Kind: "read", Card: "s1-1.r2", Key: "e", Who: "alex", End: "ok", At: in, Usage: cardcost.ParseUsage("input=600 output=400 " + sprint.UsageSubscription)},
	} {
		spendRecordConsumer(pr, c)
	}
	s.Work.Put(pr)
	return s
}

// spendRecordConsumer writes one consumer onto a primary the way a step does, for a world
// built outside a step. The production writer is unexported.
func spendRecordConsumer(pr *sprint.Card, c sprint.Consumer) {
	key := sprint.FieldCostRecord + c.Key
	if pr.Fields[key] != "" {
		return
	}
	total := cardcost.ParseTotal(pr.Fields[sprint.FieldCostTotal])
	pr.Fields[sprint.FieldCostTotal] = total.Add(c.Usage).String()
	orDash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	w := []string{
		"kind=" + c.Kind, "card=" + c.Card, "attempt=" + strconv.Itoa(c.Attempt), "take=" + strconv.Itoa(c.Take),
		"gen=" + strconv.Itoa(c.Gen), "who=" + orDash(c.Who), "on_route=" + orDash(c.Route), "on_model=" + orDash(c.Model),
		"on_tier=" + orDash(c.Tier), "end=" + orDash(strings.ReplaceAll(c.End, " ", "-")), "at=" + orDash(c.At),
	}
	pr.Fields[key] = strings.Join(w, " ") + " " + c.Usage.String()
}

// The release spend gate's store readout answers what the store's cards recorded over the
// window: every priced call of a paid provider whatever its end, nothing before the window,
// and a subscription friend's tokens; it is the reader the release library is handed.
func TestTheSpendStoreReadoutIsWhatTheCardsRecordedOverTheWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	r := snapshotSpend{s: spendSnapshot(now)}
	w := release.SpendWindowFrom(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), now)
	ctx := context.Background()

	providers, err := r.Providers(ctx, w)
	require.NoError(t, err)
	assert.Equal(t, []string{"openrouter"}, providers)
	usd, err := r.Spend(ctx, "openrouter", w)
	require.NoError(t, err)
	assert.InDelta(t, 836.0, usd, 1e-9, "$500 + $300 + $36 in the window, never the $50 before it")
	tokens, err := r.Tokens(ctx, w)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"alex": 1000}, tokens)
	assert.NotNil(t, release.SpendStore, "nova-sprint hands the release library its store reader")
}
