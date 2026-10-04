package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWhereJSONSaysTheReadyBufferIsLow pins the ready buffer the program reads
// off the where view (docs/SPEC-SPRINT-DASHBOARD.md): ready is the ready
// primaries across the work table's streams, width is the total width of the
// fleet members whose status is up, buffer is "<ready>/<2*width>" and low is
// true while ready is under width. Red before: the four fields are absent.
func TestWhereJSONSaysTheReadyBufferIsLow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = []string{"m1"}
	ta.ok("init --readers reader-a --members m1:8")
	ta.ok("add --stream s1 --count 3")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, int64(3), w.Ready)
	assert.Equal(t, 8, w.Width)
	assert.Equal(t, "3/16", w.Buffer)
	assert.True(t, w.Low)

	// 20 ready on width 8 is not low: the fleet's width is the line, not twice it
	ta.ok("add --stream s1 --count 17")
	ta.json("where", &w)
	assert.Equal(t, int64(20), w.Ready)
	assert.Equal(t, "20/16", w.Buffer)
	assert.False(t, w.Low)
}
