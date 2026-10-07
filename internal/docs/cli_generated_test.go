package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLIReferenceIsGeneratedFromHelp verifies that docs/CLI.md contains
// the clidoc markers around tool reference blocks. The clidoc tool
// regenerates these blocks from the actual tool help output.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	// Read CLI.md
	cli, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err, "docs/CLI.md: %v", err)

	// Check that marker sections exist
	beginRe := regexp.MustCompile(`(?m)^<!-- clidoc:begin ([a-z-]+) -->$`)
	endRe := regexp.MustCompile(`(?m)^<!-- clidoc:end ([a-z-]+) -->$`)

	var begins, ends []string
	for _, m := range beginRe.FindAllStringSubmatch(string(cli), -1) {
		begins = append(begins, m[1])
	}
	for _, m := range endRe.FindAllStringSubmatch(string(cli), -1) {
		ends = append(ends, m[1])
	}

	require.NotEmpty(t, begins, "no clidoc begin markers found")
	require.Equal(t, len(begins), len(ends), "mismatched clidoc markers")

	// Check that begin/end pairs match
	for i, tool := range begins {
		require.Equal(t, tool, ends[i], "clidoc marker mismatch for tool %s", tool)
	}

	// For each tool section, verify the reference block exists
	for _, tool := range begins {
		sectionStart := strings.Index(string(cli), "## "+tool+"\n\n")
		require.NotZero(t, sectionStart, "no section for tool %s", tool)

		// Find the code block after this section
		afterSection := string(cli[sectionStart:])
		codeStart := strings.Index(afterSection, "```")
		require.NotZero(t, codeStart, "no code block for tool %s", tool)

		// Find end of code block
		codeEnd := strings.Index(afterSection[codeStart+3:], "```")
		require.NotZero(t, codeEnd, "unclosed code block for tool %s", tool)

		// Verify there's content in the reference block
		refBlock := afterSection[codeStart+3 : codeStart+3+codeEnd]
		require.True(t, strings.TrimSpace(refBlock) != "", "empty reference block for tool %s", tool)
	}
}
