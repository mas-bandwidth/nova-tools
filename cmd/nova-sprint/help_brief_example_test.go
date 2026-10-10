package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/cardtree"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// cleanLine normalizes a line of text, unquoting Go string literals if scanning source code.
func cleanLine(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, "\"RESULT:"); idx >= 0 {
		line = line[idx:]
	}
	line = strings.TrimSuffix(line, " +")
	line = strings.TrimSuffix(line, "+")
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "\"") && strings.HasSuffix(line, "\"") && len(line) >= 2 {
		if unquoted, err := strconv.Unquote(line); err == nil {
			return strings.TrimRight(unquoted, "\r\n")
		}
	}
	return line
}

// extractBriefExamples scans text (either rendered help output or Go source code)
// for any brief example carrying a "RESULT:" line.
func extractBriefExamples(text string) []string {
	lines := strings.Split(text, "\n")
	var examples []string
	var current []string
	inExample := false

	for _, rawLine := range lines {
		line := cleanLine(rawLine)
		if strings.HasPrefix(line, "RESULT:") {
			if inExample && len(current) > 0 {
				examples = append(examples, strings.TrimRight(strings.Join(current, "\n"), "\n"))
				current = nil
			}
			inExample = true
		}
		if inExample {
			current = append(current, line)
			if strings.HasPrefix(line, "STEP 6.") && strings.Contains(line, "report).") {
				examples = append(examples, strings.TrimRight(strings.Join(current, "\n"), "\n"))
				current = nil
				inExample = false
			}
		}
	}
	if inExample && len(current) > 0 {
		examples = append(examples, strings.TrimRight(strings.Join(current, "\n"), "\n"))
	}
	return examples
}

func TestEveryBriefExampleInTheHelpPassesTheCardLint(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })

	helpOf := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		require.Equal(t, 0, code, "%v: %s", args, errb.String())
		return out.String()
	}

	// 1. Rendered help: check add and brief specifically.
	for _, cmd := range [][]string{
		{"help", "add"},
		{"add", "-h"},
		{"help", "brief"},
		{"brief", "-h"},
	} {
		rendered := helpOf(cmd...)
		examples := extractBriefExamples(rendered)
		require.NotEmpty(t, examples, "%v must print at least one brief example", cmd)
		for _, ex := range examples {
			assertBriefPassesLint(t, ex, strings.Join(cmd, " "))
		}
	}

	// 2. Check all other verbs in the verb table in case any carries a brief example.
	for _, v := range verbs {
		args := append(strings.Fields(v.name), "-h")
		rendered := helpOf(args...)
		for _, ex := range extractBriefExamples(rendered) {
			assertBriefPassesLint(t, ex, v.name+" -h")
		}
	}

	// 3. Check verbhelp.go directly for any example carrying a RESULT: line.
	raw, err := os.ReadFile("verbhelp.go")
	require.NoError(t, err)
	examplesInFile := extractBriefExamples(string(raw))
	require.NotEmpty(t, examplesInFile, "verbhelp.go must contain at least one brief example carrying RESULT:")
	for _, ex := range examplesInFile {
		assertBriefPassesLint(t, ex, "verbhelp.go")
	}
	assertBriefPassesLint(t, briefExampleCard, "verbhelp.go briefExampleCard")

	// 4. Verify that briefExampleCard matches nova-swarm template --name card.
	template, err := swarm.Template("card")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(template), strings.TrimSpace(briefExampleCard), "briefExampleCard must match nova-swarm template --name card")
}

func assertBriefPassesLint(t *testing.T, brief, source string) {
	t.Helper()
	// Must include RESULT:
	assert.Contains(t, brief, "RESULT:", "%s: must include RESULT:", source)
	// Must include Deadline: finish within <n> minutes.
	assert.Contains(t, brief, "Deadline: finish within", "%s: must include Deadline: finish within <n> minutes.", source)
	// Must include the six RULES sentences verbatim
	for _, rule := range swarm.DefaultChildRules {
		assert.Contains(t, brief, rule.Sentence, "%s: missing rule sentence: %s", source, rule.Sentence)
	}
	// Must include THE TASK.
	assert.Contains(t, brief, "THE TASK.", "%s: must include THE TASK.", source)
	// Must include STEP 2. and go test -count=1 -timeout 600s
	assert.Contains(t, brief, "STEP 2.", "%s: must include STEP 2.", source)
	assert.Contains(t, brief, "-timeout 600s", "%s: must include -timeout 600s in test command", source)

	// Validates against internal/swarm's card lint (under swarm.DefaultChildRules)
	findings := swarm.LintCardChildWith([]byte(brief), swarm.DefaultChildRules)
	assert.Empty(t, findings, "%s: card lint findings: %v", source, findings)

	// Validates against cardhdr model reading
	_, why := cardhdr.ReadModel(brief)
	assert.Empty(t, why, "%s: cardhdr.ReadModel why: %s", source, why)

	// Validates against cardtree lint
	treeFindings := cardtree.Lint(brief)
	assert.Empty(t, treeFindings, "%s: cardtree findings: %v", source, treeFindings)
}

// A card with the help's example brief is admitted by add without refusal.
func TestAddAdmitsTheBriefExampleFromHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers r1")
	dir := t.TempDir()
	path := dir + "/card.md"
	require.NoError(t, os.WriteFile(path, []byte(briefExampleCard), 0o600))
	code, out, errs := ta.do("add --one --stream s1 --count 1 --brief-file " + path)
	assert.Equal(t, 0, code, "stderr: %s", errs)
	assert.NotContains(t, errs, "LINT DRIFT")
	assert.NotContains(t, errs, "fails the card lint")
	assert.Contains(t, out, "ADD OK")
}

// A card's brief can be replaced with the help's example brief.
func TestBriefAdmitsTheBriefExampleFromHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers r1")
	ta.ok("add --one --stream s1 --count 1")
	dir := t.TempDir()
	path := dir + "/card.md"
	require.NoError(t, os.WriteFile(path, []byte(briefExampleCard), 0o600))
	code, out, errs := ta.do("brief s1-1 --brief-file " + path)
	assert.Equal(t, 0, code, "stderr: %s", errs)
	assert.NotContains(t, errs, "LINT DRIFT")
	assert.NotContains(t, errs, "fails the card lint")
	assert.Contains(t, out, "s1-1 brief replaced")
}
