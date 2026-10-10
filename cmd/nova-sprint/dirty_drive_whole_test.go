package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The split of the drive's whole-table reads by their cause
// (dirty_drive_functional_test.go): a whole read after the first tick is the
// twin's own catching up, or another writer between the twin's read and its
// catch-up (a stale part). Only the first is a failure everywhere; the second
// is a load effect, asserted against a bench's store alone.

// driveWholeRead is one whole-table read after the drive's first tick: the
// tick it happened in, its 1-based number, and the part that took it.
type driveWholeRead struct {
	Tick int
	Part store.PartTime
}

// driveWholeReads splits every whole-table read the drive's tick parts took
// after the first tick in two: the load ones, in a part whose twin was stale,
// another writer having written since it last read it (Stale > 0), and the
// strict ones, in a part that saw no other writer (Stale == 0). The first
// tick is left out: its read builds the twin, so every table it reads whole is
// expected. The strict reads are asserted everywhere; the load reads only
// against a bench's store (GateStoreEnv), and printed elsewhere.
func driveWholeReads(ticks [][]store.PartTime) (load, strict []driveWholeRead) {
	for i, times := range ticks {
		if i == 0 {
			continue // the twin's first read builds it whole: expected
		}
		for _, pt := range times {
			if pt.Reads == 0 {
				continue
			}
			wr := driveWholeRead{Tick: i + 1, Part: pt}
			if pt.Stale > 0 {
				load = append(load, wr)
			} else {
				strict = append(strict, wr)
			}
		}
	}
	return load, strict
}

// TestDriveWholeReadsFirstTickIsNeverCounted pins that the first tick is left
// out of the split: its read builds the twin, so every whole table it takes is
// expected, whether or not its part was stale.
func TestDriveWholeReadsFirstTickIsNeverCounted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}, {Name: "deal", Reads: 1, Stale: 3}},
		{{Name: "first read", Reads: 0, Stale: 2}},
	}
	load, strict := driveWholeReads(ticks)
	assert.Empty(t, load)
	assert.Empty(t, strict)
}

// TestDriveWholeReadsStaleWholeReadIsLoad pins that a whole read in a part
// that was stale (another writer wrote since the twin's read) is the load
// kind, with its tick and counts carried for the note and the report.
func TestDriveWholeReadsStaleWholeReadIsLoad(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{{Name: "first read", Reads: 1, Trips: 9, Stale: 2}},
	}
	load, strict := driveWholeReads(ticks)
	assert.Empty(t, strict)
	require.Len(t, load, 1)
	assert.Equal(t, 2, load[0].Tick)
	assert.Equal(t, "first read", load[0].Part.Name)
	assert.Equal(t, int64(1), load[0].Part.Reads)
	assert.Equal(t, int64(2), load[0].Part.Stale)
}

// TestDriveWholeReadsWholeReadWithNoStaleIsAsserted pins that a whole read in a
// part that saw no other writer is the strict kind: the twin itself did not
// catch up, and the read is asserted everywhere.
func TestDriveWholeReadsWholeReadWithNoStaleIsAsserted(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{{Name: "first read", Reads: 1, Trips: 9}},
	}
	load, strict := driveWholeReads(ticks)
	assert.Empty(t, load)
	require.Len(t, strict, 1)
	assert.Equal(t, 2, strict[0].Tick)
	assert.Equal(t, int64(1), strict[0].Part.Reads)
	assert.Zero(t, strict[0].Part.Stale)
}

// TestDriveWholeReadsBothKindsInOneTickAreSplit pins that one tick with two
// whole reads of different causes splits them by part, and that a part with no
// whole read is left out whatever its stale count.
func TestDriveWholeReadsBothKindsInOneTickAreSplit(t *testing.T) {
	t.Parallel()
	ticks := [][]store.PartTime{
		{{Name: "first read", Reads: 4}},
		{
			{Table: "work", Name: "drain", Reads: 1, Stale: 1},
			{Table: "merge", Name: "batch", Reads: 0, Stale: 5},
			{Table: "fleet", Name: "sync", Reads: 2},
		},
	}
	load, strict := driveWholeReads(ticks)
	require.Len(t, load, 1)
	require.Len(t, strict, 1)
	assert.Equal(t, "drain", load[0].Part.Name)
	assert.Equal(t, int64(1), load[0].Part.Reads)
	assert.Equal(t, "sync", strict[0].Part.Name)
	assert.Equal(t, int64(2), strict[0].Part.Reads)
}
