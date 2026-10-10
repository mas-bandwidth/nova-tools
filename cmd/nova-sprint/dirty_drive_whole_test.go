package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The drive's whole-table reads after its first tick
// (dirty_drive_functional_test.go): every one is the twin failing to catch a
// table up, so every one is asserted, whatever the part's Stale count.
//
// A whole read after the first tick is a change-stream gap, not load. catchUp
// (twin.go:412-436) reads the table whole only when the table's change stream
// cannot account for the revisions between (!ok: TableChanges returns a
// GapError or a GrantError, redis.go:1022-1090), and it increments
// Stats.stale only on the catch-up success path (twin.go:437). Within one tick
// part Reads and Stale therefore come from different tables: a part with
// Stale > 0 caught one table up from the change stream and can still carry a
// whole read of another table the stream could not account for. A write by
// another writer between the twin's read and its catch-up is caught up from
// the change stream and counted stale, never read whole (twin_test.go:66-109
// pins Stale > 0, Reads == 0 for a write during the tick). So store.PartTime's
// Stale does not classify a whole read, and no whole read is demoted to load.
//
// A gap is a product defect to fix in the change stream, not a runner's load:
// the row hide (c48490de4) and the order-only set (f45296afd) were fixed the
// same way, each a write the stream layer classified as naming no records when
// it named or changed none. The drive's failure names the gap (the catch-up's
// note, tick.go's Said) beside the tick, part and counts, so the on-record
// cause is the verb, not the load.

// driveWholeRead is one whole-table read after the drive's first tick: the
// tick it happened in and the part that took it.
type driveWholeRead struct {
	Tick int
	Part store.PartTime
}

// driveWholeReads is every whole-table read the drive's tick parts took after
// the first tick, in tick order. The first tick is left out: its read builds
// the twin, so every table it reads whole is expected.
//
// It does not split them by store.PartTime.Stale: a whole read is the twin
// failing (a change-stream gap), and Stale counts a catch-up from the change
// stream, a different table's read. The drive asserts every one; the failure's
// note names the gap.
func driveWholeReads(ticks [][]store.PartTime) []driveWholeRead {
	var out []driveWholeRead
	for i, times := range ticks {
		if i == 0 {
			continue // the twin's first read builds it whole: expected
		}
		for _, pt := range times {
			if pt.Reads > 0 {
				out = append(out, driveWholeRead{Tick: i + 1, Part: pt})
			}
		}
	}
	return out
}

// TestDriveWholeReadsFirstTickIsNeverCounted pins that the first tick is left
// out: its read builds the twin, so every whole table it takes is expected,
// whatever its part's stale count.
func TestDriveWholeReadsFirstTickIsNeverCounted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}, {Name: "deal", Reads: 1, Stale: 3}},
		{{Name: "first read", Reads: 0, Stale: 2}},
	}
	assert.Empty(t, driveWholeReads(ticks))
}

// TestDriveWholeReadsStaleWholeReadIsAsserted pins that a whole read in a part
// that also caught a table up (Stale > 0) is still asserted: Stale counts a
// catch-up from the change stream, a different table's read, and does not mean
// the whole read was another writer's and not the twin's. The reads are the
// merge-queue failures' own shape (nova-tools#5214, nova-tools#5197: the first
// read of one table whole, 334-402 trips, 3-4 stale), so demoting them by
// Stale would hide the gap.
func TestDriveWholeReadsStaleWholeReadIsAsserted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{{Name: "first read", Reads: 1, Trips: 402, Stale: 3}},
		{{Name: "first read", Reads: 1, Trips: 334, Stale: 4}},
	}
	got := driveWholeReads(ticks)
	assert.Len(t, got, 2)
	assert.Equal(t, 2, got[0].Tick)
	assert.Equal(t, "first read", got[0].Part.Name)
	assert.Equal(t, int64(1), got[0].Part.Reads)
	assert.Equal(t, int64(3), got[0].Part.Stale)
	assert.Equal(t, 3, got[1].Tick)
	assert.Equal(t, int64(4), got[1].Part.Stale)
}

// TestDriveWholeReadsWholeReadWithNoStaleIsAsserted pins that a whole read in a
// part that saw no catch-up is asserted too: it is the same twin failure.
func TestDriveWholeReadsWholeReadWithNoStaleIsAsserted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{{Name: "first read", Reads: 1, Trips: 9}},
	}
	got := driveWholeReads(ticks)
	assert.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Tick)
	assert.Equal(t, int64(1), got[0].Part.Reads)
	assert.Zero(t, got[0].Part.Stale)
}

// TestDriveWholeReadsWholeReadsInOneTickAreAllCounted pins that every whole
// read of a tick is carried, by part, and that a part with no whole read is
// left out whatever its stale count.
func TestDriveWholeReadsWholeReadsInOneTickAreAllCounted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{
			{Table: "work", Name: "drain", Reads: 1, Stale: 1},
			{Table: "merge", Name: "batch", Reads: 0, Stale: 5},
			{Table: "fleet", Name: "sync", Reads: 2},
		},
	}
	got := driveWholeReads(ticks)
	assert.Len(t, got, 2)
	assert.Equal(t, "drain", got[0].Part.Name)
	assert.Equal(t, int64(1), got[0].Part.Reads)
	assert.Equal(t, "sync", got[1].Part.Name)
	assert.Equal(t, int64(2), got[1].Part.Reads)
}
