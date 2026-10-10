package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheHeadlineCountsFromTheTidy asserts that after a tidy, the headline counts
// start from it (0 of 10 since the tidy) with the archive still readable.
func TestTheHeadlineCountsFromTheTidy(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("start")

	// 20 cards on stream s1
	ta.ok("add --stream s1 --count 20")

	// Move 10 cards to landed
	ta.deal(10)
	ta.ok("take --as m1 --limit 10")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 10)
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@1")
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 10")
	ta.ok("read --as reader-b --ok --limit 10")
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 10")
	ta.ok("tick")

	// Before tidy: headline reads 10/20 (50.0%)
	var before whereView
	ta.json("where", &before)
	require.Equal(t, int64(10), before.Landed)
	require.Equal(t, int64(20), before.All)
	require.Nil(t, before.StatsSince)
	require.Contains(t, before.Summary, "10/20 50.0%")

	// Tidy the store
	out := ta.ok("stats tidy --all --reason 'tidy test'")
	require.Contains(t, out, "STATS-TIDY OK")

	// After tidy: headline reads 0 of 10 since the tidy
	var after whereView
	ta.json("where", &after)
	assert.Equal(t, int64(0), after.Landed)
	assert.Equal(t, int64(10), after.All)
	require.NotNil(t, after.StatsSince)
	assert.False(t, after.StatsSince.IsZero())
	assert.Contains(t, after.Summary, fmt.Sprintf("0/10 0.0%% since %s", after.StatsSince.UTC().Format(time.RFC3339)))

	// Text frame also says "since <time>" beside the numbers
	whereText := ta.ok("where")
	assert.Contains(t, whereText, fmt.Sprintf("0/10 0.0%% since %s", after.StatsSince.UTC().Format(time.RFC3339)))

	// View coordinator also reflects the tidy counts
	coordText := ta.ok("view coordinator")
	assert.Contains(t, coordText, "landed 0/10")
	assert.Contains(t, coordText, "stats_since="+after.StatsSince.UTC().Format(time.RFC3339))

	// Archive is still readable in the store
	var archiveKey string
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "archive=") {
			archiveKey = strings.TrimPrefix(f, "archive=")
			break
		}
	}
	require.NotEmpty(t, archiveKey)
	ctx := context.Background()
	raw, ok, err := ta.m.GetKey(ctx, archiveKey)
	require.NoError(t, err)
	require.True(t, ok, "archive key %s should exist", archiveKey)
	var arch store.StatsArchive
	require.NoError(t, json.Unmarshal([]byte(raw), &arch))
	assert.Equal(t, store.ArchiveDone, arch.State)
	assert.Equal(t, "tidy test", arch.Reason)
}
