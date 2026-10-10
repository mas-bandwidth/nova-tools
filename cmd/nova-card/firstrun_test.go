package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: the
// banner's examples are RUN, every refusal names its door, and docs/TESTS.md's
// first run is compared against what the tool prints, line for line.

func runCard(args ...string) (exit int, stdout, stderr string) {
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// sitting runs this binary's entry point with the documented arguments, the two
// documented paths mapped onto this test's own (the fixture is typed from the root of
// a checkout; ./cards is a directory the run makes) and mapped back in what the tool
// prints, so the comparator reads the documented spelling. No chdir, so the test runs
// in parallel (internal/ci/parallel_class_test.go).
func sitting(t *testing.T) func(args []string) onboarding.Result {
	t.Helper()
	cards := filepath.Join(t.TempDir(), "cards")
	stand := strings.NewReplacer("./cmd/nova-card/testdata", "testdata", "./cards", cards)
	back := strings.NewReplacer(cards, "./cards")
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = stand.Replace(a)
		}
		var out, errb bytes.Buffer
		code := run(local, &out, &errb)
		return onboarding.Result{Code: code, Stdout: back.Replace(out.String()), Stderr: back.Replace(errb.String())}
	}
}

// (a) A bare command refuses in one line and names its door.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runCard()
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Equal(t, 1, strings.Count(stderr, "\n"))
	assert.Contains(t, stderr, "run: nova-card help")
	exit, stdout, _ = runCard("help")
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, preAlpha)
	for _, verb := range []string{"generate", "lint", "template", "version"} {
		exit, stdout, stderr := runCard(verb, "-h")
		assert.Equal(t, 0, exit, "%s -h: %s", verb, stderr)
		assert.Contains(t, stdout, "effect: ", "%s -h names its effect", verb)
	}
}

// (b) The docs/TESTS.md first run is EXECUTED, every command in order, and its whole
// output compared with the block by the one comparator; the banner's example block
// is the same list of commands.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	// The banner's example lines, named here so the pasted-examples rule
	// (SPEC-TOOLWORK.md documents rule 6) reads the command text in this test;
	// the transcript runs the same lines.
	documentedExamples := []string{
		"nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards",
		"nova-card lint --card ./cards/finding-internal-bus-send.md",
		"nova-card lint --card ./cards/finding-cmd-nova-bus-main.md",
	}

	_, banner, _ := runCard("help")
	examples, err := onboarding.ExampleLines(banner, "nova-card")
	require.NoError(t, err)
	require.Equal(t, documentedExamples, examples, "the banner's examples and the ones this test names are one list")
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-card")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-card", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, strings.TrimPrefix(s.Line, "$ "))
	}
	require.Equal(t, documentedExamples, commands, "the transcript runs the banner's examples")
	run := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}

// A red brief is named on its LINT DRIFT line, and generate writes nothing while
// one brief is red.
func TestARedBriefIsNamedAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	card := filepath.Join(dir, "bad.md")
	require.NoError(t, os.WriteFile(card, []byte("RESULT: bad sha=0123456789ab tier: cheap\nKIND: fix-red\n\nTHE TASK. <fill>\n"), 0o644))
	exit, stdout, _ := runCard("lint", "--card", card)
	assert.Equal(t, 1, exit)
	assert.Contains(t, stdout, "LINT DRIFT card=bad check=model-lines line=1")
	assert.Contains(t, stdout, "check=placeholder")

	// a PATHS entry that names nothing in the checkout is a red line too
	repo := t.TempDir()
	findings := filepath.Join(dir, "f.tsv")
	require.NoError(t, os.WriteFile(findings, []byte("internal/none/x.go:1\tgone\tfix it\tinternal/none TestX\n"), 0o644))
	out := filepath.Join(dir, "cards")
	exit, stdout, stderr := runCard("generate", "--from", "findings", "--file", findings, "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out)
	assert.Equal(t, 1, exit, stderr)
	assert.Contains(t, stdout, "check=paths-at-base")
	assert.NoDirExists(t, out)

	// --dry-run plans and writes nothing
	dry := filepath.Join(dir, "dry")
	exit, stdout, stderr = runCard("generate", "--from", "findings", "--file", "testdata/findings.tsv", "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", dry, "--dry-run")
	assert.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "id\tfile\ttest\twave\tdeps\n")
	assert.Contains(t, stdout, "cards=2 waves=1 tier=pro dry-run=yes")
	assert.NoDirExists(t, dry)

	// an --out that already holds a brief is refused
	exit, _, stderr = runCard("generate", "--from", "findings", "--file", "testdata/findings.tsv", "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", dir)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "already holds")
}

func TestRepoOfURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "mas-bandwidth/nova-tools", repoOfURL("git@example.com:mas-bandwidth/nova-tools.git"))
	assert.Equal(t, "mas-bandwidth/nova-tools", repoOfURL("https://example.com/mas-bandwidth/nova-tools"))
	assert.Equal(t, "", repoOfURL("nonsense"))
}
