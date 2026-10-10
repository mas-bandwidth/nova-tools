package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `lint --card` GAINS THE FOUR TOKENS OF SPEC-TOOLWORK §5 RULE 1 (issue #1651).
//
// The rules themselves are tested in internal/swarm/lintheader_test.go; these tests are
// the CLI's half: the token reaches the card writer on a LINT DRIFT line with its
// remedy, the exit code is the lint's usual 2, and the flags that turn the checks on
// behave. Every one was red before the flags existed -- `flag provided but not defined:
// -typed`, exit 2 with a usage refusal and no LINT line at all.

// typedCardText renders a card that passes the twelve older rules, with the typed header
// lines it is handed written under the contract line.
func typedCardText(t *testing.T, header ...string) string {
	t.Helper()
	good := strings.SplitN(lintGoodCard(), "\n", 2)
	body := good[0] + "\n"
	for _, h := range header {
		body += h + "\n"
	}
	return body + good[1]
}

func lintTrustFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "trust.txt")
	write(t, p, body)
	return p
}

// A card missing KIND: draws kind-declared, and the drift line carries the remedy the
// way every other token's does (#1464).
func TestLintCardMissingKindDrawsKindDeclared(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "nokind.card", typedCardText(t,
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderMissingKindDrawsKindDeclared",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a drifting card exits 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT DRIFT card=nokind.card kind-declared:", "the drift names the card and the token:\n%s", stdout)
	require.Contains(t, stdout, "remedy=", "the drift carries its remedy and says what the line should be:\n%s", stdout)
	require.Contains(t, stdout, "KIND:", "the drift carries its remedy and says what the line should be:\n%s", stdout)
}

// A card with no TEST: draws test-named; a card whose PATHS: climbs or names everywhere
// draws paths-declared.
func TestLintCardDrawsTestNamedAndPathsDeclared(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "bad.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: ../elsewhere/**, **",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--max", "0")
	require.Equal(t, 1, exit, "a drifting card exits 1, got %d\n%s", exit, stdout)
	for _, want := range []string{"test-named:", "paths-declared:"} {
		assert.Contains(t, stdout, want, "no %s in:\n%s", want, stdout)
	}
}

// KIND: report is a card kind. The wake-chain card carries it, and lint accepts
// that fixture. A nonsense kind is still a drift. `text` stays declared.
func TestLintCardAcceptsKindReportAndRefusesANonsenseKind(t *testing.T) {
	t.Parallel()

	report := writeLintCard(t, "report.card", typedCardText(t,
		"KIND: report",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestLintCardAcceptsKindReportAndRefusesANonsenseKind",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", report)
	require.Equal(t, 0, exit, "KIND: report is a kind lint accepts: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=report.card checks=", "KIND: report is a kind lint accepts: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.NotContains(t, stdout, "kind-declared", "KIND: report drew kind-declared:\n%s", stdout)

	text := writeLintCard(t, "text.card", typedCardText(t,
		"KIND: text",
		"PATHS: none",
		"TEST: none a text card changes no Go package",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr = runSwarm(t, "lint", "--card", text)
	require.Equal(t, 0, exit, "KIND: text stays accepted: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)

	bad := writeLintCard(t, "nonsense.card", typedCardText(t,
		"KIND: not-a-real-kind",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestLintCardAcceptsKindReportAndRefusesANonsenseKind",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr = runSwarm(t, "lint", "--card", bad)
	require.Equal(t, 1, exit, "a nonsense KIND is refused: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "kind-declared", "a nonsense KIND is refused: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "not-a-real-kind", "a nonsense KIND is refused: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}

// NEGATIVE CONTROL: the same card, header complete and every value one the gate reads,
// is clean at exit 0.
func TestLintCardCompleteHeaderPasses(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "good.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go, internal/swarm/lintheader_test.go",
		"TEST: internal/swarm TestCardHeaderFullHeaderDrawsNothing",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 0, exit, "a complete header is clean: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT OK card=good.card checks=", "a complete header is clean: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}

// The paused token names the kind AND carries the `trust --set trial` command, because a
// rerun is not the remedy (SPEC-TOOLWORK §5 rule 1, §1's abstain list).
func TestLintCardPausedKindNamesTheTrialRemedy(t *testing.T) {
	t.Parallel()

	trust := lintTrustFixture(t, strings.Join([]string{
		"TRUST kind=fix-red area=- state=paused cards=3/10 pass=0.33 need=0.80 run_of_fails=3/3 since=2026-09-19T14:00:00Z by=worker",
		"TRUST OK kinds=1 trial=0 trusted=0 paused=1",
		"",
	}, "\n"))
	card := writeLintCard(t, "paused.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderFullHeaderDrawsNothing",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--trust", trust)
	require.Equal(t, 1, exit, "a paused kind is a drift, exit %d:\n%s", exit, stdout)
	require.Contains(t, stdout, "paused:", "the drift names the token and the kind:\n%s", stdout)
	require.Contains(t, stdout, "kind=fix-red", "the drift names the token and the kind:\n%s", stdout)
	require.Contains(t, stdout, "trust --set trial", "the remedy is the `trust --set trial` command:\n%s", stdout)
}

// NEGATIVE CONTROL for paused: the same card against a fixture that has the kind on
// trial is clean, and so is the same card with no --trust at all -- no state is not a
// paused state.
func TestLintCardTrialKindAndNoFixtureAreClean(t *testing.T) {
	t.Parallel()

	trust := lintTrustFixture(t, "TRUST kind=fix-red area=- state=trial cards=0/10 pass=- need=0.80 run_of_fails=0/3 since=- by=-\n")
	card := writeLintCard(t, "trial.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderFullHeaderDrawsNothing",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	for _, args := range [][]string{
		{"lint", "--card", card, "--trust", trust},
		{"lint", "--card", card},
	} {
		exit, stdout, stderr := runSwarm(t, args...)
		require.Equal(t, 0, exit, "%v: a kind on trial is cut and launched: exit %d\nstdout: %s\nstderr: %s", args, exit, stdout, stderr)
	}
}

// `--typed` requires the header of a card that carries none, which is what a card cut
// under §5 must have. Without it, the cards written before §5 are left to the twelve
// older rules -- the negative control is the good card of TestLintGoodCardPasses.
func TestLintCardTypedRequiresTheHeader(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "old.card", lintGoodCard())
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--typed", "--max", "0")
	require.Equal(t, 1, exit, "--typed on a card with no header is a drift, exit %d:\n%s", exit, stdout)
	for _, want := range []string{"kind-declared:", "paths-declared:", "test-named:", "depends-on:"} {
		assert.Contains(t, stdout, want, "no %s in:\n%s", want, stdout)
	}
	exit, stdout, _ = runSwarm(t, "lint", "--card", card)
	require.Equal(t, 0, exit, "without --typed the older card is clean, exit %d:\n%s", exit, stdout)
}

// An unreadable fixture is a refusal that names the flag, not a silent pass: a lint that
// quietly checked nothing would pass a paused kind.
func TestLintTrustFixtureMustBeReadable(t *testing.T) {
	t.Parallel()

	card := writeLintCard(t, "c.card", typedCardText(t, "KIND: fix-red", "PATHS: none", "TEST: none"))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--trust", filepath.Join(t.TempDir(), "absent.txt"))
	require.Equal(t, 2, exit, "an unreadable --trust is refused at exit 2, got %d\n%s", exit, stdout)
	require.Contains(t, stderr, "--trust", "the refusal names the flag: %q", stderr)
}

// `lint --rules` is the listing a bench with a stale clone reads (#1464), so the four new
// tokens are in it, each with its remedy.
func TestLintRulesNamesTheFourNewTokens(t *testing.T) {
	t.Parallel()

	exit, stdout, _ := runSwarm(t, "lint", "--rules")
	require.Equal(t, 0, exit, "`lint --rules` is a listing: exit %d", exit)
	for _, want := range []string{"kind-declared", "paths-declared", "test-named", "paused"} {
		assert.Contains(t, stdout, "LINT RULE "+want+" remedy=", "the listing does not name %s with a remedy:\n%s", want, stdout)
	}
}

// THREE GRAMMAR FACTS THE CARDS LANE FOUND.

// `clone-step` READS THE STEP, NOT ONLY ITS FIRST LINE. The check matched the STEP 1
// line's own text, so a card whose STEP 1 reads "Get the tree" and whose body is a
// verbatim `git clone ... && cd repo` drew a drift for wording. The step is its line and
// everything under it up to the next STEP line, which is where the command actually is.
func TestLintCloneStepReadsTheWholeStepNotOnlyItsLine(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"You are a Go engineer. Work in $(pwd).",
		"STEP 1. Get the tree.",
		"    git clone -q \"$REPO_URL\" repo && cd repo && git log --oneline -1",
		"STEP 2. Write a red test named TestSomething and run go test ./internal/swarm/ against <job>/scratch.",
		"STEP 3. finish within 20 minutes.",
		"STEP 4. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, "prose-step.card", body)
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--max", "0")
	require.NotContains(t, stdout, "clone-step", "STEP 1's body clones and cds; the wording of its first line is not the rule:\n%s", stdout)
	require.Equal(t, 0, exit, "this card is clean, exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}

// NEGATIVE CONTROL for clone-step: a STEP 1 that neither clones nor cds anywhere in its
// body still draws the drift, and the message says what it wants rather than only that
// something is wrong.
func TestLintCloneStepStillDraftsAStepThatEntersNothing(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"STEP 1. Get the tree.",
		"    read the file and think about it",
		"STEP 2. Write a red test named TestSomething and run go test ./internal/swarm/ in <job>/scratch.",
		"STEP 3. finish within 20 minutes.",
		"STEP 4. Write RESULT.md.",
		"",
	}, "\n")
	card := writeLintCard(t, "nostep.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--max", "0")
	require.Equal(t, 1, exit, "a STEP 1 that enters no repository is a drift: exit %d\n%s", exit, stdout)
	require.Contains(t, stdout, "clone-step", "a STEP 1 that enters no repository is a drift: exit %d\n%s", exit, stdout)
	line := ""
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "clone-step") {
			line = l
		}
	}
	require.Contains(t, line, "git clone", "the drift names the wording it wants -- a `git clone` or a `cd` in the step:\n%s", line)
	require.Contains(t, line, "cd ", "the drift names the wording it wants -- a `git clone` or a `cd` in the step:\n%s", line)
}

// THE SIZE CEILING IS NOT A SILENT BOUND. A card writer learns of the 12000-byte cap
// today only by hitting it. Every lint says how big the card is and what the cap is, so
// a card at 11k is known to be at 11k.
func TestLintAlwaysNamesTheSizeAndTheCap(t *testing.T) {
	t.Parallel()

	clean := writeLintCard(t, "good.card", lintGoodCard())
	_, stdout, _ := runSwarm(t, "lint", "--card", clean)
	require.Contains(t, stdout, "bytes=", "the LINT OK line names the card's size and the ceiling:\n%s", stdout)
	require.Contains(t, stdout, "cap=12000", "the LINT OK line names the card's size and the ceiling:\n%s", stdout)
	drifting := writeLintCard(t, "byhand.card", "# not a contract line\nSTEP 2. do a thing\n")
	_, stdout, _ = runSwarm(t, "lint", "--card", drifting, "--max", "0")
	require.Contains(t, stdout, "LINT SIZE card=byhand.card bytes=", "a drifting card is told its size and the ceiling too:\n%s", stdout)
	require.Contains(t, stdout, "cap=12000", "a drifting card is told its size and the ceiling too:\n%s", stdout)
}
