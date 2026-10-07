package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// selectorApp is a sprint with friends amy and bob and, in stream s1, amy's cards x1 and x2
// (x2 needs x1), bob's card y1, and in stream s2 a machine's card d1 that needs x1 and x2.
func selectorApp(t *testing.T) *testApp {
	t.Helper()
	ta, _ := friendApp(t, "amy", "bob")
	ta.ok("friend sync --root " + t.TempDir())
	dir := t.TempDir()
	write := func(dir, id, who string) string {
		path := filepath.Join(dir, id+".md")
		require.NoError(t, os.WriteFile(path, []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: "+who)), 0o644))
		return path
	}
	write(dir, "x1", "friend amy")
	write(dir, "y1", "friend bob")
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("add --stream s1 x2 --one --needs x1 --brief-file " + write(t.TempDir(), "x2", "friend amy"))
	ta.ok("add --stream s2 d1 --one --needs x1,x2")
	return ta
}

// One recut by selector (the owner, 2026-10-04: "BATCH EVERYTHING"; docs/SPEC-SPRINT.md,
// "One selector, one step"): recut --who friend.amy --drop-who re-cuts every card pinned
// to amy in one store step, the twins' briefs without the WHO line, and their dependants
// follow the twins, a twin of a twin's need too; bob's card is not touched. --dry-run
// lists the same cards and writes nothing.
func TestOneCallRecutsEveryCardTheSelectorNames(t *testing.T) {
	t.Parallel()
	ta := selectorApp(t)

	applies := ta.applies()
	out := ta.ok("recut --who friend.amy --drop-who --dry-run")
	assert.Contains(t, out, "x1")
	assert.Contains(t, out, "x2")
	assert.NotContains(t, out, "y1")
	assert.Contains(t, out, "selected=2")
	assert.Equal(t, applies, ta.applies(), "a dry run wrote")
	assert.Equal(t, "friend.amy", ta.primary("x1").F(sprint.FieldWho), "a dry run changed x1")

	out = ta.ok("recut --who friend.amy --drop-who")
	assert.Equal(t, 1, strings.Count(out, "RECUT OK"), "one step: %s", out)
	assert.Contains(t, out, "selected=2")
	for _, old := range []string{"x1", "x2"} {
		assert.Contains(t, ta.ok("card "+old), "replaced by "+old+"b")
		twin := ta.primary(old + "b")
		assert.Equal(t, old, twin.F(sprint.FieldReplaces))
		assert.Empty(t, twin.F(sprint.FieldWho), "the twin of %s is pinned to no friend", old)
		assert.NotContains(t, twin.F("brief"), "WHO:", "the twin's brief drops its WHO line")
		assert.Contains(t, twin.F("brief"), "REPO: mas-bandwidth/nova-tools", "the rest of the brief is kept")
	}
	assert.Equal(t, []string{"x1b"}, sprint.Split(ta.primary("x2b").F("needs")), "the twin of x2 needs the twin of x1")
	assert.ElementsMatch(t, []string{"x1b", "x2b"}, sprint.Split(ta.primary("d1").F("needs")), "the dependant follows the twins")
	y1 := ta.primary("y1")
	assert.Equal(t, "friend.bob", y1.F(sprint.FieldWho), "bob's card is not selected")
	assert.Empty(t, y1.F(sprint.FieldReplaces))

	// a selector that names nothing changes nothing and says so
	code, _, errs := ta.do("recut --who friend.amy --drop-who")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "the selector names no card")
	ta.clean()
}

// The other verbs take the same selector (docs/SPEC-SPRINT.md, "One selector, one step"):
// brief by a transform, drop, rank and release each change every selected card in one
// step, print one line per card and the total, and an --ids-file id that is no card
// refuses the whole call with nothing written.
func TestEveryBatchVerbTakesTheSelector(t *testing.T) {
	t.Parallel()
	ta := selectorApp(t)

	out := ta.ok("brief --stream s1 --set-base release-1")
	assert.Equal(t, 1, strings.Count(out, "BRIEF OK"), out)
	assert.Contains(t, out, "selected=3")
	for _, id := range []string{"x1", "x2", "y1"} {
		assert.Contains(t, ta.primary(id).F("brief"), "\nBASE: release-1\n", "the brief of %s names the base", id)
	}
	assert.NotContains(t, ta.primary("d1").F("brief"), "BASE:", "d1 is in another stream")

	out = ta.ok("brief --who friend.amy --drop-who --dry-run")
	assert.Contains(t, out, "BRIEF OK DRY-RUN selected=2")
	assert.Equal(t, "friend.amy", ta.primary("x1").F(sprint.FieldWho), "a dry run changed x1")

	assert.Contains(t, ta.ok("rank --stream s1 --state waiting --first"), "selected=")

	ids := filepath.Join(t.TempDir(), "ids")
	require.NoError(t, os.WriteFile(ids, []byte("# the cards\ny1\nnope\n"), 0o644))
	applies := ta.applies()
	code, _, errs := ta.do("drop --ids-file " + ids + " --reason gone")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "nope: "+"no such card on the table")
	assert.Equal(t, applies, ta.applies(), "a refused selection wrote")

	out = ta.ok("drop --who friend.bob --reason 'bob is away'")
	assert.Equal(t, 1, strings.Count(out, "DROP OK"), out)
	assert.Contains(t, out, "selected=1")
	assert.Contains(t, ta.ok("card y1"), "bob is away")

	ta.ok("add --stream h h1 h2 --held")
	out = ta.ok("release --stream h --state held --reason 'the wave is read'")
	assert.Equal(t, 1, strings.Count(out, "RELEASE OK"), out)
	assert.Contains(t, out, "selected=2")
	for _, id := range []string{"h1", "h2"} {
		assert.False(t, sprint.IsHeld(ta.primary(id)), "%s is released", id)
	}

	code, _, errs = ta.do("recut --who friend.amy")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "what every twin changes")
	code, _, errs = ta.do("recut x1 --who friend.amy --drop-who")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "give no id")
	code, _, errs = ta.do("rework --state sleeping")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--state wants ready, waiting, held, merging")
	ta.clean()
}
