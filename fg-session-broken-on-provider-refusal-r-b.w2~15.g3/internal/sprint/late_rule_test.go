package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lateAnswer is the late rule's answer on wc's open late judgment.
func lateAnswer(t *testing.T, w *world, wc string) RuleAnswer {
	t.Helper()
	for _, a := range RuleAnswers(w.s, TickReq{AnswerRules: true}) {
		if a.Type == NWorkLate && a.Card == wc {
			return a
		}
	}
	t.Fatalf("no late answer on %s", wc)
	return RuleAnswer{}
}

// The late rule's default (docs/SPEC-SPRINT.md section 8, the rules table's row late;
// tla/SprintRules.tla, NeverStampedNeverReturned): a working card whose holder has never
// stamped progress on it since its take is waited on (held, raised again at the time the
// hold names), never returned by the rule; only a holder that stamped and then went silent
// past RuleProgressWindow has its card returned and dealt again.
func TestLateRuleDefaultsToWaitUntilProgressStampsExist(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// progress is the stamp on the card, as an offset from its take; nil is none
		progress *time.Duration
		returned bool
	}{
		{name: "no stamp: waited, never returned"},
		{name: "a stamp before its take (another holder's): waited, never returned", progress: new(-time.Minute)},
		{name: "stamped, then silent past the window: returned and dealt again", progress: new(time.Minute), returned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := dealt(t)
			wc := w.s.Fleet.Card("s1-1.w1")
			require.Equal(t, Working, wc.Col)
			gen := wc.Int("gen")
			if tc.progress != nil {
				wc.Fields[FieldProgress] = stamp(stampAt(wc, "taken").Add(*tc.progress))
			}
			// the card past its deadline, its holder past its own
			w.tick(DeadlineUnfinished + time.Minute)
			p, _ := TickDeadlines(w.s, TickReq{})
			w.must(p)
			a := lateAnswer(t, w, wc.ID)
			if !tc.returned {
				assert.Equal(t, ActHold, a.Act, "a holder that never stamped is waited on: %s", a.Why)
				assert.NotEqual(t, ActRedeal, a.Act)
			} else {
				assert.Equal(t, ActRedeal, a.Act, "stamped and silent: %s", a.Why)
			}
			p, _ = TickRuleLate(w.s, TickReq{AnswerRules: true})
			w.must(p)
			after := w.s.Fleet.Card(wc.ID)
			if !tc.returned {
				assert.Equal(t, Working, after.Col, "it stays in working")
				assert.Equal(t, gen, after.Int("gen"), "not withdrawn")
				return
			}
			assert.Greater(t, after.Int("gen"), gen, "withdrawn and dealt again")
		})
	}
}

// Only the holder stamps its card's progress (docs/SPEC-SPRINT.md section 8, the rules
// table's row late): the stamp is the server's time, and a stamp from another row, of a
// generation that is not the live one, or of a card not working is refused and writes
// nothing.
func TestProgressIsStampedByTheHolderAlone(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	wc := w.s.Fleet.Card("s1-1.w1")
	other := "m1"
	if wc.Row == other {
		other = "m2"
	}
	gen := wc.Int("gen")
	for _, tc := range []struct {
		name string
		req  ProgressReq
		why  string
	}{
		{"another row", ProgressReq{Sel: Sel{IDs: []string{wc.ID}}, As: other}, "only its holder stamps its progress"},
		{"a stale generation", ProgressReq{Sel: Sel{IDs: []string{wc.ID}}, As: wc.Row, Gens: map[string]int{wc.ID: gen + 1}}, "not the live one"},
		{"a card not working", ProgressReq{Sel: Sel{IDs: []string{"s1-1"}}, As: wc.Row}, "not working"},
		{"no holder named", ProgressReq{Sel: Sel{IDs: []string{wc.ID}}}, "names its holder"},
	} {
		p := Progress(w.s, tc.req)
		assert.Empty(t, p.Units, tc.name)
		require.Len(t, p.Refused, 1, tc.name)
		assert.Contains(t, p.Refused[0].Why, tc.why, tc.name)
	}
	assert.Empty(t, wc.F(FieldProgress), "nothing stamped")
	w.tick(time.Minute)
	w.must(Progress(w.s, ProgressReq{Sel: Sel{IDs: []string{wc.ID}}, As: wc.Row, Gens: map[string]int{wc.ID: gen}}))
	after := w.s.Fleet.Card(wc.ID)
	assert.Equal(t, stamp(w.s.Now), after.F(FieldProgress), "the server's time")
	assert.Equal(t, Working, after.Col)
	assert.Equal(t, gen, after.Int("gen"))
}
