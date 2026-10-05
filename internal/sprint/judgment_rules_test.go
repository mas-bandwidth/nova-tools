package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAMechanicalJudgmentIsAnsweredByItsRule is the card's named test: each
// mechanical answer logs its rule, a repeated finding stays, and a rule named
// in RulesOff leaves its judgment open. Time moves by the world's clock.
func TestAMechanicalJudgmentIsAnsweredByItsRule(t *testing.T) {
	t.Parallel()
	t.Run("wired where the tick can run it", func(t *testing.T) {
		t.Parallel()
		end := TickEndWith(true, false)
		assert.True(t, hasPart(end, PartJudgmentFriend))
		assert.True(t, hasPart(end, PartJudgmentDeal))
		assert.True(t, hasPart(end, PartJudgmentHold))
		assert.True(t, hasPart(end, PartJudgmentRestore))
		assert.False(t, hasPart(end, PartJudgmentReader), "a first finding stays a judgment in store/rule_answers_test.go")
		assert.False(t, hasPart(end, PartJudgmentDeadline), "an unstamped late card stays working in store/rule_answers_test.go")
		assert.False(t, hasPart(TickEndWith(false, false), PartJudgmentFriend))
		var ask []TickPartDef
		for _, u := range TickTables {
			if u.Table == Readers {
				ask = u.Parts
			}
		}
		require.Len(t, ask, 1, "one ask: a second part spends an operation id when the tick cannot see its twin")
		assert.Equal(t, "ask", ask[0].Name)
	})

	t.Run("a first finding is reworked", func(t *testing.T) {
		t.Parallel()
		w := brokenRead(t, "f.go:3: the empty case returns nil")
		w.part(TickJudgmentReader, ruleReq())
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"))
		assert.NotContains(t, openTypes(w, "s1-1"), NReadBroken)
		require.NotEmpty(t, ruleNotes(w, RuleReaderBroken))
		assert.Equal(t, 1, RuleAnswersInHour(w.notes, w.s.Now))
	})

	t.Run("the same finding twice stays", func(t *testing.T) {
		t.Parallel()
		const finding = "f.go:3: the empty case returns nil"
		w := brokenRead(t, finding)
		w.part(TickJudgmentReader, ruleReq())
		wc := w.s.Fleet.Placed(w.s.Work.Card("s1-1").F("work"))
		require.NotNil(t, wc)
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Head: "h2"}))
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		rc := askedRead(w, "s1-1")
		w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "broken", Finding: finding, Sel: Sel{IDs: []string{rc.ID}}}))
		require.Equal(t, NBriefWrong, openTypes(w, "s1-1"))
		attempt := w.s.Work.Card("s1-1").Int("attempt")
		w.part(TickJudgmentReader, ruleReq())
		assert.Equal(t, attempt, w.s.Work.Card("s1-1").Int("attempt"), "a repeated finding is not reworked")
		assert.Equal(t, NBriefWrong, openTypes(w, "s1-1"))
	})

	t.Run("reader-broken off leaves the finding", func(t *testing.T) {
		t.Parallel()
		w := brokenRead(t, "f.go:3: the empty case returns nil")
		w.s.RulesOff = []string{RuleReaderBroken}
		w.part(TickJudgmentReader, ruleReq())
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("attempt"))
		assert.Equal(t, NReadBroken, openTypes(w, "s1-1"))
		assert.Empty(t, ruleNotes(w, RuleReaderBroken))
	})

	t.Run("a friend who has not started is taken back and dealt again", func(t *testing.T) {
		t.Parallel()
		w, seats := friendLate(t)
		w.part(TickDeadlines, TickReq{})
		require.Contains(t, openTypes(w, "s1-1"), NWorkLate)
		w.part(TickJudgmentFriend, ruleReq())
		w.part(TickJudgmentDealAgain, withFriends(seats))
		wc := w.s.Fleet.Placed("s1-1.w1")
		require.NotNil(t, wc)
		assert.Equal(t, FriendRow("bob"), wc.Row)
		assert.Equal(t, Working, wc.Col)
		assert.NotContains(t, openTypes(w, "s1-1"), NWorkLate)
		require.NotEmpty(t, ruleNotes(w, RuleFriendUnstarted))
	})

	t.Run("friend-unstarted off leaves her the card", func(t *testing.T) {
		t.Parallel()
		w, _ := friendLate(t)
		w.part(TickDeadlines, TickReq{})
		w.s.RulesOff = []string{RuleFriendUnstarted}
		w.part(TickJudgmentFriend, ruleReq())
		wc := w.s.Fleet.Placed("s1-1.w1")
		require.NotNil(t, wc)
		assert.Equal(t, FriendRow("amy"), wc.Row)
		assert.Contains(t, openTypes(w, "s1-1"), NWorkLate)
		assert.Empty(t, ruleNotes(w, RuleFriendUnstarted))
	})

	t.Run("a returned read is asked of another reader", func(t *testing.T) {
		t.Parallel()
		w, from := returnedReadWorld(t)
		w.part(TickJudgmentReturn, ruleReq())
		var held []string
		for _, rc := range w.s.Readers.Of("s1-1") {
			if rc.Placed() && (rc.Col == Asked || rc.Col == Reading) {
				held = append(held, rc.F("reader"))
			}
		}
		require.Len(t, held, 1)
		assert.NotEqual(t, from, held[0])
		require.NotEmpty(t, ruleNotes(w, RuleReturnedRead))
	})

	t.Run("returned-read off leaves the return", func(t *testing.T) {
		t.Parallel()
		w, from := returnedReadWorld(t)
		w.s.RulesOff = []string{RuleReturnedRead}
		w.part(TickJudgmentReturn, ruleReq())
		rc := w.s.Readers.Card(ReadCardID("s1-1", 1, from))
		require.NotNil(t, rc)
		assert.NotEmpty(t, rc.F(FieldReturned))
		assert.Empty(t, ruleNotes(w, RuleReturnedRead))
	})

	t.Run("no live run is finished failed and reworked once", func(t *testing.T) {
		t.Parallel()
		w := lateMachine(t)
		w.part(TickDeadlines, TickReq{})
		require.Contains(t, openTypes(w, "s1-1"), NWorkLate)
		w.part(TickJudgmentDeadline, ruleReq())
		w.part(TickJudgmentDeadlineRework, ruleReq())
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"))
		assert.Equal(t, "1", pr.F(FieldDeadlineRework))
		assert.NotContains(t, openTypes(w, "s1-1"), NWorkLate)
		require.NotEmpty(t, ruleNotes(w, RuleDeadlineNoRun))
		// the next deadline of the new attempt is left
		wc := w.s.Fleet.Placed(pr.F("work"))
		require.NotNil(t, wc)
		// a rework deals the next attempt ready; its unfinished bound starts at the take
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		w.tick(DeadlineUnfinished + time.Minute)
		w.part(TickDeadlines, TickReq{})
		require.Contains(t, openTypes(w, "s1-1"), NWorkLate)
		attempt := pr.Int("attempt")
		w.part(TickJudgmentDeadline, ruleReq())
		assert.Equal(t, Working, w.s.Fleet.Placed(wc.ID).Col)
		assert.Equal(t, attempt, w.s.Work.Card("s1-1").Int("attempt"))
	})

	t.Run("a live run is not finished by the deadline rule", func(t *testing.T) {
		t.Parallel()
		w := lateMachine(t)
		wc := w.s.Fleet.Placed(w.s.Work.Card("s1-1").F("work"))
		wc.Fields[FieldProgress] = stamp(w.s.Now)
		w.part(TickDeadlines, TickReq{})
		w.part(TickJudgmentDeadline, ruleReq())
		assert.Equal(t, Working, w.s.Fleet.Placed(wc.ID).Col)
		assert.Empty(t, ruleNotes(w, RuleDeadlineNoRun))
	})

	t.Run("deadline-no-run off leaves the card working", func(t *testing.T) {
		t.Parallel()
		w := lateMachine(t)
		w.part(TickDeadlines, TickReq{})
		w.s.RulesOff = []string{RuleDeadlineNoRun}
		w.part(TickJudgmentDeadline, ruleReq())
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Contains(t, openTypes(w, "s1-1"), NWorkLate)
		assert.Empty(t, ruleNotes(w, RuleDeadlineNoRun))
	})

	t.Run("a HOLD that names an unlanded card waits", func(t *testing.T) {
		t.Parallel()
		w := holdFailed(t, "Verdict: HOLD\nneeds s1-2 before this lands\n")
		w.part(TickJudgmentHold, ruleReq())
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, "hold", pr.F("result"))
		assert.Equal(t, "s1-2", pr.F(FieldHoldNeed))
		require.NotEmpty(t, ruleNotes(w, RuleHoldUnlanded))
		assert.Contains(t, openTypes(w, "s1-1"), NWorkFailed)
		w.part(TickRuleRework, ruleReq())
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("attempt"), "the failed rule does not rework a shielded HOLD")
		w.part(TickJudgmentHoldRestore, ruleReq())
		assert.Equal(t, "failed", w.s.Work.Card("s1-1").F("result"))
		n := len(ruleNotes(w, RuleHoldUnlanded))
		w.part(TickJudgmentHold, ruleReq())
		assert.Equal(t, n, len(ruleNotes(w, RuleHoldUnlanded)), "the wait is recorded once")
	})

	t.Run("a HOLD that names no unlanded card stays for the coordinator", func(t *testing.T) {
		t.Parallel()
		w := holdFailed(t, "Verdict: HOLD\nthe gate is red and no card is named\n")
		w.part(TickJudgmentHold, ruleReq())
		assert.Equal(t, "hold", w.s.Work.Card("s1-1").F("result"))
		assert.Empty(t, w.s.Work.Card("s1-1").F(FieldHoldNeed))
		assert.Empty(t, ruleNotes(w, RuleHoldUnlanded))
		assert.Contains(t, openTypes(w, "s1-1"), NWorkFailed)
		w.part(TickRuleRework, ruleReq())
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("attempt"))
	})

	t.Run("hold-unlanded off leaves the failure to the failed rule", func(t *testing.T) {
		t.Parallel()
		w := holdFailed(t, "Verdict: HOLD\nneeds s1-2\n")
		w.s.RulesOff = []string{RuleHoldUnlanded}
		w.part(TickJudgmentHold, ruleReq())
		assert.Equal(t, "failed", w.s.Work.Card("s1-1").F("result"))
		assert.Contains(t, openTypes(w, "s1-1"), NWorkFailed)
		assert.Empty(t, ruleNotes(w, RuleHoldUnlanded))
	})

	t.Run("the hour count drops notes older than an hour", func(t *testing.T) {
		t.Parallel()
		now := t0
		notes := []Note{
			{What: RuleSaid(RuleReaderBroken, "rework"), At: now.Add(-time.Minute)},
			{What: RuleSaid(RuleFriendUnstarted, "take back"), At: now.Add(-2 * time.Hour)},
			{What: "a judgment, not a rule", At: now},
		}
		assert.Equal(t, 1, RuleAnswersInHour(notes, now))
	})
}

func ruleReq() TickReq { return TickReq{AnswerRules: true} }

func withFriends(seats []FriendSeat) TickReq {
	r := ruleReq()
	r.Friends = seats
	return r
}

func hasPart(parts []TickPartDef, name string) bool {
	for _, p := range parts {
		if p.Name == name {
			return true
		}
	}
	return false
}

func ruleNotes(w *world, rule string) []Note {
	prefix := NRuleAnswered + " " + rule + ":"
	var out []Note
	for _, n := range w.notes {
		if strings.HasPrefix(n.What, prefix) {
			out = append(out, n)
		}
	}
	return out
}

func brokenRead(t *testing.T, finding string) *world {
	t.Helper()
	w := tierWorld(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	rc := askedRead(w, "s1-1")
	w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "broken", Finding: finding, Sel: Sel{IDs: []string{rc.ID}}}))
	require.Equal(t, NReadBroken, openTypes(w, "s1-1"))
	return w
}

func friendLate(t *testing.T) (*world, []FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up}, {Name: "bob", Width: 1, Status: Up}}
	dealWith(w, seats...)
	require.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
	w.tick(DeadlineUnfinished + time.Minute)
	return w, seats
}

func returnedReadWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	rc := askedRead(w, "s1-1")
	w.must(Read(w.s, ReadReq{As: rc.Row, Return: true, Reason: "no verdict", Sel: Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
	require.NotEmpty(t, w.s.Readers.Card(rc.ID).F(FieldReturned))
	return w, rc.Row
}

func lateMachine(t *testing.T) *world {
	t.Helper()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.tick(DeadlineUnfinished + time.Minute)
	return w
}

func holdFailed(t *testing.T, report string) *world {
	t.Helper()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: true, Report: report}))
	require.Contains(t, openTypes(w, "s1-1"), NWorkFailed)
	return w
}
