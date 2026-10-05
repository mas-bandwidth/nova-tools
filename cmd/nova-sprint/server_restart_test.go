package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A server restart keeps every in-flight read whose lease is live, and only
// takes back reads whose lease has lapsed (tla/ServerLanes.tla, Restart;
// LiveLeaseNeverTakenBack, EveryLapsedReadTakenBack; docs/SPEC-SPRINT.md section 6).
func TestAServerRestartKeepsReadsWithLiveLeases(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask")
	ta.ok("read --as reader-a --begin s1-1.r1.reader-a")

	// Verify read card is in flight in Reading column on reader-a
	var q struct {
		Cards []queueCard
	}
	ta.json("queue --as reader-a", &q)
	require.Len(t, q.Cards, 1)
	assert.Equal(t, "s1-1.r1.reader-a", q.Cards[0].ID)
	assert.Equal(t, string(sprint.Reading), q.Cards[0].Col)

	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)

	// 1. Live lease survives restart:
	// Advance 5 minutes (within 10m DefaultReadLease)
	ta.a.sleep(5 * time.Minute)

	// Server start before first tick
	require.NoError(t, ta.a.serverStart(context.Background(), st))

	// Read is still in flight in Reading on reader-a
	ta.json("queue --as reader-a", &q)
	require.Len(t, q.Cards, 1)
	assert.Equal(t, "s1-1.r1.reader-a", q.Cards[0].ID)
	assert.Equal(t, string(sprint.Reading), q.Cards[0].Col)

	// 2. Lapsed lease is taken back on restart:
	// Advance past 10 minutes (6 more minutes = 11 minutes total from begun)
	ta.a.sleep(6 * time.Minute)

	// Server start before first tick
	require.NoError(t, ta.a.serverStart(context.Background(), st))

	// In-flight read whose lease lapsed was taken back: no longer in Reading
	ta.json("queue --as reader-a", &q)
	for _, c := range q.Cards {
		assert.NotEqual(t, string(sprint.Reading), c.Col, "no read remains in reading after lease lapsed")
	}

	// Verify the card was retired with retired_by: lapsed
	readingCards, err := st.ReadCells(context.Background(), sprint.Readers, "reader-a", sprint.Reading)
	require.NoError(t, err)
	assert.Empty(t, readingCards, "reading cell is empty on reader-a")

	recs, err := st.Records(context.Background(), sprint.Readers, []string{"s1-1.r1.reader-a"})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.NotNil(t, recs[0])
	assert.Equal(t, sprint.RetiredByLapsed, recs[0].F("retired_by"))

	// The primary can be re-asked now that the lapsed read was taken back
	ta.ok("ask")
	assert.Contains(t, ta.askedOf("reader-b"), "s1-1.r1.reader-b", "primary was re-asked of reader-b")
}
