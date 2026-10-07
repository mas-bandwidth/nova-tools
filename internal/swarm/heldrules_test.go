package swarm

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RULES BY REFERENCE (heldrules.go, nova-tools#5174 rule 6): the member holds the rules
// files of its build and appends one to every card at stage time; the card does not carry it.

// refCard is a card's own text, with no RULES paragraph: a header naming its repository, a
// task, and a step that runs go test with its timeout.
const refCard = "RESULT: c1 sha=0123456789ab\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n\n" +
	"THE TASK. Fix the empty case in internal/x/x.go.\nLibraries considered: the standard library's strings; nothing else fits.\n\n" +
	"STEP 1. Run go test -count=1 -timeout 600s ./internal/x/ and keep the failing line."

// The held copy of fleet/child-rules.txt is the file, byte for byte, and parses as the file
// does: what the member injects is what the repository says.
func TestTheHeldRulesAreTheFleetFile(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile(ourRulesPath)
	require.NoError(t, err)
	got, ok := HeldRulesText(DefaultRulesName)
	require.True(t, ok, "this build holds %s", DefaultRulesName)
	assert.Equal(t, string(want), string(got))
	rules, err := HeldRules(DefaultRulesName)
	require.NoError(t, err)
	assert.Equal(t, len(ourRules(t)), len(rules))
	assert.Contains(t, HeldRulesNames(), DefaultRulesName)
	for _, bad := range []string{"", "../child-rules.txt", "fleet/child-rules.txt", "loops.yml", "child-rules.absent.txt"} {
		_, ok := HeldRulesText(bad)
		assert.False(t, ok, "%q is no held rules file", bad)
	}
	_, err = HeldRules("child-rules.absent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no rules file child-rules.absent.txt (it holds "+DefaultRulesName)
}

// Which file a brief's repository has: fleet/child-rules.txt for this repository, its own
// file for another that has one, none for a repository with none (its card carries its own
// rules), and the add's held set for a brief that names no repository.
func TestOwnRulesNameIsTheRepositorysFileOrNone(t *testing.T) {
	t.Parallel()
	held := func(name string) bool { return name == "child-rules.netcode.txt" }
	netcode := strings.Replace(refCard, "mas-bandwidth/nova-tools", "mas-bandwidth/netcode", 1)
	url := strings.Replace(refCard, "mas-bandwidth/nova-tools", "https://example.com/mas-bandwidth/netcode.git", 1)
	schema := strings.Replace(refCard, "mas-bandwidth/nova-tools", "mas-bandwidth/schema", 1)
	for _, c := range []struct{ brief, want string }{
		{refCard, DefaultRulesName},
		{netcode, "child-rules.netcode.txt"},
		{url, "child-rules.netcode.txt"},
		{schema, ""},
		{"no header at all", "child-rules.space.txt"},
		{"", "child-rules.space.txt"},
	} {
		assert.Equal(t, c.want, ownRulesIn(c.brief, "child-rules.space.txt", held), "brief %.40q", c.brief)
	}
	assert.Equal(t, "", OwnRulesName(netcode, DefaultRulesName), "this build holds no netcode file")
	assert.Equal(t, DefaultRulesName, OwnRulesName(refCard, "child-rules.space.txt"))
}

// The staged brief is the card's text, one blank line, then the RULES paragraph: exactly
// the card that carried its rules at its end. A card that already carries them is handed as
// it is, the staging is idempotent, and an empty brief stays empty.
func TestStagedBriefAppendsTheRulesParagraphOnce(t *testing.T) {
	t.Parallel()
	rules := ourRules(t)
	carried := refCard + "\n\n" + strings.TrimSuffix(RulesParagraph(rules), "\n")
	assert.Equal(t, carried, StagedBrief(refCard, rules))
	assert.Equal(t, carried, StagedBrief(refCard+"\n\n", rules), "trailing newlines are the card's end")
	assert.Equal(t, carried, StagedBrief(carried, rules), "a card that carries its rules is handed as it is")
	assert.Equal(t, carried, StagedBrief(StagedBrief(refCard, rules), rules))
	assert.Equal(t, "", StagedBrief("", rules))
	assert.Equal(t, refCard, StagedBrief(refCard, nil))
	assert.Empty(t, LintCardChildWith([]byte(StagedBrief(refCard, rules)), rules), "the staged brief passes the full lint")
}

// A card linted by reference passes without the rules it does not carry, and is still
// refused for a line that contradicts them; an empty card is still the one finding.
func TestLintByReferenceRefusesOnlyWhatContradictsTheRules(t *testing.T) {
	t.Parallel()
	rules := ourRules(t)
	require.NotEmpty(t, LintCardChildWith([]byte(refCard), rules), "carried rules are required without the reference")
	assert.Empty(t, LintCardChildByReference([]byte(refCard), rules))
	bad := refCard + "\nSTEP 2. Run go test ./... then go clean -cache, then git stash.\n"
	var checks []string
	for _, f := range LintCardChildByReference([]byte(bad), rules) {
		checks = append(checks, f.Check)
		assert.Equal(t, 9, f.Line, "%s is found at the card's own line", f.Check)
	}
	assert.ElementsMatch(t, []string{"step-go-test-timeout", "step-go-clean", "step-stash"}, checks)
	unfilled := strings.Replace(refCard, "the standard library's strings; nothing else fits.", "<what was found>", 1)
	got := LintCardChildByReference([]byte(unfilled), rules)
	require.Len(t, got, 1)
	assert.Equal(t, LibrariesConsideredRule, got[0].Check)
	got = LintCardChildByReference([]byte(" \n"), rules)
	require.Len(t, got, 1)
	assert.Equal(t, EmptyCardCheck, got[0].Check)
}
