package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Contains(t, ta.ok("card dep1"), "needs old-tb (ready)")
	assert.Contains(t, ta.ok("card old"), "replaced by old-tb")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	ta.clean()

	// a drop of a card a waiting card still needs is refused, nothing written
	// (docs/SPEC-SPRINT.md section 11). A need naming a dropped sentinel is
	// admitted and opens the blocked judgment; relink repairs that pair.
	ta.ok("add --stream s2 dep3 --one --needs other")
	code, out, errs := ta.do("drop other --reason re-cut")
	require.Equal(t, 1, code, "drop of a needed card: %s%s", out, errs)
	require.Contains(t, errs, "other is needed by dep3; drop them too with --cascade", "drop of a needed card: %s", errs)
	require.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	ta.ok("add --stream s1 --sentinel sold")
	ta.ok("drop sold --reason re-cut")
	ta.ok("add --stream s2 dep4 --one --needs sold")
	require.Regexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	ta.ok("add --stream s1 other-tb --one")
	assert.Contains(t, ta.dry("relink sold other-tb --dry-run"), "RELINK DRY-RUN old=sold new=other-tb; nothing was changed")
	out = ta.ok("relink sold other-tb --reason 're-cut as its twin'")
	assert.Contains(t, out, "dep4 needs sold -> other-tb")
	assert.Contains(t, ta.ok("card dep4"), "needs other-tb (ready)")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	code, _, errs = ta.do("relink sold other-tb")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "nothing waits on sold")
	ta.clean()
}
