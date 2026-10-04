package sprintcohort

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cohortSnapshot() *sprint.Snapshot {
	return &sprint.Snapshot{Epoch: 7, Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet), Readers: sprint.NewTable(sprint.Readers), Merge: sprint.NewTable(sprint.Merge)}
}
func put(t *sprint.Table, id, row, col string, fields map[string]string) {
	if row != "" && !t.HasRow(row) {
		t.SetRows(append(t.Rows(), row))
	}
	t.Put(&sprint.Card{ID: id, Row: row, Col: col, Fields: fields})
}

func TestExactCohortKeepsFirstWorkReadAndProviderReturnsDistinct(t *testing.T) {
	t.Parallel()
	s := cohortSnapshot()
	put(s.Work, "p", "run", sprint.Review, map[string]string{"attempt": "2", "brief": "BASE: feature/base\n", "cost_record:p.w1#g1": "kind=work card=p.w1 attempt=1 take=1 gen=1 end=provider-failure at=2026-10-03T01:00:00Z", "cost_record:p.w1#g2": "kind=work card=p.w1 attempt=1 gen=2 end=failed at=2026-10-03T01:01:00Z"})
	put(s.Work, "p-other", "run2", sprint.Landed, map[string]string{})
	put(s.Work, "stop", "run", sprint.Landed, map[string]string{"kind": "sentinel"})
	// The first work record and broken read are kept but no longer placed.
	put(s.Fleet, "p.w1", "", "", map[string]string{"primary": "p", "attempt": "1", "ok": "no"})
	put(s.Fleet, "p.w2", "worker", sprint.DoneOK, map[string]string{"primary": "p", "attempt": "2", "ok": "yes"})
	put(s.Readers, "p.r1.a", "", "", map[string]string{"primary": "p", "verdict": "broken"})
	put(s.Readers, "p.r1.b", "b", sprint.OK, map[string]string{"primary": "p", "verdict": "ok"})
	put(s.Readers, "p.r2.a", "a", sprint.Asked, map[string]string{"primary": "p"})
	put(s.Readers, "p.r1.old", "", "", map[string]string{"primary": "p", "retired": "2026-10-03T01:02:00Z", "retired_by": "level"})
	v := Summarize(s, Selection{Stream: "run"})
	require.Len(t, v.Rows, 1)
	assert.Equal(t, []string{"stop"}, v.Excluded)
	assert.Equal(t, 1, v.Primaries)
	assert.Equal(t, Outcomes{Failed: 1}, v.FirstWork)
	assert.Equal(t, Outcomes{OK: 1, Failed: 1}, v.AllWork)
	assert.Equal(t, Outcomes{OK: 1, Broken: 1, Retired: 1}, v.FirstReads)
	assert.Equal(t, Outcomes{OK: 1, Broken: 1, Pending: 1, Retired: 1}, v.Reads)
	assert.Equal(t, 2, v.RecordedWorkTakes)
	assert.Equal(t, 1, v.ProviderReturns)
	assert.Equal(t, 1, v.RetryCards)
	assert.Equal(t, 1, v.RetryAttempts)
	assert.Equal(t, "feature/base", v.Rows[0].LandingTarget)
	assert.Nil(t, v.Rows[0].DevLanded)
}

func TestCostTotalsSurviveCutHistoryWithoutClaimingAnInvoice(t *testing.T) {
	t.Parallel()
	s := cohortSnapshot()
	put(s.Work, "p", "run", sprint.Working, map[string]string{"cost_total": "records=70 charged_usd=1.234 actual_usd=0.25 actual_by=harness actual_of=1 predicted_usd=0.984 predicted_of=68 charged_of=69", "cost_cut": "6"})
	v := Summarize(s, Selection{IDs: []string{"p"}})
	assert.Equal(t, "1.234", v.Cost.Charged)
	assert.Equal(t, 70, v.Cost.Records)
	assert.Equal(t, 69, v.Cost.ChargedOf)
	assert.Equal(t, []string{"harness"}, v.Cost.ActualBy)
	assert.Equal(t, 6, v.Cost.Cut)
	assert.False(t, v.Cost.CompleteInvoice)
	assert.Equal(t, "unknown", v.Cost.InflightSpend)
	assert.Empty(t, v.Rows[0].Cost.Consumers)
}

func TestDevLandingRequiresMatchingReviewedReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		field    string
		value    string
		verified bool
	}{
		{"verified", "", "", true}, {"no review", "dev_review", "", false}, {"wrong branch", "dev_branch", "feature", false}, {"stale attempt", "dev_attempt", "1", false}, {"stale head", "dev_head", strings.Repeat("b", 40), false}, {"short tip", "dev_tip", "abcdef123456", false}, {"bad timestamp", "dev_verified_at", "today", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := cohortSnapshot()
			fields := map[string]string{"attempt": "2", "head": strings.Repeat("a", 40), "dev_head": strings.Repeat("a", 40), "dev_tip": strings.Repeat("c", 40), "dev_attempt": "2", "dev_branch": "dev", "dev_repo": "example/repo", "dev_review": "review receipt", "dev_verified_at": "2026-10-03T12:00:00Z", "staged_base": "feature/base"}
			if tc.field != "" {
				fields[tc.field] = tc.value
			}
			put(s.Work, "p", "run", sprint.Merging, fields)
			v := Summarize(s, Selection{IDs: []string{"p"}})
			assert.Equal(t, 1, v.BranchStaged)
			if tc.verified {
				assert.Equal(t, 1, v.DevLandedVerified)
				require.NotNil(t, v.Rows[0].DevLanded)
				assert.True(t, *v.Rows[0].DevLanded)
			} else {
				assert.Equal(t, 1, v.DevLandingUnknown)
				assert.Nil(t, v.Rows[0].DevLanded)
			}
		})
	}
	s := cohortSnapshot()
	put(s.Work, "legacy", "run", sprint.Landed, map[string]string{})
	v := Summarize(s, Selection{IDs: []string{"legacy"}})
	assert.Equal(t, 1, v.BranchStaged)
	assert.Equal(t, 1, v.DevLandingUnknown)
	assert.Nil(t, v.Rows[0].DevLanded)
}

func TestSelectionAndHistoricalRecordDiscoveryAreExact(t *testing.T) {
	t.Parallel()
	for _, q := range []Selection{{}, {Stream: "run", IDs: []string{"p"}}, {IDs: []string{"p", "p"}}, {IDs: []string{"p.w1"}}} {
		assert.Error(t, q.Validate())
	}
	s := cohortSnapshot()
	put(s.Work, "p", "run", sprint.Working, map[string]string{"attempt": "2", "cost_record:p.r1.retired#v": "kind=read card=p.r1.retired attempt=1 end=broken at=2026-10-03T01:00:00Z"})
	s.Readers.SetRows([]string{"current"})
	q := Selection{IDs: []string{"p", "missing"}}
	require.NoError(t, q.Validate())
	_, missing, _ := Primaries(s, q)
	assert.Equal(t, []string{"missing"}, missing)
	records := Records(q)(s)
	assert.Equal(t, []string{"p.w1", "p.w2"}, records[sprint.Fleet])
	assert.Equal(t, []string{"p.r1.current", "p.r1.retired", "p.r2.current"}, records[sprint.Readers])
	assert.Equal(t, []string{"p"}, records[sprint.Merge])
}
