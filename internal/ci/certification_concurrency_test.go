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

	// Test 2: Certification triggers on push to dev and release/**
	t.Run("certification_triggers", func(t *testing.T) {
		t.Parallel()

		// Check push triggers
		require.Contains(t, certContent, "push:")
		require.Contains(t, certContent, "branches:")

		// Check for dev trigger
		hasDevTrigger := strings.Contains(certContent, "dev")
		require.True(t, hasDevTrigger, "certification.yml should trigger on push to dev")

		// Check for release/** trigger
		hasReleaseTrigger := strings.Contains(certContent, "release/")
		require.True(t, hasReleaseTrigger, "certification.yml should trigger on push to release branches")

		// Check workflow_dispatch trigger
		require.Contains(t, certContent, "workflow_dispatch:")
	})

	// Test 3: Pull request CI keeps its cancel-in-progress
	t.Run("pr_ci_cancel_in_progress", func(t *testing.T) {
		t.Parallel()

		// ci.yml should have cancel-in-progress true for PRs
		require.Contains(t, ciContent, "cancel-in-progress: true")
	})
}
