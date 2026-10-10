package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// where --json carries landedSeries (cards landed per 10-minute bucket over 24 hours,
// split between friends and fleet).
func TestWhereCarriesLandedSeries(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})

	out := ta.ok("where --json")
	var doc struct {
		LandedSeries *sprint.LandedSeries `json:"landedSeries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	require.NotNil(t, doc.LandedSeries)
	assert.Equal(t, 144, doc.LandedSeries.Buckets)
	assert.Equal(t, 600, doc.LandedSeries.BucketSeconds)
	assert.Equal(t, 2, doc.LandedSeries.Totals.Fleet+doc.LandedSeries.Totals.Friends+doc.LandedSeries.Totals.Unknown)
}

// The landed series where --json carries once the tick has counted the where record is
// the record's landings (sprint.SeriesLandings, from the tables), bucketed at where's time,
// and it is the series the log gives (sprint.LandedSeriesFrom): the same landings, at the
// same times, to the same rows. where reads no log line for it.
func TestTheRecordsLandedSeriesIsTheLogs(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	// a sentinel released waiting -> landed carries a landed stamp but is no landing of
	// work: the log's series skips it, so the record's must (7 of them on the live store,
	// 2026-10-10, counted as unknown)
	ta.ok("add --stream s2 --sentinel gate")
	ta.ok("release gate --reason 'nothing waits on it'")
	require.Equal(t, sprint.Landed, ta.primary("gate").Col)
	ta.ok("tick")

	ctx := context.Background()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	now := ta.a.now()
	fromLog, err := sprint.LandedSeriesFrom(ctx, st, now)
	require.NoError(t, err)
	require.Equal(t, 2, fromLog.Totals.Fleet, "both cards landed, worked on m1: %+v", fromLog.Totals)
	pinned, err := st.Pinned(ctx)
	require.NoError(t, err)
	facts, err := pinned.WhereFacts(ctx, 0)
	require.NoError(t, err)
	require.True(t, facts.SeriesCounted, "the tick counted the series into the where record")
	assert.Equal(t, fromLog, sprint.LandedSeriesOfLandings(facts.Series, now), "the record's series is the log's")

	lines := ta.m.LogLines
	out := ta.ok("where --json")
	assert.Equal(t, lines, ta.m.LogLines, "where --json read the log for its series")
	var doc struct {
		LandedSeries *sprint.LandedSeries `json:"landedSeries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	require.NotNil(t, doc.LandedSeries)
	assert.Equal(t, fromLog, *doc.LandedSeries, "where --json's series is the log's")
}

func TestWhereHelpNamesLandedSeries(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	assert.Contains(t, ta.ok("help where"), "landedSeries")
}

// where --json --cards --redis <addr> is the dashboard's call (dashboard.go): the series
// comes from the store --redis names and the epoch --at-epoch names, whatever flag of
// where comes before them.
func TestWhereLandedSeriesReadsTheNamedStoreAndEpoch(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	// mem:B is the sprint with the landings; the env's store (mem:0) is an empty one
	landings := ta.m
	ta.a.backend = func(_ context.Context, addr string, _ sprint.Names) (store.Backend, error) {
		if addr == "mem:B" {
			return landings, nil
		}
		return ta.m, nil
	}
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	ta.m = store.NewMem()
	ta.ok("init --readers reader-a,reader-b --members m1:8")

	landed := func(line string) sprint.SeriesTotals {
		t.Helper()
		code, out, errs := ta.do(line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
		assert.NotContains(t, errs, "landedSeries omitted", line)
		var doc struct {
			LandedSeries *sprint.LandedSeries `json:"landedSeries"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &doc), "%s: %s", line, out)
		require.NotNil(t, doc.LandedSeries, "%s: no landedSeries", line)
		return doc.LandedSeries.Totals
	}
	for _, line := range []string{
		"where --json --cards --redis mem:B",
		"where --json --rows --redis mem:B",
		"where --redis mem:B --json --cards",
		"where --json --all --stale 1m --cards --rows --redis=mem:B",
	} {
		got := landed(line)
		assert.Equal(t, 2, got.Fleet+got.Friends+got.Unknown, "%s: the series is not mem:B's", line)
	}
	got := landed("where --json --cards")
	assert.Equal(t, 0, got.Fleet+got.Friends+got.Unknown, "where --json --cards: the env's store has no landings")

	// a clear on mem:B: epoch 1 is empty, --at-epoch 0 is the sprint that landed
	ta.ok("clear --redis mem:B --confirm sprint")
	got = landed("where --json --cards --redis mem:B")
	assert.Equal(t, 0, got.Fleet+got.Friends+got.Unknown, "the new epoch has no landings")
	got = landed("where --json --cards --at-epoch 0 --redis mem:B")
	assert.Equal(t, 2, got.Fleet+got.Friends+got.Unknown, "--at-epoch 0 after --cards: not epoch 0's series")
}

// The wrapper's flag set is where's: an unknown flag with --json is refused in the
// same words where itself refuses it, so a flag added to where and not to
// whereSeriesFlags fails here instead of dropping --redis or --at-epoch after it.
func TestWhereSeriesFlagsAreWheresFlags(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, _, plain := ta.do("where --zz")
	require.Equal(t, 2, code)
	code, _, withJSON := ta.do("where --json --zz")
	require.Equal(t, 2, code)
	assert.Equal(t, plain, withJSON)
}
