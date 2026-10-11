package swarm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The decision is three conditions, all necessary; each row flips exactly one of them from
// the row that reports, so a condition dropped from DeadlineReports turns a row red.
func TestDeadlineReportsOnlyAJobTheDeadlineEndedThatCommittedAndPublishedNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  DeadlineRun
		want bool
	}{
		{"deadline, commits, no result", DeadlineRun{Deadlined: true, Commits: 1}, true},
		{"many commits", DeadlineRun{Deadlined: true, Commits: 7}, true},
		{"the deadline did not end it", DeadlineRun{Commits: 1}, false},
		{"the card published its own", DeadlineRun{Deadlined: true, Published: true, Commits: 1}, false},
		{"nothing committed", DeadlineRun{Deadlined: true}, false},
	} {
		require.Equal(t, tc.want, DeadlineReports(tc.run), tc.name)
	}
}
