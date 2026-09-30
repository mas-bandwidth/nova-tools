package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// EVERY RULE THE COORDINATOR GIVES A CHILD IS A RULE OF `lint --card --child-rules`.
//
// A card written for a bench worker under the twelve shape rules carries none of the child
// rules and lints as it always has; a card written for a child of the coordinator is held
// to all of them when `--child-rules` says so (and always by `nova-sprint add`).

// filledLibraries replaces the placeholder of the card template's Libraries considered
// line with what a writer would say, the state of a finished card.
func filledLibraries(body string) string {
	return regexp.MustCompile(`(?m)^Libraries considered:.*$`).ReplaceAllString(body, "Libraries considered: testify for the asserts, used; nothing else found for this work")
}

// `template --name card` prints a card that passes the lint as printed: the twelve shape
// rules, the typed-header rules and every child rule. Under a rules file carrying
// [libraries-considered] its placeholder line is the one finding until filled. It is linted
// for real (the verbatim template shortcut of the card templates is not taken for it).
func TestCardTemplateLintsCleanWithTheChildRules(t *testing.T) {
	t.Parallel()
	exit, printed, stderr := runSwarm(t, "template", "--name", "card")
	if exit != 0 || stderr != "" {
		t.Fatalf("template --name card: exit %d, stderr %q", exit, stderr)
	}
	libs := filepath.Join(t.TempDir(), "libs.txt")
	require.NoError(t, os.WriteFile(libs, []byte("[libraries-considered] A card that builds code carries a filled Libraries considered line.\n"), 0o600))
	exit, stdout, _ := runSwarm(t, "lint", "--card", writeLintCard(t, "printed.md", printed), "--child-rules-file", libs, "--max", "0")
	assert.Equal(t, 1, exit)
	assert.Contains(t, stdout, "rule-libraries-considered: ")
	assert.Equal(t, 1, strings.Count(stdout, "LINT DRIFT "))
	body := filledLibraries(printed)
	card := writeLintCard(t, "card.md", body)
	for _, args := range [][]string{{"--card", card}, {"--card", card, "--child-rules"}} {
		exit, stdout, _ := runSwarm(t, append([]string{"lint"}, args...)...)
		if exit != 0 || !strings.HasPrefix(stdout, "LINT OK card=card.md checks=") {
			t.Fatalf("lint %v: exit %d\n%s", args, exit, stdout)
		}
	}
	for _, r := range swarm.DefaultChildRules {
		if !strings.Contains(body, r.Sentence) {
			t.Errorf("the printed card does not quote %s", r.Name)
		}
	}
}

// Without the flag a worker card is linted as before; with it the same card is refused
// naming each missing rule and its remedy, and the OK card stays exit 0.
func TestChildRulesAreAskedForByTheFlag(t *testing.T) {
	t.Parallel()
	card := writeLintCard(t, "good.card", lintGoodCard())
	if exit, stdout, _ := runSwarm(t, "lint", "--card", card); exit != 0 {
		t.Fatalf("a worker card lints clean without the flag: exit %d\n%s", exit, stdout)
	}
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--child-rules", "--max", "0")
	if exit != 1 {
		t.Fatalf("the same card under --child-rules drifts at exit 1, got %d\n%s", exit, stdout)
	}
	drifts := 0
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "LINT DRIFT card=good.card rule-") || strings.HasPrefix(line, "LINT DRIFT card=good.card step-") {
			drifts++
			if !strings.Contains(line, " remedy=") {
				t.Errorf("a child-rule drift names no remedy: %s", line)
			}
		}
	}
	// every general rule is missing; the card's `go test` without -timeout is no scan hit,
	// for the general set carries no Go rule and no libraries check
	if want := len(swarm.DefaultChildRules); drifts != want {
		t.Errorf("%d child-rule drifts, want %d (every general rule)\n%s", drifts, want, stdout)
	}
	if !strings.Contains(stdout, "rule-no-server: 1: missing: Never start a server on this machine.") {
		t.Errorf("the missing rule is named with its sentence:\n%s", stdout)
	}
	if strings.Contains(stdout, "step-go-test-timeout") {
		t.Errorf("the general rules scan a go test they have no rule for:\n%s", stdout)
	}
}

// `--child-rules-file` holds the card to the coordinator's own sentences: this
// repository's file requires its rules (and switches on its Go scans), and a file of other
// sentences requires those, each refusal naming the rule and quoting its sentence.
func TestChildRulesFileIsTheCoordinatorsRuleSet(t *testing.T) {
	t.Parallel()
	card := writeLintCard(t, "good.card", lintGoodCard())
	ours := "../../fleet/child-rules.txt"
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--child-rules-file", ours, "--max", "0")
	if !strings.Contains(stdout, "rule-no-redis-server: 1: missing: NEVER start a redis-server on this machine.") ||
		!strings.Contains(stdout, "step-go-test-timeout: 5:") || !strings.Contains(stdout, "remedy=the card quotes this rule verbatim") || exit == 0 {
		t.Errorf("this repository's file: exit %d\n%s", exit, stdout)
	}
	mine := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(mine, []byte("# mine\n[ticket] Quote the ticket number.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exit, stdout, _ = runSwarm(t, "lint", "--card", card, "--child-rules-file", mine)
	if exit == 0 || !strings.Contains(stdout, "rule-ticket: 1: missing: Quote the ticket number.") || strings.Count(stdout, "LINT DRIFT card=good.card rule-") != 1 {
		t.Errorf("a one-rule file: exit %d\n%s", exit, stdout)
	}
	withRule := writeLintCard(t, "ticket.card", lintGoodCard()+"\nRULES.\nQuote the ticket number.\n")
	if exit, stdout, _ = runSwarm(t, "lint", "--card", withRule, "--child-rules-file", mine); exit != 0 || !strings.Contains(stdout, "LINT OK") {
		t.Errorf("a card carrying the file's sentence: exit %d\n%s", exit, stdout)
	}
	exit, _, stderr := runSwarm(t, "lint", "--card", card, "--child-rules-file", filepath.Join(t.TempDir(), "absent.txt"))
	if exit != 2 || !strings.Contains(stderr, "--child-rules-file wants a readable file of one required sentence per line") {
		t.Errorf("an unreadable file: exit %d, %q", exit, stderr)
	}
}

// A scan check on the command line: a card that runs a forbidden command draws the step
// token at its line.
func TestChildScanThroughTheCommand(t *testing.T) {
	t.Parallel()
	_, body, _ := runSwarm(t, "template", "--name", "card")
	card := writeLintCard(t, "bad.card", filledLibraries(body)+"STEP 7. git push --force origin HEAD && git stash\n")
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--child-rules", "--max", "0")
	if exit != 1 || !strings.Contains(stdout, "LINT DRIFT card=bad.card step-force-push: ") || !strings.Contains(stdout, "LINT DRIFT card=bad.card step-stash: ") {
		t.Fatalf("exit %d\n%s", exit, stdout)
	}
}

// `--rules` prints every child rule and scan with its remedy, so a bench with a stale
// clone reads the rules from the binary; the sentence is in the rule's remedy.
func TestLintRulesListsTheChildRules(t *testing.T) {
	t.Parallel()
	exit, stdout, _ := runSwarm(t, "lint", "--rules")
	if exit != 0 {
		t.Fatalf("exit %d", exit)
	}
	for _, r := range swarm.DefaultChildRules {
		if !strings.Contains(stdout, "LINT RULE rule-"+r.Name+" remedy=") {
			t.Errorf("--rules does not list rule-%s", r.Name)
		}
	}
	for _, name := range []string{"step-redis-server", "step-go-clean", "step-kill", "step-rm-rf", "step-force-push", "step-rebase", "step-stash", "step-merge", "step-go-test-timeout"} {
		if !strings.Contains(stdout, "LINT RULE "+name+" remedy=") {
			t.Errorf("--rules does not list %s", name)
		}
	}
}

// The cards the shift actually wrote still lint as they did, flag or no flag: the child
// rules are a second reading of the card a child of the coordinator is handed, not a
// change to the bench cards' verdicts.
func TestFixtureCardsKeepTheirVerdictsWithoutTheFlag(t *testing.T) {
	t.Parallel()
	stdout, code := lintCardFile(t, "queue-1282-bench-hygiene-home-guard.md")
	if code != 0 || !strings.Contains(stdout, "LINT OK") {
		t.Fatalf("the control card stays clean without --child-rules: exit %d\n%s", code, stdout)
	}
}
