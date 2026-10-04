package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A HOLD naming a brief defect (docs/SPEC-SPRINT.md section 1, a brief defect): the work card
// ends in its member's defect cell, never in ok or failed, so ok% is unchanged; the primary
// counts it, the stream's control card counts it, and the judgment asks to re-cut the brief,
// never to redeal it.
func TestAHoldNamingABriefDefectCountsAgainstTheStreamNotTheWorker(t *testing.T) {
	t.Parallel()
	// okPct is the member's ok% as the fleet and friends tables compute it: pct(ok/ok+failed).
	okPct := func(s *Snapshot, row string) int {
		ok, failed := s.Fleet.Count(row, DoneOK), s.Fleet.Count(row, DoneFailed)
		if ok+failed == 0 {
			return -1
		}
		return 100 * ok / (ok + failed)
	}
	rows := []struct {
		name, report, reason string
	}{
		{"the base lacks a PATHS file", "friend amy HOLD: HOLD, brief defect: the PATHS file internal/seatcheck/seatcheck.go does not exist on the base sprint/x", BriefDefectBase},
		{"a duplicate of landed work", "friend amy HOLD: HOLD (brief defect): a duplicate of landed work; PR 5300 landed the same change", BriefDefectDuplicate},
		{"a decision", "friend amy HOLD: Brief defect: the task needs a decision the owner has not made", BriefDefectDecision},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.reason, BriefDefectOf(tc.report))
			w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
			dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
			row := FriendRow("amy")
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: row, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
			require.Equal(t, 100, okPct(w.s, row))

			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, As: row, Gens: gensOf(w.s, "s1-2.w1"), Failed: true, Report: tc.report}))
			wc := w.s.Fleet.Card("s1-2.w1")
			require.NotNil(t, wc)
			assert.Equal(t, DoneDefect, wc.Col, "the work card ends in the defect cell")
			assert.Equal(t, tc.reason, wc.F(FieldBriefDefect))
			assert.Equal(t, 1, w.s.Fleet.Count(row, DoneOK))
			assert.Equal(t, 0, w.s.Fleet.Count(row, DoneFailed), "a brief defect is never the worker's failure")
			assert.Equal(t, 100, okPct(w.s, row), "ok% is unchanged")

			pr := w.s.Primary("s1-2")
			assert.Equal(t, Review, pr.Col)
			assert.Equal(t, "failed", pr.F("result"), "no work came back: it is not read")
			assert.Equal(t, tc.reason, pr.F(FieldBriefDefect))
			assert.Equal(t, 0, pr.Int("failed"), "the attempt's failure is the brief's: no escalation counts it")
			assert.Equal(t, "1", w.s.StreamCtl("s1").F(FieldBriefDefects), "the stream counts it")

			assert.Empty(t, w.notesOf(NWorkFailed), "no failed-work judgment, so nothing asks a redeal")
			js := w.notesOf(NBriefDefect)
			require.Len(t, js, 1)
			assert.Equal(t, Judgment, js[0].Kind)
			assert.Equal(t, "s1", js[0].Stream)
			assert.Equal(t, []string{"s1-2"}, js[0].Primaries)
			assert.Equal(t, Decisions[NBriefDefect], js[0].Decisions)
			assert.Contains(t, js[0].Decisions, DecisionRecut)
			assert.NotContains(t, js[0].Decisions, "rework with a fix")
			assert.Contains(t, js[0].What, tc.reason)
			assert.Empty(t, Check(w.s, nil), "what is always true holds")
		})
	}

	t.Run("a HOLD naming no brief defect is the worker's failed work", func(t *testing.T) {
		t.Parallel()
		for _, report := range []string{"friend amy HOLD: the gate is red", "friend amy HOLD: not a brief defect: the gate is red", "friend amy FAIL: no brief defect, the test is wrong"} {
			assert.Empty(t, BriefDefectOf(report), report)
		}
		w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
		dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
		row := FriendRow("amy")
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: row, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, As: row, Gens: gensOf(w.s, "s1-2.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
		assert.Equal(t, DoneFailed, w.s.Fleet.Card("s1-2.w1").Col)
		assert.Equal(t, 50, okPct(w.s, row))
		assert.Len(t, w.notesOf(NWorkFailed), 1)
		assert.Empty(t, w.notesOf(NBriefDefect))
		assert.Empty(t, w.s.StreamCtl("s1").F(FieldBriefDefects))
	})

	t.Run("a machine worker's brief defect counts on the stream, not on the fleet's ok%", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.", "c: another\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		member := wc.Row
		w.must(Take(w.s, TakeReq{As: member}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: member, Gens: gensOf(w.s, "s1-1.w1"), Failed: true,
			Report: "brief defect: the base lacks cmd/nova-sprint/machinery.go, which PR 5281 adds"}))
		assert.Equal(t, DoneDefect, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, -1, okPct(w.s, member), "nothing counts in its ok%")
		assert.Equal(t, "1", w.s.StreamCtl("s1").F(FieldBriefDefects))
		assert.Len(t, w.notesOf(NBriefDefect), 1)
	})
}
