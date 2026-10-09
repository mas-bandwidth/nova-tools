package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The twin's verbs from the command line (docs/SPEC-SPRINT.md section 2, "A card replaced
// by its twin"): add --replaces is one card and needs no --one; card shows the dependent's
// new need; relink repairs a drop and an add made apart and answers their judgments.
func TestAddReplacesAndRelinkFromTheCommandLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 old other")
	ta.ok("add --stream s2 dep1 dep2 --needs old")
	out := ta.ok("add --stream s1 old-tb --replaces old")
	assert.Contains(t, out, "dep1 needs old -> old-tb")
	assert.Contains(t, ta.ok("card dep1"), "needs old-tb (ready), relinked: old -> old-tb")
	assert.Contains(t, ta.ok("card old"), "replaced by old-tb")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	ta.clean()

	// drop detaches dep3 at once, so a later relink finds nothing still waiting on other
	ta.ok("add --stream s2 dep3 --needs other --one")
	out = ta.ok("drop other --reason re-cut")
	assert.Contains(t, out, "DETACHED")
	assert.Contains(t, out, "dep3")
	ta.ok("add --stream s1 other-tb --one")
	code, _, errs := ta.do("relink other other-tb")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "nothing waits on other")
	ta.clean()
}
