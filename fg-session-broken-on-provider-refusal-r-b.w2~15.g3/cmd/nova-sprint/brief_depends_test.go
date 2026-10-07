package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A brief that differs in its DEPENDS-ON: line alone is taken on a RUNNING
// machine (applied by the next tick, as every work-table change is while it
// runs) and for a card dealt, its needs re-pointed (the comfort list of
// 2026-10-03, item 2: re-pointing six dependants after a drop meant stop, six
// briefs, start); a waiting card's whole brief is replaced while the machine
// runs, and a dealt card's whole brief is still refused; a need that is no primary
// on the table, the card itself, or one not landed for a ready card is refused.
func TestBriefDependsOnOnlyChangeIsTakenInAnyState(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	ta.ok("add --one --stream a a-1 --brief-file " + writeHeaderBrief(t, dir, "a-1", "-", "internal/a1.go"))
	ta.ok("add --one --stream a a-2 --brief-file " + writeHeaderBrief(t, dir, "a-2", "a-1", "internal/a2.go"))
	ta.ok("add --one --stream a a-3 --brief-file " + writeHeaderBrief(t, dir, "a-3", "-", "internal/a3.go"))
	require.Equal(t, "a-1", ta.primary("a-2").F("needs"))

	for id, line := range map[string]string{
		"a-3 ready":    "brief a-3 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-3", "a-2", "internal/a3.go"),
		"itself":       "brief a-2 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-2", "a-2", "internal/a2.go"),
		"no such card": "brief a-2 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-2", "zz-9", "internal/a2.go"),
	} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 1, code, id)
		assert.Contains(t, errs, "nothing was changed", id)
	}
	assert.Equal(t, "a-1", ta.primary("a-2").F("needs"))
	assert.Equal(t, "", ta.primary("a-3").F("needs"))

	// the machine runs: the DEPENDS-ON change alone is taken, the needs follow it
	ta.ok("start")
	out := ta.ok("brief a-2 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-2", "a-3", "internal/a2.go"))
	assert.Contains(t, out, "MOVED a-2 DEPENDS-ON replaced: needs a-3 ")
	ta.ok("tick")
	assert.Equal(t, "a-3", ta.primary("a-2").F("needs"))
	assert.Contains(t, ta.primary("a-2").F("brief"), "DEPENDS-ON: a-3\n")
	assert.Equal(t, "waiting", ta.primary("a-2").Col)

	// a waiting card takes a whole new brief while the machine runs; the needs stay
	out = ta.ok("brief a-2 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-2", "a-3", "internal/other.go"))
	assert.Contains(t, out, "a-2 brief replaced")
	ta.ok("tick")
	assert.Contains(t, ta.primary("a-2").F("brief"), "PATHS: internal/other.go\n")
	assert.Equal(t, "a-3", ta.primary("a-2").F("needs"))
	assert.Equal(t, "waiting", ta.primary("a-2").Col)

	// a card dealt: the change applies to its next attempt, the needs follow
	require.Equal(t, "working", ta.primary("a-1").Col, "the tick dealt a-1")
	out = ta.ok("brief a-1 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-1", "a-3", "internal/a1.go"))
	assert.Contains(t, out, "MOVED a-1 DEPENDS-ON replaced: needs a-3 ")
	ta.ok("tick")
	assert.Equal(t, "a-3", ta.primary("a-1").F("needs"))
	assert.Contains(t, ta.primary("a-1").F("brief"), "DEPENDS-ON: a-3\n")
	assert.Equal(t, "working", ta.primary("a-1").Col)
	code, _, errs := ta.do("brief a-1 --brief-file " + writeHeaderBrief(t, t.TempDir(), "a-1", "a-3", "internal/other.go"))
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "a-1 is working: a card working, merging or landed keeps its brief")
}
