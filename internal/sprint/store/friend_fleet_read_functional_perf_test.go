//go:build functional && perf

package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A dedicated isolated Redis performance gate, with no other functional cases
// running, leaves half of the 200 ms dashboard source budget for its other facts
// (SPEC-SPRINT-DASHBOARD; STANDARD section 8: wall budgets are release gates).
// It does not certify live network or fleet latency.
func TestRedisFriendUsageProjectionRetainsTotalsWithinReadBudget(t *testing.T) {
	t.Parallel()
	durations := redisFriendUsageProjectionReadSamples(t, 60)
	assert.LessOrEqual(t, durations[59], 100*time.Millisecond, "isolated usage-read p99 budget; live end-to-end acceptance remains separate")
}
