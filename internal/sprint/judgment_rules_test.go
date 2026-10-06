package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mechanical judgments the coordinator answered by hand on epoch 15 are the tick's, by
// rule (docs/SPEC-SPRINT.md section 8, the rules table; judgment_rules.go): each is answered
// by its rule and logged with the rule's name, a repeated finding is left for the
// coordinator, and a rule turned off by name leaves its judgment open. On the core's twin
// (world), every part applied as the tick applies it; the clock is the snapshot's.

// on is a tick request with the rules on, and the friends' seats given.
func on(seats ...FriendSeat) TickReq { return TickReq{AnswerRules: true, Friends: seats} }

// answerOn is the rule answer on the open judgment of the type on the subject.
func answerOn(t *testing.T, w *world, r TickReq, typ, subject string) RuleAnswer {
	t.Helper()
	for _, a := range RuleAnswers(w.s, r) {
		if a.Type == typ && a.Subject == subject {
			return a
		}
	}
	t.Fatalf("no %q judgment open on %s: %+v", typ, subject, w.s.Open)
	return RuleAnswer{}
}

// openOf is the open judgments of the type on the subject.
func openOf(w *world, typ, subject string) []Open {
	var out []Open
	for _, o := range w.openOn(subject) {
		if o.Note.Kind == Judgment && o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// logged is the decided notes that say the rule answered.
func logged(w *world, rule string) []Note {
	var out []Note
	for _, n := range w.notes {
		if n.Kind == Decided && strings.HasPrefix(n.What, NRuleAnswered+" "+rule+": ") {
			out = append(out, n)
		}
	}
	return out
}

// rules runs the tick's rule parts, each on the state the one before left.
func rules(w *world, r TickReq) {
	w.t.Helper()
	for _, part := range TickRules {
		p, _ := part.Fn(w.s, r)
		w.must(p)
	}
}

// deadlines runs the tick's deadline part, which raises the late judgments.
func deadlines(w *world, r TickReq) {
	w.t.Helper()
	p, _ := TickDeadlines(w.s, r)
	w.must(p)
}

// brokenOnce has a reader find s1-1's attempt broken with the finding.
func brokenOnce(t *testing.T, w *world, finding string) {
	t.Helper()
	if w.s.Work.Card("s1-1").Col != Review {
		wc := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
		if wc.Col == Ready {
			w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		}
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: "r"}))
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	pr := w.s.Work.Card("s1-1")
	var rc *Card
	for _, c := range readsAt(w.s, pr, pr.Int("attempt")) {
		if c.Col == Asked {
			rc = c
		}
	}
	require.NotNil(t, rc, "a read is asked")
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "broken", Finding: finding, Sel: Sel{IDs: []string{rc.ID}}}))
}

// friendDealt is a world with s1-1 a friend's card dealt to amy, past its bound; her seat as
// the tick reads it, running what is given. A friend's dealt card is ready on her row until
// she starts it, and working once she has (docs/SPEC-SPRINT.md, "Working on her row means
// started"): running it, her start receipt takes it into working (startLanes); not running
// it, it waits ready, dealt and never taken.
func friendDealt(t *testing.T, running ...string) (*world, FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend"))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro", Running: running}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash,pro"}
	dealWith(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, FriendRow("amy"), wc.Row)
	require.Equal(t, Ready, wc.Col, "dealt to her: ready until she starts it")
	if len(running) > 0 {
		startLanes(w, amy)
		wc = w.s.Fleet.Card("s1-1.w1")
		require.Equal(t, Working, wc.Col, "started: working on her row")
	}
	w.tick(DeadlineUnfinished * 4)
	deadlines(w, on(amy, bob))
	require.Len(t, openOf(w, NWorkLate, "s1-1"), 1, "past its bound on her row: a late judgment")
	return w, bob
}

// readLate is a world with s1-1 in review, its one read asked and past its deadline.
func readLate(t *testing.T) *world {
	t.Helper()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.tick(DeadlineUnbegun + time.Minute)
	deadlines(w, TickReq{})
	require.Len(t, openOf(w, NReadLate, "s1-1"), 1, "the read is late")
	return w
}

// heldFor is a world with s1-1 and s1-2, s1-1's work come back failed with the report given.
func heldFor(t *testing.T, report string) *world {
	t.Helper()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	wc := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Report: report}))
	require.Len(t, openOf(w, NWorkFailed, "s1-1"), 1)
	return w
}

func TestAMechanicalJudgmentIsAnsweredByItsRule(t *testing.T) {
	t.Parallel()

	t.Run("every rule is named, and nova-config can turn each off", func(t *testing.T) {
		t.Parallel()
		for _, rule := range []string{RuleFriendTake, RuleHoldNeed, RuleReadBroken, RuleReadLate} {
			assert.Contains(t, RuleNames, rule)
		}
	})

	t.Run("read-broken: the first finding is the fix; the same finding twice is left for the coordinator", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		finished(w, "s1-1", false)
		finding := "a.go:12 drops the error from Close; return it"
		brokenOnce(t, w, finding)
		a := answerOn(t, w, on(), NReadBroken, "s1-1")
		require.Equal(t, RuleReadBroken, a.Rule)
		require.Equal(t, ActRework, a.Act, a.Why)
		rules(w, on())
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"), "reworked")
		assert.Equal(t, finding, pr.F("fix"), "the finding is the fix")
		assert.Empty(t, openOf(w, NReadBroken, "s1-1"), "answered")
		assert.Len(t, logged(w, RuleReadBroken), 1, "logged with the rule's name")

		brokenOnce(t, w, finding)
		for _, a := range RuleAnswers(w.s, on()) {
			if a.Subject == "s1-1" && a.Type == NReadBroken {
				assert.False(t, a.Answers(), "the same finding twice is not answered by rule: %s %s", a.Act, a.Why)
			}
		}
		rules(w, on())
		assert.Equal(t, 2, w.s.Work.Card("s1-1").Int("attempt"), "not reworked again")
		assert.NotEmpty(t, w.s.Work.Card("s1-1").F(FieldBriefDefect), "marked a brief defect")
		assert.Len(t, openOf(w, NBriefWrong, "s1-1"), 1, "the brief defect stays open for the coordinator")
		assert.Len(t, logged(w, RuleReadBroken), 1)
	})

	t.Run("friend-take: a friend's card she has not started past its bound is taken back and dealt again", func(t *testing.T) {
		t.Parallel()
		w, bob := friendDealt(t)
		amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
		a := answerOn(t, w, on(amy, bob), NWorkLate, "s1-1")
		require.Equal(t, RuleFriendTake, a.Rule)
		require.Equal(t, ActTake, a.Act, a.Why)
		rules(w, on(amy, bob))
		wc := w.s.Fleet.Card("s1-1.w1")
		assert.Equal(t, Withdrawn, wc.Col, "taken back")
		assert.Equal(t, FriendRow("amy"), wc.F(FieldTakenFrom))
		assert.Equal(t, Ready, w.state("s1-1"))
		assert.Empty(t, openOf(w, NWorkLate, "s1-1"), "answered")
		assert.Len(t, logged(w, RuleFriendTake), 1, "logged with the rule's name")
		assert.Contains(t, wc.F(FieldRuleAnswer), RuleFriendTake+": "+ActTake+" at ")

		dealWith(w, amy, bob)
		again := w.s.Fleet.Card("s1-1.w1")
		assert.Equal(t, FriendRow("bob"), again.Row, "dealt again, to another friend")
		w.clean("taken back by rule")
	})

	t.Run("friend-take: a card she started stays hers", func(t *testing.T) {
		t.Parallel()
		w, bob := friendDealt(t, "s1-1.w1")
		amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro", Running: []string{"s1-1.w1"}}
		a := answerOn(t, w, on(amy, bob), NWorkLate, "s1-1")
		assert.Equal(t, ActLeft, a.Act, a.Why)
		rules(w, on(amy, bob))
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Len(t, openOf(w, NWorkLate, "s1-1"), 1)
	})

	t.Run("read-late: a late read is asked of another reader, once an attempt", func(t *testing.T) {
		t.Parallel()
		w := readLate(t)
		pr := w.s.Work.Card("s1-1")
		late := readsAt(w.s, pr, 1)
		require.Len(t, late, 1)
		from := late[0].F("reader")
		a := answerOn(t, w, on(), NReadLate, "s1-1")
		require.Equal(t, RuleReadLate, a.Rule)
		require.Equal(t, ActAsk, a.Act, a.Why)
		rules(w, on())
		assert.False(t, w.s.Readers.Card(late[0].ID).Placed(), "the late read is taken back")
		now := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
		require.Len(t, now, 1)
		assert.NotEqual(t, from, now[0].F("reader"), "asked of another reader")
		assert.Empty(t, openOf(w, NReadLate, "s1-1"), "answered")
		assert.Len(t, logged(w, RuleReadLate), 1, "logged with the rule's name")
		assert.Equal(t, "1", w.s.Work.Card("s1-1").F(FieldRuleReread))

		w.tick(DeadlineUnbegun + time.Minute)
		deadlines(w, TickReq{})
		require.Len(t, openOf(w, NReadLate, "s1-1"), 1, "the second read is late too")
		a = answerOn(t, w, on(), NReadLate, "s1-1")
		assert.Equal(t, ActLeft, a.Act, "the second late read of the attempt is a mind's: %s", a.Why)
		rules(w, on())
		assert.Len(t, openOf(w, NReadLate, "s1-1"), 1)
		w.clean("asked another reader by rule")
	})

	t.Run("hold-need: a HOLD naming a card that has not landed waits for it, then is reworked", func(t *testing.T) {
		t.Parallel()
		w := heldFor(t, "HOLD: needs s1-2 to land first; its API is missing")
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		require.Equal(t, RuleHoldNeed, a.Rule)
		require.Equal(t, ActNeed, a.Act, a.Why)
		rules(w, on())
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, 1, pr.Int("attempt"), "not reworked while s1-2 has not landed")
		assert.Equal(t, "s1-2@1", pr.F(FieldRuleNeed))
		o := openOf(w, NWorkFailed, "s1-1")
		require.Len(t, o, 1, "the judgment waits")
		rules(w, on())
		assert.Equal(t, "s1-2@1", w.s.Work.Card("s1-1").F(FieldRuleNeed), "recorded once")

		w.place(w.s.Work, "s1-2", "s1", Landed)
		a = answerOn(t, w, on(), NWorkFailed, "s1-1")
		require.Equal(t, ActRework, a.Act, a.Why)
		rules(w, on())
		pr = w.s.Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"), "reworked once s1-2 landed")
		assert.Contains(t, pr.F("fix"), "s1-2 has landed")
		assert.Empty(t, openOf(w, NWorkFailed, "s1-1"), "answered")
		assert.Len(t, logged(w, RuleHoldNeed), 1, "logged with the rule's name")
	})

	t.Run("hold-need: a HOLD naming no card waiting to land stays the failed rule's", func(t *testing.T) {
		t.Parallel()
		w := heldFor(t, "friend amy HOLD: no pushed head found")
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		assert.Equal(t, RuleFailed, a.Rule, a.Why)
		assert.Equal(t, ActRework, a.Act, a.Why)
	})

	t.Run("hold-need: the card it waits for dropped is the coordinator's", func(t *testing.T) {
		t.Parallel()
		w := heldFor(t, "HOLD: needs s1-2 to land first")
		rules(w, on())
		require.Equal(t, "s1-2@1", w.s.Work.Card("s1-1").F(FieldRuleNeed))
		w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "not wanted"}))
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		assert.Equal(t, RuleHoldNeed, a.Rule)
		assert.Equal(t, ActLeft, a.Act, a.Why)
		rules(w, on())
		assert.Equal(t, 1, w.s.Work.Card("s1-1").Int("attempt"))
		assert.Len(t, openOf(w, NWorkFailed, "s1-1"), 1)
	})

	t.Run("a rule turned off leaves its judgment open", func(t *testing.T) {
		t.Parallel()
		w := heldFor(t, "HOLD: needs s1-2 to land first")
		w.s.RulesOff = []string{RuleHoldNeed}
		assert.Equal(t, ActOff, answerOn(t, w, on(), NWorkFailed, "s1-1").Act)
		rules(w, on())
		assert.Empty(t, w.s.Work.Card("s1-1").F(FieldRuleNeed))

		r := readLate(t)
		r.s.RulesOff = []string{RuleReadLate}
		rules(r, on())
		assert.Len(t, openOf(r, NReadLate, "s1-1"), 1)
		assert.Empty(t, logged(r, RuleReadLate))

		f, bob := friendDealt(t)
		f.s.RulesOff = []string{RuleFriendTake}
		amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
		rules(f, on(amy, bob))
		assert.Equal(t, Ready, f.s.Fleet.Card("s1-1.w1").Col, "not taken back: still ready on her row")
		assert.Equal(t, FriendRow("amy"), f.s.Fleet.Card("s1-1.w1").Row)
		assert.Len(t, openOf(f, NWorkLate, "s1-1"), 1)

		b := setup(t, 1)
		b.s.RulesOff = []string{RuleReadBroken}
		finished(b, "s1-1", false)
		brokenOnce(t, b, "a.go:1 off by one")
		rules(b, on())
		assert.Equal(t, 1, b.s.Work.Card("s1-1").Int("attempt"))
		assert.Len(t, openOf(b, NReadBroken, "s1-1"), 1)
	})

	t.Run("the coordinator's count of rule answers in the hour", func(t *testing.T) {
		t.Parallel()
		now := t0
		at := func(d time.Duration) string { return stamp(now.Add(-d)) }
		cards := []*Card{
			{ID: "a", Fields: map[string]string{FieldRuleAnswer: RuleFriendTake + ": " + ActTake + " at " + at(time.Minute)}},
			{ID: "b", Fields: map[string]string{FieldRuleAnswer: RuleReadLate + ": " + ActAsk + " at " + at(59*time.Minute)}},
			{ID: "c", Fields: map[string]string{FieldRuleAnswer: RuleReadLate + ": " + ActAsk + " at " + at(61*time.Minute)}},
			{ID: "d", Fields: map[string]string{}},
		}
		assert.Equal(t, map[string]int{RuleFriendTake: 1, RuleReadLate: 1}, RuleAnsweredWithin(cards, now, time.Hour))
	})
}
