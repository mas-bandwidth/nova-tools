package sprintdash

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheMergeRowCarriesTheBaseGateAndTheDrift pins the merge row
// (docs/SPEC-SPRINT-DASHBOARD.md, "Merge row"): where --json's merge_row, drawn
// on the page, carries the base gate (green or red, and the failing test) and
// the drift between the base and the development branch.
func TestTheMergeRowCarriesTheBaseGateAndTheDrift(t *testing.T) {
	t.Parallel()

	oldest, syncMin, promoMin := 41, 12, 7
	raw := []byte(`{"merging":4,"review":9,"landed_per_30m":3,"oldest_merging_min":41,"base_gate":"red","failing_test":"TestTheTreeGate","base_lacks":5,"dev_lacks":2,"sync_minutes":12,"promotion_minutes":7}`)
	var row MergeRow
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.Equal(t, MergeRow{
		Merging: 4, Review: 9, LandedPer30m: 3, OldestMergingMin: &oldest,
		BaseGate: "red", FailingTest: "TestTheTreeGate", BaseLacks: 5, DevLacks: 2,
		SyncMinutes: &syncMin, PromotionMinutes: &promoMin,
	}, row)
	assert.Equal(t, "merging 4 · review 9 · landed 3/30m · oldest 41m · base gate red · failing test TestTheTreeGate · base lacks 5, dev lacks 2 · sync 12m · promotion 7m", row.Line())

	green := MergeRow{BaseGate: "green", DevLacks: 1}
	assert.Equal(t, "merging 0 · review 0 · landed 0/30m · oldest - · base gate green · failing test - · base lacks 0, dev lacks 1 · sync - · promotion -", green.Line())

	doc := parsePage(t, file("index.html"))
	panel := doc.one(t, "a merge row", byID("merge-row"))
	_, hidden := panel.attr["hidden"]
	assert.False(t, hidden, "the merge row is on the page; the readers panel stays the one at ?all=1")
	assert.Equal(t, "Merge row", textOf(panel.one(t, "the merge row's title", func(n *node) bool { return n.name == "h2" })))
	for _, id := range []string{"mr-merging", "mr-review", "mr-landed", "mr-oldest", "mr-gate", "mr-failing", "mr-drift", "mr-sync", "mr-promo"} {
		assert.NotNil(t, panel.find(byID(id)), "the merge row has #%s", id)
		require.NotEmpty(t, panel.find(byID(id)), "the merge row has #%s", id)
	}

	js := string(file("app.js"))
	for _, key := range []string{"d.merge_row", "base_gate", "failing_test", "base_lacks", "dev_lacks", "landed_per_30m", "oldest_merging_min", "sync_minutes", "promotion_minutes", "function renderMergeRow", "renderMergeRow(d)"} {
		assert.Contains(t, js, key)
	}

	sec := readSpec(t).section(t, "Merge row")
	assert.Contains(t, sec, "Is this progress visible in the sprint dashboard yet?")
	assert.Contains(t, sec, "base gate")
	assert.Contains(t, sec, "failing test")
	assert.Contains(t, sec, "base lacks")
}
