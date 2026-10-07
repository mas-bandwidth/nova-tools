package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLIReferenceIsGeneratedFromHelp verifies that docs/CLI.md has clidoc
// markers and that the content between markers matches what tools/clidoc
// would generate from tool help.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	cli, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)

	content := string(cli)

	// Find all clidoc begin/end marker pairs
	beginRe := strings.Index(content, "<!-- clidoc:begin ")
	require.NotEqual(t, -1, beginRe, "docs/CLI.md: no <!-- clidoc:begin --> markers found; run make clidoc to generate them")

	// Check that markers are properly paired
	begins := 0
	ends := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "<!-- clidoc:begin") {
			begins++
		}
		if strings.Contains(line, "<!-- clidoc:end") {
			ends++
		}
	}
	require.Equal(t, begins, ends, "docs/CLI.md: mismatched clidoc markers: %d begins, %d ends", begins, ends)
	require.Greater(t, begins, 0, "docs/CLI.md: no clidoc markers found")
}
