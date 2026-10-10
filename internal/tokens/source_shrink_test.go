package tokens

// The per-source shrink comparison: the day-total comparison cannot see one declared
// source's loss when another declared source rises by more than it. These tests are pure:
// every row is built in memory, and there is no file and no clock.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourceShrinkRow is one in-memory day row: a count per type and the sources cell that
// names the labels that wrote it.
func sourceShrinkRow(model, repo string, counts map[Type]int64, sources ...string) DayRow {
	var c Counts
	for t, v := range counts {
		c.Set(t, v)
	}
	return DayRow{Date: "2026-09-11", Model: model, Repo: repo, Counts: c, Basis: UTC, Sources: sources}
}

// sourceShrinkTotals is the day's per-type total over rows, the thing Shrinks compares.
func sourceShrinkTotals(rows []DayRow) Counts {
	var c Counts
	for _, r := range rows {
		c.Add(r.Counts)
	}
	return c
}

// One declared source drops its row while another rises by more: the day total RISES, so
// the day-total comparison sees nothing, and only the per-source comparison catches it.
func TestSourceShrinkOneDropsAnotherRisesMoreIsRefused(t *testing.T) {
	t.Parallel()

	old := []DayRow{
		sourceShrinkRow("a-model", "schema", map[Type]int64{Input: 10}, "swarm:a"),
		sourceShrinkRow("b-model", "schema", map[Type]int64{Input: 5}, "swarm:b"),
	}
	merged := []DayRow{
		sourceShrinkRow("b-model", "schema", map[Type]int64{Input: 20}, "swarm:b"),
	}
	declared := []string{"swarm:a", "swarm:b"}

	require.Emptyf(t, Shrinks(sourceShrinkTotals(old), sourceShrinkTotals(merged), "2026-09-11"),
		"the day total rose from 15 to 20, so the day-total comparison must see no shrink")
	got := SourceShrinks(old, merged, declared, "2026-09-11")
	require.Lenf(t, got, 1, "one source's row vanished and the other rose; got %v", got)
	assert.Equal(t, Shrink{Day: "2026-09-11", Source: "swarm:a", Type: Input, File: "10", Now: Dash}, got[0])
}

// Every declared source rising is not a shrink, whatever the amounts.
func TestSourceShrinkEverySourceRisingIsNoShrink(t *testing.T) {
	t.Parallel()

	old := []DayRow{
		sourceShrinkRow("a-model", "schema", map[Type]int64{Input: 10}, "swarm:a"),
		sourceShrinkRow("b-model", "schema", map[Type]int64{Input: 5}, "swarm:b"),
	}
	merged := []DayRow{
		sourceShrinkRow("a-model", "schema", map[Type]int64{Input: 20}, "swarm:a"),
		sourceShrinkRow("b-model", "schema", map[Type]int64{Input: 20}, "swarm:b"),
	}
	assert.Emptyf(t, SourceShrinks(old, merged, []string{"swarm:a", "swarm:b"}, "2026-09-11"),
		"every declared source rose; no source shrank")
}

// Retained rows never count toward a declared label: the declared source's own row vanishing
// is a shrink even when a retained row of another source carries a bigger number in the
// merged file. A comparison that summed the merged file rather than the rows naming the
// label would hide it.
func TestSourceShrinkRetainedRowsNeverCount(t *testing.T) {
	t.Parallel()

	old := []DayRow{
		sourceShrinkRow("mine", "schema", map[Type]int64{Input: 10}, "swarm:mine"),
		sourceShrinkRow("other", "schema", map[Type]int64{Input: 1000}, "swarm:other"),
	}
	merged := []DayRow{
		sourceShrinkRow("other", "schema", map[Type]int64{Input: 1000}, "swarm:other"),
	}
	got := SourceShrinks(old, merged, []string{"swarm:mine"}, "2026-09-11")
	require.Lenf(t, got, 1, "the declared source's row vanished; got %v", got)
	assert.Equal(t, Shrink{Day: "2026-09-11", Source: "swarm:mine", Type: Input, File: "10", Now: Dash}, got[0])
}

// A row whose sources cell names two declared labels counts toward each: when it falls, each
// of the two labels has a shrink of its own.
func TestSourceShrinkARowOfTwoDeclaredSourcesCountsForEach(t *testing.T) {
	t.Parallel()

	old := []DayRow{
		sourceShrinkRow("shared", "schema", map[Type]int64{Input: 10}, "swarm:a", "swarm:b"),
	}
	merged := []DayRow{
		sourceShrinkRow("shared", "schema", map[Type]int64{Input: 4}, "swarm:a", "swarm:b"),
	}
	got := SourceShrinks(old, merged, []string{"swarm:a", "swarm:b"}, "2026-09-11")
	want := []Shrink{
		{Day: "2026-09-11", Source: "swarm:a", Type: Input, File: "10", Now: "4"},
		{Day: "2026-09-11", Source: "swarm:b", Type: Input, File: "10", Now: "4"},
	}
	assert.Equal(t, want, got)
}

// A number becoming a dash is a shrink for the label that reported it.
func TestSourceShrinkANumberBecomingADashIsAShrink(t *testing.T) {
	t.Parallel()

	old := []DayRow{
		sourceShrinkRow("a-model", "schema", map[Type]int64{Input: 10, Reasoning: 40}, "swarm:a"),
	}
	merged := []DayRow{
		sourceShrinkRow("a-model", "schema", map[Type]int64{Input: 10}, "swarm:a"),
	}
	got := SourceShrinks(old, merged, []string{"swarm:a"}, "2026-09-11")
	require.Lenf(t, got, 1, "reasoning went from a number to a dash; got %v", got)
	assert.Equal(t, Shrink{Day: "2026-09-11", Source: "swarm:a", Type: Reasoning, File: "40", Now: Dash}, got[0])
}
