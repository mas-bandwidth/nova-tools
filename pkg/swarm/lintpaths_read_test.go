package swarm

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The marker is refused through both lint entrances; START remains the place
// for read-only metadata, and no suggested write scope is generated for it.
func TestPathsReadMarkerLintEntrances(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, paths, start, entry string }{
		{"mixed PATHS", "a.go (read), b.go", "none", "a.go (read)"},
		{"standalone marker", "(read)", "none", "(read)"},
		{"first marked entry", "a.go, b.go (read), c.go (read)", "none", "b.go (read)"},
		{"plain scope", "a.go, b.go", "none", ""},
		{"START read scope", "b.go", "a.go (read)", ""},
		{"empty write scope", "none", "a.go (read)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := []byte("RESULT: c sha=0123456789ab tier: pro\nKIND: read\nPATHS: " + tc.paths + "\nTEST: none fixture\nSTART: " + tc.start + "\nSTOP: report\n\nTHE TASK. Read the fixture.\n")
			typed := findingsFor(LintCardHeader(raw, nil, false), "paths-declared")
			brief, fix := LintBrief(raw, BriefBase{})
			marked := findingsFor(brief, "paths-read-marker")
			if tc.entry == "" {
				assert.Empty(t, typed)
				assert.Empty(t, marked)
				return
			}
			require.Len(t, typed, 1)
			require.Len(t, marked, 1)
			for _, f := range []CardHeaderFinding{typed[0], marked[0]} {
				assert.Equal(t, 3, f.Line)
				assert.Contains(t, f.Excerpt, fmt.Sprintf("PATHS entry %q", tc.entry))
				assert.Contains(t, f.Excerpt, "START")
				assert.Contains(t, f.Excerpt, "(read)")
			}
			assert.Empty(t, fix, "invalid metadata must not become a NEW or widened PATHS suggestion")
			assert.Contains(t, BriefRemedy("paths-read-marker"), "START")
		})
	}
}

func TestPathsReadMarkerIgnoresBodyAndKeepsCarryFinding(t *testing.T) {
	t.Parallel()
	raw := []byte("RESULT: c sha=0123456789ab tier: pro\nPATHS: b.go\nSTART: a.go (read)\n\nTHE TASK. Read a.go (read).\nPATHS: a.go (read)\n")
	findings, _ := LintBrief(raw, BriefBase{})
	assert.Empty(t, findingsFor(findings, "paths-read-marker"))
	raw = []byte("RESULT: c sha=0123456789ab tier: pro\nPR: owner/repo#1\nPATHS: a.go (read)\n\nTHE TASK. Finish the PR.\n")
	findings, fix := LintBrief(raw, BriefBase{})
	assert.Len(t, findingsFor(findings, "pr-carry"), 1)
	assert.Len(t, findingsFor(findings, "paths-read-marker"), 1)
	assert.Empty(t, fix)
}
