package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func TestCompleteCostTotalsUseLatestPrimaryAggregateIncludingDroppedCards(t *testing.T) {
	t.Parallel()
	lines := []sprint.Line{
		{Kind: sprint.LineMove, Table: sprint.Work, Card: "s1-1", Primary: "s1-1", Stream: "s1", Set: map[string]string{sprint.FieldCostTotal: "records=1 actual_usd=0.4 charged_usd=0.4 actual_of=1 charged_of=1"}},
		// A later total replaces the earlier snapshot; it is never added twice.
		{Kind: sprint.LineMove, Table: sprint.Work, Card: "s1-1", Primary: "s1-1", Stream: "s1", Set: map[string]string{sprint.FieldCostTotal: "records=70 actual_usd=0.5 charged_usd=0.5 actual_of=70 charged_of=70"}},
		{Kind: sprint.LineMove, Table: sprint.Work, Card: "s1-2", Primary: "s1-2", Stream: "s1", Set: map[string]string{sprint.FieldCostTotal: "records=1"}},
		{Kind: sprint.LineMove, Table: sprint.Work, Card: "s2-1", Primary: "s2-1", Stream: "s2", Set: map[string]string{sprint.FieldCostTotal: "records=1 predicted_usd=0.2 charged_usd=0.2 predicted_of=1 charged_of=1"}},
		// Dropping the primary removes it from the table but does not erase its last cost_total.
		{Kind: sprint.LineMove, Table: sprint.Work, Card: "s2-1", Primary: "s2-1", Stream: "s2", Removed: true},
	}
	got := costTotalsFromLines(lines)
	require.Equal(t, "0.7", got.ChargedUSD)
	assert.Equal(t, 72, got.Records)
	assert.Equal(t, 71, got.ChargedRecords)
	assert.Equal(t, 1, got.UnreconciledRecords())
	assert.Equal(t, "0.5", got.Streams["s1"].ChargedUSD)
	assert.Equal(t, 71, got.Streams["s1"].Records)
	assert.Equal(t, "0.2", got.Streams["s2"].ChargedUSD)
}

type costTotalsLogProbe struct {
	Backend
	after *[]string
	fail  *bool
}

func (p *costTotalsLogProbe) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	*p.after = append(*p.after, after)
	if *p.fail {
		*p.fail = false
		return nil, nil, errors.New("injected log read failure")
	}
	return p.Backend.LogSince(ctx, after, max)
}

func (p *costTotalsLogProbe) AtEpoch(epoch uint64, old bool) Backend {
	return &costTotalsLogProbe{Backend: p.Backend.AtEpoch(epoch, old), after: p.after, fail: p.fail}
}

func TestCostTotalsCacheUsesOneIncrementalCursorAcrossFreshStoreReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	after := []string{}
	fail := false
	probe := &costTotalsLogProbe{Backend: h.m, after: &after, fail: &fail}
	h.st.B = probe
	cache := &CostTotalsCache{}

	firstStore, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, firstStore)
	require.NoError(t, err)
	require.Len(t, after, 1, "a small epoch log fits in one initial page")
	firstTail := cache.after
	assert.NotEmpty(t, firstTail)

	// where creates a new Store on each read. The app-owned cache resumes after the
	// same log tail instead of rescanning all historical lines.
	secondStore, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, secondStore)
	require.NoError(t, err)
	require.Len(t, after, 1, "an unchanged tail needs no second log query")

	*probe.fail = true
	_, err = h.st.Run(h.ctx, AddStep(sprint.AddReq{Brief: proBrief, Stream: "s1", Count: 1}))
	require.NoError(t, err)
	thirdStore, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, thirdStore)
	require.ErrorContains(t, err, "injected log read failure")
	assert.Equal(t, firstTail, cache.after, "failed reads do not advance the saved cursor")
	_, err = cache.Read(h.ctx, thirdStore)
	require.NoError(t, err, "the same tail is retried after a failed read")
	assert.NotEqual(t, firstTail, cache.after, "the successful retry advances through appended lines")
	require.Len(t, after, 3)
	assert.Equal(t, firstTail, after[1])
	assert.Equal(t, firstTail, after[2])
	newTail := cache.after

	fourthStore, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, fourthStore)
	require.NoError(t, err)
	require.Len(t, after, 3, "a fresh Store reuses the app cache without a full rescan")

	// A new backend object and a cleared epoch each get an independent cold cursor.
	otherAfter := []string{}
	otherFail := false
	other := &costTotalsLogProbe{Backend: h.m, after: &otherAfter, fail: &otherFail}
	h.st.B = other
	otherStore, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, otherStore)
	require.NoError(t, err)
	require.Len(t, otherAfter, 1)
	assert.Empty(t, otherAfter[0], "backend identity changes reset the cursor")
	_, err = h.st.Clear(h.ctx)
	require.NoError(t, err)
	newEpoch, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	_, err = cache.Read(h.ctx, newEpoch)
	require.NoError(t, err)
	require.Len(t, otherAfter, 2)
	assert.Empty(t, otherAfter[1], "a new epoch starts a cold cursor after tail %s", newTail)
}
