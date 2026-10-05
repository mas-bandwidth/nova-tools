package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// recut from the command line (docs/SPEC-SPRINT.md section 2, "A card replaced by its
// twin"): the twin takes the next letter and the old card's edges, the old card is
// dropped "replaced by" it, and no blocked judgment is raised.
func TestRecutFromTheCommandLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 old other")
	ta.ok("add --stream s2 dep1 --needs old --one")
	out := ta.ok("recut old --tier heavy")
	assert.Contains(t, out, "dep1 needs old -> oldb")
	assert.Contains(t, ta.ok("card dep1"), "needs oldb")
	assert.Contains(t, ta.ok("card old"), "replaced by oldb")
	fields := ta.ok("card oldb --fields")
	assert.Contains(t, fields, "replaces=old")
	assert.Contains(t, fields, "tier=heavy")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	code, _, errs := ta.do("recut other")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants one primary and --tier")
	code, _, errs = ta.do("recut oldb --tier heavy")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "pinned to tier heavy already")
	ta.clean()
}
