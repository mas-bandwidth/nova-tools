package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The drive's whole-table reads after its first tick
// (dirty_drive_functional_test.go, THE GATE) are not split by a part's
// store.PartTime.Stale, and the drive's assertion (no whole read after the
// first tick) is left as it was.
//
// catchUp (internal/sprint/store/twin.go:412-437) reads a table whole only on
// !ok: the table's change stream does not account for every revision between
// (a GapError) or the user lacks its read (a GrantError). Stats.stale is
// incremented only on the catch-up success path after that branch
// (twin.go:437). A write another writer makes between the twin's read and its
// catch-up is caught up from the change stream and counted stale, not read
// whole (store/twin_test.go, TestAWriteDuringTheTickIsReadAgain). Stale counts
// one table's catch-up while Reads counts another table's whole read in the
// same part: Stale does not classify a whole read.
//
// So a whole read after the first tick is the twin failing because a write
// beside the loop is one the table's change stream does not name. That is a
// product defect to establish per table, or as a new brief, before the gate's
// assertion is changed.

// TestDriveWholeReadsStaleIsNotTheWholeReadCause pins the crux: a tick's whole
// reads and its stale catch-ups are separate sums, so a whole read cannot be
// gated on its part's stale count.
func TestDriveWholeReadsStaleIsNotTheWholeReadCause(t *testing.T) {
	t.Parallel()
	r := store.TickResult{Times: []store.PartTime{
		{Table: "work", Name: "first read", Reads: 1, Stale: 3},
		{Table: "merge", Name: "first read", Reads: 2},
	}}
	c := r.Cost()
	assert.Equal(t, int64(3), c.Reads, "the whole reads of both parts")
	assert.Equal(t, int64(3), c.Stale, "the catch-ups of a part that also read whole")
}
