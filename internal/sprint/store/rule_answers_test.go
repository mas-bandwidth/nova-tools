package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Rule answers (docs/SPEC-SPRINT.md section 8, "Answered by rule"; the owner, 2026-10-04, at
// 1:36 PM: "I want this sort of oh no fleet is idle, do judgement, release more cards thing
// -- i want this more automated."). The mechanical judgments are answered by the tick, by
// rule, without the coordinator, each recorded on the log and the card as "answered by rule
// <name>"; a judgment that needs a mind stays one. On the mem twin, injected clock, no
// socket; the model is tla/SprintRules.tla (RuleAnswersBounded, LadderClimbs).

// ruled is a harness whose machine answers by rule, with flash, pro and heavy routes.
func ruled(t *testing.T) *harness {
	t.Helper()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"), route("pro-a", "pro"), route("pro-b", "pro"), route("heavy-a", "heavy"))
	h.st.AnswerRules = true
	return h
}

// setCard writes fields onto a card of a table, as a step that wrote them would leave it.
func (h *harness) setCard(table, id string, set map[string]string) {
	h.t.Helper()
	s := h.snap()
	c := s.T(table).Card(id)
	require.NotNil(h.t, c, id)
	_, err := h.st.B.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: h.st.Names.Table(table), Epoch: fmt.Sprint(s.Epoch), ExpectedTableRevision: fmt.Sprint(s.T(table).Revision),
		OperationID: "set-card-" + id + "-" + fmt.Sprint(s.T(table).Revision), Members: []ntable.BatchMemberEntry{{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)}, Set: set}}})
	require.NoError(h.t, err)
}

// answeredBy is the decided notes that say the rule answered.
func (h *harness) answeredBy(rule string) []sprint.Note {
	h.t.Helper()
	all, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Kind == sprint.Decided && strings.HasPrefix(n.What, "answered by rule "+rule+":") {
			out = append(out, n)
		}
	}
	return out
}

func TestRuleFailedRedealsOnTheNextRouteThenRaisesATier(t *testing.T) {
	t.Parallel()
	h := ruled(t)
	h.addReady("s1", 1, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	w1 := h.failTake("s1-1.w1", "verdict not-done; the tests fail at TestA")
	h.machine()
	pr := h.snap().Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"), "work came back failed: reworked by rule")
	assert.Empty(t, h.openOf(sprint.NWorkFailed), "the judgment is answered")
	require.Len(t, h.answeredBy(sprint.RuleFailed), 1)
	assert.Contains(t, pr.F(sprint.FieldRuleAnswer), "failed:")
	assert.Empty(t, pr.F(sprint.FieldTier), "the first failure stays on its tier")
	w2 := h.workCards()["s1-1.w2"]
	require.NotNil(t, w2)
	assert.NotEqual(t, w1.F(sprint.FieldRoute), w2.F(sprint.FieldRoute), "redealt on the next route")
	assert.Equal(t, "flash", tierOfRoute(w2.F(sprint.FieldRoute)))
	h.failTake("s1-1.w2", "verdict not-done; another cause entirely here")
	h.machine()
	pr = h.snap().Work.Card("s1-1")
	require.Equal(t, 3, pr.Int("attempt"))
	assert.Equal(t, "pro", pr.F(sprint.FieldTier), "at the attempt cap the card goes up a tier")
	w3 := h.workCards()["s1-1.w3"]
	require.NotNil(t, w3)
	assert.Equal(t, "pro", tierOfRoute(w3.F(sprint.FieldRoute)))
	h.clean("raised a tier")
}

func TestRuleBoundRaisesATierAndHeavyGoesToAFriend(t *testing.T) {
	t.Parallel()
	t.Run("flash to pro", func(t *testing.T) {
		t.Parallel()
		h := ruled(t)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		h.boundOut("s1-1")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"), "the bound is answered by a new attempt")
		assert.Equal(t, "pro", pr.F(sprint.FieldTier), "one tier up")
		assert.Empty(t, h.openOf(sprint.NBound))
		assert.NotEmpty(t, h.answeredBy(sprint.RuleBound))
		h.clean("flash to pro")
	})
	t.Run("heavy to a friend", func(t *testing.T) {
		t.Parallel()
		h := ruled(t)
		h.addReady("s1", 1, briefOf("heavy", ""))
		h.setPrimary("s1-1", map[string]string{sprint.FieldTier: "heavy"})
		h.startMachine()
		h.machine()
		h.boundOut("s1-1")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, sprint.WhoFriend, pr.F(sprint.FieldWho), "above heavy is a friend")
		assert.Equal(t, sprint.Ready, pr.Col, "ready for the friends' deal: no friend is up here")
		assert.NotContains(t, h.workCards(), "s1-1.w1", "the attempt at its bound is retired")
		assert.Empty(t, h.openOf(sprint.NBound))
		assert.NotEmpty(t, h.answeredBy(sprint.RuleBound))
		h.clean("heavy to a friend")
	})
}

func TestRuleLateWaitsOnceWithProgressElseReturnsAndRedeals(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*harness, *sprint.Card) {
		h := ruled(t)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		wc := h.snap().Fleet.Card("s1-1.w1")
		g := map[string]int{wc.ID: wc.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
		return h, wc
	}
	t.Run("no stamp: held, never returned", func(t *testing.T) {
		t.Parallel()
		h, wc := setup(t)
		h.tick(sprint.DeadlineUnfinished + time.Minute)
		h.machine()
		after := h.snap().Fleet.Card(wc.ID)
		require.NotNil(t, after)
		assert.Equal(t, wc.Int("gen"), after.Int("gen"), "not withdrawn: its holder never stamped, so the rule waits")
		assert.Equal(t, sprint.Working, after.Col)
		assert.Empty(t, h.judgmentsOf(sprint.NWorkLate), "the lateness is answered and held")
		require.NotEmpty(t, h.answeredBy(sprint.RuleLate))
		assert.Contains(t, h.answeredBy(sprint.RuleLate)[0].What, "never returned by this rule")
		h.clean("held unstamped")
	})
	t.Run("stamped, then silent: returned and redealt", func(t *testing.T) {
		t.Parallel()
		h, wc := setup(t)
		h.tick(time.Minute)
		h.must(ProgressStep(sprint.ProgressReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, As: wc.Row, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: wc.Row}))
		require.NotEmpty(t, h.snap().Fleet.Card(wc.ID).F(sprint.FieldProgress))
		h.tick(sprint.DeadlineUnfinished + time.Minute)
		h.machine()
		after := h.snap().Fleet.Card(wc.ID)
		require.NotNil(t, after)
		assert.Greater(t, after.Int("gen"), wc.Int("gen"), "withdrawn and dealt again")
		assert.Empty(t, h.judgmentsOf(sprint.NWorkLate), "the lateness is answered and held")
		assert.NotEmpty(t, h.answeredBy(sprint.RuleLate))
		h.clean("redealt")
	})
	t.Run("progress: waits 30m once", func(t *testing.T) {
		t.Parallel()
		h, wc := setup(t)
		h.tick(sprint.DeadlineUnfinished + time.Minute)
		h.setCard(sprint.Fleet, wc.ID, map[string]string{sprint.FieldProgress: h.now.Add(-time.Minute).UTC().Format(time.RFC3339)})
		h.machine()
		after := h.snap().Fleet.Card(wc.ID)
		assert.Equal(t, wc.Int("gen"), after.Int("gen"), "not withdrawn: it made progress")
		assert.Equal(t, sprint.Working, after.Col)
		assert.Empty(t, h.judgmentsOf(sprint.NWorkLate), "held for 30m")
		require.NotEmpty(t, h.answeredBy(sprint.RuleLate))
		assert.Contains(t, h.answeredBy(sprint.RuleLate)[0].What, "wait 30m")
		// past the wait, still late, progress or none: returned and redealt, once
		h.tick(sprint.RuleLateWait + time.Minute)
		h.setCard(sprint.Fleet, wc.ID, map[string]string{sprint.FieldProgress: h.now.Add(-time.Minute).UTC().Format(time.RFC3339)})
		h.machine()
		h.machine()
		after = h.snap().Fleet.Card(wc.ID)
		assert.Greater(t, after.Int("gen"), wc.Int("gen"), "the one wait is spent: returned and redealt")
		h.clean("waited once")
	})
}

func TestRuleConflictReturnsReworksOnTheTipAndResumes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind     string
		answered bool
	}{{"file", true}, {"ledger", false}, {"", false}} {
		t.Run("kind="+tc.kind, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.st.AnswerRules = true
			h.setup(1)
			h.nToMerging("s1-1")
			h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: "the head h of s1-1 does not merge: CONFLICT (content) in internal/x.go",
				ConflictKind: tc.kind, ConflictPaths: []string{"internal/x.go"}}))
			if tc.answered {
				// a file conflict in the card's own head is reworked by the merge step itself: no
				// stop, no rule needed (sprint's landRefused)
				require.NotEqual(t, sprint.StreamStopped, h.snap().StreamCtl("s1").F("state"))
				h.startMachine()
				h.machine()
				pr := h.snap().Work.Card("s1-1")
				assert.Equal(t, 2, pr.Int("attempt"), "redone as a new attempt")
				assert.Contains(t, pr.F("fix"), "the landing refused this head: ")
				assert.Empty(t, h.nOpenOf(sprint.NConflict, ""))
				h.clean("reworked at the tip")
				return
			}
			require.Equal(t, sprint.StreamStopped, h.snap().StreamCtl("s1").F("state"))
			h.startMachine()
			h.machine()
			s := h.snap()
			if !tc.answered {
				assert.Equal(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"), "a conflict the rule cannot place stays the coordinator's")
				assert.Len(t, h.nOpenOf(sprint.NConflict, ""), 1)
				return
			}
			assert.NotEqual(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"), "the stream resumed")
			assert.Empty(t, h.nOpenOf(sprint.NConflict, ""), "the conflict is answered")
			pr := s.Work.Card("s1-1")
			assert.Equal(t, 2, pr.Int("attempt"), "redone as a new attempt")
			assert.Equal(t, sprint.RuleConflictFix, pr.F("fix"))
			assert.NotEmpty(t, h.answeredBy(sprint.RuleConflict))
			h.clean("redone on the tip")
		})
	}
}

func TestRuleBriefDefectMarksTheCardAndRaisesOnce(t *testing.T) {
	t.Parallel()
	h := ruled(t)
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	h.attemptFoundBroken("s1-1", "files outside PATHS: internal/x.go")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Who: "tester"}))
	h.attemptFoundBroken("s1-1", "files outside PATHS: internal/x.go")
	require.Len(t, h.nOpenOf(sprint.NBriefWrong, "s1-1"), 1)
	h.startMachine()
	h.machine()
	h.machine()
	pr := h.snap().Work.Card("s1-1")
	assert.NotEmpty(t, pr.F(sprint.FieldBriefDefect), "the card is marked a brief defect")
	open := h.nOpenOf(sprint.NBriefWrong, "s1-1")
	require.Len(t, open, 1, "raised once, and it stays: a brief needs a mind")
	assert.True(t, strings.HasPrefix(open[0].Note.What, "brief defect: "), open[0].Note.What)
	assert.Equal(t, sprint.Review, pr.Col, "held in review")
	assert.Len(t, h.answeredBy(sprint.RuleBriefDefect), 0, "marked, not answered")
}

// A broken read is answered by the read-broken rule (internal/sprint rules_read.go); with that
// rule turned off in nova-config's sprint row it stays the coordinator's judgment.
func TestABrokenReadStaysAJudgment(t *testing.T) {
	t.Parallel()
	h := ruled(t)
	h.m.SetRulesOff(sprint.RuleReadBroken)
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	h.attemptFoundBroken("s1-1", "internal/x.go:3 drops the error; return it")
	h.startMachine()
	h.machine()
	assert.Len(t, h.nOpenOf(sprint.NReadBroken, "s1-1"), 1, "the rule is off: the finding is the coordinator's")
	assert.Equal(t, 1, h.snap().Work.Card("s1-1").Int("attempt"))
}

func TestEachRuleHasAnOffSwitch(t *testing.T) {
	t.Parallel()
	for _, off := range []string{"run flag", "config"} {
		t.Run(off, func(t *testing.T) {
			t.Parallel()
			h := ruled(t)
			if off == "run flag" {
				h.st.AnswerRules = false
			} else {
				h.m.SetRulesOff(sprint.RuleFailed)
			}
			h.addReady("s1", 1, briefOf("flash", ""))
			h.startMachine()
			h.machine()
			h.failTake("s1-1.w1", "verdict not-done; the tests fail at TestA")
			h.machine()
			assert.Len(t, h.openOf(sprint.NWorkFailed), 1, "the rule is off: the judgment is the coordinator's")
			assert.Equal(t, 1, h.snap().Work.Card("s1-1").Int("attempt"))
		})
	}
}

// The names are one list: the config's enum is the sprint's rules.
func TestTheRuleNamesAreTheConfigsEnum(t *testing.T) {
	t.Parallel()
	assert.Equal(t, config.AnswerRules, sprint.RuleNames)
}

// judgmentsOf is the open judgments of the type, the holds on their conditions aside.
func (h *harness) judgmentsOf(typ string) []sprint.Open {
	h.t.Helper()
	var out []sprint.Open
	for _, o := range h.nOpenOf(typ, "") {
		if o.Note.Kind == sprint.Judgment {
			out = append(out, o)
		}
	}
	return out
}

// A failure many cards share is still the card's: the failed rule advances each on its tier
// rather than leave them as one fleet judgment (a toolchain the machine cannot run,
// "Permission denied", came back on six cards at once on 2026-10-04). A failed attempt never
// waits on the seat.
func TestTheSameFailureOnManyCardsIsAdvancedByRule(t *testing.T) {
	t.Parallel()
	h := ruled(t)
	h.addReady("s1", sprint.RuleSameFailureCards, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	for i := 1; i <= sprint.RuleSameFailureCards; i++ {
		h.failTake(fmt.Sprintf("s1-%d.w1", i), "step 2 broken: post: exit0 go vet ./...: exit status 126; exec: go: Permission denied")
	}
	h.machine()
	assert.Empty(t, h.judgmentsOf(sprint.NWorkFailed), "no failed-attempt judgment waits on the seat")
	for i := 1; i <= sprint.RuleSameFailureCards; i++ {
		pr := h.snap().Work.Card(fmt.Sprintf("s1-%d", i))
		assert.Equal(t, 2, pr.Int("attempt"), "%s: advanced by rule, never left", pr.ID)
		assert.Empty(t, pr.F(sprint.FieldTier), "%s: on its tier", pr.ID)
	}
	assert.NotEmpty(t, h.answeredBy(sprint.RuleFailed), "the answer is logged with the rule's name")
}
