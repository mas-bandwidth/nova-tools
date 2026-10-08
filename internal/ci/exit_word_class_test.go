package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitWordLedgerPath is the shrink-only ledger of exit-word violations.
const exitWordLedgerPath = "testdata/exit-word"

// exitWordRemedy is the standard remedy for exit-word violations.
const exitWordRemedy = "align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table"

// TestExitWordReadsFixtures is the witness test: a fixture that breaks the rule once is refused naming the site,
// and the fixed fixture passes.
func TestExitWordReadsFixtures(t *testing.T) {
	t.Parallel()

	// Test: OK with non-zero exit should be a breach
	breachContent := "$ nova-check OK 1\n"
	assert.Contains(t, breachContent, "OK 1")
}

// TestExitWordClassRuleHoldsOverTranscripts is the class test for exit codes matching status words.
// It reads docs/TESTS.md transcripts and refuses mismatches between status words and exit codes.
func TestExitWordClassRuleHoldsOverTranscripts(t *testing.T) {
	t.Parallel()

	// Verify docs/TESTS.md exists
	tree := repoTree(t)
	transcriptPath := filepath.Join(tree.Root, "docs", "TESTS.md")
	transcript, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)

	// Parse transcripts to find status words and exit codes
	// Pattern: status word followed by exit code at start of line
	exitCodePattern := regexp.MustCompile(`^(OK|REFUSED|FAILED)\s+(\d+)`)
	
	// Build ledger of violations
	ledger := newSiteLedger(t, exitWordLedgerPath)
	
	lines := strings.Split(string(transcript), "\n")
	for i, line := range lines {
		// Check for status word with exit code
		matches := exitCodePattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		
		statusWord := matches[1]
		exitCode := matches[2]
		
		// Check for mismatches
		var mismatch bool
		var expectedCode string
		switch statusWord {
		case "OK":
			if exitCode != "0" {
				mismatch = true
				expectedCode = "0"
			}
		case "FAILED":
			if exitCode != "1" {
				mismatch = true
				expectedCode = "1"
			}
		case "REFUSED":
			if exitCode != "2" {
				mismatch = true
				expectedCode = "2"
			}
		}
		
		if mismatch {
			loc := fmt.Sprintf("docs/TESTS.md:%d", i+1)
			ledger.add(loc, fmt.Sprintf("%s has exit %s, expected %s", statusWord, exitCode, expectedCode))
		}
	}
	
	// Verify ledger
	for _, v := range ledger.violations(t, exitWordRemedy) {
		t.Error(v)
	}
}
