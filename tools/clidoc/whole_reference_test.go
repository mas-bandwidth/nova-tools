package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheReferenceIsGeneratedWholeAndCarriesOnlyFirstRunProse holds docs/CLI.md
// to the whole-file contract: it opens with the fixed generated preamble, every
// `## ` section names a nova tool and carries one clidoc block, and every line
// outside a clidoc block is either a heading, a blank line or a line of that
// tool's `### First run` prose. A line someone writes by hand anywhere else is
// a failure naming the line and the command that regenerates the file.
//
// The test is a unit test that only reads the file: it does not build the tools
// or run clidoc, so it never touches a socket, a store or the network.
func TestTheReferenceIsGeneratedWholeAndCarriesOnlyFirstRunProse(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err, "reading docs/CLI.md: %v", err)
	text := string(raw)

	require.True(t, strings.HasPrefix(text, preamble),
		"docs/CLI.md does not open with the generated preamble; run `make clidoc` to regenerate the file whole")

	body := strings.TrimPrefix(text, preamble)
	var faults []string

	var (
		tool         string
		inBlock      bool
		blockTool    string
		seenFirstRun bool
		inFirstRun   bool
	)

	for i, line := range strings.Split(body, "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(line)

		if rest, ok := strings.CutPrefix(line, "## "); ok {
			if !strings.HasPrefix(rest, "nova-") {
				faults = append(faults, fmt.Sprintf("line %d: `## %s` is not a nova tool section", n, rest))
			}
			if tool != "" && !seenFirstRun {
				faults = append(faults, fmt.Sprintf("## %s: no `### First run` section", tool))
			}
			tool = strings.Fields(rest)[0]
			inBlock, inFirstRun, seenFirstRun = false, false, false
			continue
		}

		if m := beginMarker.FindStringSubmatch(line); m != nil {
			if m[1] != tool {
				faults = append(faults, fmt.Sprintf("line %d: clidoc:begin %s inside ## %s", n, m[1], tool))
			}
			inBlock = true
			blockTool = m[1]
			continue
		}
		if m := endMarker.FindStringSubmatch(line); m != nil {
			if !inBlock {
				faults = append(faults, fmt.Sprintf("line %d: clidoc:end %s with no begin marker", n, m[1]))
			}
			if m[1] != blockTool || m[1] != tool {
				faults = append(faults, fmt.Sprintf("line %d: clidoc:end %s does not close the block for ## %s", n, m[1], tool))
			}
			inBlock = false
			continue
		}

		if inBlock {
			continue
		}

		if trimmed == "### First run" {
			if seenFirstRun {
				faults = append(faults, fmt.Sprintf("line %d: second `### First run` in ## %s", n, tool))
			}
			seenFirstRun = true
			inFirstRun = true
			continue
		}

		if inFirstRun {
			continue
		}

		if trimmed != "" {
			faults = append(faults, fmt.Sprintf("line %d: hand-written line outside a clidoc block or `### First run` section: %q", n, trimmed))
		}
	}
	if tool != "" && !seenFirstRun {
		faults = append(faults, fmt.Sprintf("## %s: no `### First run` section", tool))
	}

	assert.Empty(t, faults,
		"docs/CLI.md carries %d line(s) that are not generated or First run prose; run `make clidoc` to regenerate the file whole:\n%s",
		len(faults), strings.Join(faults, "\n"))
}
