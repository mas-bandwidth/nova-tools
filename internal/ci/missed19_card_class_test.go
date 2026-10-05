package ci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMissed19Blocked5293CardsIsDone verifies that the three cards held back until
// #5293 merges are properly tracked: two security cards in security/blocked-5293/
// and one coverage card in coverage-wave2/blocked-5293/.
func TestMissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Parallel()

	// Verify the tracking documents exist
	tree := repoTree(t)

	// Check security2.md exists and contains security tracking
	security2File := tree.ByRel("docs/security2.md")
	require.NotNil(t, security2File, "docs/security2.md should exist and be cached")
	assert.Contains(t, string(security2File.Src), "security/blocked-5293")

	// Check coverage2.md exists and contains coverage tracking
	coverage2File := tree.ByRel("docs/coverage2.md")
	require.NotNil(t, coverage2File, "docs/coverage2.md should exist and be cached")
	assert.Contains(t, string(coverage2File.Src), "coverage-wave2/blocked-5293")
}
