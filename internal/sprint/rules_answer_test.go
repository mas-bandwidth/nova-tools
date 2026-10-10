package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judgments the seat answered by hand with a loop every few minutes are the tick's
// (docs/SPEC-SPRINT.md section 8, answered by rule): a reader's broken read with a finding
// reworks the card with the finding, a friend's card too; work that came back failed on a
// harness fault is reworked with its failure as the fix; a report that arrives for an
// attempt the deadline already failed finishes that attempt. On the core's twin (world).

// answerHead is the head a friend's attempt pushes.
const answerHead = "0123456789abcdef0123456789abcdef01234567"

// friendInReview is a world with s1-1 a friend's card dealt to amy, started, and finished:
// LAND at answerHead, or failed with the report given; amy's seat as the tick reads it.
func friendInReview(t *testing.T, failed bool, report string) (*world, FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
	dealWith(w, amy)
	startLanes(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Working, wc.Col, "started on her row")
	r := FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: report, Failed: failed}
	if !failed {
		r.Head = answerHead
	}
	w.must(Finish(w.s, r))
	require.Equal(t, Review, w.state("s1-1"))
	return w, amy
}

func TestABrokenReadWithAFindingReworksAFriendsCardByRule(t *testing.T) {
	t.Parallel()
	finding := "internal/x/a.go:12 drops the error from Close; return it"
	w, amy := friendInReview(t, false, "friend amy LAND: done")
	brokenOnce(t, w, finding)
	require.Len(t, openOf(w, NReadBroken, "s1-1"), 1, "the reader's finding is a judgment")

	a := answerOn(t, w, on(amy), NReadBroken, "s1-1")
	rules(w, on(amy))
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, 1, pr.Int("reworks"), "a friend's card found broken with a finding is reworked by rule: %s %s", a.Act, a.Why)
	assert.Equal(t, Ready, pr.Col, "its next attempt waits for her deal")
	assert.Equal(t, finding, pr.F("fix"), "the finding is the fix")
	assert.Empty(t, openOf(w, NReadBroken, "s1-1"), "the judgment is answered")
	require.Len(t, logged(w, RuleReadBroken), 1, "a decided note names the rule")
	assert.Contains(t, pr.F(FieldNote), NRuleAnswered+" "+RuleReadBroken+": ", "a note on the card names the rule")
	dealWith(w, amy)
	wc := w.s.Fleet.Card(WorkCardID("s1-1", 2))
	require.NotNil(t, wc, "the next attempt is dealt")
	assert.Equal(t, FriendRow("amy"), wc.Row, "to her")
	assert.Equal(t, finding, wc.F("fix"), "carrying the finding")
}

// harnessFaults is a failed report of each harness-fault class, as a friend's finish or a
// member's says it: none of them is a finding about the card's work.
var harnessFaults = map[string]string{
	"no result":       "friend amy FAIL: the turn ended with no RESULT.md",
	"deadline":        "friend amy FAIL: the deadline passed with no result",
	"verdict pending": "friend amy verdict PENDING is not LAND, HOLD or FAIL; Verdict: PENDING",
	"verdict none":    "friend amy verdict none is not LAND, HOLD or FAIL; the report has no verdict line",
	"cost line":       "friend amy HOLD: Cost: $0.31 tokens 2000 price_route=pro-a",
	"no head":         "friend amy LAND with no Head: <full sha>; Verdict: LAND, the tests pass",
	"lane died":       "friend amy FAIL: Verdict: FAIL nova-friend of amy: the runner ended job s1-1.w1 with no report, and no run of it is live: exit 1",
	"provider 5xx":    "friend amy FAIL: the provider answered 502 Bad Gateway",
	"push refused":    "friend amy HOLD: push refused: the head is not on the staged commit",
	"staged base tip": "friend amy HOLD: the staged BASE tip does not hold the card's work",
	"no step line":    "friend amy HOLD: step 2 not-done: the result carries no line for this step",
	"not started":     "friend amy HOLD: not started",
	"staging":         "friend amy FAIL: refused at staging: no bench mirror of mas-bandwidth/nova-tools",
	ClassEmptyRun:     emptyRunReport("amy"),
}

func TestAHarnessFaultFailureReworksTheCardByRule(t *testing.T) {
	t.Parallel()
	for class, report := range harnessFaults {
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			w, amy := friendInReview(t, true, report)
			require.Len(t, openOf(w, NWorkFailed, "s1-1"), 1, "work came back failed: a judgment")
			assert.Equal(t, class, HarnessFault(report), "the report's class")
			a := answerOn(t, w, on(amy), NWorkFailed, "s1-1")
			rules(w, on(amy))
			pr := w.s.Work.Card("s1-1")
			require.Equal(t, 1, pr.Int("reworks"), "a harness fault is reworked by rule: %s %s", a.Act, a.Why)
			assert.Contains(t, pr.F("fix"), report, "the failure is the fix")
			assert.Empty(t, pr.F(FieldTier), "on its tier")
			assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "the judgment is answered")
			require.Len(t, logged(w, RuleFailed), 1, "a decided note names the rule")
			assert.Contains(t, pr.F(FieldNote), NRuleAnswered+" "+RuleFailed+": ", "a note on the card names the rule")
		})
	}
	t.Run("a HOLD with findings: reworked with them", func(t *testing.T) {
		t.Parallel()
		report := "friend amy HOLD: internal/x/a.go:40 asserts the old name; the brief's rename misses it"
		w, amy := friendInReview(t, true, report)
		assert.Empty(t, HarnessFault(report))
		rules(w, on(amy))
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, 1, pr.Int("reworks"), "a HOLD with findings is reworked by rule")
		assert.Contains(t, pr.F("fix"), "internal/x/a.go:40 asserts the old name", "its findings are the fix")
		assert.Empty(t, openOf(w, NWorkFailed, "s1-1"))
	})
	t.Run("outside every class: a judgment", func(t *testing.T) {
		t.Parallel()
		report := "friend amy FAIL: the change is larger than the brief says and I stopped"
		w, amy := friendInReview(t, true, report)
		assert.Empty(t, HarnessFault(report))
		rules(w, on(amy))
		assert.Zero(t, w.s.Work.Card("s1-1").Int("reworks"), "a failure no class names waits for a person")
		assert.Len(t, openOf(w, NWorkFailed, "s1-1"), 1)
	})
	t.Run("at the attempt bound: the brief's judgment", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("friend amy"))
		w.s.Work.SetProp(PropAttempts, "1")
		amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
		dealWith(w, amy)
		startLanes(w, amy)
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: harnessFaults["lane died"]}))
		rules(w, on(amy))
		assert.Zero(t, w.s.Work.Card("s1-1").Int("reworks"), "never past the attempt bound")
		assert.Len(t, openOf(w, NBriefWrong, "s1-1"), 1, "the brief's judgment stands")
	})
	t.Run("the same fault on many machine cards: each reworked on its tier", func(t *testing.T) {
		t.Parallel()
		var briefs []string
		for range RuleSameFailureCards {
			briefs = append(briefs, "c: the work (s1)\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
		}
		w := friendWorld(t, briefs...)
		dealWith(w)
		report := "verdict not-done; step 2 not-done: the result carries no line for this step"
		for i := 1; i <= RuleSameFailureCards; i++ {
			id := WorkCardID("s1-"+itoa(i), 1)
			wc := w.s.Fleet.Card(id)
			w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{id}}, Gens: gensOf(w.s, id)}))
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{id}}, Gens: gensOf(w.s, id), Failed: true, Report: report}))
		}
		rules(w, on())
		for i := 1; i <= RuleSameFailureCards; i++ {
			pr := w.s.Work.Card("s1-" + itoa(i))
			assert.Equal(t, 1, pr.Int("reworks"), "%s: a harness fault is the harness's, never the fleet's judgment", pr.ID)
			assert.Empty(t, pr.F(FieldTier), "%s: on its tier", pr.ID)
		}
	})
}

func TestFinishAcceptsAReportForAnAttemptTheDeadlineFailed(t *testing.T) {
	t.Parallel()
	t.Run("a LAND finishes the failed attempt ok", func(t *testing.T) {
		t.Parallel()
		w, _ := friendInReview(t, true, harnessFaults["lane died"])
		require.Len(t, openOf(w, NWorkFailed, "s1-1"), 1)
		p := w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Head: answerHead, Report: "friend amy LAND: done, the tests pass"}))
		require.Len(t, p.Units, 1)
		wc := w.s.Fleet.Card("s1-1.w1")
		assert.Equal(t, DoneOK, wc.Col, "the attempt is finished ok")
		assert.Equal(t, answerHead, wc.F("head"))
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, Review, pr.Col)
		assert.Equal(t, "ok", pr.F("result"), "its work stands")
		assert.Equal(t, answerHead, pr.F("head"))
		assert.Equal(t, 1, pr.Int("attempt"), "the card does not go round again")
		assert.Equal(t, 1, pr.Int("failed"), "the deadline's failure is not counted twice")
		assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "the failed judgment is closed")
		assert.Contains(t, p.Units[0].Moved, "late report", "the log says why it finished from failed")
	})
	t.Run("a HOLD replaces the failure with its findings", func(t *testing.T) {
		t.Parallel()
		w, amy := friendInReview(t, true, harnessFaults["lane died"])
		hold := "friend amy HOLD: internal/x/a.go:40 asserts the old name; the brief's rename misses it"
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: hold}))
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, 1, pr.Int("failed"), "one failed attempt")
		open := openOf(w, NWorkFailed, "s1-1")
		require.Len(t, open, 1, "one judgment, the HOLD's")
		assert.Contains(t, open[0].Note.What, "internal/x/a.go:40")
		rules(w, on(amy))
		assert.Contains(t, w.s.Work.Card("s1-1").F("fix"), "internal/x/a.go:40", "reworked with the HOLD's findings")
	})
	t.Run("an attempt its worker failed: a retried finish is refused as before", func(t *testing.T) {
		t.Parallel()
		w, _ := friendInReview(t, true, "friend amy FAIL: red")
		p := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy FAIL: red"})
		require.Len(t, p.Refused, 1, "the worker's own failure was its report")
		assert.Contains(t, p.Refused[0].Why, "not working")
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("failed"), "counted once")
	})
	t.Run("a FAIL, or the report it failed on already, is refused", func(t *testing.T) {
		t.Parallel()
		w, _ := friendInReview(t, true, harnessFaults["lane died"]) // an attempt the deadline failed
		p := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy FAIL: the change is larger than the brief says and I stopped"})
		require.Len(t, p.Refused, 1, "a FAIL is no LAND or HOLD")
		assert.Contains(t, p.Refused[0].Why, "no LAND or HOLD")
		p = Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: harnessFaults["lane died"]})
		require.Len(t, p.Refused, 1, "the report it failed on already is the finish that failed it, retried")
		assert.Contains(t, p.Refused[0].Why, "not working")
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("failed"), "the attempt is not finished twice")
		// the worker's own HOLD retried after a restart: that attempt was its worker's finish,
		// so no report is late for it (deadlineFailed), the retry least of all
		w, _ = friendInReview(t, true, harnessFaults["not started"])
		p = Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: harnessFaults["not started"]})
		require.Len(t, p.Refused, 1, "the attempt is not finished twice")
		assert.Contains(t, p.Refused[0].Why, "not working")
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("failed"))
	})
	t.Run("a later attempt started: refused as before", func(t *testing.T) {
		t.Parallel()
		w, amy := friendInReview(t, true, harnessFaults["lane died"])
		rules(w, on(amy))
		require.Equal(t, 1, w.s.Work.Card("s1-1").Int("reworks"))
		p := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Head: answerHead, Report: "friend amy LAND: done"})
		require.Len(t, p.Refused, 1, "the attempt after it has begun")
		assert.Contains(t, p.Refused[0].Why, "not working")
	})
}
