package sprintdash

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheMergeRowCarriesTheBaseGateAndTheDrift pins the merge row (docs/SPEC-SPRINT-DASHBOARD.md,
// "Merge"; the owner, 2026-10-04: "Is this progress visible in the sprint dashboard yet?"): the
// row where --json carries as merge_row and the page draws under the progress bar, with the
// base's gate (red naming the failing test, green, or not known) and the drift between the
// base and the development branch.
func TestTheMergeRowCarriesTheBaseGateAndTheDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }

	// red: an open base-red judgment names the failing test, whatever the lander last saw
	red := MergeRowOf(MergeFacts{
		Now: now, Merging: 4, Review: 9,
		Landed:       []time.Time{ago(45), ago(29), ago(10), ago(0), now.Add(time.Minute)},
		MergingRead:  true,
		MergingSince: []time.Time{ago(12), ago(41), {}},
		BaseRed:      []string{"the base sprint/mechanical-2026-10-02 fails its tree gate, refused 3 times, first refused at 11:02:00 UTC: --- FAIL: TestTheTreeGate (0.01s) in ./internal/ci"},
		LanderGreen:  true,
		BaseLacks:    5, DevLacks: 2,
		LastSync: ago(12), LastPromotion: ago(7),
	})
	assert.Equal(t, int64(4), red.Merging)
	assert.Equal(t, int64(9), red.Review)
	assert.Equal(t, int64(3), red.LandedPer30m, "the landings of the last 30 minutes, the edges in, none from the future")
	require.NotNil(t, red.OldestMergingMin)
	assert.Equal(t, 41, *red.OldestMergingMin, "the oldest merging card by its accepted stamp; an unreadable stamp is skipped")
	assert.Equal(t, GateRed, red.BaseGate)
	assert.Equal(t, "TestTheTreeGate", red.FailingTest)
	assert.Equal(t, 5, red.BaseLacks)
	assert.Equal(t, 2, red.DevLacks)
	require.NotNil(t, red.SyncMinutes)
	assert.Equal(t, 12, *red.SyncMinutes)
	require.NotNil(t, red.PromotionMinutes)
	assert.Equal(t, 7, *red.PromotionMinutes)
	assert.Equal(t, "merging 4 · review 9 · landed 3/30m · oldest 41m · base red TestTheTreeGate · base lacks 5, dev lacks 2 · sync 12m ago · promoted 7m ago", red.Line())

	// a finding naming no test is carried as the finding itself, cut to one short line
	plain := MergeRowOf(MergeFacts{Now: now, BaseRed: []string{"the base b fails its tree gate, refused 3 times, first refused at 11:02:00 UTC: go vet ./... exit 1"}})
	assert.Equal(t, GateRed, plain.BaseGate)
	assert.Equal(t, "go vet ./... exit 1", plain.FailingTest)

	// green: no base-red judgment, and the lander's tree gate passed at its last landing
	green := MergeRowOf(MergeFacts{Now: now, LanderGreen: true, DevLacks: 1})
	assert.Equal(t, GateGreen, green.BaseGate)
	assert.Empty(t, green.FailingTest)
	assert.Nil(t, green.OldestMergingMin, "the merging cards not read: not known, never 0")
	assert.Nil(t, green.SyncMinutes, "no sync recorded: not known")
	assert.Nil(t, green.PromotionMinutes, "no promotion recorded: not known")
	assert.Equal(t, "merging 0 · review 0 · landed 0/30m · oldest - · base green · base lacks 0, dev lacks 1 · sync - · promoted -", green.Line())

	// neither: the gate is not known, never green by default
	assert.Equal(t, GateUnknown, MergeRowOf(MergeFacts{Now: now}).BaseGate)
	// merging cards read and none merging: no oldest, still not 0
	assert.Nil(t, MergeRowOf(MergeFacts{Now: now, MergingRead: true}).OldestMergingMin)

	// the JSON is where --json's merge_row, the keys the page reads
	b, err := json.Marshal(red)
	require.NoError(t, err)
	var keys map[string]any
	require.NoError(t, json.Unmarshal(b, &keys))
	js := string(file("app.js"))
	for _, k := range []string{"merging", "review", "landed_per_30m", "oldest_merging_min", "base_gate", "failing_test", "base_lacks", "dev_lacks", "sync_minutes", "promotion_minutes"} {
		assert.Contains(t, keys, k, "merge_row carries %s", k)
		assert.Contains(t, js, "m."+k, "the page reads merge_row.%s", k)
	}
	assert.Len(t, keys, 10, "merge_row carries the spec's fields and nothing else: %v", keys)
	assert.Contains(t, js, "d.merge_row")
	assert.Contains(t, js, "renderMerge(d);")

	// the page: one panel titled "Merge" right under the progress bar, a cell per field, shown
	doc := parsePage(t, file("index.html"))
	panel := doc.one(t, "the merge row", byID("merge-row"))
	_, hidden := panel.attr["hidden"]
	assert.False(t, hidden, "the merge row shows by default")
	assert.Equal(t, "Merge", textOf(panel.one(t, "the merge row's title", func(n *node) bool { return n.name == "h2" })))
	for _, id := range []string{"mr-merging", "mr-review", "mr-landed", "mr-oldest", "mr-gate", "mr-drift", "mr-sync", "mr-promoted"} {
		panel.one(t, "the merge row's #"+id, byID(id))
	}

	// the spec says so, with the owner's line quoted after the lock
	sp := readSpec(t)
	sec := sp.section(t, "Merge")
	for _, s := range []string{"merge_row", "base_gate", "failing_test", "base lacks", "dev lacks", "landed per 30 minutes"} {
		assert.Contains(t, sec, s)
	}
	assert.Contains(t, sp.section(t, "LOCK 2"), "Is this progress visible in the sprint dashboard yet?")
	assert.Contains(t, sp.section(t, "Page"), "progress bar, Merge, Work")
}
