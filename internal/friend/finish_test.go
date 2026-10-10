package friend

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The finish contract (DecideFinish): the head is the branch's, a lane that committed
// and wrote nothing gets one recovery turn, and a run that was no model is a fault.

func TestHeadFromTheBranchBeatsHeadInTheReport(t *testing.T) {
	t.Parallel()
	reportSHA := strings.Repeat("a", 40)
	tip := strings.Repeat("b", 40)
	in := FinishInput{
		Friend: "zhi", Card: "c1", Branch: "sprint/the-fix",
		Report:        "Verdict: LAND\nHead: " + reportSHA + "\n\nThe branch has the fix.\n",
		ReportPresent: true,
		Tip:           tip,
		Commits:       []string{"bbbbbbb the fix"},
	}
	dec := DecideFinish(in)
	assert.Equal(t, ActFinish, dec.Act)
	assert.Equal(t, tip, dec.Head)
	assert.True(t, dec.OK)
	assert.False(t, dec.Failed)
	assert.Equal(t, "report named "+reportSHA+", branch is "+tip, dec.Judgment)
	card := Card{ID: "c1", Outbox: filepath.Join("outbox", "c1~15")}
	argv := branchFinishArgv("zhi", card, in.Branch, dec, dec.Judgment)
	assert.NotContains(t, argv, "--failed")
	assert.Contains(t, argv, "--head")
	assert.Contains(t, argv, tip)
	if i := slices.Index(argv, "--head"); i >= 0 && i+1 < len(argv) {
		assert.Equal(t, tip, argv[i+1], "the head sent is the branch tip, never the report's sha")
		assert.NotEqual(t, reportSHA, argv[i+1])
	}

	named := in
	named.Tip, named.Commits = "", nil
	none := DecideFinish(named)
	assert.Equal(t, ActFinish, none.Act)
	assert.Empty(t, none.Head, "a lane with no commits finishes with no head")
	assert.True(t, none.Failed, "a LAND with no branch tip is not ok")
	assert.True(t, none.applyInLane(named), "a report with no commits is the branch finish")
	assert.True(t, none.SendsFinish)
	noneArgv := branchFinishArgv("zhi", card, named.Branch, none, "the friend did it")
	assert.NotContains(t, noneArgv, "--head")
	assert.NotContains(t, strings.Join(noneArgv, " "), reportSHA)
	assert.Contains(t, noneArgv, "--failed")
}

func TestNoReportWithCommitsGetsOneRecoveryTurnThenFailsNamingIt(t *testing.T) {
	t.Parallel()
	tip := strings.Repeat("b", 40)
	in := FinishInput{
		Friend: "zhi", Card: "c1", Branch: "sprint/the-fix",
		Tip: tip, Commits: []string{"abc1234 the work"},
	}
	dec := DecideFinish(in)
	assert.Equal(t, ActRecover, dec.Act)
	assert.False(t, dec.AttemptSpent)
	assert.False(t, dec.SendsFinish)
	assert.Contains(t, dec.Prompt, "Your branch sprint/the-fix at "+tip+" has these commits:")
	assert.Contains(t, dec.Prompt, "abc1234 the work")
	assert.Contains(t, dec.Prompt, "Write REPORT.md in the form below and stop.")
	assert.Contains(t, dec.Prompt, FinishForm)
	assert.True(t, dec.applyInLane(in))

	in.RecoveryDone = true
	failed := DecideFinish(in)
	assert.Equal(t, ActFail, failed.Act)
	assert.True(t, failed.Failed)
	assert.True(t, failed.AttemptSpent)
	assert.True(t, failed.SendsFinish)
	assert.Equal(t, "no report after one recovery turn", failed.Reason)
	assert.NotContains(t, failed.Reason, "wrote no report")
	assert.NotContains(t, failed.Judgment, "wrote no report")
	argv := branchFinishArgv("zhi", Card{ID: "c1", Outbox: filepath.Join("outbox", "c1~15")}, in.Branch, failed, failed.Reason)
	assert.Contains(t, argv, "--failed")
}

func TestZeroUsageIsAFaultNotAFinish(t *testing.T) {
	t.Parallel()
	usages := []UsageFact{
		{Measured: true, Zero: true},
		{Measured: true, Absent: true},
		{Absent: true},
	}
	for _, usage := range usages {
		in := FinishInput{Friend: "zhi", Card: "c1", Exit: 7, Stderr: "harness said nothing", Usage: usage}
		dec := DecideFinish(in)
		assert.Equal(t, ActFault, dec.Act)
		assert.True(t, dec.BackToQueue)
		assert.False(t, dec.AttemptSpent)
		assert.False(t, dec.SendsFinish)
		assert.False(t, dec.OK)
		assert.Contains(t, dec.Judgment, "fault: zhi lane for c1 ran no model:")
		assert.Contains(t, dec.Judgment, "exit 7")
		assert.Contains(t, dec.Judgment, "harness said nothing")
	}
}

func TestCostOnlyHoldIsTheSameFault(t *testing.T) {
	t.Parallel()
	in := FinishInput{
		Friend: "zhi", Card: "c1", Exit: 0,
		ReportPresent: true,
		Report:        "Verdict: HOLD\n\nCost: $0.00 (list price, route flash-deepseek41-direct) tokens input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=deepseek/deepseek-v4.1-flash harness=dsh price_route=flash-deepseek41-direct\n",
	}
	dec := DecideFinish(in)
	assert.Equal(t, ActFault, dec.Act)
	assert.True(t, dec.BackToQueue)
	assert.False(t, dec.AttemptSpent)
	assert.False(t, dec.SendsFinish)
	assert.Contains(t, dec.Judgment, "fault: zhi lane for c1 ran no model:")
	assert.True(t, dec.applyInLane(in), "a cost line is enough for the daemon to take the fault")

	withWork := in
	withWork.Tip = strings.Repeat("c", 40)
	withWork.Commits = []string{"ccccccc the work"}
	kept := DecideFinish(withWork)
	assert.Equal(t, ActFinish, kept.Act, "commits mean the work was real: not a fault")
	assert.True(t, kept.UsageUnknown)
	assert.False(t, kept.BackToQueue)
}

func TestThreeFaultsFailWithTheFaultReason(t *testing.T) {
	t.Parallel()
	base := FinishInput{
		Friend: "zhi", Card: "c1", Exit: 1, Stderr: "empty",
		Usage: UsageFact{Measured: true, Zero: true},
	}
	for _, n := range []int{0, 1} {
		in := base
		in.Faults = n
		dec := DecideFinish(in)
		assert.Equal(t, ActFault, dec.Act)
		assert.False(t, dec.AttemptSpent)
		assert.True(t, dec.BackToQueue)
	}
	in := base
	in.Faults = 2
	dec := DecideFinish(in)
	assert.Equal(t, ActFail, dec.Act)
	assert.True(t, dec.Failed)
	assert.Equal(t, "FAIL", dec.Verdict)
	assert.True(t, dec.AttemptSpent)
	assert.True(t, dec.SendsFinish)
	assert.False(t, dec.BackToQueue)
	assert.Contains(t, dec.Reason, "fault: zhi lane for c1 ran no model:")
	assert.Equal(t, dec.Judgment, dec.Reason)
}

func TestMissingVerdictWithReportAndCommitsFinishesOk(t *testing.T) {
	t.Parallel()
	tip := strings.Repeat("d", 40)
	in := FinishInput{
		Friend: "zhi", Card: "c1", Branch: "sprint/the-fix",
		Report: "The branch has the fix.\n", ReportPresent: true,
		Tip: tip, Commits: []string{"ddddddd the fix"},
	}
	dec := DecideFinish(in)
	assert.Equal(t, ActFinish, dec.Act)
	assert.True(t, dec.OK)
	assert.False(t, dec.Failed)
	assert.Equal(t, "ok", dec.Verdict)
	assert.Equal(t, tip, dec.Head)
	assert.Contains(t, dec.Judgment, "verdict line missing, inferred ok from the report")
	argv := branchFinishArgv("zhi", Card{ID: "c1", Outbox: filepath.Join("outbox", "c1~15")}, in.Branch, dec, dec.Judgment)
	assert.NotContains(t, argv, "--failed")
	assert.Contains(t, argv, tip)
}

func TestUnmeasuredNoReportStaysOutOfTheLane(t *testing.T) {
	t.Parallel()
	in := FinishInput{Friend: "zhi", Card: "c1", Exit: 0, Usage: UsageFact{Absent: true}}
	dec := DecideFinish(in)
	assert.Equal(t, ActFault, dec.Act)
	assert.False(t, dec.applyInLane(in), "no token source and no cost line stays the harness fault")

	measured := in
	measured.Usage = UsageFact{Measured: true, Zero: true}
	assert.True(t, DecideFinish(measured).applyInLane(measured))

	real := FinishInput{
		Friend: "zhi", Card: "c1",
		Report: "Verdict: LAND\n\nThe branch has the fix.\n", ReportPresent: true,
		Usage: UsageFact{Measured: true, Zero: true},
		Tip:   strings.Repeat("e", 40), Commits: []string{"eeeeeee the fix"},
	}
	done := DecideFinish(real)
	assert.Equal(t, ActFinish, done.Act)
	assert.True(t, done.UsageUnknown)
	assert.True(t, done.OK)
}

// noCommitLane is a lane whose turn wrote a LAND report naming reportSHA, and
// whose checkout committed nothing. The card is working on her row, so the
// outbox pass would finish the report if the lane left it.
func noCommitLane(t *testing.T, reportSHA string) (*rig, Card, *loop, *lane) {
	t.Helper()
	r := newRig(t)
	h := workCard("nocommit.w1", "working")
	inboxJob(t, r.d.Dir, h.Job, h.Brief)
	outboxReport(t, r.d.Dir, h.Job, "Verdict: LAND\nHead: "+reportSHA+"\n\nthe friend did it\n")
	r.d.heldCards = []HeldCard{h}
	card := Card{
		ID:     h.Card,
		Brief:  filepath.Join(r.d.Dir, "inbox", h.Job, "BRIEF.md"),
		Outbox: filepath.Join(r.d.Dir, "outbox", h.Job),
	}
	ln := &lane{n: 1, card: &card}
	l := &loop{
		d:   r.d,
		ctx: context.Background(),
		lanes: &laneSet{lanes: []*lane{ln}, state: LaneState{Started: map[string]Started{
			h.Job: {Lane: 1, Card: card},
		}}},
	}
	return r, card, l, ln
}

// TestNoCommitReportIsFinishedFromTheBranchWithAnEmptyHead is the lane and the
// outbox. A report that names a sha, with no commit on the branch, is finished
// from the branch with an empty head. Friend sync is not handed that sha.
func TestNoCommitReportIsFinishedFromTheBranchWithAnEmptyHead(t *testing.T) {
	t.Parallel()
	const reportSHA = "0123456789abcdef0123456789abcdef01234567"

	t.Run("lane", func(t *testing.T) {
		t.Parallel()
		r, card, l, ln := noCommitLane(t, reportSHA)
		f := &finishes{}
		r.d.Finish = f.finish
		took := l.branchFinish(laneResult{ln: ln, t: &turn{}, turn: LaneTurn{Exit: 0}}, card, LaneEnd{Exit: 0}, "lane end", t0)
		assert.True(t, took)
		sent := f.got()
		require.Len(t, sent, 1)
		assert.NotContains(t, sent[0], "--head")
		assert.NotContains(t, strings.Join(sent[0], " "), reportSHA)
		assert.Contains(t, sent[0], "--failed")
		raw, err := os.ReadFile(card.Report())
		require.NoError(t, err)
		_, head := reportVerdict(string(raw))
		assert.Empty(t, head)
		assert.NotContains(t, string(raw), reportSHA)

		l.outboxStep(t0)
		assert.Len(t, f.got(), 1, "the outbox pass does not send the report's sha")
	})

	t.Run("outbox", func(t *testing.T) {
		t.Parallel()
		r, card, l, ln := noCommitLane(t, reportSHA)
		took := l.branchFinish(laneResult{ln: ln, t: &turn{}, turn: LaneTurn{Exit: 0}}, card, LaneEnd{Exit: 0}, "lane end", t0)
		assert.True(t, took, "the lane takes the report even when it cannot send")
		raw, err := os.ReadFile(card.Report())
		require.NoError(t, err)
		_, head := reportVerdict(string(raw))
		assert.Empty(t, head)
		assert.NotContains(t, string(raw), reportSHA)

		f := &finishes{}
		r.d.Finish = f.finish
		l.outboxStep(t0)
		sent := f.got()
		require.Len(t, sent, 1, "friend sync's outbox pass still finishes the report")
		assert.NotContains(t, sent[0], "--head")
		assert.NotContains(t, strings.Join(sent[0], " "), reportSHA)
	})
}
