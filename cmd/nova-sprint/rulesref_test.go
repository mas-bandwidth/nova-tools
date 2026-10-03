package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// RULES BY REFERENCE (nova-tools#5174 rule 6): under a rules file the members hold
// (fleet/child-rules*.txt of the build), add stores the card's text alone, holds it to the
// rules as the member stages it (swarm.LintCardChildByReference), and names the file on the
// card (sprint.FieldRules); the member injects it at stage time.

// cardRulesOf is the rules file the primary names, "" when it names none.
func (ta *testApp) cardRulesOf(id string) string {
	ta.t.Helper()
	_, _, _, f, ok := ta.m.Record(sprint.Work, id)
	require.True(ta.t, ok, "%s is on the work table", id)
	return f[sprint.FieldRules]
}

// briefFile writes text as a brief file named name under dir and returns its path.
func briefFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}

// The briefs of the tests below: one on this repository, one on a repository the members
// hold no rules file for.
const (
	homeBrief   = "RESULT: h sha=0123456789ab\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n\nhandle the empty case"
	schemaBrief = "RESULT: x sha=0123456789ab\nREPO: mas-bandwidth/schema\nBASE: main\n\nhandle the empty case"
)

// A brief that does not carry the held rules is admitted, stored as given, and names the
// file; one that contradicts them is refused naming the line, nothing written; one that still
// carries them is admitted as before; the many-brief form and the brief verb hold the same,
// and a replacement brief rewrites its card's name; and a file named as a held one whose text
// is not this build's copy is refused, since the members would inject their copy.
func TestAddStoresTheCardTextAloneAndNamesItsRules(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --rules " + ourRulesFile)
	ours, err := swarm.ReadChildRules(ourRulesFile)
	require.NoError(t, err)
	dir := t.TempDir()
	text := "handle the empty case\n\nLibraries considered: none; this card writes no code."
	ta.ok("add --stream s1 s1-a --brief-file " + briefFile(t, dir, "a.md", text+"\n"))
	_, _, _, f, ok := ta.m.Record(sprint.Work, "s1-a")
	require.True(t, ok)
	assert.Equal(t, text, f["brief"], "the stored brief is the card's text alone")
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("s1-a"))

	bad := text + "\nSTEP 1. Run go test ./internal/x/ and then go clean -cache."
	code, out, errs := ta.do("add --stream s2 s2-a --max 0 --brief-file " + briefFile(t, dir, "bad.md", bad))
	require.Equal(t, 2, code, "a brief that contradicts the rules: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, errs, "LINT DRIFT brief step-go-test-timeout: 4: ")
	assert.Contains(t, errs, "LINT DRIFT brief step-go-clean: 4: ")
	assert.NotContains(t, errs, "missing:", "no rule the member injects is missing")

	carried := swarm.StagedBrief(text, ours) // the card as it carried its rules: the shape the member stages
	ta.ok("add --stream s3 s3-a --brief-file " + briefFile(t, dir, "carried.md", carried))
	many := t.TempDir()
	briefFile(t, many, "m-1.md", text)
	briefFile(t, many, "m-2.md", schemaBrief+"\n\n"+swarm.RulesParagraph(ours))
	ta.ok("add --stream s4 --brief-dir " + many)
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("m-1"))
	assert.Empty(t, ta.cardRulesOf("m-2"), "each card of one add names its own: a schema card carries its own rules")
	ta.ok("brief s1-a --brief-file " + briefFile(t, dir, "again.md", schemaBrief+"\n\n"+swarm.RulesParagraph(ours)))
	assert.Empty(t, ta.cardRulesOf("s1-a"), "a replacement brief that carries its own rules names none")
	ta.ok("brief s1-a --brief-file " + briefFile(t, dir, "home.md", homeBrief))
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("s1-a"))

	other := filepath.Join(t.TempDir(), swarm.DefaultRulesName)
	require.NoError(t, os.WriteFile(other, []byte("[ticket] Quote the ticket number.\n"), 0o600))
	code, _, errs = ta.do("add --stream s5 s5-a --rules " + other + " --brief 'Quote the ticket number.'")
	require.Equal(t, 2, code, "a held name with other text: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "its text is not this build's copy")

	// the bytes a card no longer carries: the RULES paragraph and the blank line before it
	saved := len(carried) - len(text)
	assert.Equal(t, len(swarm.RulesParagraph(ours))+1, saved)
	t.Logf("bytes saved per card: %d (the RULES paragraph of %s)", saved, swarm.DefaultRulesName)
}

// A card on a repository the members hold no rules file for carries its own rules, as before
// rules by reference, and names none. What a card names is its own, so a later add on the same
// stream never changes it, in either order: a nova-tools card keeps its file after a schema
// card's add, and a schema card names none after a nova-tools card's add under the file.
func TestALaterAddLeavesAnEarlierCardsRules(t *testing.T) {
	t.Parallel()
	ours, err := swarm.ReadChildRules(ourRulesFile)
	require.NoError(t, err)
	carriedSchema := schemaBrief + "\n\n" + swarm.RulesParagraph(ours)

	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --rules " + ourRulesFile)
	dir := t.TempDir()
	code, _, errs := ta.do("add --stream s1 s1-x --max 0 --brief-file " + briefFile(t, dir, "bare.md", schemaBrief))
	require.Equal(t, 2, code, "a schema card without the rules: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "LINT DRIFT brief rule-no-redis-server: 1: missing:", "it carries its own rules, as before")
	ta.ok("add --stream s1 s1-h --brief-file " + briefFile(t, dir, "home.md", homeBrief))
	ta.ok("add --stream s1 s1-x --brief-file " + briefFile(t, dir, "carried.md", carriedSchema))
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("s1-h"), "a schema card's add leaves the nova-tools card its rules")
	assert.Empty(t, ta.cardRulesOf("s1-x"))

	tb := newTestApp(t)
	tb.ok("init --readers reader-a --members m1")
	general := schemaBrief + "\n\n" + swarm.ChildRulesParagraph()
	tb.ok("add --stream s2 s2-x --brief-file " + briefFile(t, dir, "general.md", general))
	tb.ok("add --stream s2 s2-h --rules " + ourRulesFile + " --brief-file " + briefFile(t, dir, "home2.md", homeBrief))
	assert.Empty(t, tb.cardRulesOf("s2-x"), "a nova-tools card's add names no Go rules on the schema card")
	assert.Equal(t, swarm.DefaultRulesName, tb.cardRulesOf("s2-h"))
}

// A card moved to another stream keeps the rules file it names: move admits it again from its
// own record (sprint.MoveCards), so a moved card's child is handed the same rules.
func TestMoveKeepsACardsRules(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --rules " + ourRulesFile)
	ta.ok("add --stream s1 s1-h --brief-file " + briefFile(t, t.TempDir(), "home.md", homeBrief))
	require.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("s1-h"))
	ta.ok("move s1-h --stream s2")
	row, _, _, _, ok := ta.m.Record(sprint.Work, "s1-h")
	require.True(t, ok)
	assert.Equal(t, "s2", row, "the card moved")
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf("s1-h"), "a moved card keeps its rules")
}
