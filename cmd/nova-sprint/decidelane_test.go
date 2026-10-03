package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fixedGrade is the fixed backend answering every grade with pro at 0.81.
func fixedGrade() decide.Fixed {
	return decide.Fixed{Table: map[string]decide.FixedAnswer{decide.GradeQuestion: {Choice: decide.GradePro, P: map[string]float64{decide.GradeScript: 0.02, decide.GradeFlash: 0.17, decide.GradePro: 0.81}}}}
}

// The server's decide lane end to end on the twin, with the fixed backend and the test's
// clock: a round grades every card never dealt, records each grade and writes it on the
// card, which `card` shows; a round with nothing to do says nothing; the attempt decision a
// finish carries is recorded; when a card lands or is dropped, the outcome of each of its
// decisions is attached, once, the dropped card read unplaced; and the record calibrates.
func TestTheDecideLaneGradesRecordsAndAttachesOutcomes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetDecideLayer2("0.7", "")
	ta.ok("add --stream s1 --count 3 --brief-file " + proBriefFile(t))
	dir := t.TempDir()
	ta.a.decide = newDecideLane(dir, fixedGrade(), ta.a.now)
	ctx := context.Background()
	var out bytes.Buffer
	round := func() string {
		out.Reset()
		ta.a.decideRound(ctx, "mem:0", &out)
		return out.String()
	}
	assert.Contains(t, round(), "DECIDE recorded=0 graded=3 written=3 attached=0")
	g, ok := decide.ParseDecided(ta.primary("s1-1").F(sprint.FieldGrade))
	require.True(t, ok)
	assert.Equal(t, decide.GradePro, g.Value)
	assert.True(t, strings.HasPrefix(g.Op, "s1-1@grade."), g.Op)
	assert.Contains(t, ta.ok("card s1-1"), "tier=pro ceiling=pro grade=pro:0.81")
	assert.Empty(t, round(), "graded: nothing to do")

	// the work: s1-1 finished with its attempt decision, all three read and accepted, s1-3
	// dropped, the rest landed
	ta.deal(3)
	ta.ok("take --as m1 --limit 3")
	gens := map[string]string{}
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	for _, c := range q.Cards {
		gens[c.ID] = strconv.Itoa(c.Gen)
		assert.Equal(t, "0.7", c.Packet.DecideAttempt, "the packet hands the member the attempt bar")
	}
	attempt := decide.Fixed{Table: map[string]decide.FixedAnswer{decide.AttemptQuestion: {Choice: decide.ClassDone, P: map[string]float64{decide.ClassDone: 0.9, decide.ClassNeedsPro: 0.1}}}}
	d, err := decide.AttemptDecision(ctx, attempt, "s1-1", 1, ta.primary("s1-1").F("brief"), "head: x\nverdict: ok\nreport: done\n", "pushed=x to b: done", ta.a.now())
	require.NoError(t, err)
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	var o, e bytes.Buffer
	ta.beat()
	code := ta.a.run(ta.withEpoch([]string{"finish", "--as", "m1", "s1-1.w1@" + gens["s1-1.w1"], "--report", "done", "--decision", string(raw)}), &o, &e)
	require.Equal(t, 0, code, e.String())
	code, _, errs := ta.do("finish --as m1 s1-2.w1@" + gens["s1-2.w1"] + " --decision '{\"id\":\"not one\"}'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "finish REFUSED: --decision: ", "a decision that does not fit is refused, nothing finished")
	ta.ok("finish --as m1 s1-2.w1@" + gens["s1-2.w1"] + " s1-3.w1@" + gens["s1-3.w1"])
	assert.Contains(t, ta.ok("card s1-1"), "decided=done:0.90", "the ATTEMPT line shows the decision")
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("drop s1-3 --reason 'out of scope'")
	assert.Contains(t, round(), "DECIDE recorded=1 graded=0 written=0 attached=1", "the decision recorded; s1-3's grade labelled dropped, read unplaced")
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 10")
	assert.Contains(t, round(), "DECIDE recorded=0 graded=0 written=0 attached=3")
	assert.Empty(t, round(), "every outcome attached once")

	attempts, err := decide.Load(filepath.Join(dir, "attempt.jsonl"))
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, d.ID, attempts[0].ID)
	require.NotNil(t, attempts[0].Outcome)
	assert.Equal(t, decide.LabelLanded, attempts[0].Outcome.Label)
	assert.Equal(t, "s1-1 landed at attempt 1 on pro", attempts[0].Outcome.Note)
	grades, err := decide.Load(filepath.Join(dir, "grade.jsonl"))
	require.NoError(t, err)
	labels := map[string]string{}
	for _, gd := range grades {
		require.NotNil(t, gd.Outcome, gd.ID)
		labels[gd.Inputs["card"]] = gd.Outcome.Label
	}
	assert.Equal(t, map[string]string{"s1-1": "pro", "s1-2": "pro", "s1-3": decide.LabelDropped}, labels)
	cal, err := decide.Calibrate(grades, decide.GradeName, "grade=pro", []string{"pro"}, []string{decide.LabelDropped})
	require.NoError(t, err)
	assert.Equal(t, 2, len(cal.Positives))
	ta.clean()
}

// A round whose work table cannot be read says so once, and again only when it changes;
// with no backend the lane grades nothing, and its loop says so when it starts.
func TestTheDecideLaneSaysAFailureOnceAndGradesNothingWithNoKey(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
	ta.a.decide = newDecideLane(t.TempDir(), nil, ta.a.now)
	var out bytes.Buffer
	ta.a.decideRound(context.Background(), "mem:0", &out)
	assert.Empty(t, out.String(), "no backend: nothing graded, nothing said")
	assert.Empty(t, ta.primary("s1-1").F(sprint.FieldGrade))
	for range 3 {
		ta.a.decideRound(context.Background(), "", &out)
	}
	assert.Equal(t, 1, strings.Count(out.String(), "DECIDE FAILED the work table could not be read"), out.String())
	out.Reset()
	stopped, stop := context.WithCancel(context.Background())
	stop()
	ta.a.decideLoop(stopped, "mem:0", &out)
	assert.Contains(t, out.String(), "no grading: JEV_API_KEY is absent from this environment", "the loop says it grades nothing")
}
