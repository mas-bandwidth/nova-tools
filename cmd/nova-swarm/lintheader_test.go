package main

import (
	"path/filepath"
	"strings"
	"testing"
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
	card := writeLintCard(t, "nokind.card", typedCardText(t,
		"PATHS: internal/swarm/lintheader.go",
		"TEST: internal/swarm TestCardHeaderMissingKindDrawsKindDeclared",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("a drifting card exits 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=nokind.card kind-declared:") {
		t.Fatalf("the drift names the card and the token:\n%s", stdout)
	}
	if !strings.Contains(stdout, "remedy=") || !strings.Contains(stdout, "KIND:") {
		t.Fatalf("the drift carries its remedy and says what the line should be:\n%s", stdout)
	}
}

// A card with no TEST: draws test-named; a card whose PATHS: climbs or names everywhere
// draws paths-declared.
func TestLintCardDrawsTestNamedAndPathsDeclared(t *testing.T) {
	card := writeLintCard(t, "bad.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: ../elsewhere/**, **",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--max", "0")
	if exit != 2 {
		t.Fatalf("a drifting card exits 2, got %d\n%s", exit, stdout)
	}
	for _, want := range []string{"test-named:", "paths-declared:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %s in:\n%s", want, stdout)
		}
	}
}

// NEGATIVE CONTROL: the same card, header complete and every value one the gate reads,
// is clean at exit 0.
func TestLintCardCompleteHeaderPasses(t *testing.T) {
	card := writeLintCard(t, "good.card", typedCardText(t,
		"KIND: fix-red",
		"PATHS: internal/swarm/lintheader.go, internal/swarm/lintheader_test.go",
		"TEST: internal/swarm TestCardHeaderFullHeaderDrawsNothing",
		"LEGS: go",
		"SOURCE: mas-bandwidth/nova-tools#1651",
	))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	if exit != 0 || !strings.Contains(stdout, "LINT OK card=good.card checks=") {
		t.Fatalf("a complete header is clean: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

// The paused token names the kind AND carries the `trust --set trial` command, because a
// rerun is not the remedy (SPEC-TOOLWORK §5 rule 1, §1's abstain list).
func TestLintCardPausedKindNamesTheTrialRemedy(t *testing.T) {
	trust := lintTrustFixture(t, strings.Join([]string{
		"TRUST kind=fix-red area=- state=paused cards=3/10 pass=0.33 need=0.80 run_of_fails=3/3 since=2026-09-19T14:00:00Z by=rowan",
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
	if exit != 2 {
		t.Fatalf("a paused kind is a drift, exit %d:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, "paused:") || !strings.Contains(stdout, "kind=fix-red") {
		t.Fatalf("the drift names the token and the kind:\n%s", stdout)
	}
	if !strings.Contains(stdout, "trust --set trial") {
		t.Fatalf("the remedy is the `trust --set trial` command:\n%s", stdout)
	}
}

// NEGATIVE CONTROL for paused: the same card against a fixture that has the kind on
// trial is clean, and so is the same card with no --trust at all -- no state is not a
// paused state.
func TestLintCardTrialKindAndNoFixtureAreClean(t *testing.T) {
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
		if exit != 0 {
			t.Fatalf("%v: a kind on trial is cut and launched: exit %d\nstdout: %s\nstderr: %s", args, exit, stdout, stderr)
		}
	}
}

// `--typed` requires the header of a card that carries none, which is what a card cut
// under §5 must have. Without it, the cards written before §5 are left to the twelve
// older rules -- the negative control is the good card of TestLintGoodCardPasses.
func TestLintCardTypedRequiresTheHeader(t *testing.T) {
	card := writeLintCard(t, "old.card", lintGoodCard())
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--typed", "--max", "0")
	if exit != 2 {
		t.Fatalf("--typed on a card with no header is a drift, exit %d:\n%s", exit, stdout)
	}
	for _, want := range []string{"kind-declared:", "paths-declared:", "test-named:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no %s in:\n%s", want, stdout)
		}
	}
	if exit, stdout, _ := runSwarm(t, "lint", "--card", card); exit != 0 {
		t.Fatalf("without --typed the older card is clean, exit %d:\n%s", exit, stdout)
	}
}

// An unreadable fixture is a refusal that names the flag, not a silent pass: a lint that
// quietly checked nothing would pass a paused kind.
func TestLintTrustFixtureMustBeReadable(t *testing.T) {
	card := writeLintCard(t, "c.card", typedCardText(t, "KIND: fix-red", "PATHS: none", "TEST: none"))
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--trust", filepath.Join(t.TempDir(), "absent.txt"))
	if exit != 2 {
		t.Fatalf("an unreadable --trust is refused at exit 2, got %d\n%s", exit, stdout)
	}
	if !strings.Contains(stderr, "--trust") {
		t.Fatalf("the refusal names the flag: %q", stderr)
	}
}

// `lint --rules` is the listing a bench with a stale clone reads (#1464), so the four new
// tokens are in it, each with its remedy.
func TestLintRulesNamesTheFourNewTokens(t *testing.T) {
	exit, stdout, _ := runSwarm(t, "lint", "--rules")
	if exit != 0 {
		t.Fatalf("`lint --rules` is a listing: exit %d", exit)
	}
	for _, want := range []string{"kind-declared", "paths-declared", "test-named", "paused"} {
		if !strings.Contains(stdout, "LINT RULE "+want+" remedy=") {
			t.Errorf("the listing does not name %s with a remedy:\n%s", want, stdout)
		}
	}
}
