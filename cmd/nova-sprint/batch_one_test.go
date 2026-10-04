package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The verbs push back on singles (the owner, 2026-10-03: "It feels very much like we are
// dealing cards one at a time. Stop this. BATCH EVERYTHING."): add refuses one card unless
// --one says it is meant; --brief-dir, --count and several --brief-file are waves and pass.
func TestAddRefusesASingleCardWithoutOne(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream s1 --brief-dir <dir>; or say --one for a single card"
	one := writeBrief(t, "the one work")
	for _, single := range []string{"add --stream s1 lone", "add --stream s1 --brief-file " + one} {
		code, out, errs := ta.do(single)
		assert.Equal(t, 2, code, single) // could not run: the standard's refusal exit
		assert.Contains(t, errs, line, single)
		assert.NotContains(t, out, "ADD OK", single)
	}
	var w whereView
	ta.json("where", &w)
	assert.Zero(t, w.All, "nothing was admitted")
	ta.ok("add --stream s1 lone --one")
	ta.ok("add --stream s1 --one --brief-file " + one)
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s1 --brief-dir " + writeBriefDir(t, 3))
	ta.ok("add --stream s1 --sentinel gate")
	ta.json("where", &w)
	assert.EqualValues(t, 8, w.All, "--one, --count, --brief-dir and a sentinel admit")
	ta.clean()
}

// rework and drop of one card while the inbox holds a judgment group of several naming it
// are refused: the group is answered whole, or --one says the one card is meant; a group of
// one is answered by naming the card as before.
func TestReworkAndDropRefuseOneCardOfAGroupWithoutOne(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "boom one")
	ta.failOnce("m1", "s1-2.w1@1", "boom two")
	g := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, []string{"s1-1", "s1-2"}, g.Primaries, "%+v", g)
	for verb, args := range map[string]string{"rework": " s1-1 --fix 'again'", "drop": " s1-1 --reason 'obsolete'"} {
		code, out, errs := ta.do(verb + args)
		assert.Equal(t, 2, code, verb) // could not run: the standard's refusal exit
		assert.Equal(t, "nova-sprint "+verb+" REFUSED: the inbox holds a group of 2 for this card; answer the group; run: nova-sprint "+verb+" --group "+g.ID+" --expect 2; or say --one\n", errs, verb)
		assert.NotContains(t, out, "MOVED", verb)
	}
	assert.Equal(t, sprint.Review, ta.primary("s1-1").Col, "nothing moved")
	ta.ok("rework s1-1 --one --fix 'again'")
	assert.Equal(t, sprint.Working, ta.primary("s1-1").Col, "--one reworks the one card")
	ta.ok("drop s1-2 --reason 'obsolete'") // the group is of one now: named as before
	ta.clean()
}
