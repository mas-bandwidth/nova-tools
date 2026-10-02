package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/TESTS.md promises that every transcript line is real output pasted whole, and its
// nova-sandbox section is the page a stranger meets first. Field VALUES are the recording
// machine's and differ by platform, so they are not compared; field NAMES, in order, are.
// CHECK OK is compared against a live run: its names come from one format string in
// main.go with no platform branch above it (`hosts=` was the field that went missing from
// the documented line). PROBE OK and SANDBOX OK print a darwin-only wall, so they are
// pinned against main.go's format strings themselves: the PROBE OK block had lost gpu=,
// the SANDBOX OK block read-noexec=, ancestors= and gpu=.
func TestTheTranscriptsNameEveryFieldTheVerbsPrint(t *testing.T) {
	t.Parallel()

	section := sandboxSection(t)
	r := saw(t, os.Environ(), "check")
	require.Equal(t, 0, r.Code, "nova-sandbox check exited %d, want 0", r.Code)
	printed := map[string][]string{"CHECK OK ": checkFieldNames(onlyLine(t, "nova-sandbox check's stdout", r.Stdout, "CHECK OK "))}
	for _, line := range strings.Split(testkit.ReadFile(t, "main.go"), "\n") {
		for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
			i := strings.Index(line, `"`+prefix)
			if i < 0 {
				continue
			}
			rest := line[i+1:]
			end := strings.Index(rest, `"`)
			require.GreaterOrEqual(t, end, 0, "main.go's %q format string is not closed on its line: %q", prefix, line)
			require.Empty(t, printed[prefix], "main.go prints more than one %q line; this test cannot say which one the transcript pins", prefix)
			printed[prefix] = checkFieldNames(rest[:end])
		}
	}
	for _, prefix := range []string{"CHECK OK ", "PROBE OK ", "SANDBOX OK "} {
		require.NotEmpty(t, printed[prefix], "the verb prints no %q fields to pin the transcript to", prefix)
		documented := onlyLine(t, "TESTS.md's nova-sandbox section", section, prefix)
		assert.Equal(t, printed[prefix], checkFieldNames(documented), "TESTS.md's %q line does not name every field the verb prints, in order: %q", prefix, documented)
	}
}

// sandboxSection is docs/TESTS.md's `## nova-sandbox` section.
func sandboxSection(t *testing.T) string {
	t.Helper()
	section, ok := onboarding.Section(testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md")), "nova-sandbox")
	require.True(t, ok, "TESTS.md has no ## nova-sandbox section")
	return section
}

// onlyLine is the one line of text that opens with prefix once trimmed, failing the test
// when where holds none or more than one.
func onlyLine(t *testing.T, where, text, prefix string) string {
	t.Helper()
	var found string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
			require.Empty(t, found, "%s has more than one %q line: %q and %q", where, prefix, found, l)
			found = l
		}
	}
	require.NotEmpty(t, found, "%s has no %q line to pin", where, prefix)
	return found
}

// checkFieldNames is the card's one rule: split the line on whitespace, and for each token
// matching ^[a-z0-9_-]+= take the text before the first '='. The class is wider than
// letters because the grammar holds hyphenated and digit names (read-noexec=, cwdb64=); a
// letters-only read drops exactly the mid-line insertion this pin exists to catch. It
// STOPS after `note`, whose value is a sentence: everything after it is prose.
func checkFieldNames(line string) []string {
	field := regexp.MustCompile(`^[a-z0-9_-]+=`)
	var names []string
	for _, tok := range strings.Fields(line) {
		if !field.MatchString(tok) {
			continue
		}
		name := tok[:strings.Index(tok, "=")]
		names = append(names, name)
		if name == "note" {
			break
		}
	}
	return names
}

// TestTheCommandReferenceExamplesAreWhatTheToolPrints executes the documented
// examples from docs/CLI.md and compares their output through onboarding.Compare.
func TestTheCommandReferenceExamplesAreWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "CLI.md"))
	section, ok := onboarding.Section(raw, "nova-sandbox")
	require.True(t, ok, "docs/CLI.md has no `## nova-sandbox` section")
	// Steps 1 and 2: each a fenced block of its own, opening with its command.
	for _, line := range []string{"$ nova-sandbox check --bogus", "$ nova-sandbox bogus"} {
		var steps []onboarding.Step
		for i, block := range strings.Split(section, "```") {
			if lines := strings.Split(strings.Trim(block, "\n"), "\n"); i%2 == 1 && strings.HasPrefix(lines[0], line) {
				var err error
				steps, err = onboarding.Steps("nova-sandbox", lines)
				require.NoError(t, err)
				break
			}
		}
		require.Len(t, steps, 1, "docs/CLI.md's block opening %q", line)
		for _, p := range onboarding.Compare(steps[0], saw(t, os.Environ(), steps[0].Args...), nil) {
			t.Errorf("%s: %s", line, p)
		}
	}

	// Steps 3, 4, 5 (create, reuse, prune), in order: $ nova-sandbox worktree --repo
	// /path/to/workdir --scratch /path/to/workdir/scratch, then --pr 123 twice and --prune,
	// over a fake git and forge, with the documented paths made this test's own.
	wtLines, err := onboarding.Transcript(raw, "nova-sandbox", "worktree")
	require.NoError(t, err)
	wtSteps, err := onboarding.Steps("nova-sandbox", wtLines)
	require.NoError(t, err)
	require.Len(t, wtSteps, 3, "the worktree transcript is create, reuse and prune")
	j := newWJob(t)
	useFakeGit(t, newFakeGit(j.repo))
	useForge(t, &fakeForge{byID: map[int]worktreePR{123: {Head: "0123456789abcdef0123456789abcdef01234567", Base: "main", State: "open"}}})
	swap(t, &worktreeGUID, func() string { return "1f450ab70c635e66f675ff8a4e395760" })
	const docRepo, docScratch = "/path/to/workdir", "/path/to/workdir/scratch"
	scratchNorm := []onboarding.Norm{onboarding.Path(docScratch, j.scratch)}
	for i, norms := range [][]onboarding.Norm{scratchNorm, scratchNorm, nil} {
		step := wtSteps[i]
		args := slices.Clone(step.Args)
		for k, a := range args {
			if to, ok := map[string]string{docRepo: j.repo, docScratch: j.scratch}[a]; ok {
				args[k] = to
			}
		}
		for _, p := range onboarding.Compare(step, saw(t, nil, args...), norms) {
			t.Errorf("%s: %s", step.Line, p)
		}
	}
}
