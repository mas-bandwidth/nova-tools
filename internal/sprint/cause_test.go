package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A failed attempt carries its cause from the moment it fails (docs/SPEC-SPRINT.md section 1,
// "The cause of a failed attempt"; cause.go). The owner, 2026-10-05, reading 72 to 76 percent
// ok on the friends table: "75% seems really low." That night's HOLDs were files outside the
// brief's PATHS, no remote or worktree to push from, names a guardrail refused and lanes that
// died without a report; nearly every one came back as a twin that landed, and each counted
// against the worker. On the twin store (store.Mem): a HOLD naming files outside PATHS is a
// brief fault, a finish with no head from a lane that died is machinery, a reader's broken read
// of an ok finish is the work's own fault; each counts in its own column beside ok, and ok%
// moves only on the last.

// causeRig is a sprint on the twin: one member m1 of width 4, readers reader-a..c, and
// stream s1 of three flash cards.
type causeRig struct {
	t   *testing.T
	st  *store.Store
	m   *store.Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newCauseRig(t *testing.T) *causeRig {
	t.Helper()
	r := &causeRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.RowsAdd(r.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	require.NoError(t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(t, err)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 3, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	return r
}

func (r *causeRig) must(step store.Step) {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
}

func (r *causeRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// finish deals primary id's attempt, takes it and finishes it as req says.
func (r *causeRig) finish(id string, req sprint.FinishReq) *sprint.Card {
	r.t.Helper()
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	wc := r.snap().Fleet.Card(r.snap().Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	req.Sel, req.Gens = sprint.Sel{IDs: []string{wc.ID}}, map[string]int{wc.ID: wc.Int("gen")}
	r.must(store.FinishStep(req))
	return r.snap().Fleet.Card(wc.ID)
}

// row is m1's fleet row as the table renders it: done, ok% and the four counters.
func (r *causeRig) row() map[string]string {
	r.t.Helper()
	shapes, err := r.m.Shapes(r.ctx, []string{"t-fleet"})
	require.NoError(r.t, err)
	ft := shapes[0]
	for _, row := range ft.Rows {
		if row.Key != "m1" {
			continue
		}
		out := map[string]string{}
		for _, col := range []string{sprint.Done, sprint.OkPct, sprint.DoneOK, sprint.DoneFailed, sprint.DoneBrief, sprint.DoneMachinery} {
			j := ft.Column(col)
			require.GreaterOrEqual(r.t, j, 0, col)
			out[col] = ntable.CellText(ft.Columns, row, j)
		}
		return out
	}
	r.t.Fatal("no row m1")
	return nil
}

func TestAFailedAttemptCarriesItsCauseAndOkPercentCountsOnlyWorkFaults(t *testing.T) {
	t.Parallel()
	r := newCauseRig(t)
	counts := func(done, pct, ok, failed, brief, machinery string) map[string]string {
		return map[string]string{sprint.Done: done, sprint.OkPct: pct, sprint.DoneOK: ok, sprint.DoneFailed: failed, sprint.DoneBrief: brief, sprint.DoneMachinery: machinery}
	}

	// s1-3's work finishes ok: 100%
	ok := r.finish("s1-3", sprint.FinishReq{As: "m1", Head: "0123456789abcdef0123456789abcdef01234567"})
	assert.Equal(t, sprint.DoneOK, ok.Col)
	assert.Empty(t, ok.F(sprint.FieldCause), "an ok finish has no cause")
	assert.Equal(t, counts("1", "100.0%", "1", "0", "0", "0"), r.row())

	// s1-1's worker holds: the fix is in a file outside the brief's PATHS. A brief fault: done
	// moves, ok% does not
	brief := r.finish("s1-1", sprint.FinishReq{As: "m1", Failed: true,
		Report: "HOLD: the fix is in internal/x.go, outside the brief's PATHS; PATHS-PROPOSED: internal/x.go"})
	assert.Equal(t, sprint.DoneBrief, brief.Col)
	assert.Equal(t, sprint.CauseBrief, brief.F(sprint.FieldCause))
	assert.Equal(t, "no", brief.F("ok"), "the take still finished failed")
	assert.Equal(t, counts("2", "100.0%", "1", "0", "1", "0"), r.row())

	// s1-2 finishes with no head: its lane died without a report. A machinery fault
	mach := r.finish("s1-2", sprint.FinishReq{As: "m1", Failed: true, Report: "the lane died without a report"})
	assert.Equal(t, sprint.DoneMachinery, mach.Col)
	assert.Equal(t, sprint.CauseMachinery, mach.F(sprint.FieldCause))
	assert.Equal(t, counts("3", "100.0%", "1", "0", "1", "1"), r.row())

	// every failed primary is in review all the same, its failed-work record kept
	s := r.snap()
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Equal(t, sprint.Review, s.Work.Card(id).Col, id)
		assert.Equal(t, "failed", s.Work.Card(id).F("result"), id)
	}

	// a reader finds s1-3's fix wrong; its rework counts the ok finish as the work's own
	// fault, and ok% moves on it alone
	r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}}))
	var read *sprint.Card
	for _, rc := range r.snap().Readers.Of("s1-3") {
		if rc.Col == sprint.Asked {
			read = rc
			break
		}
	}
	require.NotNil(t, read, "s1-3 is asked of a reader")
	r.must(store.ReadStep(sprint.ReadReq{As: read.Row, Verdict: "broken", Finding: "internal/x.go:5: wrong fix: the guard is inverted", Sel: sprint.Sel{IDs: []string{read.ID}}}))
	assert.Equal(t, counts("3", "100.0%", "1", "0", "1", "1"), r.row(), "a broken read alone moves nothing: the rework does")
	r.must(store.ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}, Fix: "invert the guard"}))
	work := r.snap().Fleet.Card(ok.ID)
	assert.Equal(t, sprint.DoneFailed, work.Col)
	assert.Equal(t, sprint.CauseWork, work.F(sprint.FieldCause))
	assert.Equal(t, counts("3", "0.0%", "0", "1", "1", "1"), r.row())

	// the day's per-cause totals, one line
	assert.Equal(t, map[string]int{sprint.CauseWork: 1, sprint.CauseBrief: 1, sprint.CauseMachinery: 1}, sprint.CauseCounts(r.snap().Fleet))
	assert.Equal(t, "failed attempts: work 1 · brief 1 · machinery 1", sprint.CauseLine(sprint.CauseCounts(r.snap().Fleet)))
}

// The cause of a failed finish by its report's words: a cause given wins; the brief's bound
// and a brief the worker could not satisfy as written are the brief's; the machinery failing
// the attempt is the machinery's; anything else is the work's.
func TestFailureCauseReadsTheReport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		given, report string
		atBound       bool
		want          string
	}{
		{"", "friend amy HOLD: the fix needs cmd/x.go, which is outside PATHS", false, sprint.CauseBrief},
		{"", "friend amy HOLD: brief defect: the card's PATHS cannot hold its STOP", false, sprint.CauseBrief},
		{"", "HOLD; PATHS-PROPOSED: internal/a.go,internal/b.go", false, sprint.CauseBrief},
		{"", "the doc guardrail refused the name nova-x", false, sprint.CauseBrief},
		{"", "the head fails the lander's checks: it changes files outside its PATHS (E12): x.go", false, sprint.CauseBrief},
		{"", "the same finding as attempt 1", true, sprint.CauseBrief},
		{"", "friend amy HOLD: no remote or worktree to push from", false, sprint.CauseMachinery},
		{"", "friend amy HOLD: no push: the push was refused", false, sprint.CauseMachinery},
		{"", "the lane died without a report", false, sprint.CauseMachinery},
		{"", "the packet has no tier", false, sprint.CauseMachinery},
		{"", "the daemon did not deliver the brief", false, sprint.CauseMachinery},
		{"", "friend amy FAIL: the test TestX fails: the fix is wrong", false, sprint.CauseWork},
		{"", "friend amy verdict none is not LAND, HOLD or FAIL; All green", false, sprint.CauseWork},
		{"", "", false, sprint.CauseWork},
		{sprint.CauseMachinery, "outside PATHS", false, sprint.CauseMachinery},
		{"nonsense", "a wrong fix", false, sprint.CauseWork},
	} {
		assert.Equal(t, tc.want, sprint.FailureCause(tc.given, tc.report, tc.atBound), "%q %q", tc.given, tc.report)
	}
	assert.Equal(t, sprint.DoneFailed, sprint.CauseColumn(sprint.CauseWork), "the work's faults stay in failed, the column ok% reads")
	assert.Equal(t, sprint.DoneBrief, sprint.CauseColumn(sprint.CauseBrief))
	assert.Equal(t, sprint.DoneMachinery, sprint.CauseColumn(sprint.CauseMachinery))
}
