package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// `nova-worker lint --card` is the mechanical shape check that runs BEFORE any spend. A card
// defect is paid for in tokens, so the lint names it for free: the wrong clone step, a step
// out of order, no red test, no test command, no deadline, a file or package that is never
// named, a `../scratch` the wall refuses, a `nova-sandbox` probe, no final RESULT.md, an
// oversize card. These tests are red against the tree before the verb exists: `lint` is an
// unknown subcommand at exit 2 and prints nothing on stdout.

// lintGoodCard is a card that passes every check.
func lintGoodCard() string {
	return strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"You are a Go engineer. Work in $(pwd).",
		"STEP 1. pwd && { [ -d repo ] || git clone -q https://github.com/mas-bandwidth/nova-tools.git repo; } && cd repo && git log --oneline -1",
		"STEP 2. Read docs/SPEC-WORKER.md first.",
		"STEP 3. Write a red test named TestCardLintPasses, run go test ./internal/swarm/, and record the failing output in <working directory>/scratch/red.txt using that absolute path.",
		"STEP 4. Run: export TMPDIR=\"$PWD/scratch\" && go test ./internal/swarm/ ./cmd/nova-worker/",
		"STEP 5. finish within 20 minutes.",
		"STEP 6. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
}

// writeLintCard writes one card under t.TempDir() and returns its path.
func writeLintCard(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	write(t, path, body)
	return path
}

func TestLintGoodCardPasses(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "good.card", lintGoodCard())
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 0, exit, "a good card lints clean at exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=good.card checks=", "the OK line names the card and the checks: %q", stdout)
	lines := strings.Count(strings.TrimSuffix(stdout, "\n"), "\n") + 1
	require.Equal(t, 1, lines, "LINT OK is one line, got %d:\n%s", lines, stdout)
	require.Empty(t, stderr, "a clean lint writes nothing to stderr: %q", stderr)
}

// A card that writes its scratch above the job is the defect that killed cards tonight: the
// `../scratch` is refused by the wall, and the lint names the line before any spend.
func TestLintNamesTheParentPathLine(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"RESULT: CARD-1111 do the thing",
		"STEP 1. pwd && git clone -q https://github.com/mas-bandwidth/nova-tools.git repo && cd repo",
		"STEP 2. Write a red test named TestThing and record the failing output in ../scratch/red.txt.",
		"STEP 3. Run: go test ./internal/swarm/",
		"STEP 4. finish within 20 minutes.",
		"STEP 5. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, "parent.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a card with a ../ path drifts at exit 1, got %d\nstdout: %s", exit, stdout)
	require.Contains(t, stdout, "LINT DRIFT card=parent.card no-parent-path: 3:", "the no-parent-path finding names check, line and card: %q", stdout)
	require.Contains(t, stdout, "../scratch/red.txt", "the finding quotes the offending line: %q", stdout)
}

// A card that names no red test is the defect the work order says to catch: the check name
// itself is the answer, so the reader does not have to find the missing piece.
func TestLintNamesTheMissingRedTest(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"RESULT: CARD-2222 change a constant",
		"STEP 1. pwd && git clone -q https://github.com/mas-bandwidth/nova-tools.git repo && cd repo",
		"STEP 2. Open internal/thing.go and change the constant 3 to 4.",
		"STEP 3. Run: go test ./internal/thing/",
		"STEP 4. finish within 10 minutes.",
		"STEP 5. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, "nored.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a card with no red test drifts at exit 1, got %d\nstdout: %s", exit, stdout)
	require.Contains(t, stdout, "LINT DRIFT card=nored.card red-test:", "the red-test finding names the check: %q", stdout)
}

// A card that reaches for the sandbox is a probe this tool does not run, and its defect is
// named as plainly as the others.
func TestLintRefusesNovaSandbox(t *testing.T) {
	t.Parallel()

	body := strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-WORKER.md first.",
		"STEP 2. nova-sandbox probe --secret /root/.ssh/id_rsa", 1)
	card := writeLintCard(t, "sandbox.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a card invoking nova-sandbox drifts at exit 1, got %d\nstdout: %s", exit, stdout)
	require.Contains(t, stdout, "LINT DRIFT card=sandbox.card no-sandbox: 4:", "the no-sandbox finding names check and line: %q", stdout)
}

// issue #1471: a new friend who follows the help (`nova-worker template --name <t>` then
// `nova-worker lint --card`) meets a result-first refusal before writing a word. Every card
// template the tool ships must lint clean, and a template that is not a card (result, worker,
// setup, capacity) must answer by name rather than as a result-first drift.
func TestTemplateThenLintPasses(t *testing.T) {
	t.Parallel()

	cardTemplates := map[string]bool{
		"read-pr":   true,
		"probe-row": true,
		"fix-card":  true,
		"card":      true,
	}
	for _, name := range swarm.TemplateNames() {
		t.Run(name, func(t *testing.T) {
			exit, body, stderr := runSwarm(t, "template", "--name", name)
			require.Equal(t, 0, exit, "template --name %s exited %d: %s", name, exit, stderr)
			card := writeLintCard(t, name+".md", body)
			exit, stdout, _ := runSwarm(t, "lint", "--card", card)
			if cardTemplates[name] {
				require.Equal(t, 0, exit, "the %s card template lints clean at exit 0, got %d\nstdout: %s", name, exit, stdout)
				require.Contains(t, stdout, "LINT OK", "the %s card template reports LINT OK: %q", name, stdout)
				return
			}
			require.NotContains(t, stdout, "result-first", "the %s template is not a card; lint says so by name, not as a result-first drift: %q", name, stdout)
			require.Contains(t, stdout, "not a card", "the %s template gets a named not-a-card answer: %q", name, stdout)
			require.Contains(t, stdout, name, "the %s template gets a named not-a-card answer: %q", name, stdout)
		})
	}
}

// A card over the ceiling is ADVISED and never refused (issues #1494, #1527). This test
// wanted exit 2 and a `LINT DRIFT` until 2026-09-19, which is the reading that made two
// managers trim good cards to reach a number while two others shipped over it on purpose.
// The ceiling is a reading budget, not an input limit -- a 12422-byte card was measured
// through the harness untruncated -- so the finding says how big, says `advisory`, and
// leaves the verdict alone.
func TestLintAdvisesAnOversizeCardAndDoesNotRefuseIt(t *testing.T) {
	t.Parallel()

	body := lintGoodCard() + "RESULT: padding " + strings.Repeat("x", 12000) + "\n"
	card := writeLintCard(t, "big.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 0, exit, "an oversize card is advice, not a defect, so the lint exits 0, got %d\nstdout: %s", exit, stdout)
	require.Contains(t, stdout, "LINT NOTE card=big.card size:", "the size finding is a NOTE naming the check: %q", stdout)
	require.NotContains(t, stdout, "LINT DRIFT card=big.card size:", "the size finding is never a DRIFT, which is the line a caller refuses on: %q", stdout)
	require.Contains(t, stdout, "advisory", "the word a manager needs is in the line: advisory, not a limit: %q", stdout)
	require.Contains(t, stdout, "bytes=", "a clean card still carries its size and the cap: %q", stdout)
	require.Contains(t, stdout, "cap=12000", "a clean card still carries its size and the cap: %q", stdout)
	// the note says where the refusal is: nova-sprint add holds a brief to 16384 bytes
	assert.Contains(t, stdout, "nova-sprint add refuses a brief over 16384 bytes")
}

// The lint's two numbers are internal/cardlimits': 12000 is the advice, and the bound the
// note names is the one nova-sprint add enforces (the store reads it there too), above the
// advice.
func TestTheSizeNoteAgreesWithTheSprintsBriefBound(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 16384, cardRefusedBytes, "the note names the bound the sprint enforces")
	assert.Equal(t, 12000, cardMaxBytes, "the advice")
	assert.Less(t, cardMaxBytes, cardRefusedBytes, "the advice sits under the bound")
}

// A card that is BOTH over the ceiling and drifting is refused for the drift alone, and the
// closing size line answers the advisory question in the bytes.
func TestAnOversizeDriftingCardIsRefusedForTheDriftAndSaysTheCeilingIsAdvisory(t *testing.T) {
	t.Parallel()

	body := lintGoodCard() + "STEP 6. cd ../elsewhere\n" + "RESULT: padding " + strings.Repeat("x", 12000) + "\n"
	card := writeLintCard(t, "bigdrift.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a card that walks above the job is refused, got %d\nstdout: %s", exit, stdout)
	require.Contains(t, stdout, "LINT DRIFT card=bigdrift.card no-parent-path:", "the drift it is refused for is named: %q", stdout)
	require.NotContains(t, stdout, "LINT DRIFT card=bigdrift.card size:", "the size is still never a DRIFT, even on a card that has one: %q", stdout)
	require.Contains(t, stdout, "LINT SIZE card=bigdrift.card bytes=", "the closing size line says advisory=true: %q", stdout)
	require.Contains(t, stdout, "advisory=true", "the closing size line says advisory=true: %q", stdout)
}

// The no-sandbox rule is an INVOCATION of nova-sandbox, a command line, never the word: a
// card about the tool's own help names it in a sentence, a path and a possessive, and could
// never pass before (the night of 2026-10-03, friction 13).
func TestLintPassesACardAboutNovaSandboxItself(t *testing.T) {
	t.Parallel()

	body := strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-WORKER.md first.",
		"STEP 2. Read cmd/nova-sandbox/help.go: nova-sandbox's help must name every verb, and make `nova-sandbox help` print them in order.", 1)
	card := writeLintCard(t, "about-sandbox.card", body)
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 0, exit, "a card about nova-sandbox lints clean, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=about-sandbox.card", "%q", stdout)
}

// An invocation at any command position drifts: first on the step, after a shell separator,
// after an imperative run.
func TestLintNamesEveryNovaSandboxCommandLine(t *testing.T) {
	t.Parallel()

	for _, step := range []string{
		"STEP 2. nova-sandbox probe --secret /root/.ssh/id_rsa",
		"STEP 2. cd repo && nova-sandbox run -- go test ./...",
		"STEP 2. Run: nova-sandbox check",
		"STEP 2. Then run `nova-sandbox probe` and read its output.",
		"STEP 2. $ nova-sandbox",
	} {
		body := strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-WORKER.md first.", step, 1)
		card := writeLintCard(t, "sandbox.card", body)
		exit, stdout, _ := runSwarm(t, "lint", "--card", card)
		assert.Equal(t, 1, exit, "%q: exit %d\nstdout: %s", step, exit, stdout)
		assert.Contains(t, stdout, "LINT DRIFT card=sandbox.card no-sandbox: 4:", "%q: %q", step, stdout)
	}
}

// `lint <file>` is `lint --card <file>`: every child tried the positional first (friction
// 12). Two files, or both forms, are refused.
func TestLintTakesTheCardFileAsAPositional(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "good.card", lintGoodCard())
	exit, stdout, stderr := runSwarm(t, "lint", card)
	require.Equal(t, 0, exit, "lint <file>: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=good.card checks=", "%q", stdout)
	exit, _, stderr = runSwarm(t, "lint", card, "--typed")
	require.Equal(t, 1, exit, "the flags after the file still apply: exit %d\nstderr: %s", exit, stderr)
	exit, _, stderr = runSwarm(t, "lint", "--card", card, card)
	require.Equal(t, 2, exit, "both forms: exit %d", exit)
	require.Contains(t, stderr, "name two files; give one", "%q", stderr)
	exit, _, stderr = runSwarm(t, "lint", card, card)
	require.Equal(t, 2, exit, "two files: exit %d", exit)
	require.Contains(t, stderr, "takes one positional argument, the --card file", "%q", stderr)
}
