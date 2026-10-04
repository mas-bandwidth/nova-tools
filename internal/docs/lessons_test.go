package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLessonsFileIsCapped holds docs/LESSONS.md to its cap: the reviewed
// lessons stay short enough to read in full, at most 40 physical lines.
func TestLessonsFileIsCapped(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/LESSONS.md")
	require.NoError(t, err, "docs/LESSONS.md: %v", err)
	lines := strings.Count(string(raw), "\n")
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		lines++
	}
	require.LessOrEqual(t, lines, 40, "docs/LESSONS.md has %d lines; cap is 40", lines)
}
