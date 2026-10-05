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
	security2Path := "docs/security2.md"
	security2Content, err := readTreeFile(tree, security2Path)
	require.NoError(t, err)
	assert.Contains(t, security2Content, "security/blocked-5293")

	// Check coverage2.md exists and contains coverage tracking
	coverage2Path := "docs/coverage2.md"
	coverage2Content, err := readTreeFile(tree, coverage2Path)
	require.NoError(t, err)
	assert.Contains(t, coverage2Content, "coverage-wave2/blocked-5293")
}
