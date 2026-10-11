package member

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The card tells the child the wall the member ends it at, in minutes, and to have its
// RESULT.md written by 80% of it, whatever the brief names (fault 9, 2026-10-10: a slow member's
// DeepSeek children planned on a brief's 120 minutes and were ended at the route's 40 with
// no RESULT.md). A packet with no deadline says nothing of one. The same paragraph tells it
// its temp files go under $TMPDIR, never /tmp (fault 1: a /tmp write is a wall denial and the
// run is refused).
func TestTheCardTellsTheChildItsWall(t *testing.T) {
	t.Parallel()
	brief := "DEADLINE: finish within 120 minutes\n"
	routeSeconds := 40 * 60 // a count of seconds, as Packet.Deadline carries it, not a test bound
	for _, kind := range []string{"", "read"} {
		text := CardText(Packet{Card: "c.w1", Kind: kind, Brief: brief, Deadline: routeSeconds})
		assert.Contains(t, text, "Your wall: this run is ended at 40 minutes from its start, whatever time the brief names", kind)
		assert.Contains(t, text, "write RESULT.md by minute 32", kind)
		assert.Contains(t, text, "Temp files go under $TMPDIR", kind)
		assert.Contains(t, text, "never write to /tmp", kind)
		assert.NotContains(t, CardText(Packet{Card: "c.w1", Kind: kind, Brief: brief}), "Your wall:", "no deadline, no wall line")
	}
	assert.Equal(t, "", WallText(0))
	assert.Contains(t, WallText(30), "ended at 1 minutes", "never a wall of zero minutes")
	assert.Contains(t, WallText(30), "by minute 1")
}

// The read brief says where a reader's scratch is: $JOB/tmp, a writable directory inside
// its job, made before the child starts and removed with the job. A reader that honored
// only /tmp was refused by the wall and returned no verdict, spending a reader (the
// a-reader-writes-scratch-inside-its-job fault, 2026-10-11). A work card's brief says no
// such thing: its scratch text is its own.
func TestTheReadBriefNamesTheScratchInsideItsJob(t *testing.T) {
	t.Parallel()
	// a count of seconds, as Packet.Deadline carries it, not a test bound
	wallSeconds := 10 * 60
	read := CardText(Packet{Card: "c.r1", Kind: "read", Brief: "x", Deadline: wallSeconds})
	assert.Contains(t, read, "$JOB/tmp inside your job", "the read brief names the scratch inside the job")
	assert.Contains(t, read, "removed with the job")
	assert.Contains(t, read, "never to /tmp")
	work := CardText(Packet{Card: "c.w1", Kind: "work", Brief: "x", Deadline: wallSeconds})
	assert.NotContains(t, work, "$JOB/tmp inside your job", "a work card is told its own scratch, not a reader's")
}

// The finish minute is WallFinishShare of the wall, floored to whole minutes and never zero;
// each row sits on a boundary of that floor.
func TestWallTextFinishMinuteAtTheMarginBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		seconds  int
		wall, by int
	}{
		{59, 1, 1}, {60, 1, 1}, {74, 1, 1}, {75, 1, 1}, {149, 2, 1}, {150, 2, 2}, {600, 10, 8}, {601, 10, 8}, {2400, 40, 32}, {7200, 120, 96},
	} {
		got := WallText(tc.seconds)
		assert.Contains(t, got, fmt.Sprintf("ended at %d minutes", tc.wall), "%ds", tc.seconds)
		assert.Contains(t, got, fmt.Sprintf("by minute %d,", tc.by), "%ds", tc.seconds)
	}
}
