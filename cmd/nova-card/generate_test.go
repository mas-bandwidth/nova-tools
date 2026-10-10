package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// fixtureCheckout is a one-commit repository on branch dev with an origin, the
// files written under it; git runs with a clean environment and a fixed identity.
func fixtureCheckout(t *testing.T, files map[string]string) (dir string, git func(args ...string) string) {
	t.Helper()
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(goenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	for rel, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	git("init", "-q", "-b", "dev")
	git("remote", "add", "origin", "git@example.com:example/repo.git")
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	return dir, git
}

// readCheckout reads the branch off a checkout's HEAD, and a detached HEAD names
// none, so generate refuses rather than writing BASE: HEAD into every card. The
// branch is read by git symbolic-ref, never by comparing a name to HEAD
// (pkg/typedrec TestOneTypedParser holds the tree to that).
func TestReadCheckoutReadsTheBranchAndNoneAtADetachedHEAD(t *testing.T) {
	t.Parallel()
	dir, git := fixtureCheckout(t, map[string]string{"a.txt": "a\n"})
	var h cardgen.Header
	require.NoError(t, readCheckout(dir, &h))
	assert.Equal(t, "dev", h.Base)
	assert.Equal(t, "example/repo", h.Repo)
	assert.Regexp(t, "^[0-9a-f]{40}$", h.Sha)

	git("checkout", "-q", "--detach")
	detached := cardgen.Header{}
	require.NoError(t, readCheckout(dir, &detached))
	assert.Empty(t, detached.Base, "a detached HEAD is no branch")
	assert.Equal(t, h.Sha, detached.Sha)
	exit, _, stderr := runCard("generate", "--from", "findings", "--file", "testdata/findings.tsv", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "cards"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "no base branch")
}

// A directory cut by --max is admitted as it is: no kept card needs a cut one.
func TestMaxLeavesNoNeedOnACutCard(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	ledger := cardgen.Ledgers["serial-tests"]
	for rel, text := range map[string]string{
		"cmd/a/a_test.go": "package main\n", "cmd/b/b_test.go": "package main\n", "cmd/c/c_test.go": "package main\n",
		"internal/ci/ci_test.go": "package ci\n", // the class test's package, a START package of every ledger card
		ledger.File:              "cmd/a/a_test.go:TestA serial: t.Setenv\ncmd/b/b_test.go:TestB serial: t.Chdir\ncmd/c/c_test.go:TestC serial: os.Setenv\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	exit, stdout, stderr := runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", filepath.Join(t.TempDir(), "cards"), "--max", "2", "--dry-run")
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "cards=2 waves=1 tier=flash dry-run=yes")
	assert.Contains(t, stdout, "serial-tests-cmd-b-b\tcmd/b/b_test.go\tinternal/ci TestEveryTestOpensWithTParallel\t1\t-\n")
	assert.NotContains(t, stdout, "serial-tests-cmd-c-c", "the cut card is named nowhere")
}

// --from help with one tool named twice is refused before anything is written: the
// two cards share one id, and one file cannot hold both.
func TestAToolNamedTwiceIsRefusedNotOverwritten(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture tool is a shell script")
	}
	bin := t.TempDir()
	require.NoError(t, testbin.WriteExecutable(filepath.Join(bin, "nova-x"), []byte("#!/bin/sh\necho 'nova-x: a fixture'\n"), 0o755))
	out := filepath.Join(t.TempDir(), "cards")
	exit, _, stderr := runCard("generate", "--from", "help", "--tool", "nova-x", "--tool", "nova-x", "--bin-dir", bin, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "card help-nova-x is planned twice")
	assert.NoDirExists(t, out)
	exit, stdout, stderr := runCard("generate", "--from", "help", "--tool", "nova-x", "--bin-dir", bin, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out)
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "cards=1 waves=1 tier=pro")
}

// With a checkout, a help card's TEST is read off the tool's package, not assumed:
// the test that runs the examples where there is one, the one to write where not.
func TestAHelpCardReadsItsTestOffTheCheckout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture tool is a shell script")
	}
	bin := t.TempDir()
	for _, tool := range []string{"nova-x", "nova-y"} {
		require.NoError(t, testbin.WriteExecutable(filepath.Join(bin, tool), []byte("#!/bin/sh\necho '"+tool+": a fixture'\n"), 0o755))
	}
	repo := t.TempDir()
	for rel, text := range map[string]string{
		"cmd/nova-x/main.go":          "package main\n",
		"cmd/nova-x/firstrun_test.go": "package main\n\nfunc TestUsageBannerExamplesRun(t *testing.T) {\n\tonboarding.ExampleLines(banner, \"nova-x\")\n}\n",
		"cmd/nova-y/main.go":          "package main\n",
		"cmd/nova-y/y_test.go":        "package main\n",
		"docs/CLI.md":                 "# CLI\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	exit, stdout, stderr := runCard("generate", "--from", "help", "--tool", "nova-x", "--tool", "nova-y", "--bin-dir", bin, "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", filepath.Join(t.TempDir(), "cards"), "--dry-run")
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "help-nova-x\tcmd/nova-x/main.go\tcmd/nova-x TestUsageBannerExamplesRun\t1\t-\n")
	assert.Contains(t, stdout, "help-nova-y\tcmd/nova-y/main.go\tcmd/nova-y TestHelpExampleLinesRunAsPrinted\t1\t-\n")
}

// A findings card on a package with no test file is generated, not refused: its test
// glob names nothing at the base because the card creates the file, said on NEW:.
func TestAPackageWithNoTestFileIsGeneratedWithItsNEWLine(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "internal", "none"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "internal", "none", "x.go"), []byte("package none\n"), 0o644))
	findings := filepath.Join(t.TempDir(), "f.tsv")
	require.NoError(t, os.WriteFile(findings, []byte("internal/none/x.go:1\twrong\tfix it\t\n"), 0o644))
	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "findings", "--file", findings, "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	brief, err := os.ReadFile(filepath.Join(out, "finding-internal-none-x.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief), "\nPATHS: internal/none/*.go, internal/none/*_test.go\nNEW: internal/none/x_test.go\n")
}

// The banner names every verb on a usage line, and every generate line names the
// flags every source takes (--max, --dry-run): a flag a usage line leaves out is a
// flag a stranger cannot find.
func TestTheBannerNamesEveryVerbAndTheGenerateFlags(t *testing.T) {
	t.Parallel()
	_, banner, _ := runCard("help")
	for _, verb := range verbs {
		assert.Contains(t, banner, "\n  nova-card "+verb, "no usage line for %s", verb)
	}
	// the usage block: the lines under "usage:" up to the next blank line
	_, block, _ := strings.Cut(banner, "\nusage:\n")
	block, _, _ = strings.Cut(block, "\n\n")
	lines := 0
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "  nova-card generate ") {
			continue
		}
		lines++
		for _, flag := range []string{"[--max <n>]", "[--dry-run]", "[--tier flash|pro]"} {
			assert.Contains(t, line, flag, "a generate usage line without %s", flag)
		}
	}
	assert.Equal(t, 3, lines, "one usage line per source")
}

// generate holds every brief to the card checks nova-sprint add runs before it leaves:
// a name --name gives, carried in from a source row, is a red line and nothing is
// written; lint holds a brief that names a --dropped card the same.
func TestGenerateRefusesABriefWithTheCardChecksFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	findings := filepath.Join(dir, "f.tsv")
	require.NoError(t, os.WriteFile(findings, []byte("pkg/bus/send.go:1\tthe receipt ada filed is lost\tkeep it\tinternal/bus TestX\n"), 0o644))
	out := filepath.Join(dir, "cards")
	args := []string{"generate", "--from", "findings", "--file", findings, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out}
	exit, stdout, stderr := runCard(append(args, "--name", "bench7,ada")...)
	assert.Equal(t, 1, exit, stderr)
	assert.Contains(t, stdout, "LINT DRIFT card=finding-internal-bus-send check=personal-name")
	assert.NoDirExists(t, out)
	exit, stdout, stderr = runCard(args...)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	brief := filepath.Join(out, "finding-internal-bus-send.md")
	assert.FileExists(t, brief)
	exit, stdout, _ = runCard("lint", "--card", brief, "--dropped", "old-card.w1")
	assert.Equal(t, 0, exit, stdout)
	raw, err := os.ReadFile(brief)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(brief, append(raw, []byte("Carry old-card.w1 over.\n")...), 0o644))
	exit, stdout, _ = runCard("lint", "--card", brief, "--dropped", "old-card.w1")
	assert.Equal(t, 1, exit)
	assert.Contains(t, stdout, "check=dropped-card")
}

// A card whose PATHS name TLA+ model work is generated frontier, as nova-sprint add tiers it
// (sprint.ModelTier; docs/SPEC-SPRINT.md, the card decides its model): the source's own tier
// gives way, a card on the run records alone keeps it, and an explicit --tier below frontier
// is a red line naming the reason, nothing written.
func TestAGeneratedCardThatWritesAModelIsTieredFrontier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	findings := filepath.Join(dir, "f.tsv")
	require.NoError(t, os.WriteFile(findings, []byte(
		"tla/Lease.tla:3\tthe lease can be held twice\tadd the invariant\tinternal/x TestX\n"+
			"tla/RUNS.tsv:1\tthe run record has no date\tadd the date\tinternal/x TestY\n"), 0o644))
	out := filepath.Join(dir, "cards")
	args := []string{"generate", "--from", "findings", "--file", findings, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", out}
	exit, stdout, stderr := runCard(args...)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "cards=2 waves=1 tier=pro frontier=1")
	raw, err := os.ReadFile(filepath.Join(out, "finding-tla-lease-tla.md"))
	require.NoError(t, err)
	line1, _, _ := strings.Cut(string(raw), "\n")
	assert.True(t, strings.HasSuffix(line1, " tier: frontier"), line1)
	raw, err = os.ReadFile(filepath.Join(out, "finding-tla-runs-tsv.md"))
	require.NoError(t, err)
	line1, _, _ = strings.Cut(string(raw), "\n")
	assert.True(t, strings.HasSuffix(line1, " tier: pro"), "run records alone are no model: %s", line1)

	out2 := filepath.Join(dir, "cards2")
	exit, stdout, _ = runCard(append(args[:len(args)-1], out2, "--tier", "pro")...)
	assert.Equal(t, 1, exit, stdout)
	assert.Contains(t, stdout, "LINT DRIFT card=finding-tla-lease-tla check=model-tier line=1: PATHS name TLA+ model work (tla/Lease.tla)")
	assert.Contains(t, stdout, "line 1 names tier pro")
	assert.NotContains(t, stdout, "card=finding-tla-runs-tsv check=model-tier")
	assert.NoDirExists(t, out2)
}

// TestTheUsageBannerPrintsEachExampleOnce verifies that each example line appears
// exactly once in the help output for the generate command.
func TestTheUsageBannerPrintsEachExampleOnce(t *testing.T) {
	t.Parallel()
	// Get the main help output
	_, banner, _ := runCard("help")

	// Check that concrete example paths appear exactly once in the example block
	exampleLines := []string{
		"./cmd/nova-card/testdata/findings.tsv",
		"./cards/finding-internal-bus-send.md",
		"./cards/finding-cmd-nova-bus-main.md",
	}

	for _, line := range exampleLines {
		count := strings.Count(banner, line)
		assert.Equal(t, 1, count, "example line should appear exactly once, found %d times: %s", count, line)
	}
}
