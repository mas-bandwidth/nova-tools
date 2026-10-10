package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HoldSchema is a valid schema with all required fields.
func TestHoldSchemaIsValid(t *testing.T) {
	t.Parallel()
	s := HoldSchema()
	assert.Empty(t, s.Problems())
	assert.Equal(t, HoldName, s.Name)
	assert.Len(t, s.Questions, 2)
	_, hasClass := s.Questions["class"]
	_, hasProposed := s.Questions["proposed_paths"]
	assert.True(t, hasClass)
	assert.True(t, hasProposed)
}

// ExtractProposedPaths returns PATHS-PROPOSED line if present.
func TestExtractProposedPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		report   string
		expected string
	}{
		{
			name:     "has pathsoffered",
			report:   "PATHS-PROPOSED: file1.go file2.go",
			expected: "file1.go file2.go",
		},
		{
			name:     "no pathsoffered but has needed paths",
			report:   "need to update path internal/auth/main.go",
			expected: "need to update path internal/auth/main.go",
		},
		{
			name:     "no paths at all",
			report:   "some random text",
			expected: "",
		},
		{
			name:     "pathsoffered with spaces",
			report:   "  PATHS-PROPOSED:   path1 path2  \nother text",
			expected: "path1 path2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractProposedPaths(tc.report)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestHoldReasonClassesAndProposedPaths tests the hold reason classifier.
func TestHoldReasonClassesAndProposedPaths(t *testing.T) {
	t.Parallel()
	// Read all fixtures from testdata/holdreason
	fixtureDir := filepath.Join("testdata", "holdreason")
	fixtures, err := os.ReadDir(fixtureDir)
	require.NoError(t, err)
	require.NotEmpty(t, fixtures)

	for _, f := range fixtures {
		t.Run(f.Name(), func(t *testing.T) {
			path := filepath.Join(fixtureDir, f.Name())
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			report := string(raw)

			// Build the state
			_ = HoldState(report)

			// Verify the schema works with the state
			s := HoldSchema()
			require.Empty(t, s.Problems())

			// Extract proposed paths
			proposed := ExtractProposedPaths(report)
			t.Logf("proposed paths: %q", proposed)

			// Verify class names are valid
			classes := HoldClassifications()
			require.Len(t, classes, 5)

			// Check if classification can be inferred from the report content
			// (in real use, the model would classify this)
			hasPathsTooNarrow := strings.Contains(strings.ToLower(report), "too narrow") || strings.Contains(strings.ToLower(report), "paths too")
			hasMissingDep := strings.Contains(strings.ToLower(report), "missing") && strings.Contains(strings.ToLower(report), "depend")
			hasAlreadyDone := strings.Contains(strings.ToLower(report), "already done") || strings.Contains(strings.ToLower(report), "no changes needed")
			hasDefect := strings.Contains(strings.ToLower(report), "defect") || strings.Contains(strings.ToLower(report), "problem")
			hasHarnessFailure := strings.Contains(strings.ToLower(report), "harness") && (strings.Contains(strings.ToLower(report), "failure") || strings.Contains(strings.ToLower(report), "timed out"))

			// At least one classification hint should be present
			hasAny := hasPathsTooNarrow || hasMissingDep || hasAlreadyDone || hasDefect || hasHarnessFailure
			assert.True(t, hasAny, "fixture %s should have at least one classification hint", f.Name())
		})
	}
}

// HoldSchema hash should be stable.
func TestHoldSchemaHashIsStable(t *testing.T) {
	t.Parallel()
	s1 := HoldSchema()
	s2 := HoldSchema()
	assert.Equal(t, s1.Hash(), s2.Hash())
}
