//go:build unix

package swarm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// THE DEADLINE IS HELD BY THE MACHINERY, and Reap's contract is one bool: did anything in
// the group survive? The unit tier starts no subprocess, and the survivor path needs a
// group that outlives its own terminate -- that lifecycle is the functional tier's
// (timeout_functional_test.go, build tag functional). What a unit test pins here is the
// rest of the function: the non-positive refusal answers false without asking the kernel,
// and a group the kernel no longer knows (absentGroup, proc_unix_cover_test.go) is reaped
// on the first liveness read -- the terminate's ESRCH is ignored by design, so the reap
// ends in the same breath it was called, with no sleep and no clock to wait on.

// TestDeadlineCoverReap pins every answer Reap can give with no process to reap: false,
// and false at once. A grace of 0 walks the same path with both wait loops skipped, so
// the liveness check between them is named, not only the loop that holds it.
func TestDeadlineCoverReap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		pgid  int
		grace time.Duration
	}{
		{"a zero group is refused", 0, TerminateGrace},
		{"a negative group is refused", -1, TerminateGrace},
		{"an absent group is reaped with no survivor", absentGroup, TerminateGrace},
		{"an absent group with no grace ends at once", absentGroup, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, Reap(tc.pgid, "started", tc.grace),
				"Reap(%d, started, %v) found a survivor it cannot have", tc.pgid, tc.grace)
		})
	}
}
