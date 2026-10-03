package main

import (
	"context"
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
// rules as the member stages it (swarm.LintCardChildByReference) and records the file as the
// stream's; the member injects it at stage time.

// streamRules is the sprint's record of each stream's rules by reference.
func (ta *testApp) streamRules() map[string]string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(ta.t, err)
	got, err := st.StreamRules(context.Background())
	require.NoError(ta.t, err)
	return got
}

// briefFile writes text as a brief file named name under dir and returns its path.
func briefFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}

// A brief that does not carry the held rules is admitted and stored as given, and its stream
// records the file; one that contradicts them is refused naming the line, and records
// nothing; one that still carries them is admitted as before; the many-brief form and the
// brief verb hold the same; and a file named as a held one whose text is not this build's
// copy is refused, since the members would inject their copy.
func TestAddStoresTheCardTextAloneAndRecordsTheStreamsRules(t *testing.T) {
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
	assert.Equal(t, map[string]string{"s1": swarm.DefaultRulesName}, ta.streamRules())

	bad := text + "\nSTEP 1. Run go test ./internal/x/ and then go clean -cache."
	code, out, errs := ta.do("add --stream s2 s2-a --max 0 --brief-file " + briefFile(t, dir, "bad.md", bad))
	require.Equal(t, 2, code, "a brief that contradicts the rules: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, errs, "LINT DRIFT brief step-go-test-timeout: 4: ")
	assert.Contains(t, errs, "LINT DRIFT brief step-go-clean: 4: ")
	assert.NotContains(t, errs, "missing:", "no rule the member injects is missing")
	assert.NotContains(t, ta.streamRules(), "s2", "a refused add records nothing")

	carried := swarm.StagedBrief(text, ours) // the card as it carried its rules: the shape the member stages
	ta.ok("add --stream s3 s3-a --brief-file " + briefFile(t, dir, "carried.md", carried))
	many := t.TempDir()
	briefFile(t, many, "m-1.md", text)
	briefFile(t, many, "m-2.md", carried)
	ta.ok("add --stream s4 --brief-dir " + many)
	assert.Equal(t, map[string]string{"s1": swarm.DefaultRulesName, "s3": swarm.DefaultRulesName, "s4": swarm.DefaultRulesName}, ta.streamRules())
	ta.ok("brief s1-a --brief-file " + briefFile(t, dir, "again.md", text+"\nAnd say what was not done."))

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
// rules by reference: under this repository's file it is held to carry it, and its stream
// records nothing, so the member injects nothing. A later add replaces a stream's record,
// removing it when the add's cards carry their own; and one add of cards held to two files
// is refused, since a stream holds one.
func TestACardOnAnotherRepositoryCarriesItsOwnRules(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --rules " + ourRulesFile)
	ours, err := swarm.ReadChildRules(ourRulesFile)
	require.NoError(t, err)
	dir := t.TempDir()
	home := "RESULT: h sha=0123456789ab\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n\nhandle the empty case"
	schema := "RESULT: x sha=0123456789ab\nREPO: mas-bandwidth/schema\nBASE: main\n\nhandle the empty case"

	code, _, errs := ta.do("add --stream s1 s1-x --max 0 --brief-file " + briefFile(t, dir, "bare.md", schema))
	require.Equal(t, 2, code, "a schema card without the rules: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "LINT DRIFT brief rule-no-redis-server: 1: missing:", "it carries its own rules, as before")
	ta.ok("add --stream s1 s1-h --brief-file " + briefFile(t, dir, "home.md", home))
	assert.Equal(t, map[string]string{"s1": swarm.DefaultRulesName}, ta.streamRules())
	ta.ok("add --stream s1 s1-x --brief-file " + briefFile(t, dir, "carried.md", schema+"\n\n"+swarm.RulesParagraph(ours)))
	assert.Empty(t, ta.streamRules(), "an add whose card carries its own rules removes the stream's record")
	ta.ok("add --stream s1 s1-h2 --brief-file " + briefFile(t, dir, "home2.md", home))
	assert.Equal(t, map[string]string{"s1": swarm.DefaultRulesName}, ta.streamRules(), "a later held add records again")

	mixed := t.TempDir()
	briefFile(t, mixed, "m-1.md", home)
	briefFile(t, mixed, "m-2.md", schema+"\n\n"+swarm.RulesParagraph(ours))
	code, _, errs = ta.do("add --stream s2 --brief-dir " + mixed)
	require.Equal(t, 2, code, "one add of cards held to two rule files: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "a stream holds one rules file")
	assert.NotContains(t, ta.streamRules(), "s2")
}
