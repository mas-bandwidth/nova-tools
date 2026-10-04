package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// card --brief prints the brief alone, nothing in front of it and nothing after
// (the comfort list of 2026-10-03, item 3: --fields was the only way to get a
// brief's text out, and it printed the story first); a card with no brief is
// refused naming the verb that gives one; --json is one object.
func TestCardBriefPrintsTheBriefAlone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --one --stream a --count 1 --brief-file " + writeBrief(t, "the whole work"))
	ta.ok("add --one --stream a --count 1")

	out := ta.ok("card a-1 --brief")
	assert.Equal(t, strings.TrimSuffix(passingBrief("the whole work"), "\n")+"\n", out)

	var j struct{ ID, Brief string }
	ta.json("card a-1 --brief", &j)
	assert.Equal(t, "a-1", j.ID)
	assert.Equal(t, strings.TrimSuffix(passingBrief("the whole work"), "\n"), j.Brief)

	code, out, errs := ta.do("card a-2 --brief")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errs, "a-2 has no brief; run: nova-sprint brief a-2 --brief-file <path>")

	code, _, errs = ta.do("card a-1 --brief --fields")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "give one of them")
}
