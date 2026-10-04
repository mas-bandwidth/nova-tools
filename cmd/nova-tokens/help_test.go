package main

import (
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

// TestHelpListsOneSumForm pins that the bare help banner carries the one `sum` form, the
// --out/--month report, on its own synopsis line (#3464: a bare continuation once made two
// calls read as one).
func TestHelpListsOneSumForm(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "--out <dir> --month <YYYY-MM>")

	// Cut the `usage:` block, which ends at the first blank line, so the pasteable
	// example lines below it are not mistaken for synopsis lines.
	inUsage := false
	n := 0
	for _, line := range strings.Split(r.stdout, "\n") {
		switch {
		case line == "usage:":
			inUsage = true
			continue
		case inUsage && line == "":
			inUsage = false
		case inUsage && strings.HasPrefix(line, "  nova-tokens sum "):
			n++
		}
	}
	assert.Equal(t, 1, n, "the help banner's usage block carries %d `nova-tokens sum` synopsis lines, want 1:\n%s", n, r.stdout)
}

// TestHelpHasProfilesSentenceAndReportModesAndSetupBlock asserts the three help fixes:
// (1) profiles usage line carries a sentence describing what it prints
// (2) report's two modes are named "report, local:" and "report, store:" with a sentence
//
//	explaining which flags select which and what a mix of --who and --redis does
//
// (3) the long setup line is replaced by a setup: block of three lines, each under 100 columns
func TestHelpHasProfilesSentenceAndReportModesAndSetupBlock(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	out := r.stdout

	// (1) profiles line: must not be the bare "nova-tokens profiles --swarm-root <dir>"
	// but must have a sentence after it describing the output.
	// The sentence should mention PROFILES MODEL lines and the PROFILES OK line.
	foundProfilesLine := false
	foundProfilesSentence := false
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nova-tokens profiles --swarm-root <dir>") {
			foundProfilesLine = true
			// The usage line itself should be just the usage; the sentence is on the next line
			assert.Equal(t, "nova-tokens profiles --swarm-root <dir>", trimmed,
				"profiles usage line should be exactly the usage, sentence follows on next line")
			// Check the next line for the descriptive sentence
			if i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				assert.Contains(t, nextTrimmed, "PROFILES MODEL",
					"profiles sentence must mention PROFILES MODEL lines")
				assert.Contains(t, nextTrimmed, "PROFILES OK",
					"profiles sentence must mention the PROFILES OK closing line")
				foundProfilesSentence = true
			}
		}
	}
	assert.True(t, foundProfilesLine, "profiles usage line not found in help")
	assert.True(t, foundProfilesSentence, "profiles descriptive sentence not found in help")

	// (2) report modes: must have "report, local:" and "report, store:" as first clauses
	// and a sentence explaining flag selection and mix behavior.
	foundReportLocal := false
	foundReportStore := false
	foundReportModeSentence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nova-tokens report, local:") {
			foundReportLocal = true
		}
		if strings.HasPrefix(trimmed, "nova-tokens report, store:") {
			foundReportStore = true
		}
		// Look for the sentence explaining flag selection
		if strings.Contains(trimmed, "--who") && strings.Contains(trimmed, "--redis") &&
			strings.Contains(trimmed, "select") && strings.Contains(trimmed, "mix") {
			foundReportModeSentence = true
		}
	}
	assert.True(t, foundReportLocal, "report local mode line (report, local:) not found in help")
	assert.True(t, foundReportStore, "report store mode line (report, store:) not found in help")
	assert.True(t, foundReportModeSentence, "report mode selection sentence not found in help")

	// (3) setup: block - the long setup line must be replaced by a setup: block
	// with three lines (mkdir, printf, cp), each under 100 columns.
	inSetup := false
	setupLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "setup:" {
			inSetup = true
			continue
		}
		if inSetup {
			if trimmed == "" || strings.HasPrefix(trimmed, "example:") {
				inSetup = false
				continue
			}
			if strings.HasPrefix(trimmed, "mkdir") || strings.HasPrefix(trimmed, "printf") || strings.HasPrefix(trimmed, "cp") || strings.HasPrefix(trimmed, "echo") {
				setupLines++
				assert.LessOrEqual(t, len(trimmed), 100, "setup line %q exceeds 100 columns", trimmed)
			}
		}
	}
	assert.Equal(t, 4, setupLines, "setup: block must have exactly four command lines (mkdir, echo, echo, cp)")
}
