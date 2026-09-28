package docs

import (
	"os"
	"strings"
	"testing"
)

// issue2208_test.go proves the three spec-demanded behaviours for the
// nova-test failures verb (nova-tools #2208, items 19-21):
//
//	19. failures lists bounded failing steps with source-backed excerpts
//	20. full logs remain retrievable
//	21. successful checks are distinguished from unexecuted, skipped or missing
//
// These tests read docs/SPEC-TEST.md and assert that each behaviour is
// specified; a missing phrase reddens here so the gap is visible before
// the nova-test binary exists.

const specTestPath = "../../docs/SPEC-TEST.md"

func readSpecTest(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(specTestPath)
	if err != nil {
		t.Fatalf("%s: %v", specTestPath, err)
	}
	return string(body)
}

// TestIssue2208 proves the failures verb spec (nova-tools #2208, items 19-21).
func TestIssue2208(t *testing.T) {
	t.Parallel()
	content := readSpecTest(t)

	t.Run("TestFailuresListsBoundedStepsWithExcerpts", func(t *testing.T) {
		// Spec item 19: failures lists bounded failing steps with source-backed excerpts.
		for _, want := range []string{
			"`failures`",
			"bounded failing steps",
			"source-backed excerpts",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("docs/SPEC-TEST.md missing %q (nova-tools #2208 item 19)", want)
			}
		}
		if !strings.Contains(content, "- `failures`") {
			t.Errorf("docs/SPEC-TEST.md missing the failures verb in the Verbs list (nova-tools #2208 item 19)")
		}
	})

	t.Run("TestFullLogsRemainRetrievable", func(t *testing.T) {
		// Spec item 20: full logs remain retrievable alongside bounded output.
		for _, want := range []string{
			"full logs remain retrievable",
			"log holding the whole list",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("docs/SPEC-TEST.md missing %q (nova-tools #2208 item 20)", want)
			}
		}
	})

	t.Run("TestFailuresDistinguishUnexecutedSkippedMissing", func(t *testing.T) {
		// Spec item 21: successful checks are distinguished from unexecuted, skipped or missing.
		// Normalize whitespace so line breaks in the spec don't break the check.
		normalized := strings.Join(strings.Fields(content), " ")
		for _, want := range []string{
			"unexecuted",
			"skipped",
			"missing",
			"Successful checks are distinguished",
		} {
			if !strings.Contains(normalized, want) {
				t.Errorf("docs/SPEC-TEST.md missing %q (nova-tools #2208 item 21)", want)
			}
		}
	})
}
