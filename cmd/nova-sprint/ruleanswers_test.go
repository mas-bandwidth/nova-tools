package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rules prints what the tick's rules would answer, and writes nothing; tick answers by rule
// unless --answer-rules=false (docs/SPEC-SPRINT.md section 8, answered by rule).
func TestRulesPrintsTheAnswersAndTickAppliesThem(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'verdict not-done; the tests fail'")
	before := ta.applies()
	out := ta.ok("rules")
	assert.Equal(t, before, ta.applies(), "rules writes nothing")
	assert.Regexp(t, `RULE \S+ type=work\\x20came\\x20back\\x20failed subject=s1-1 card=s1-1 rule=failed act=rework `, out)
	assert.Contains(t, out, "RULES OK judgments=1 acting=1 left=0 off=0 by=failed_rework=1")
	ta.ok("tick")
	require.Contains(t, ta.ok("inbox"), "work came back failed", "a tick by hand answers by rule only when asked")
	out = ta.ok("tick --answer-rules")
	assert.Contains(t, out, "answered by rule failed")
	assert.NotRegexp(t, `(?m)^JUDGMENT .*work came back failed`, ta.ok("inbox"))
	ta.ok("tick") // the rework is the next pump's
	assert.Contains(t, ta.ok("card --fields s1-1"), "rule_answer=failed:")
	ta.clean()
}
