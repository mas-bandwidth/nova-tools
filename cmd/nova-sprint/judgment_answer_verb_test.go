package main

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// openFailed is the open "work came back failed" judgments the twin holds now.
func (ta *testApp) openFailed() int {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	v, err := st.Inbox(context.Background(), 0, 0, 10000)
	require.NoError(ta.t, err)
	n := 0
	for _, o := range v.Open {
		if o.Note.Type == sprint.NWorkFailed {
			n++
		}
	}
	return n
}

// failedFinishVerb deals s1-1 to m1 and finishes it failed through the finish verb.
func failedFinishVerb(ta *testApp) {
	ta.t.Helper()
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(ta.t))
	ta.deal(1)
	ta.ok("take --as m1 --limit 1")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(ta.t, q.Cards, 1)
	ta.ok("finish --as m1 s1-1.w1@" + strconv.Itoa(q.Cards[0].Gen) + " --failed --report 'tests red'")
}

// run --answer-rules reaches the finish verb (docs/SPEC-SPRINT.md, judgment-answer-latencyb-t-bb.w1):
// a failed finish the failed rule answers is answered in the finish's own step and is never
// open; with the rules off, the same finish raises its judgment as before.
func TestTheFinishVerbAnswersAFailedFinishAtRaiseWhenTheRunAnswersByRule(t *testing.T) {
	t.Parallel()
	t.Run("rules on", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.a.setAnswersByRule(true)
		failedFinishVerb(ta)
		assert.Zero(t, ta.openFailed(), "the rule answered it in the finish's step")
		assert.Equal(t, 2, ta.primary("s1-1").Int("attempt"), "the next attempt was made in the same step")
		ta.clean()
	})
	t.Run("rules off", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.a.setAnswersByRule(false)
		failedFinishVerb(ta)
		assert.Equal(t, 1, ta.openFailed(), "left to a later pass")
		ta.clean()
	})
}
