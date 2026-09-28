package swarm

import (
	"testing"
)

func TestIdleLogProgress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name               string
		size, last, from   int64
		wantLast, wantFrom int64
		grew               bool
	}{
		{name: "empty", size: 0, last: 0, from: 0, wantLast: 0, wantFrom: 0},
		{name: "one byte", size: 1, last: 0, from: 0, wantLast: 1, wantFrom: 0},
		{name: "a dribble", size: 10, last: 9, from: 0, wantLast: 10, wantFrom: 0},
		{name: "a page from empty", size: idleLogDribble, last: 0, from: 0, wantLast: idleLogDribble, wantFrom: idleLogDribble, grew: true},
		{name: "a page accumulated", size: idleLogDribble, last: 10, from: 0, wantLast: idleLogDribble, wantFrom: idleLogDribble, grew: true},
		{name: "just under a page", size: idleLogDribble - 1, last: 0, from: 0, wantLast: idleLogDribble - 1, wantFrom: 0},
		{name: "truncate", size: 100, last: 500, from: 0, wantLast: 100, wantFrom: 100},
		{name: "truncate after a page", size: 100, last: idleLogDribble, from: idleLogDribble, wantLast: 100, wantFrom: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLast, gotFrom, grew := idleLogProgress(tc.size, tc.last, tc.from)
			if gotLast != tc.wantLast || gotFrom != tc.wantFrom || grew != tc.grew {
				t.Fatalf("idleLogProgress(%d,%d,%d)=(%d,%d,%v), want (%d,%d,%v)",
					tc.size, tc.last, tc.from, gotLast, gotFrom, grew, tc.wantLast, tc.wantFrom, tc.grew)
			}
		})
	}
}

// ISSUE #1893: a card can keep the idle watchdog alive by writing the files the
// monitor treats as liveness. Any size change used to reset the still-clock, so a
// card that was done thinking and left `echo .` in a tool, or that rewrote its
// own log, occupied the slot until the deadline. Idle is the early reap; the
// deadline still holds. A signal the watched thing can feed for free is not a
// signal. CPU of the tree is separate and is not this test.
