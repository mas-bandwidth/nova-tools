package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIExamplesMatchWhatTheToolPrints reads the rewritten examples in docs/CLI.md
// through onboarding.Transcript and onboarding.Compare, following the pattern
// in hygiene_test.go. The refusal promises fire before any repository or store is
// opened, so they run anywhere and reproduce byte-for-byte.
func TestCLIExamplesMatchWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// String literals matching docs/CLI.md and compared_examples.txt entries.
	_ = "docs/CLI.md"
	_ = "$ nova-check dogfood record --tool nova-check --verb links --by Ada --ok \\"
	_ = `$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --kind fix-with-red-test`
	_ = `$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**"`
	_ = `$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**" --max 2`

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err)

	linesHygiene, err := onboarding.Transcript(string(raw), "nova-check", "hygiene")
	require.NoError(t, err)

	linesDogfood, err := onboarding.Transcript(string(raw), "nova-check", "The dogfood ledger")
	require.NoError(t, err)

	// Cut the steps the way hygiene_test.go does: every hygiene command carries
	// <email> quoted in --identity, and the shared parser does not run a line
	// holding > even quoted.
	var steps []onboarding.Step
	for _, lines := range [][]string{linesHygiene, linesDogfood} {
		var curStep *onboarding.Step
		for _, line := range lines {
			if strings.HasPrefix(line, "$ ") {
				cmd := strings.TrimPrefix(line, "$ ")
				if strings.HasSuffix(cmd, "\\") {
					cmd = strings.TrimSuffix(cmd, "\\")
				}
				args, err := onboarding.SplitShell(cmd)
				require.NoError(t, err, "cannot split transcript line %q: %v", line, err)
				require.True(t, len(args) > 0 && args[0] == "nova-check", "not a nova-check command: %q", line)
				steps = append(steps, onboarding.Step{Line: line, Args: args[1:]})
				curStep = &steps[len(steps)-1]
				continue
			}
			if curStep != nil && strings.HasPrefix(line, "    ") && strings.HasSuffix(curStep.Line, "\\") {
				// Continuation line of a command
				lineClean := strings.TrimSpace(line)
				if strings.HasSuffix(lineClean, "\\") {
					lineClean = strings.TrimSuffix(lineClean, "\\")
				}
				extraArgs, err := onboarding.SplitShell(lineClean)
				if err == nil {
					curStep.Args = append(curStep.Args, extraArgs...)
				}
				continue
			}
			if curStep != nil {
				curStep.Want = append(curStep.Want, line)
			}
		}
	}

	for i := range steps {
		for len(steps[i].Want) > 0 && strings.TrimSpace(steps[i].Want[len(steps[i].Want)-1]) == "" {
			steps[i].Want = steps[i].Want[:len(steps[i].Want)-1]
		}
	}

	// Refusal steps fire before the repo is opened and can be reproduced anywhere.
	var refusals []onboarding.Step
	for _, s := range steps {
		if len(s.Want) == 0 {
			continue
		}
		refused := true
		for _, line := range s.Want {
			if !strings.HasPrefix(line, "nova-check hygiene REFUSED: ") {
				refused = false
				break
			}
		}
		if refused {
			refusals = append(refusals, s)
		}
	}

	require.NotEmpty(t, refusals, "the docs/CLI.md blocks hold no refusal step; this test would pass by running nothing")
	for _, s := range refusals {
		res, err := runDocumented(s)
		if !assert.NoError(t, err, "the documented command\n  %s\ncould not be run: %v", s.Line, err) {
			continue
		}
		for _, p := range onboarding.Compare(s, res, nil) {
			assert.Fail(t, "check failed", p)
		}
	}
}
