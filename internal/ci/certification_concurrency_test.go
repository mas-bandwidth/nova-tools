package ci

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCertificationConcurrencyOnDevNeverCancels(t *testing.T) {
	t.Parallel()

	// Read certification.yml
	certData, err := os.ReadFile("../../.github/workflows/certification.yml")
	require.NoError(t, err)
	certContent := string(certData)

	// Read ci.yml
	ciData, err := os.ReadFile("../../.github/workflows/ci.yml")
	require.NoError(t, err)
	ciContent := string(ciData)

	// Test 1: Certification on dev has conditional cancel-in-progress based on event
	t.Run("certification_on_dev_cancel_in_progress", func(t *testing.T) {
		t.Parallel()

		// Check that certification.yml has cancel-in-progress in the concurrency block
		require.Contains(t, certContent, "concurrency:")
		require.Contains(t, certContent, "cancel-in-progress:")

		// The cancel-in-progress should use conditional expression based on github.event_name
		hasConditionalCancel := strings.Contains(certContent, "${{") &&
			(strings.Contains(certContent, "github.event_name") ||
				strings.Contains(certContent, "workflow_dispatch") ||
				strings.Contains(certContent, "pull_request"))

		require.True(t, hasConditionalCancel, "certification.yml should have conditional cancel-in-progress based on event type")
	})

	// Test 2: Certification never triggers on a push. It is a nightly test and
	// on demand only: a push run put 50 hosted shards and 17 minutes beside every
	// dev commit and its red landed on the commit a release promotes (v1.4.6,
	// 2026-10-16). The owner: CI is one minute on average, two at most, end to end,
	// no exceptions.
	t.Run("certification_triggers", func(t *testing.T) {
		t.Parallel()
		on := certContent[strings.Index(certContent, "\non:\n"):]
		on = on[:strings.Index(on, "\n\n")]
		require.NotContains(t, on, "push:", "certification.yml must never run on a push; it is nightly and on demand")
		require.NotContains(t, on, "pull_request:", "certification.yml must never run on a pull request")
		require.Contains(t, on, "schedule:")
		require.Contains(t, on, "workflow_dispatch:")
	})

	// Test 3: Pull request CI keeps its cancel-in-progress
	t.Run("pr_ci_cancel_in_progress", func(t *testing.T) {
		t.Parallel()

		// ci.yml should have cancel-in-progress true for PRs
		require.Contains(t, ciContent, "cancel-in-progress: true")
	})
}
