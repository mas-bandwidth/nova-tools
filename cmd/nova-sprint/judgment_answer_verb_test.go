package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

// runStartup runs the run verb over the app's in-memory store and stops it once its startup
// is read: the binary under the loop is replaced after the first tick, so the loop stops
// (run_replaced_test). What run --answer-rules set on the app is left for the verbs after it.
func runStartup(t *testing.T, ta *testApp, args ...string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.WriteFile(exe, []byte("the build the loop began with"), 0o755))
	ta.a.executable = func() (string, error) { return exe, nil }
	ta.a.ticked = func(n int, _ time.Time, _ string) {
		if n == 1 {
			// a release installs a new build under the running loop, so it stops
			require.NoError(t, os.WriteFile(exe, []byte("the build installed under it, longer"), 0o755))
		}
	}
	var out, errb bytes.Buffer
	code := ta.a.run(args, &out, &errb)
	require.Equal(t, exitReplaced, code, "%v: exit %d\n%s%s", args, code, out.String(), errb.String())
}

// dealTakeAndFinishFailed deals one ready primary to m1, takes it and finishes it failed
// through the finish verb.
func dealTakeAndFinishFailed(ta *testApp) {
	ta.t.Helper()
	ta.deal(1)
	ta.ok("take --as m1 --limit 1")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(ta.t, q.Cards, 1)
	ta.ok("finish --as m1 s1-1.w1@" + strconv.Itoa(q.Cards[0].Gen) + " --failed --report 'tests red'")
}

// run --answer-rules reaches the finish verb (docs/SPEC-SPRINT.md, judgment-answer-latencyb-t-bb.w2):
// cmdRun sets the app flag at startup, so a failed finish the failed rule answers is answered
// in the finish's own step and is never open; with the rules off, the same finish raises its
// judgment as before. The flag is set through the run command, not the test's own setter.
func TestTheFinishVerbAnswersAFailedFinishAtRaiseWhenTheRunAnswersByRule(t *testing.T) {
	t.Parallel()
	t.Run("run --answer-rules", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a --members m1")
		ta.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(t))
		runStartup(t, ta, "run", "--answer-rules")
		require.True(t, ta.a.answersByRule(), "run --answer-rules switches the at-raise answer on")
		dealTakeAndFinishFailed(ta)
		assert.Zero(t, ta.openFailed(), "the rule answered it in the finish's step")
		assert.Equal(t, 2, ta.primary("s1-1").Int("attempt"), "the next attempt was made in the same step")
		ta.clean()
	})
	t.Run("run --answer-rules=false", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a --members m1")
		ta.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(t))
		runStartup(t, ta, "run", "--answer-rules=false")
		require.False(t, ta.a.answersByRule(), "run --answer-rules=false leaves the judgment to the coordinator")
		dealTakeAndFinishFailed(ta)
		assert.Equal(t, 1, ta.openFailed(), "left to a later pass")
		assert.Equal(t, 1, ta.primary("s1-1").Int("attempt"), "no rework was made in the finish's step")
		ta.clean()
	})
}
