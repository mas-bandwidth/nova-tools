package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A LINT THAT NAMES A RULE AND NO REMEDY COSTS A CARD WRITER A GUESS (issue #1464).
//
// Writing a card by hand for `native` on 2026-09-18, five drifts came back and three of them
// pointed at line 1 -- which is the contract line WORKER-CARDS.md practice 1 says line 1 must
// be. After a rewrite against the practices two survived and the writer could not tell what
// either wanted:
//
//	LINT DRIFT result-first: 1: fixed: row 5 writer_bound_count on go
//	LINT DRIFT scratch-absolute: 24: write; everything you clone, scratch and report goes under that absolute path.
//
// `nova-swarm help` says of every listing that it carries "one MORE line naming the remedy".
// `lint` named the rule, quoted the line and stopped. And the rule TOKENS are in no document
// a bench can reach: `WORKER-CARDS.md` (now in the nova-work-old repository) carries the practices in prose and names none of
// them, and a bench's clone of this repository is months behind its installed binary.
//
// So: every drift carries its remedy, and the binary can print the whole table on demand --
// `nova-swarm lint --rules` -- because a bench with a stale clone still has the binary.

// TestEveryDriftCarriesItsRemedy: the card of #1464, in its own words, and every drift it
// produces names what the rule wants. This is a class test: it does not name the five checks
// that card happened to trip, it asserts the property over every drift.
func TestEveryDriftCarriesItsRemedy(t *testing.T) {
	t.Parallel()

	// The card as it was written by hand, line 1 the contract line of a schema fix.
	body := strings.Join([]string{
		"# fixed: row 5 writer_bound_count on go",
		"Work in the job directory you were given: everything you clone, scratch and report",
		"goes under that absolute path.",
		"Change internal/codegen/gotable/fixedversioning_test.go and run the tests.",
		"Read ../notes.txt first, then nova-sandbox probe.",
		"",
	}, "\n")
	card := writeLintCard(t, "byhand.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	require.Equal(t, 1, exit, "a drifting card exits 1, got %d\nstdout: %s", exit, stdout)
	drifts := 0
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if !strings.HasPrefix(line, "LINT DRIFT ") {
			continue
		}
		drifts++
		if !assert.Contains(t, line, " remedy=", "a drift names the rule and no remedy, which is the whole of #1464:\n%s", line) {
			continue
		}
		remedy := line[strings.Index(line, " remedy=")+len(" remedy="):]
		assert.NotEmpty(t, strings.TrimSpace(remedy), "a drift's remedy is empty:\n%s", line)
	}
	require.NotZero(t, drifts, "this card drifts; the lint found nothing:\n%s", stdout)
}

// TestLintRulesPrintsEveryRuleAndItsRemedy: the second half of #1464. The rules are written
// down nowhere a bench can read -- its clone of this repository is months behind the binary
// it runs -- so the BINARY prints them. One line per rule, the token and what it wants, and
// the count agrees with the count the LINT OK line publishes, so a check cannot be added
// without its remedy.
func TestLintRulesPrintsEveryRuleAndItsRemedy(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runSwarm(t, "lint", "--rules")
	require.Equal(t, 0, exit, "`lint --rules` is a listing, not a refusal: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Empty(t, stderr, "a listing writes nothing to stderr: %q", stderr)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		rest, ok := strings.CutPrefix(line, "LINT RULE ")
		require.True(t, ok, "every line of the listing is one rule: %q", line)
		name, remedy, ok := strings.Cut(rest, " remedy=")
		if !ok || strings.TrimSpace(remedy) == "" {
			t.Errorf("a rule is listed with no remedy: %q", line)
			continue
		}
		seen[strings.TrimSpace(name)] = true
	}
	assert.Len(t, seen, cardLintChecks, "the listing holds %d rules and the LINT OK line publishes checks=%d; they are one set", len(seen), cardLintChecks)
	// The five tokens of #1464, by name: a card writer who met them on a bench must be able
	// to look every one of them up in the binary itself.
	for _, want := range []string{"result-first", "clone-step", "steps-numbered", "deadline", "result-last", "scratch-absolute"} {
		assert.True(t, seen[want], "the listing does not name the rule %s; it named %v", want, seen)
	}
}

// TestLintRulesNeedsNoCard: a card writer asking what the rules want has no card yet, which
// is exactly when the question is asked.
func TestLintRulesNeedsNoCard(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runSwarm(t, "lint", "--rules")
	require.Equal(t, 0, exit, "`lint --rules` answers without --card: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	require.Contains(t, stdout, "LINT RULE ", "`lint --rules` answers without --card: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
}
