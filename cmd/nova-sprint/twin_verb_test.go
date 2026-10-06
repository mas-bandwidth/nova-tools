package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// twin from the command line (docs/SPEC-SPRINT.md section 2, "twin"): a merging card is
// returned first, then twinned in one step: the twin takes the next number, the widened
// PATHS, the corrected TASK and the card's edges; --carry wants a pushed head.
func TestTwinFromTheCommandLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.toMerging("s1")
	ta.ok("add --stream s2 dep --needs s1-2 --one")
	code, _, errs := ta.do("twin s1-2 --carry")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no attempt of s1-2 pushed a full head")
	out := ta.ok("twin s1-2 --paths 'internal/x/**' --instruction 'read the spec first' --tier heavy")
	assert.Contains(t, out, "s1-2 merging -> review", "returned first")
	assert.Contains(t, out, "dep needs s1-2 -> s1-4", "the dependent follows the twin")
	assert.Contains(t, ta.ok("card s1-2"), "twinned as s1-4")
	fields := ta.ok("card s1-4 --fields")
	assert.Contains(t, fields, "replaces=s1-2")
	assert.Contains(t, fields, "tier=heavy")
	assert.Contains(t, ta.primary("s1-4").F("brief"), "THE TASK. read the spec first")
	assert.Contains(t, ta.primary("s1-4").F("brief"), "PATHS: internal/x/**")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*blocked on something dropped`, ta.ok("inbox"))
	code, _, errs = ta.do("twin s1-2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no primary s1-2 on the work table")
	code, _, errs = ta.do("twin s1-1 s1-3")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants one card")
	ta.clean()
}
