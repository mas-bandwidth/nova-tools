package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2207 pins nova-tools#2207: the nova-test `status --since` survey
// of recent runs. docs/SPEC-TEST.md must carry the survey contract -- the
// `status` verb over the run store, the `--since` window listing only runs
// since the boundary, and the per-run field rendering (queue, cancellation
// drain, execution and end-to-end latency, attempt identities and prior
// failures preserved) -- plus the four demanded behaviours 15-18.
func TestIssue2207(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-TEST.md")
	if err != nil {
		t.Fatalf("docs/SPEC-TEST.md: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"status --since",
		"queue",
		"cancellation drain",
		"execution and end-to-end latency",
		"attempt identities",
		"prior failures",
		"over the run store",
		"lists only runs since the boundary",
		"several runs at varied states",
		"--since",
		"window",
		"per-run field rendering",
		"TestStatusSinceSurveysRecentRuns",
		"TestStatusReportsQueueAndCancellationDrain",
		"TestStatusReportsLatency",
		"TestStatusPreservesAttemptsAndPriorFailures",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-TEST.md missing %q (nova-tools#2207)", want)
		}
	}
}
