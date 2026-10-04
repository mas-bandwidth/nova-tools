package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// primary is a primary as the store holds it, placed.
func (ta *testApp) primary(id string) *sprint.Card {
	ta.t.Helper()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	c := s.Work.Placed(id)
	require.NotNil(ta.t, c, "no primary %s placed", id)
	return c
}

// brief end to end on the twin (the owner, 2026-10-01: "What other things
// should you be able to do to mutate a stopped sprint" / "I don't want you
// manually hopping in and working around it and doing manual stuff."): a brief
// that fails the card lint refused with nothing changed, replaced on a STOPPED
// machine with the card's id, stream, score and needs kept, and refused once
// the card is dealt.
func TestBriefReplacesAnUnstartedPrimarysBriefOnAStoppedSprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 1 --brief-file " + writeBrief(t, "the old work"))
	ta.ok("add --stream a a-2 --needs a-1 --brief-file " + writeBrief(t, "the old second"))
	good := writeBrief(t, "the new work")
	before := *ta.primary("a-2")

	applies := ta.applies()
	code, out, errs := ta.do("brief a-2 --brief 'handle the empty case'")
	assert.Equal(t, 2, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "LINT DRIFT brief rule-worktree")
	assert.Contains(t, errs, "nova-sprint brief REFUSED: the brief fails the card lint")
	assert.Equal(t, applies, ta.applies(), "a brief that fails the lint wrote")
	assert.Equal(t, before.Fields["brief"], ta.primary("a-2").F("brief"))

	// over the bound a brief may be, as add's: refused whole, nothing written
	code, out, errs = ta.do("brief a-2 --brief-file " + writeBrief(t, strings.Repeat("x", store.MaxBriefBytes)))
	assert.NotEqual(t, 0, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, "over the bound")
	assert.Equal(t, applies, ta.applies(), "a brief over the bound wrote")

	assert.Contains(t, ta.ok("brief a-2 --brief-file "+good), "a-2 brief replaced")
	after := ta.primary("a-2")
	assert.Equal(t, strings.TrimSuffix(passingBrief("the new work"), "\n"), after.F("brief"))
	assert.Equal(t, before.Row, after.Row)
	assert.Equal(t, before.Col, after.Col)
	assert.Equal(t, before.Score, after.Score)
	assert.Equal(t, "a-1", after.F("needs"))

	ta.deal(1)
	code, _, errs = ta.do("brief a-1 --brief-file " + good)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "a-1 is working: a card dealt, working, in review, merging or landed keeps its brief")
	// the refusal says what changes a working card instead, whether the machine
	// runs or not: stopping it would not let the brief be replaced
	remedy := "run: nova-sprint drop a-1 --reason '<why>', then nova-sprint add --stream a <new id> --brief-file <path>, or once it finishes (review), nova-sprint rework a-1 --fix '<what changes>'"
	assert.Contains(t, errs, remedy)
	ta.ok("start")
	code, _, errs = ta.do("brief a-1 --brief-file " + good)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, remedy)
	assert.NotContains(t, errs, "run: nova-sprint stop")
	ta.clean()
}

// brief with --rules opens the store itself (the wave-2 card builder, 2026-10-02: `brief <id>
// --brief-file <f> --rules <file>` panicked on a nil store): briefRules reads the rules from
// the file and opens no store, so brief opens it as add does, and the brief is replaced.
func TestBriefWithRulesOpensTheStoreAndReplacesTheBrief(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 1 --brief-file " + writeBrief(t, "the old work"))
	dir := t.TempDir()
	rules, brief := filepath.Join(dir, "rules.txt"), filepath.Join(dir, "new.md")
	require.NoError(t, os.WriteFile(rules, []byte("Be careful.\n"), 0o600))
	require.NoError(t, os.WriteFile(brief, []byte("the new work\n\nBe careful.\n"), 0o600))
	assert.Contains(t, ta.ok("brief a-1 --rules "+rules+" --brief-file "+brief), "a-1 brief replaced")
	assert.Equal(t, "the new work\n\nBe careful.", ta.primary("a-1").F("brief"))
}

// brief while the machine runs (docs/SPEC-SPRINT.md, the brief verb): a
// waiting card's brief is replaced, queued for the tick's pump like every
// coordinator verb on a RUNNING machine, and in place after the tick; brief
// --dir replaces one brief per file in one call, its moved= the count; a dealt
// card is still refused, and so is the whole --dir call that names one,
// nothing written.
func TestBriefReplacesWaitingBriefsWhileRunningAndByDir(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 1 --brief-file " + writeBrief(t, "the old work"))
	ta.ok("add --stream a a-2 --needs a-1 --brief-file " + writeBrief(t, "the old second"))
	ta.ok("add --stream a a-3 --needs a-1 --brief-file " + writeBrief(t, "the old third"))
	ta.deal(1)
	ta.ok("start")
	text := func(lead string) string { return strings.TrimSuffix(passingBrief(lead), "\n") }

	assert.Contains(t, ta.ok("brief a-2 --brief-file "+writeBrief(t, "the new second")), "a-2 brief replaced")
	ta.ok("tick")
	assert.Equal(t, text("the new second"), ta.primary("a-2").F("brief"))
	assert.Equal(t, sprint.Waiting, ta.primary("a-2").Col)

	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a-2", "the newer second", "")
	writeNeedsBrief(t, dir, "a-3", "the new third", "")
	out := ta.ok("brief --dir " + dir)
	assert.Contains(t, out, "BRIEF OK moved=2 refused=0")
	assert.Contains(t, out, "a-3 brief replaced")
	ta.ok("tick")
	assert.Equal(t, text("the newer second"), ta.primary("a-2").F("brief"))
	assert.Equal(t, text("the new third"), ta.primary("a-3").F("brief"))

	// a dealt card keeps its brief: the call that names one writes nothing
	writeNeedsBrief(t, dir, "a-1", "the new work", "")
	writeNeedsBrief(t, dir, "a-2", "the newest second", "")
	applies := ta.applies()
	code, _, errs := ta.do("brief --dir " + dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "a-1 is working: a card dealt, working, in review, merging or landed keeps its brief")
	assert.Equal(t, applies, ta.applies(), "a refused --dir wrote")
	assert.Equal(t, text("the newer second"), ta.primary("a-2").F("brief"))

	// one brief failing the card lint refuses the call, naming its file
	bad := t.TempDir()
	writeNeedsBrief(t, bad, "a-2", "fine", "")
	require.NoError(t, os.WriteFile(filepath.Join(bad, "a-3.md"), []byte("handle the empty case\n"), 0o600))
	code, out, errs = ta.do("brief --dir " + bad)
	assert.Equal(t, 2, code)
	assert.NotContains(t, out, "MOVED")
	assert.Contains(t, errs, filepath.Join(bad, "a-3.md"))
	assert.Contains(t, errs, "nova-sprint brief REFUSED: the brief of")
	assert.Equal(t, applies, ta.applies(), "a brief failing the lint wrote")

	code, _, errs = ta.do("brief a-2 --dir " + dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--dir names each card by its file")
}
