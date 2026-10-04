package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// inbox prints an alias beside each judgment, j<n>, stable for the life of the
// judgment, and every verb that takes a judgment id takes the alias in its
// place: --answers, --group, wait, ack and inbox --open (the comfort list of
// 2026-10-03, item 10: the 40-character ids were copied exactly). An alias
// naming no judgment is refused naming it.
func TestInboxAliasesNameJudgmentsAndTheVerbsTakeThem(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 --count 1")
	ta.deal(2)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	ta.failOnce("m1", "s2-1.w1@1", "tests red")
	first, second := ta.group(sprint.NWorkFailed, "s1"), ta.group(sprint.NWorkFailed, "s2")
	require.NotEmpty(t, first.Alias)
	require.NotEmpty(t, second.Alias)
	require.NotEqual(t, first.Alias, second.Alias)
	assert.True(t, sprint.IsAlias(first.Alias), first.Alias)
	out := ta.ok("inbox")
	assert.Contains(t, out, "JUDGMENT "+first.ID+"   work came back failed  alias="+first.Alias+"  stream=s1")
	var j struct {
		Judgments []struct{ ID, Alias string }
	}
	ta.json("inbox", &j)
	require.Len(t, j.Judgments, 2)
	for _, x := range j.Judgments {
		if x.ID == first.ID {
			assert.Equal(t, first.Alias, x.Alias, "the JSON judgments carry the alias")
		}
	}

	assert.Contains(t, ta.ok("inbox --open "+second.Alias), "s2-1")
	assert.Contains(t, ta.ok("wait "+second.Alias+" --for 30m"), "WAIT OK note="+second.ID)
	code, _, errs := ta.do("rework s1-1 --fix 'the fix' --answers j99")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--answers: no judgment j99 in this epoch; run: nova-sprint inbox")

	out = ta.ok("rework s1-1 --fix 'the fix' --answers " + first.Alias)
	assert.Contains(t, out, "MOVED s1-1 work review -> working (rework)")
	// the first judgment answered, the second keeps its alias
	assert.Equal(t, second.Alias, ta.group(sprint.NWorkFailed, "s2").Alias, "the alias moved")
	out = ta.ok("rework --group " + second.Alias + " --fix 'the fix'")
	assert.Contains(t, out, "MOVED s2-1 work review -> working (rework)")
	ta.clean()
}
