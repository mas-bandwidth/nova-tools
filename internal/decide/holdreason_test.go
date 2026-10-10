package decide

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HoldSchema is a valid schema with all required fields, and its class
// question states each class's criterion as SPEC-NOVA-DECIDE section 15 does.
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
	criteria := s.Questions["class"].Criteria
	assert.Len(t, criteria, len(HoldClassifications()))
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for _, class := range HoldClassifications() {
		rule, ok := criteria[class]
		assert.True(t, ok, "the class question names %s", class)
		row := "| `" + class + "` | " + rule + " |"
		assert.True(t, strings.Contains(string(spec), row), "SPEC-NOVA-DECIDE section 15 does not state the %s criterion as the schema asks it; want the row %q", class, row)
	}
}

// ExtractProposedPaths returns the PATHS-PROPOSED line if present, else the
// paths the report names as needed.
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
			name:     "pathsoffered separated by commas",
			report:   "PATHS-PROPOSED: a/b.go, c/d.go",
			expected: "a/b.go c/d.go",
		},
		{
			name:     "no pathsoffered but has needed paths",
			report:   "need to update path internal/auth/main.go",
			expected: "internal/auth/main.go",
		},
		{
			name:     "needed paths without the word path",
			report:   "needs to update internal/auth/main.go internal/auth/token.go",
			expected: "internal/auth/main.go internal/auth/token.go",
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
		{
			name:     "a change with no path token",
			report:   "No changes needed",
			expected: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, ExtractProposedPaths(tc.report))
		})
	}
}

// holdLabel is one hand-labelled report of the backfill set: the class a person
// gave it, whether the report carries a PATHS-PROPOSED line, and the paths it
// proposes.
type holdLabel struct {
	File     string `json:"file"`
	Class    string `json:"class"`
	Proposed bool   `json:"proposed"`
	Paths    string `json:"paths"`
}

// TestHoldReasonClassesAndProposedPaths classifies each report of the labelled
// backfill set through the fixed backend, extracts its proposed paths, and
// calibrates the class question over the set: each class's fixture is
// classified as the person labelled it, its paths are the ones the extractor
// returns, and the calibration separates each class from the rest.
func TestHoldReasonClassesAndProposedPaths(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("testdata", "holdreason")
	raw, err := os.ReadFile(filepath.Join(dir, "labels.json"))
	require.NoError(t, err)
	var labels []holdLabel
	require.NoError(t, json.Unmarshal(raw, &labels))
	require.NotEmpty(t, labels)

	record := filepath.Join(t.TempDir(), "holdreason.jsonl")
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	for i, l := range labels {
		report, err := os.ReadFile(filepath.Join(dir, l.File))
		require.NoError(t, err, l.File)
		fixed := Fixed{Table: map[string]FixedAnswer{
			"class":          {Choice: l.Class, P: holdClassProb(l.Class)},
			"proposed_paths": {Noul: p(boolProb(l.Proposed))},
		}}
		d, _, err := Make(context.Background(), fixed, HoldSchema(), HoldState(string(report)), record, fmt.Sprintf("hold-%02d", i), map[string]string{"report": l.File}, now)
		require.NoError(t, err, l.File)
		assert.Equal(t, l.Class, d.Answers["class"].Value, "%s is classified %s", l.File, l.Class)
		assert.Equal(t, l.Proposed, d.Answers["proposed_paths"].Value == "yes", "%s carries PATHS-PROPOSED=%v", l.File, l.Proposed)
		assert.Equal(t, l.Paths, ExtractProposedPaths(string(report)), "%s names its proposed paths", l.File)
		_, _, err = Attach(record, Outcome{ID: d.ID, Label: l.Class, At: now.Format(time.RFC3339)})
		require.NoError(t, err, l.File)
	}

	all, err := Load(record)
	require.NoError(t, err)
	require.Len(t, all, len(labels))
	for _, class := range HoldClassifications() {
		positive := []string{class}
		var negative []string
		for _, other := range HoldClassifications() {
			if other != class {
				negative = append(negative, other)
			}
		}
		cal, err := Calibrate(all, HoldName, "class="+class, positive, negative)
		require.NoError(t, err, "calibrate %s", class)
		assert.InDelta(t, 1.0, cal.AUC(), 1e-9, "%s AUC", class)
		bar := cal.At(0.5)
		assert.Equal(t, 1.0, ratio(bar.Caught, bar.Caught+bar.Bounced), "%s precision", class)
		assert.Equal(t, 1.0, ratio(bar.Caught, len(cal.Positives)), "%s recall", class)
	}
}

// holdClassProb is a fixed backend's class answer: the labelled class at 0.8,
// each other class at 0.05.
func holdClassProb(class string) map[string]float64 {
	out := make(map[string]float64, len(holdCriteria))
	for _, c := range HoldClassifications() {
		out[c] = 0.05
	}
	out[class] = 0.8
	return out
}

// boolProb is a fixed backend's noul probability for a boolean answer.
func boolProb(v bool) float64 {
	if v {
		return 0.9
	}
	return 0.1
}

// ratio is a/(a+b), and 0 when the denominator is 0.
func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// HoldSchema hash should be stable.
func TestHoldSchemaHashIsStable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, HoldSchema().Hash(), HoldSchema().Hash())
}
