//go:build unix

package swarm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The unit tier of the lane reap: its judgments on fakes, no real process and no real time.
// The reap of real processes, through real grace, is the functional tier
// (lanereap_functional_test.go).

// TestLaneSessionIsNeverTheAskersOwn pins the guard: a lane names its native's session, and a
// sid equal to the asking process's own session (or none known) ends no session at all.
func TestLaneSessionIsNeverTheAskersOwn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ sid, own, want int }{
		{sid: 42, own: 7, want: 42},
		{sid: 7, own: 7, want: 0},
		{sid: 42, own: -1, want: 0},
		{sid: 0, own: 7, want: 0},
		{sid: -3, own: 7, want: 0},
	} {
		require.Equal(t, c.want, laneSession(c.sid, c.own), "sid %d own %d", c.sid, c.own)
	}
}
