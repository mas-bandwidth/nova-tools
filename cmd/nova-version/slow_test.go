//go:build slow && !windows

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SLOW: 9.1 s on hetzner at dev 64b9bec48, over the five-second line.
// TestHelpExampleLinesRunAsPrinted runs nova-version's example block and its
// docs/CLI.md section from a checkout root. Each line must work as printed.
// The banner and main.go's documented copy must agree; fenced documentation
// blocks run in order so earlier lines can create inputs for later ones.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	binDir := buildBinary(t, root, "nova-version")

	var banner bytes.Buffer
	update.Main("nova-version", []string{"help"}, "", &banner, &banner)
	printed, err := onboarding.ExampleLines(banner.String(), "nova-version")
	require.NoError(t, err, "nova-version help: %v\n%s", err, banner.String())
	{
		documented := mainDocumentedExamples(t)
		assert.Equal(t, documented, printed, "main.go documents the `example:` block as %q, but `nova-version help` prints %q; a documented block that drifts from the printed one is how a stranger's paste breaks", documented, printed)
	}
	work := checkout(t, root)
	for _, line := range printed {
		runExample(t, work, binDir, line)
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	require.NoError(t, err, "docs/CLI.md: %v", err)
	for _, heading := range []string{"First run", "Capture and compare installed binaries"} {
		block, err := onboarding.Transcript(string(doc), "nova-version", heading)
		require.NoError(t, err, "docs/CLI.md `### %s`: %v", heading, err)
		work := checkout(t, root)
		for _, line := range block {
			if strings.TrimSpace(line) == "" {
				continue
			}
			runExample(t, work, binDir, line)
		}
	}
}
