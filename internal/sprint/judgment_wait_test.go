package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openOfType is the open judgments of the type.
func openOfType(w *world, typ string) []Open {
	var out []Open
	for _, o := range w.s.Open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// failFinish is s1-<n>'s work card finished failed, with the report given.
func failFinish(w *world, n int, report string, rules bool) {
	w.t.Helper()
	id := "s1-" + itoa(n) + ".w1"
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{id}}, Gens: gensOf(w.s, id), Failed: true, Report: report, AnswerRules: rules}))
}

// Judgment answer latency (docs/SPEC-SPRINT.md section 8, judgment-answer-latencyb.w1): a
// judgment a rule answers is answered in the step that raises it, and the rest are ordered
// by the cards blocked behind each and record their wait, raise to answer.
func TestRuleableJudgmentsAreAnsweredAtRaiseAndTheRestRecordTheirWait(t *testing.T) {
	t.Parallel()

	t.Run("a failed finish the failed rule answers is never open", func(t *testing.T) {
		t.Parallel()
		w := dealt(t)
		failFinish(w, 1, "tests red", true)
		assert.Empty(t, openOfType(w, NWorkFailed), "the judgment was raised and left for a later pass")
		for _, n := range w.notesOf(NWorkFailed) {
			assert.Equal(t, Decided, n.Kind, "a judgment of the type was written: %+v", n)
			assert.Contains(t, n.What, "answered by rule failed")
			assert.Zero(t, n.Waited, "answered at raise waits nothing")
		}
		require.Len(t, w.notesOf(NWorkFailed), 1, "one decided note records the answer")
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, Working, pr.Col, "the next attempt is made in the same step")
		assert.Equal(t, "2", pr.F("attempt"))
		assert.Equal(t, "1", pr.F(FieldRuleFails))
		assert.Contains(t, pr.F(FieldRuleAnswer), RuleFailed+": "+ActRework)
		require.NotNil(t, w.s.Fleet.Card("s1-1.w2"), "the new attempt's work card")
		w.clean("answered at raise")
	})

	t.Run("a failed finish the rule leaves or does not answer keeps its judgment", func(t *testing.T) {
		t.Parallel()
		for name, prep := range map[string]func(*world, *bool){
			"the rule is off":        func(w *world, _ *bool) { w.s.RulesOff = []string{RuleFailed} },
			"the caller asks for it": func(_ *world, rules *bool) { *rules = false },
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				w := dealt(t)
				rules := true
				prep(w, &rules)
				failFinish(w, 1, "tests red", rules)
				assert.Len(t, openOfType(w, NWorkFailed), 1)
				assert.Equal(t, Review, w.s.Work.Card("s1-1").Col)
			})
		}
	})

	t.Run("the rest are ordered by the cards behind them", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		// a stopped stream's judgment names the card it stopped on, d1, which nothing needs;
		// the cards behind its stream are three (they need d2), behind b1 one
		w.must(Add(w.s, AddReq{Stream: "sd", IDs: []string{"d1", "d2"}}))
		w.must(Add(w.s, AddReq{Stream: "se", IDs: []string{"e1", "e2", "e3"}, Needs: []string{"d2"}}))
		w.must(Add(w.s, AddReq{Stream: "sb", IDs: []string{"b1"}}))
		w.must(Add(w.s, AddReq{Stream: "sc", IDs: []string{"c1"}, Needs: []string{"b1"}}))
		stopped := judgment(NConflict, "sd", t0.Add(-time.Minute), 0)
		stopped.StreamLevel, stopped.Primaries = true, []string{"d1"}
		w.note(judgment(NBlocked, "sb", t0.Add(-time.Hour), 0, "b1"), stopped)
		groups := Inbox(InboxReq{Now: t0, Open: w.s.Open, Weights: NeedWeights(w.s)})
		require.Len(t, groups, 2)
		assert.Equal(t, NConflict, groups[0].Type, "the judgment with the most cards behind it first")
		assert.Equal(t, 3, groups[0].Behind)
		assert.Equal(t, 1, groups[1].Behind)
		old := Inbox(InboxReq{Now: t0, Open: w.s.Open, Weights: Weights(w.s)})
		assert.Equal(t, NBlocked, old[0].Type, "the primaries' own weights rank the stream's judgment last")
	})

	t.Run("each answer records its wait and the last day shows the median and p90", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 3)
		w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 3}}))
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 10}}))
		w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: 10}}))
		failFinish(w, 3, "alpha broke", true) // answered at raise: waits nothing
		failFinish(w, 1, "beta broke", false)
		w.tick(5 * time.Minute)
		p, _ := TickRuleRework(w.s, TickReq{AnswerRules: true})
		w.must(p)
		failFinish(w, 2, "gamma broke", false)
		w.tick(15 * time.Minute)
		p, _ = TickRuleRework(w.s, TickReq{AnswerRules: true})
		w.must(p)
		assert.Empty(t, openOfType(w, NWorkFailed))

		var waits []time.Duration
		for _, n := range w.notes {
			if n.Kind == Decided {
				waits = append(waits, n.Waited)
			}
		}
		assert.ElementsMatch(t, []time.Duration{0, 5 * time.Minute, 15 * time.Minute}, waits, "the wait is on the answer itself")

		got := AnswerWaits(w.notes, w.s.Now, 24*time.Hour)
		assert.Equal(t, AnswerWait{N: 3, P50: 5 * time.Minute, P90: 15 * time.Minute}, got)
		assert.Equal(t, "answered 24h: n=3 wait p50=5m0s p90=15m0s", got.Line())
		assert.Zero(t, AnswerWaits(w.notes, w.s.Now.Add(25*time.Hour), 24*time.Hour).N, "an answer older than the window is not counted")
		assert.Empty(t, AnswerWaits(nil, w.s.Now, 24*time.Hour).Line())
	})
}
