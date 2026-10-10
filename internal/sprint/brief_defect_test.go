package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A HOLD naming a brief defect (docs/SPEC-SPRINT.md section 1, a brief defect): each of the
// three reasons alone, with no label, is one; the work card ends in its member's defect cell,
// never in ok or failed, so ok% is unchanged; the primary counts it, the stream's control
// card counts it, and the judgment asks to re-cut the brief, never to redeal it: rework
// refuses it, naming drop then add, and a stalled one is offered the re-cut, never rework.
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
		{"the base lacks a PATHS file", "friend amy HOLD: the base lacks a PATHS file: internal/seatcheck/seatcheck.go is on sprint/y only", BriefDefectBase},
		{"a PATHS file that does not exist on the base", "friend amy HOLD: internal/seatcheck/seatcheck.go does not exist on the base sprint/x", BriefDefectBase},
		{"a duplicate of landed work", "friend amy HOLD: a duplicate of landed work; PR 5300 landed the same change", BriefDefectDuplicate},
		{"a friend's nothing to do, already in the base", "friend amy FAIL: nothing to do: the staged base already contains this card's implementation", BriefDefectDuplicate},
		{"a member's nothing to do, already in the base", "nothing to do: the staged base already contains this card's implementation", BriefDefectDuplicate},
		{"a decision delivered", "friend amy HOLD: a decision delivered; the owner decided it and the card asks for it again", BriefDefectDecision},
		{"a labelled decision delivered", "friend amy HOLD: brief defect: a decision delivered", BriefDefectDecision},
		{"a label with none of the three", "friend amy HOLD: brief defect: the TEST line names a test the PATHS cannot reach", BriefDefectOther},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.reason, BriefDefectOf(tc.report))
			w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
			dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
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

			// the judgment asks to re-cut, never to redeal: rework refuses it, naming drop then
			// add, and the judgment stays open
			assert.NotContains(t, ReworkResolves, NBriefDefect)
			p := Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "deal it again"})
			require.Len(t, p.Refused, 1, "rework of a brief defect: %+v", p)
			why := p.Refused[0].Why
			assert.Contains(t, why, "brief defect")
			drop, add := "nova-sprint drop s1-2", "nova-sprint add --stream s1"
			require.Contains(t, why, drop)
			require.Contains(t, why, add)
			assert.Less(t, strings.Index(why, drop), strings.Index(why, add), "drop, then add")
			open := w.openOn("s1-2")
			require.Len(t, open, 1)
			assert.Equal(t, NBriefDefect, open[0].Note.Type)

			// stalled, with nothing open on it, it is offered the re-cut, never rework, and the
			// judgment raised again is the brief's, never "stranded in review"
			w.s.Open = nil
			var f *Finding
			for _, x := range Unheld(running(w), w.s.Now) {
				if x.Subject == "s1-2" {
					f = &x
				}
			}
			require.NotNil(t, f, "the primary with nothing open on it is stalled")
			assert.Equal(t, []string{DecisionRecut, "drop", "wait"}, f.Decisions)
			note, ok := reviewJudgment(w.s, pr, reviewStep{})
			require.True(t, ok)
			assert.Equal(t, NBriefDefect, note.Type)
			assert.Equal(t, Decisions[NBriefDefect], note.Decisions)
		})
	}

	t.Run("a HOLD naming no brief defect is the worker's failed work", func(t *testing.T) {
		t.Parallel()
		for _, report := range []string{
			"friend amy HOLD: the gate is red",
			"friend amy HOLD: not a brief defect: the gate is red",
			"friend amy FAIL: no brief defect, the test is wrong",
			"friend amy HOLD: hold-is-a-brief-defect: the gate is red",
			"friend amy HOLD: card hold-is-a-brief-defect.w2 ran; the gate is red",
			"friend amy HOLD: not a duplicate of landed work; the gate is red",
			"friend amy HOLD: my decision was to stop; the gate is red",
		} {
			assert.Empty(t, BriefDefectOf(report), report)
		}
		w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
		dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
		row := FriendRow("amy")
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: row, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, As: row, Gens: gensOf(w.s, "s1-2.w1"), Failed: true,
			Report: "friend amy HOLD: hold-is-a-brief-defect: the gate is red"}))
		assert.Equal(t, DoneFailed, w.s.Fleet.Card("s1-2.w1").Col)
		assert.Equal(t, 50, okPct(w.s, row))
		assert.Len(t, w.notesOf(NWorkFailed), 1)
		assert.Empty(t, w.notesOf(NBriefDefect))
		assert.Empty(t, w.s.StreamCtl("s1").F(FieldBriefDefects))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "the fix"}))
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
			Report: "the base lacks cmd/nova-sprint/machinery.go, which PR 5281 adds"}))
		assert.Equal(t, DoneDefect, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, -1, okPct(w.s, member), "nothing counts in its ok%")
		assert.Equal(t, "1", w.s.StreamCtl("s1").F(FieldBriefDefects))
		assert.Len(t, w.notesOf(NBriefDefect), 1)
	})
}

// Fault 17 (2026-10-10): a worker that finds its card already done at the staged base says
// so, and that is the brief's duplicate, never the worker's failure. Reversed: a nothing to
// do that names no base already holding the work, or a negated one, is no brief defect.
func TestNothingToDoAlreadyInTheBaseIsADuplicateOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, BriefDefectDuplicate, BriefDefectOf("friend freddy HOLD: nothing to do: the base already has this change (commit 1a2b3c4)"))
	for _, report := range []string{
		"nothing to do: the brief asks for nothing that can change",
		"friend amy FAIL: nothing to do; the lane wrote no report",
		"friend amy HOLD: not the staged base already contains it; the test fails on the base",
		"friend amy HOLD: the database already contains a row for this key",
	} {
		assert.Empty(t, BriefDefectOf(report), report)
	}
}
