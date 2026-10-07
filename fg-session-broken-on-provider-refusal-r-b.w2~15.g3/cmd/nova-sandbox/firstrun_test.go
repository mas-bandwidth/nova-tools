package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/TESTS.md promises that every transcript line is real output pasted whole, and its
// `### First run` block is the page a stranger meets first. Field VALUES are the recording
// machine's and differ by platform, so they are deliberately not compared here. The field
// NAMES come from one format string at cmd/nova-sandbox/main.go:437 with no platform branch
// above it, so they are the same on every bench -- which is what makes the drift real and
// this test portable. `hosts=` was the field that went missing from the documented line.
func TestTheCheckTranscriptNamesEveryFieldTheVerbPrints(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	var documented string
	inSection := false
	for _, l := range strings.Split(string(doc), "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(l, "## ") {
			inSection = l == "## nova-sandbox"
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(trimmed, "CHECK OK ") {
			require.Empty(t, documented, "the nova-sandbox section has more than one CHECK OK line: %q and %q", documented, trimmed)
			documented = trimmed
		}
	}
	require.NotEmpty(t, documented, "TESTS.md has no CHECK OK transcript line to pin")

	var out, errb bytes.Buffer
	code := run([]string{"check"}, strings.NewReader(""), &out, &errb, os.Environ())
	require.Equal(t, 0, code, "nova-sandbox check exited %d, want 0", code)
	var printed string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "CHECK OK ") {
			require.Empty(t, printed, "nova-sandbox check printed more than one CHECK OK line: %q and %q", printed, l)
			printed = strings.TrimSpace(l)
		}
	}
	require.NotEmpty(t, printed, "nova-sandbox check printed no CHECK OK line")

	documentedFields := checkFieldNames(documented)
	printedFields := checkFieldNames(printed)
	missing := diffFieldNames(printedFields, documentedFields)
	extra := diffFieldNames(documentedFields, printedFields)
	assert.Equal(t, printedFields, documentedFields, "TESTS.md CHECK OK line does not name every field the verb prints\n documented: %q\n printed:    %q\n documented fields: %v\n printed fields:    %v\n missing from document: %v\n in document but not printed: %v",
		documented, printed, documentedFields, printedFields, missing, extra)
}

// checkFieldNames is the card's one rule: split the line on whitespace, and for each token
// matching ^[a-z0-9_-]+= take the text before the first '='. The class is wider
// than letters because the grammar holds hyphenated and digit names
// (read-noexec=, cwdb64=); a letters-only read drops exactly the mid-line
// insertion this pin exists to catch. It STOPS after taking `note`,
// because note's value is a sentence with spaces in it, so everything after it is prose.
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

// docs/TESTS.md's nova-sandbox transcript was recorded on a Mac, and the blocks
// underneath its Platform: line drifted from what that Mac prints: the PROBE OK
// block omits gpu=, and the SANDBOX OK block omits read-noexec=, ancestors= and
// gpu=. The CHECK OK pin above compares against a live run; these two lines
// cannot run portably here -- the wall they print is darwin-only -- so they are
// pinned against the format strings in main.go that print them, which have no
// platform branch above them. Field VALUES are the recording machine's and are
// not compared; field NAMES in order are.
func TestTheProbeAndSandboxTranscriptsNameEveryFieldTheVerbsPrint(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	documented := map[string]string{}
	inSection := false
	for _, l := range strings.Split(string(doc), "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(l, "## ") {
			inSection = l == "## nova-sandbox"
			continue
		}
		if !inSection {
			continue
		}
		for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
			if strings.HasPrefix(trimmed, prefix) {
				require.Empty(t, documented[prefix], "the nova-sandbox section has more than one %q line: %q and %q", prefix, documented[prefix], trimmed)
				documented[prefix] = trimmed
			}
		}
	}
	for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
		require.NotEmpty(t, documented[prefix], "TESTS.md has no %q transcript line to pin", prefix)
	}

	src, err := os.ReadFile("main.go")
	require.NoError(t, err)
	field := regexp.MustCompile(`^[a-z0-9_-]+=`)
	printed := map[string][]string{}
	for _, line := range strings.Split(string(src), "\n") {
		for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
			i := strings.Index(line, `"`+prefix)
			if i < 0 {
				continue
			}
			rest := line[i+1:]
			end := strings.Index(rest, `"`)
			require.GreaterOrEqual(t, end, 0, "main.go's %q format string is not closed on its line: %q", prefix, line)
			var names []string
			for _, tok := range strings.Fields(rest[:end]) {
				if !field.MatchString(tok) {
					continue
				}
				names = append(names, tok[:strings.Index(tok, "=")])
			}
			require.Empty(t, printed[prefix], "main.go prints more than one %q line; this test cannot say which one the transcript pins", prefix)
			printed[prefix] = names
		}
	}
	for _, prefix := range []string{"PROBE OK ", "SANDBOX OK "} {
		require.NotEmpty(t, printed[prefix], "main.go prints no %q line to pin the transcript to", prefix)
		documentedFields := checkFieldNames(documented[prefix])
		missing := diffFieldNames(printed[prefix], documentedFields)
		extra := diffFieldNames(documentedFields, printed[prefix])
		assert.Equal(t, printed[prefix], documentedFields, "TESTS.md %q line does not name every field the verb prints\n documented: %q\n documented fields: %v\n printed fields:    %v\n missing from document: %v\n in document but not printed: %v",
			prefix, documented[prefix], documentedFields, printed[prefix], missing, extra)
	}
}

// diffFieldNames returns the names in want that are absent from have, in want's order.
func diffFieldNames(want, have []string) []string {
	seen := make(map[string]bool, len(have))
	for _, n := range have {
		seen[n] = true
	}
	var missing []string
	for _, n := range want {
		if !seen[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// documentedStep extracts a single command and its expected output from a fenced
// code block in docs/CLI.md's ## nova-sandbox section.
func documentedStep(md, cmdPrefix string) (onboarding.Step, error) {
	section, ok := onboarding.Section(md, "nova-sandbox")
	if !ok {
		return onboarding.Step{}, fmt.Errorf("docs/CLI.md has no `## nova-sandbox` section")
	}
	fenced := false
	var blockLines []string
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "```") {
			if fenced {
				for len(blockLines) > 0 && strings.TrimSpace(blockLines[0]) == "" {
					blockLines = blockLines[1:]
				}
				if len(blockLines) > 0 && strings.HasPrefix(blockLines[0], cmdPrefix) {
					steps, err := onboarding.Steps("nova-sandbox", blockLines)
					if err != nil {
						return onboarding.Step{}, err
					}
					if len(steps) != 1 {
						return onboarding.Step{}, fmt.Errorf("block starting with %q has %d steps, want 1", cmdPrefix, len(steps))
					}
					return steps[0], nil
				}
			}
			fenced = !fenced
			blockLines = nil
			continue
		}
		if fenced {
			blockLines = append(blockLines, line)
		}
	}
	return onboarding.Step{}, fmt.Errorf("docs/CLI.md has no block starting with %q", cmdPrefix)
}

// TestTheCommandReferenceExamplesAreWhatTheToolPrints executes the documented
// examples from docs/CLI.md and compares their output through onboarding.Compare.
func TestTheCommandReferenceExamplesAreWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err)

	// Step 1: $ nova-sandbox check --bogus
	{
		step, err := documentedStep(string(raw), "$ nova-sandbox check --bogus")
		require.NoError(t, err, "documented step check --bogus: %v", err)
		var out, errb bytes.Buffer
		code := run(step.Args, strings.NewReader(""), &out, &errb, os.Environ())
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		for _, p := range onboarding.Compare(step, res, nil) {
			t.Errorf("check --bogus: %s", p)
		}
	}

	// Step 2: $ nova-sandbox bogus
	{
		step, err := documentedStep(string(raw), "$ nova-sandbox bogus")
		require.NoError(t, err, "documented step bogus: %v", err)
		var out, errb bytes.Buffer
		code := run(step.Args, strings.NewReader(""), &out, &errb, os.Environ())
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		for _, p := range onboarding.Compare(step, res, nil) {
			t.Errorf("bogus verb: %s", p)
		}
	}

	// Steps 3, 4, 5: $ nova-sandbox worktree ...
	wtLines, err := onboarding.Transcript(string(raw), "nova-sandbox", "worktree")
	require.NoError(t, err, "onboarding.Transcript worktree: %v", err)
	wtSteps, err := onboarding.Steps("nova-sandbox", wtLines)
	require.NoError(t, err, "onboarding.Steps worktree: %v", err)
	require.Len(t, wtSteps, 3, "worktree section has %d steps, want 3", len(wtSteps))

	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := "0123456789abcdef0123456789abcdef01234567"
	useForge(t, &fakeForge{byID: map[int]worktreePR{123: {Head: sha, Base: "main", State: "open"}}})

	oldGUID := worktreeGUID
	worktreeGUID = func() string { return "1f450ab70c635e66f675ff8a4e395760" }
	t.Cleanup(func() { worktreeGUID = oldGUID })

	docScratch := "/path/to/workdir/scratch"
	docRepo := "/path/to/workdir"

	localizeArgs := func(args []string) []string {
		runArgs := make([]string, len(args))
		for i, a := range args {
			switch a {
			case docRepo:
				runArgs[i] = j.repo
			case docScratch:
				runArgs[i] = j.scratch
			default:
				runArgs[i] = a
			}
		}
		return runArgs
	}

	// Step 3 (create): $ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --pr 123
	{
		step := wtSteps[0]
		var out, errb bytes.Buffer
		code := run(localizeArgs(step.Args), strings.NewReader(""), &out, &errb, nil)
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		norms := []onboarding.Norm{onboarding.Path(docScratch, j.scratch)}
		for _, p := range onboarding.Compare(step, res, norms) {
			t.Errorf("worktree create: %s", p)
		}
	}

	// Step 4 (reuse): $ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --pr 123
	{
		step := wtSteps[1]
		var out, errb bytes.Buffer
		code := run(localizeArgs(step.Args), strings.NewReader(""), &out, &errb, nil)
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		norms := []onboarding.Norm{onboarding.Path(docScratch, j.scratch)}
		for _, p := range onboarding.Compare(step, res, norms) {
			t.Errorf("worktree reuse: %s", p)
		}
	}

	// Step 5 (prune): $ nova-sandbox worktree --repo /path/to/workdir --scratch /path/to/workdir/scratch --prune
	{
		step := wtSteps[2]
		var out, errb bytes.Buffer
		code := run(localizeArgs(step.Args), strings.NewReader(""), &out, &errb, nil)
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		for _, p := range onboarding.Compare(step, res, nil) {
			t.Errorf("worktree prune: %s", p)
		}
	}
}
