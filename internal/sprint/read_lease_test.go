package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server restart keeps every in-flight read whose lease is live, and only
// takes back reads whose lease has lapsed (tla/ServerLanes.tla, Restart;
// LiveLeaseNeverTakenBack, EveryLapsedReadTakenBack).
// A read asked of a reader is held by a lease, started by read --begin
// (begun + DefaultReadLease) and renewed by reader beat (lease + DefaultReadLease).
func TestAServerRestartResumesReadsInFlight(t *testing.T) {
	t.Parallel()
	now := t0
	w := newWorld(t, "reader-a", "reader-b")
	w.s.Now = now
	w.s.Work.SetRows([]string{"s1"})
	w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp}

	p := "s1-1"
	w.s.Work.Put(&Card{
		ID:     p,
		Row:    "s1",
		Col:    Review,
		Rev:    1,
		Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"},
	})
	rcID := ReadCardID(p, 1, "reader-a")
	rc := &Card{
		ID:     rcID,
		Row:    "reader-a",
		Col:    Reading,
		Rev:    1,
		Fields: map[string]string{"kind": "read", "primary": p, "stream": "s1", "reader": "reader-a", "attempt": "1", "begun": stamp(now)},
	}
	w.s.Readers.Put(rc)

	// 1. Restart within 10 minutes: lease is live, read is kept in flight.
	w.s.Now = now.Add(5 * time.Minute)
	plan := RestartReads(w.s)
	assert.Empty(t, plan.Units, "in-flight read with live lease is kept on restart")
	assert.True(t, w.s.Readers.Card(rcID).Placed(), "read is placed")
	assert.Equal(t, Reading, w.s.Readers.Card(rcID).Col, "read remains in reading")

	// ServerRestart is an alias for RestartReads
	aliasPlan := ServerRestart(w.s)
	assert.Empty(t, aliasPlan.Units, "ServerRestart keeps live read")

	// 2. Restart past 10 minutes: lease lapsed, read is taken back and retired with retired_by: lapsed.
	w.s.Now = now.Add(11 * time.Minute)
	plan = RestartReads(w.s)
	require.Len(t, plan.Units, 1, "lapsed read is taken back on restart")
	assert.Contains(t, plan.Units[0].Moved, "taken back (lease lapsed)")
	w.must(plan)

	retired := w.s.Readers.Card(rcID)
	require.NotNil(t, retired)
	assert.False(t, retired.Placed(), "lapsed read is retired")
	assert.Equal(t, RetiredByLapsed, retired.F("retired_by"), "retired_by is lapsed")

	// After lapsed read is taken back, ask can re-ask the primary
	askPlan := Ask(w.s, AskReq{})
	require.NotEmpty(t, askPlan.Units, "primary can be asked of a reader up")
	w.must(askPlan)

	// 3. Reader beat renews the lease: lease is extended by DefaultReadLease.
	now2 := now.Add(20 * time.Minute)
	w2 := newWorld(t, "reader-a", "reader-b")
	w2.s.Now = now2
	w2.s.Work.SetRows([]string{"s1"})
	w2.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp}

	p2 := "s1-2"
	w2.s.Work.Put(&Card{
		ID:     p2,
		Row:    "s1",
		Col:    Review,
		Rev:    1,
		Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1"},
	})
	rc2ID := ReadCardID(p2, 1, "reader-a")
	rc2 := &Card{
		ID:     rc2ID,
		Row:    "reader-a",
		Col:    Reading,
		Rev:    1,
		Fields: map[string]string{"kind": "read", "primary": p2, "stream": "s1", "reader": "reader-a", "attempt": "1", "begun": stamp(now2)},
	}
	w2.s.Readers.Put(rc2)

	// At 8 minutes, reader beats and renews lease
	w2.s.Now = now2.Add(8 * time.Minute)
	renewPlan := RenewReaderLeases(w2.s, "reader-a")
	require.Len(t, renewPlan.Units, 1, "renew lease on reader beat")
	assert.Contains(t, renewPlan.Units[0].Moved, "lease renewed")
	w2.must(renewPlan)

	// At 15 minutes (5 minutes past original begun+10m, but within 8m+10m=18m renewed lease):
	w2.s.Now = now2.Add(15 * time.Minute)
	plan2 := RestartReads(w2.s)
	assert.Empty(t, plan2.Units, "renewed lease keeps read in flight across restart at 15m")
	assert.True(t, w2.s.Readers.Card(rc2ID).Placed())
	assert.Equal(t, Reading, w2.s.Readers.Card(rc2ID).Col)

	// At 19 minutes (past renewed expiration of 18m):
	w2.s.Now = now2.Add(19 * time.Minute)
	plan2 = RestartReads(w2.s)
	require.Len(t, plan2.Units, 1, "lapsed after renewal expiry")
	w2.must(plan2)
	assert.False(t, w2.s.Readers.Card(rc2ID).Placed())
	assert.Equal(t, RetiredByLapsed, w2.s.Readers.Card(rc2ID).F("retired_by"))
}
