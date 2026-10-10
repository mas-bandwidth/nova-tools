package main

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
)

// A report whose lanes have all ended names an explicit empty running list.
// An omitted list (nil) stays off the argv, so a liveness beat does not clear.
func TestAZeroRunningReportNamesAnExplicitEmptyList(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	zero := 0
	args := beatReportArgs("amy", time.Time{}, friend.BeatWords{}, friend.BeatReport{
		Width: 4, Started: started, Running: []string{}, Working: &zero,
	})
	var got string
	for i, a := range args {
		if a == "--running" && i+1 < len(args) {
			got = args[i+1]
		}
	}
	assert.Equal(t, friend.RunningNone, got)
	assert.Contains(t, args, "--working")
	omitted := beatReportArgs("amy", time.Time{}, friend.BeatWords{}, friend.BeatReport{Width: 4, Started: started})
	assert.NotContains(t, omitted, "--running")
}
