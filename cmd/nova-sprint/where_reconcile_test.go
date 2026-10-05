package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where and view coordinator show each provider's latest cost reconciliation
// (where_reconcile.go; release-check-spend-reconciledb.w1): its count of the day beside the
// sprint's records, the gap, and whether it passes the bound the release's spend gate
// refuses at; a provider that could not be read is said unknown with why. Nothing shows
// before the first reconciliation.
func TestWhereAndViewCoordinatorShowTheLatestReconcilePerProvider(t *testing.T) {
	t.Parallel()
	ta, fake, _ := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true})
	day := ta.a.now().UTC().Format("2006-01-02")
	assert.NotContains(t, ta.ok("where"), "COST RECONCILE", "nothing reconciled, nothing shown")
	assert.NotContains(t, ta.ok("where --json"), `"reconciles"`)

	fake.body = `{"data":{"usage_daily":12.5}}`
	ta.ok("cost reconcile")
	want := "COST RECONCILE opencode unknown: opencode Zen publishes no balance endpoint"
	text := ta.ok("where")
	assert.Contains(t, text, want)
	assert.Contains(t, text, "openrouter day="+day+" provider=$12.50 records=$0.00 gap=$12.50 (100.0%) OVER")

	var w struct {
		Reconciles []sprint.ReconcileRow `json:"reconciles"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &w))
	require.Len(t, w.Reconciles, 2)
	assert.Equal(t, "opencode", w.Reconciles[0].Provider)
	assert.False(t, w.Reconciles[0].Known)
	or := w.Reconciles[1]
	assert.Equal(t, "openrouter", or.Provider)
	assert.True(t, or.Known)
	assert.Equal(t, day, or.Day)
	assert.InDelta(t, 12.5, or.Used, 1e-9)
	assert.InDelta(t, 12.5, or.Gap, 1e-9)
	assert.True(t, or.Over)

	var c struct {
		Reconciles []sprint.ReconcileRow `json:"reconciles"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("view coordinator --json")), &c))
	assert.Equal(t, w.Reconciles, c.Reconciles, "the coordinator's view carries the same rows")
	assert.Contains(t, ta.ok("view coordinator"), "openrouter day="+day+" provider=$12.50 records=$0.00")

	// a read back within the bound is no longer over
	fake.body = `{"data":{"usage_daily":0}}`
	ta.ok("cost reconcile")
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &w))
	assert.False(t, w.Reconciles[1].Over)
	assert.NotContains(t, ta.ok("where"), "OVER")
}

// where --json --spend-since --spend-until carries the store's records of the window
// (spend_window), what nova-update's release cut reads; the flags come together, with
// --json, and a window that ends before it starts is refused.
func TestWhereCarriesTheStoresSpendOfAWindow(t *testing.T) {
	t.Parallel()
	ta, _, _ := balanceApp(t, sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true})
	var w struct {
		SpendWindow *struct {
			From      string                        `json:"from"`
			To        string                        `json:"to"`
			Providers map[string]sprint.WindowSpend `json:"providers"`
		} `json:"spend_window"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --spend-since 2030-01-01 --spend-until 2030-01-03T00:00:00Z")), &w))
	require.NotNil(t, w.SpendWindow)
	assert.Equal(t, "2030-01-01T00:00:00Z", w.SpendWindow.From)
	assert.Equal(t, "2030-01-03T00:00:00Z", w.SpendWindow.To)
	assert.NotNil(t, w.SpendWindow.Providers)
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &w))

	for line, want := range map[string]string{
		"where --spend-since 2030-01-01 --spend-until 2030-01-02":        "a field of the JSON view",
		"where --json --spend-since 2030-01-01":                          "given together",
		"where --json --spend-since 2030-01-03 --spend-until 2030-01-02": "ends before it starts",
		"where --json --spend-since yesterday --spend-until 2030-01-02":  "--spend-since yesterday",
	} {
		code, _, errs := ta.do(line)
		assert.NotZero(t, code, line)
		assert.Contains(t, errs, want, line)
	}
}
