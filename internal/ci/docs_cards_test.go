package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Parallel()
	testMissed19Blocked5293CardsIsDone(t)
}

func TestUmissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Parallel()
	testMissed19Blocked5293CardsIsDone(t)
}

func testMissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Helper()
	root := repoRoot(t)

	secPath := filepath.Join(root, "docs", "security2.md")
	data, err := os.ReadFile(secPath)
	require.NoError(t, err)

	content := string(data)
	// security2.md must list TWO security cards, not the coverage card
	assert.NotContains(t, content, "cov-cmd-nova-sandbox-main", "security2.md must not contain the coverage card (cov-cmd-nova-sandbox-main)")
	assert.NotContains(t, content, "cover-cmd-nova-sandbox-main", "security2.md must not contain the coverage card")

	// Count security card entries (lines starting with "- security/blocked-5293/")
	lines := strings.Split(content, "\n")
	var count int
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- security/blocked-5293/") {
			count++
		}
	}
	assert.Equal(t, 2, count, "security2.md must list exactly 2 security cards")

	// Verify coverage2.md exists and has the coverage card
	covPath := filepath.Join(root, "docs", "coverage2.md")
	covData, err := os.ReadFile(covPath)
	require.NoError(t, err)

	assert.Contains(t, string(covData), "cover-cmd-nova-sandbox-main", "coverage2.md must contain the coverage card")
}
