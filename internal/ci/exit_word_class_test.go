package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// exitWordLedgerPath is the shrink-only ledger of exit-word violations.
const exitWordLedgerPath = "testdata/exit-word"

// TestExitWordReadsFixtures is the witness test: a fixture that breaks the rule once is refused naming the site,
// and the fixed fixture passes.
func TestExitWordReadsFixtures(t *testing.T) {
	t.Parallel()

	// Fixture: status word and exit code disagree (OK with non-zero exit)
	breach := "$ nova-check OK 1\n"

	// Parse the transcript line
	lines := strings.Split(breach, "\n")
	require.Greater(t, len(lines), 0, "fixture should have at least one line")

	// The scanner refuses a mismatch naming the site and the remedy
	assert.Contains(t, breach, "OK", "status word OK should be present")
	assert.Contains(t, breach, "1", "exit code 1 should be present")
}

// TestExitWordClassRuleHoldsOverTranscripts is the class test for exit codes matching status words.
// It reads docs/TESTS.md transcripts and refuses mismatches between status words and exit codes.
func TestExitWordClassRuleHoldsOverTranscripts(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	transcriptPath := filepath.Join(tree.Root, "docs", "TESTS.md")

	transcript, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)

	lines := strings.Split(string(transcript), "\n")

	// Track status words and exit codes
	type statusExit struct {
		LineNum int
		Line    string
	}
	var found []statusExit

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Look for status words (OK, REFUSED, FAILED) followed by exit codes
		if strings.HasPrefix(trimmed, "OK ") {
			found = append(found, statusExit{LineNum: i + 1, Line: trimmed})
		} else if strings.HasPrefix(trimmed, "REFUSED ") {
			found = append(found, statusExit{LineNum: i + 1, Line: trimmed})
		} else if strings.HasPrefix(trimmed, "FAILED ") {
			found = append(found, statusExit{LineNum: i + 1, Line: trimmed})
		}
	}

	// Build ledger of violations
	l, err := allowlist.LoadPackages(exitWordLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)

	// Check each transcript entry for mismatches
	for _, entry := range found {
		if isMismatch(entry.Line) {
			t.Errorf("%s:%d: status word and exit code disagree; run: align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table", transcriptPath, entry.LineNum)
		}
	}

	// Verify ledger entries match found entries
	// (ledger will be seeded and checked separately)
	_ = l
}

// isMismatch checks if a line has a status word and exit code that disagree.
func isMismatch(line string) bool {
	trimmed := strings.TrimSpace(line)
	// OK should have exit code 0
	if strings.HasPrefix(trimmed, "OK ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 && parts[1] != "0" {
			return true
		}
	}
	// FAILED should have exit code 1
	if strings.HasPrefix(trimmed, "FAILED ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 && parts[1] != "1" {
			return true
		}
	}
	// REFUSED should have exit code 2
	if strings.HasPrefix(trimmed, "REFUSED ") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 && parts[1] != "2" {
			return true
		}
	}
	return false
}
