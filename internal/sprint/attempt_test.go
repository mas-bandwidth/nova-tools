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

	// 1. Math of ComputeOKPercent:
	assert.Equal(t, 0.0, ComputeOKPercent(0, 0), "empty denominator produces 0.0%")
	assert.Equal(t, 100.0, ComputeOKPercent(5, 0), "all ok produces 100.0%")
	assert.Equal(t, 50.0, ComputeOKPercent(1, 1), "1 ok 1 failed produces 50.0%")
	assert.InDelta(t, 66.6666, ComputeOKPercent(2, 1), 0.001, "2 ok 1 failed produces ~66.7%")

	// 2. ClassifyAttempt and finding extraction:
	t.Run("ClassifyAttempt", func(t *testing.T) {
		blame, class, finding, fix := ClassifyAttempt("status: ok\nall tests pass\nfix: card-123", false, "")
		assert.Equal(t, BlameNone, blame)
		assert.Equal(t, "card-123", fix)

		// Brief defect: PATHS do not hold
		blame, class, finding, _ = ClassifyAttempt("PATHS: paths does not hold the named file\nstatus: failed", true, "")
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectPaths, class)
		assert.Contains(t, finding, "paths does not hold")

		// Brief defect: TEST name does not exist
		blame, class, _, _ = ClassifyAttempt("finding: test name does not exist in module", true, "")
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectTestName, class)

		// Brief defect: STOP contradicts
		blame, class, _, _ = ClassifyAttempt("finding: stop contradicts the tree", true, "")
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectBrief, class)

		// Provider failure:
		blame, class, _, _ = ClassifyAttempt("provider failure: 429 rate limit exceeded", true, "")
		assert.Equal(t, BlameProvider, blame)
		assert.Equal(t, DefectProvider, class)

		// Launch refused:
		blame, class, _, _ = ClassifyAttempt("staging refused: docker daemon down", true, "")
		assert.Equal(t, BlameCoordinator, blame)
		assert.Equal(t, DefectLaunchRefused, class)

		// Worker failure:
		blame, class, _, _ = ClassifyAttempt("compiler error: unexpected token", true, "")
		assert.Equal(t, BlameWorker, blame)
		assert.Equal(t, DefectWork, class)
	})

	// 3. WorkerStats counter aggregation:
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
		assert.Equal(t, 1, stats.OK, "ok cards")
		assert.Equal(t, 1, stats.Failed, "failed counts only worker faults")
		assert.Equal(t, 1, stats.Refused, "refused counts launch refusals")
		assert.Equal(t, 1, stats.Provider, "provider counts provider failures")
		assert.Equal(t, 1, stats.Withdrawn, "withdrawn counts handed-back")
		assert.Equal(t, 2, stats.Coordinator, "coordinator defects")
		assert.Equal(t, 2, stats.Done, "done is ok + failed (attempts worker actually ran)")
		assert.Equal(t, 50.0, stats.OKPct, "ok% = 1 / (1 + 1) = 50.0%")
	})

	// 4. World test verifying finishPlan behavior and attempt accounting:
	t.Run("WorldLifecycle", func(t *testing.T) {
		w := newWorld(t, "reader-a", "reader-b")
		w.s.Coordinator = "coordinator"
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))

		// Card 1: Worker runs and succeeds
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-1", Brief: "brief 1"}}}))
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
		assert.Equal(t, "m1", w.s.Fleet.Card("s1-1.w1").Row)
		takeCard(w, "s1-1.w1")

		w.must(Finish(w.s, FinishReq{
			As:     "m1",
			Sel:    Sel{IDs: []string{"s1-1.w1"}},
			Gens:   gensOf(w.s, "s1-1.w1"),
			Head:   "head1",
			Report: "all ok",
		}))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 0, w.s.Fleet.Count("m1", DoneFailed))

		// Card 2: Worker runs and fails (worker fault)
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-2", Brief: "brief 2"}}}))
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		require.NotNil(t, w.s.Fleet.Card("s1-2.w1"))
		takeCard(w, "s1-2.w1")

		w.must(Finish(w.s, FinishReq{
			As:     "m1",
			Sel:    Sel{IDs: []string{"s1-2.w1"}},
			Gens:   gensOf(w.s, "s1-2.w1"),
			Failed: true,
			Report: "syntax error: unexpected bracket",
		}))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "worker failure charged to m1")

		// Card 3: Launch refusal (staging refused)
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-3", Brief: "brief 3"}}}))
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		require.NotNil(t, w.s.Fleet.Card("s1-3.w1"))
		takeCard(w, "s1-3.w1")

		w.must(Finish(w.s, FinishReq{
			As:     "m1",
			Sel:    Sel{IDs: []string{"s1-3.w1"}},
			Gens:   gensOf(w.s, "s1-3.w1"),
			Failed: true,
			Report: cardhdr.EndStaging + ": staging sandbox could not start",
		}))
		// Refusal is NOT counted in DoneFailed of m1:
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "staging refusal not charged to m1 failed")
		wc3 := w.s.Fleet.Card("s1-3.w1")
		require.NotNil(t, wc3)
		assert.Equal(t, Withdrawn, wc3.Col)
		assert.Equal(t, BlameCoordinator, wc3.F(FieldBlame))
		assert.Equal(t, DefectLaunchRefused, wc3.F(FieldDefectClass))
		assert.Equal(t, 0, wc3.Int(FieldAttemptsRan), "refusal does not consume attempt bound")

		// Card 4: Provider failure (take ended)
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-4", Brief: "brief 4"}}}))
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		require.NotNil(t, w.s.Fleet.Card("s1-4.w1"))
		takeCard(w, "s1-4.w1")

		w.must(Finish(w.s, FinishReq{
			As:     "m1",
			Sel:    Sel{IDs: []string{"s1-4.w1"}},
			Gens:   gensOf(w.s, "s1-4.w1"),
			Failed: true,
			Report: cardhdr.EndProvider + ": 429 rate limit exceeded",
		}))
		// Provider failure is NOT counted in DoneFailed of m1:
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "provider failure not charged to m1 failed")
		wc4 := w.s.Fleet.Card("s1-4.w1")
		require.NotNil(t, wc4)
		assert.Equal(t, Withdrawn, wc4.Col)
		assert.Equal(t, BlameProvider, wc4.F(FieldBlame))
		assert.Equal(t, DefectProvider, wc4.F(FieldDefectClass))
		assert.Equal(t, 0, wc4.Int(FieldAttemptsRan), "provider failure does not consume attempt bound")

		// Card 5: Brief defect (PATHS do not hold)
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-5", Brief: "brief 5"}}}))
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		require.NotNil(t, w.s.Fleet.Card("s1-5.w1"))
		takeCard(w, "s1-5.w1")

		w.must(Finish(w.s, FinishReq{
			As:     "m1",
			Sel:    Sel{IDs: []string{"s1-5.w1"}},
			Gens:   gensOf(w.s, "s1-5.w1"),
			Failed: true,
			Report: "finding: PATHS does not hold the named code in repository",
		}))
		// Brief defect is NOT charged to m1:
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneOK))
		assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed), "brief defect not charged to m1 failed")

		// Charged to the coordinator seat row:
		coordRow := FriendRow(w.s.Coordinator)
		assert.Equal(t, 1, w.s.Fleet.Count(coordRow, DoneFailed), "brief defect charged to coordinator seat row")

		// Recount plan re-derives the counts consistently:
		plan, rows := RecountPlan(w.s)
		assert.False(t, plan.Empty())
		var m1Row RowRecount
		for _, r := range rows {
			if r.Row == "m1" {
				m1Row = r
				break
			}
		}
		assert.Equal(t, 1, m1Row.AfterOK)
		assert.Equal(t, 1, m1Row.AfterFail)

		// FindDefects finds the coordinator and provider defects:
		defects := FindDefects(w.s, time.Time{}, "")
		assert.GreaterOrEqual(t, len(defects), 3, "defects collected launch refusal, provider failure, and brief defect")
		classes := make(map[string]bool)
		for _, d := range defects {
			classes[d.Class] = true
		}
		assert.True(t, classes[DefectLaunchRefused])
		assert.True(t, classes[DefectProvider])
		assert.True(t, classes[DefectPaths])
	})
}
