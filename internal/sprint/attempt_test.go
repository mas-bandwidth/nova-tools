package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

func TestOkPercentCountsOnlyAttemptsAWorkerRan(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0.0, ComputeOKPercent(0, 0))
	assert.Equal(t, 100.0, ComputeOKPercent(5, 0))
	assert.Equal(t, 50.0, ComputeOKPercent(1, 1))
	assert.InDelta(t, 66.6666, ComputeOKPercent(2, 1), 0.001)

	t.Run("ClassifyAttempt", func(t *testing.T) {
		blame, _, _, fix := ClassifyAttempt("status: ok\nall tests pass\nfix: card-123", false)
		assert.Equal(t, BlameNone, blame)
		assert.Equal(t, "card-123", fix)

		blame, class, finding, _ := ClassifyAttempt("PATHS: paths does not hold the named file\nstatus: failed", true)
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectPaths, class)
		assert.Contains(t, finding, "paths does not hold")

		blame, class, _, _ = ClassifyAttempt("finding: test name does not exist in module", true)
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectTestName, class)

		blame, class, _, _ = ClassifyAttempt("finding: stop contradicts the tree", true)
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectBrief, class)

		blame, class, _, _ = ClassifyAttempt(cardhdr.EndProvider+": 429 rate limit exceeded", true)
		assert.Equal(t, BlameProvider, blame)
		assert.Equal(t, DefectProvider, class)
		blame, class, _, _ = ClassifyAttempt("worker found a 429 in an API test", true)
		assert.Equal(t, BlameWorker, blame, "untyped report text cannot fabricate provider blame")
		assert.Equal(t, DefectWork, class)

		blame, class, _, _ = ClassifyAttempt(cardhdr.EndStaging+": docker daemon down", true)
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectLaunchRefused, class)

		blame, class, _, _ = ClassifyAttempt("compiler error: unexpected token", true)
		assert.Equal(t, BlameWorker, blame)
		assert.Equal(t, DefectWork, class)
	})

	t.Run("WorkerStats", func(t *testing.T) {
		cards := []*Card{
			{ID: "c1", Col: DoneOK, Fields: map[string]string{"ok": "yes"}},
			{ID: "c2", Col: DoneFailed, Fields: map[string]string{"ok": "no", FieldBlame: BlameWorker, FieldDefectClass: DefectWork}},
			{ID: "c3", Col: Withdrawn, Fields: map[string]string{FieldBlame: BlameCoordinator, FieldDefectClass: DefectLaunchRefused}},
			{ID: "c4", Col: Withdrawn, Fields: map[string]string{FieldBlame: BlameProvider, FieldDefectClass: DefectProvider}},
			{ID: "c5", Col: Withdrawn, Fields: map[string]string{"report": "HOLD: not started; handed back"}},
			{ID: "c6", Col: DoneFailed, Fields: map[string]string{FieldBlame: BlameCoordinator, FieldDefectClass: DefectPaths}},
		}
		stats := WorkerStats(cards)
		assert.Equal(t, 1, stats.OK)
		assert.Equal(t, 1, stats.Failed)
		assert.Equal(t, 1, stats.Refused)
		assert.Equal(t, 1, stats.Provider)
		assert.Equal(t, 1, stats.Withdrawn)
		assert.Equal(t, 2, stats.Coordinator)
		assert.Equal(t, 2, stats.Done)
		assert.Equal(t, 50.0, stats.OKPct)
	})

	t.Run("WorldLifecycle", func(t *testing.T) {
		w := fleetWorld(t, 0, 1, "m1")

		finish := func(id string, failed bool, report string) {
			t.Helper()
			w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{id}}))
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
			wc := id + ".w1"
			require.NotNil(t, w.s.Fleet.Card(wc))
			takeCard(w, wc)
			w.must(Finish(w.s, FinishReq{
				Sel:    Sel{IDs: []string{wc}},
				Gens:   gensOf(w.s, wc),
				Failed: failed,
				Report: report,
			}))
		}

		finish("s1-1", false, "all ok")
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 0, w.s.Fleet.Count("m1", DoneFailed))

		finish("s1-2", true, "syntax error: unexpected bracket")
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed))
		assert.Equal(t, BlameWorker, w.s.Fleet.Card("s1-2.w1").F(FieldBlame))

		finish("s1-3", true, cardhdr.EndLaunch+": worker process could not start")
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "a launch refusal is not failed work")
		wc3 := w.s.Fleet.Card("s1-3.w1")
		require.NotNil(t, wc3)
		assert.Equal(t, Withdrawn, wc3.Col)
		assert.Equal(t, BlameCoordinator, wc3.F(FieldBlame))
		assert.Equal(t, DefectLaunchRefused, wc3.F(FieldDefectClass))
		assert.Equal(t, 1, w.s.Work.Card("s1-3").Int("attempt"), "the refusal keeps the attempt, so a redeal does not spend another")
		assert.Equal(t, Ready, w.s.Work.Card("s1-3").Col)

		finish("s1-4", true, cardhdr.EndProvider+": 429 rate limit exceeded")
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "a provider failure is not failed work")
		wc4 := w.s.Fleet.Card("s1-4.w1")
		require.NotNil(t, wc4)
		assert.Equal(t, Withdrawn, wc4.Col)
		assert.Equal(t, BlameProvider, wc4.F(FieldBlame))
		assert.Equal(t, DefectProvider, wc4.F(FieldDefectClass))
		assert.Equal(t, 1, w.s.Work.Card("s1-4").Int("attempt"))

		finish("s1-5", true, "finding: PATHS does not hold the named code in repository")
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "a brief defect is not the member's failed work")
		wc5 := w.s.Fleet.Card("s1-5.w1")
		require.NotNil(t, wc5)
		assert.Equal(t, DoneDefect, wc5.Col)
		assert.Equal(t, BlameCoordinator, wc5.F(FieldBlame))
		assert.Equal(t, DefectPaths, wc5.F(FieldDefectClass))
		assert.Equal(t, "m1", wc5.Row, "the card stays on the member; ok% excludes the defect column")

		cards := w.s.Fleet.Column(DoneOK, DoneFailed, DoneDefect, Withdrawn)
		stats := WorkerStats(cards)
		assert.Equal(t, 1, stats.OK)
		assert.Equal(t, 1, stats.Failed)
		assert.Equal(t, 1, stats.Refused)
		assert.Equal(t, 1, stats.Provider)

		plan, rows := RecountPlan(w.s)
		assert.True(t, plan.Empty(), "a finish already wrote the attempt record: %s", plan.Units)
		var m1Row RowRecount
		for _, r := range rows {
			if r.Row == "m1" {
				m1Row = r
			}
		}
		assert.Equal(t, 1, m1Row.AfterOK)
		assert.Equal(t, 1, m1Row.AfterFail)
		w.must(plan)
		again, againRows := RecountPlan(w.s)
		assert.True(t, again.Empty())
		assert.Equal(t, rows, againRows)

		defects := FindDefects(w.s, time.Time{}, "")
		classes := map[string]bool{}
		for _, d := range defects {
			classes[d.Class] = true
		}
		assert.True(t, classes[DefectLaunchRefused])
		assert.True(t, classes[DefectProvider])
		assert.True(t, classes[DefectPaths])
	})
}
