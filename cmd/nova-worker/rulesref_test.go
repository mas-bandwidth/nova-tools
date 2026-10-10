package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// RULES BY REFERENCE (nova-tools#5174 rule 6): the member injects the rules into the card it
// hands the child (childCard), and the child reads what it read when the card carried them.

// refBrief is a card's own text, with no RULES paragraph.
const refBrief = "RESULT: c1 sha=0123456789ab\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n\n" +
	"THE TASK. Fix the empty case in internal/x/x.go.\nLibraries considered: the standard library's strings; nothing else fits.\n\n" +
	"STEP 1. Run go test -count=1 -timeout 600s ./internal/x/ and keep the failing line."

// The staged card a child is handed is byte for byte the same before rules by reference (the
// card carries fleet/child-rules.txt's paragraph at its end) and after (the stored card is
// its text alone, the member injects the paragraph the card names): for a work card and a
// read. A card that names nothing gets nothing injected: a card of another repository carrying
// its own rules is handed as it is, with one RULES paragraph. A card naming a file this
// build does not hold is refused.
func TestTheChildsCardIsUnchangedByRulesByReference(t *testing.T) {
	t.Parallel()
	rules, err := swarm.HeldRules(swarm.DefaultRulesName)
	require.NoError(t, err)
	carried := refBrief + "\n\n" + strings.TrimSuffix(swarm.RulesParagraph(rules), "\n")
	for _, kind := range []string{"work", "read"} {
		before := member.Packet{Card: "c1", Kind: kind, As: "m1", Primary: "c1", Stream: "s1", Attempt: 1, Gen: 1, Epoch: 3,
			Branch: "sprint/c1.g1.e3", Worker: "m2", Head: strings.Repeat("a", 40), WorkBranch: "sprint/c1.g1.e3", Brief: carried}
		want, err := childCard(before)
		require.NoError(t, err)
		assert.Equal(t, member.CardText(before), want, "%s: a card that carries its rules is handed as it is", kind)
		after := before
		after.Brief, after.Rules = refBrief, swarm.DefaultRulesName
		got, err := childCard(after)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%s: the child's card is unchanged", kind)
		before.Rules = swarm.DefaultRulesName
		got, err = childCard(before)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%s: a card that carries the recorded rules gets no second paragraph", kind)
	}
	schema := member.Packet{Card: "c2", Kind: "work", As: "m1", Primary: "c2", Stream: "s2", Attempt: 1, Gen: 1, Epoch: 3, Branch: "sprint/c2.g1.e3",
		Brief: strings.Replace(refBrief, "mas-bandwidth/nova-tools", "mas-bandwidth/schema", 1) + "\n\n" + swarm.ChildRulesParagraph()}
	got, err := childCard(schema)
	require.NoError(t, err)
	assert.Equal(t, member.CardText(schema), got, "no record: the card is handed as it is")
	assert.Equal(t, 1, strings.Count(got, "RULES."), "one RULES paragraph, the card's own")
	assert.NotContains(t, got, "GOCACHE", "no Go rule reaches a card of another repository")
	_, err = childCard(member.Packet{Card: "c1", Kind: "work", Brief: refBrief, Rules: "child-rules.absent.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "card c1: the rules by reference: this build holds no rules file child-rules.absent.txt")
}

// lint --member-injects lints the card as the member stages it: no rule it lacks is a
// finding, and a line that contradicts the rules still is.
func TestLintMemberInjectsHoldsOnlyWhatContradictsTheRules(t *testing.T) {
	t.Parallel()
	card := writeLintCard(t, "ref.card", refBrief+"\n")
	// the card's shape findings (the twelve rules of a bench card) are the lint's own; no
	// child rule is a finding
	_, stdout, stderr := runSwarm(t, "lint", "--card", card, "--member-injects", "--max", "0")
	assert.NotContains(t, stdout, " rule-", "a card without its rules, by reference:\n%s%s", stdout, stderr)
	assert.NotContains(t, stdout, " step-", "a card without its rules, by reference:\n%s%s", stdout, stderr)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card, "--child-rules-file", "../../fleet/child-rules.txt", "--max", "0")
	assert.NotEqual(t, 0, exit, "the same card held to carry the rules: exit %d\n%s", exit, stdout)
	assert.Contains(t, stdout, "rule-no-redis-server: 1: missing:", "the same card held to carry the rules:\n%s", stdout)
	good := writeLintCard(t, "good.card", lintGoodCard())
	exit, stdout, _ = runSwarm(t, "lint", "--card", good, "--member-injects", "--child-rules-file", "../../fleet/child-rules.txt", "--max", "0")
	assert.NotEqual(t, 0, exit, "a card that contradicts the rules: exit %d\n%s", exit, stdout)
	assert.Contains(t, stdout, "step-go-test-timeout: ", "a go test with no -timeout contradicts the rules:\n%s", stdout)
	assert.NotContains(t, stdout, "rule-no-redis-server", "no rule is missing by reference:\n%s", stdout)
	schema := writeLintCard(t, "schema.card", strings.Replace(refBrief, "mas-bandwidth/nova-tools", "mas-bandwidth/schema", 1)+"\n")
	exit, _, stderr = runSwarm(t, "lint", "--card", schema, "--member-injects")
	assert.Equal(t, 2, exit, "a card on a repository with no held file: exit %d\n%s", exit, stderr)
	assert.Contains(t, stderr, "so its card carries its own rules; run: nova-worker lint --card <file> --child-rules-file <file>")
}
