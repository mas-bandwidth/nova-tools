package sprint

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCostReconcileRaisesJudgmentOverThreshold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Fleet: NewTable(Fleet),
		Work:  NewTable(Work),
		Routes: []Route{
			{Name: "flash-or", Provider: "openrouter", Model: "anthropic/claude-3.5-haiku"},
		},
	}
	s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: "landed", Fields: map[string]string{}}
	book(pr,
		Consumer{Kind: "work", Card: "s1-1.w1", Route: "flash-or", Model: "anthropic/claude-3.5-haiku", Usage: cardcost.ParseUsage("input=100 actual_usd=10.0")},
	)
	s.Work.Put(pr)

	// Case 1: Provider usage $12.00 vs internal $10.00 -> gap $2.00 (16.7% > 5%)
	p := CostReconcile(s, CostReconcileReq{
		Provider:      "openrouter",
		ProviderUsage: 12.00,
	})
	require.Len(t, p.Notes, 1, "gap > 5% raises ONE judgment")
	assert.Equal(t, Judgment, p.Notes[0].Kind)
	assert.Equal(t, NCostGap, p.Notes[0].Type)
	assert.True(t, p.Notes[0].SprintLevel)
	assert.Contains(t, p.Notes[0].What, "provider openrouter cost reconciliation gap")
	require.Len(t, p.Props, 1)
	assert.Equal(t, Fleet, p.Props[0].Table)
	assert.Equal(t, "cost_reconcile_openrouter", p.Props[0].Name)

	// Case 2: Judgment already in s.Open -> raises no duplicate judgment
	s.Open = []Open{{
		Key:  OpenKey("j1", SprintSubject),
		Note: Note{ID: "j1", Kind: Judgment, Type: NCostGap, Primaries: []string{"openrouter"}},
	}}
	p2 := CostReconcile(s, CostReconcileReq{
		Provider:      "openrouter",
		ProviderUsage: 12.00,
	})
	assert.Empty(t, p2.Notes, "an existing judgment prevents duplicate notes")

	// Case 3: Within 5% threshold ($10.20 vs $10.00 -> 1.96% < 5%)
	s.Open = nil
	p3 := CostReconcile(s, CostReconcileReq{
		Provider:      "openrouter",
		ProviderUsage: 10.20,
	})
	assert.Empty(t, p3.Notes, "gap <= 5% raises no judgment")

	// Case 4: Custom threshold 20% ($11.50 vs $10.00 -> 13% gap < 20%)
	p4 := CostReconcile(s, CostReconcileReq{
		Provider:      "openrouter",
		ProviderUsage: 11.50,
		Threshold:     0.20,
	})
	assert.Empty(t, p4.Notes, "custom threshold 20% is respected")
}

func TestReadProviderUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Unknown provider returns false, nil
	usage, ok, err := ReadProviderUsage(ctx, nil, "opencode", func(string) string { return "" })
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 0.0, usage)

	// Missing API key returns error
	_, _, err = ReadProviderUsage(ctx, nil, "openrouter", func(string) string { return "" })
	assert.Error(t, err)

	// Successful response with usage_daily
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, OpenRouterKeyURL, req.URL.String())
		assert.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
		body := `{"data": {"usage_daily": 15.5, "usage": 120.0}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	usage, ok, err = ReadProviderUsage(ctx, rt, "openrouter", func(k string) string { return "test-key" })
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 15.5, usage)

	// Fallback to usage when usage_daily is missing
	rtFallback := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"data": {"usage": 42.25}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	usage, ok, err = ReadProviderUsage(ctx, rtFallback, "openrouter", func(k string) string { return "test-key" })
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 42.25, usage)
}

func TestUnreconciledSpend(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Fleet: NewTable(Fleet),
	}
	s.Fleet.SetProp("cost_reconcile_openrouter", `{"provider":"openrouter","provider_usage":15.5,"internal":10.0,"gap":5.5,"pct_gap":0.35}`)
	unrec := UnreconciledSpend(s)
	assert.InDelta(t, 5.5, unrec, 0.001)
}

func TestStreamTierCostsTotalAndUnreconciled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:   now,
		Fleet: NewTable(Fleet),
		Work:  NewTable(Work),
	}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetProp("cost_reconcile_openrouter", `{"provider":"openrouter","provider_usage":12.5,"internal":10.0,"gap":2.5}`)

	c1 := &Card{ID: "s1-1", Row: "s1", Col: "landed", Fields: map[string]string{FieldCost: "5.00"}}
	c2 := &Card{ID: "s1-2", Row: "s1", Col: "working", Fields: map[string]string{FieldCostTotal: "3.50"}}
	s.Work.Put(c1)
	s.Work.Put(c2)

	costs := StreamTierCosts(s)
	s1Costs, ok := costs["s1"]
	require.True(t, ok)
	assert.Equal(t, "$8.50", s1Costs.TotalCost, "total cost includes landed ($5.00) and working ($3.50)")
	assert.Equal(t, "$2.50", s1Costs.Unreconciled, "unreconciled gap from provider is reflected")
}
