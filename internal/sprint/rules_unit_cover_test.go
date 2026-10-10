package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bound rule, the conflict rule and their tier escalation, one answer at a time
// (docs/SPEC-SPRINT.md section 8, rules.go): every branch of ruleBound, ruleConflict,
// refusalSaid, escalation and up read through RuleAnswers on the core's world twin, with
// no store, clock, subprocess or network. The answers are picked from RuleAnswers by the
// judgment's type and subject, as the tick's parts read them.

// ruleCoverWorld is one up member, two readers and one card of the brief given in stream s1.
func ruleCoverWorld(t *testing.T, brief string) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: brief}))
	return w
}

// ctlStopped puts the stream s1 control card stopped on a conflict for card, with the
// lander's conflict kind and paths (MergeReq).
func ctlStopped(w *world, card, kind, paths string) {
	w.s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Rev: 1, Fields: map[string]string{
		"state": StreamStopped, "cause": "conflict", "card": card,
		FieldConflictKind: kind, FieldConflictPaths: paths}})
}

// stuckAt leaves the primary in merging with its merge card stuck: the conflict rule's state.
func stuckAt(w *world, id string) {
	w.place(w.s.Work, id, "s1", Merging)
	w.s.Merge.Put(&Card{ID: id, Row: "s1", Col: Stuck, Rev: 1, Fields: map[string]string{"primary": id}})
}

// conflictNote opens the stream s1 NConflict judgment with the lander's words.
func conflictNote(w *world, what string) {
	w.note(Note{Kind: Judgment, Type: NConflict, Stream: "s1", StreamLevel: true, At: t0, Card: "s1-1", What: what})
}

// conflictCover is a world with s1 stopped on a stuck conflict card of the kind and words.
func conflictCover(t *testing.T, kind, what string) *world {
	t.Helper()
	w := ruleCoverWorld(t, proBrief)
	ctlStopped(w, "s1-1", kind, "")
	stuckAt(w, "s1-1")
	conflictNote(w, what)
	return w
}

func TestSprintRulesCoverConflict(t *testing.T) {
	t.Parallel()
	const file = "the head h1 of s1-1 does not merge: CONFLICT (content): Merge conflict in internal/x.go"

	t.Run("no control card is left", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		conflictNote(w, file)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "not stopped on a conflict")
	})

	t.Run("a stream not stopped on a conflict is left", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		w.s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Rev: 1, Fields: map[string]string{
			"state": StreamMerging, "cause": "conflict", "card": "s1-1"}})
		conflictNote(w, file)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "not stopped on a conflict")
	})

	t.Run("a conflict in a file is returned to be redone at flash", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		ctlStopped(w, "s1-1", "file", "internal/x.go")
		stuckAt(w, "s1-1")
		conflictNote(w, file)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		require.Equal(t, RuleConflict, a.Rule)
		require.Equal(t, ActReturn, a.Act, a.Why)
		assert.Equal(t, "s1-1", a.Card)
		assert.Equal(t, RefusedConflict, a.set[FieldRuleRefused])
		assert.Equal(t, file, a.set[FieldRuleRefusal])
		assert.Equal(t, cardhdr.RouteFlash, a.set[FieldTierNow])
		assert.Contains(t, a.Why, "a conflict in internal/x.go, no ledger")
		assert.Contains(t, a.Why, "returned to be redone on the current tip at flash")
	})

	t.Run("no conflict paths reads a dash", func(t *testing.T) {
		t.Parallel()
		a := answerOn(t, conflictCover(t, "file", file), on(), NConflict, StreamSubject("s1"))
		require.Equal(t, ActReturn, a.Act, a.Why)
		assert.Contains(t, a.Why, "a conflict in -, no ledger")
	})

	t.Run("files outside its PATHS", func(t *testing.T) {
		t.Parallel()
		what := "the head h1 of s1-1 fails the lander's checks: it changes files outside its PATHS (E12): internal/y.go"
		a := answerOn(t, conflictCover(t, "", what), on(), NConflict, StreamSubject("s1"))
		require.Equal(t, ActReturn, a.Act, a.Why)
		assert.Equal(t, RefusedPaths, a.set[FieldRuleRefused])
		assert.Contains(t, a.Why, "files outside its PATHS")
	})

	t.Run("the lander's checks", func(t *testing.T) {
		t.Parallel()
		what := "the head h1 of s1-1 fails the lander's checks: a stranded fragment (E4)"
		a := answerOn(t, conflictCover(t, "", what), on(), NConflict, StreamSubject("s1"))
		require.Equal(t, ActReturn, a.Act, a.Why)
		assert.Equal(t, RefusedChecks, a.set[FieldRuleRefused])
		assert.Contains(t, a.Why, "the lander's checks failed")
	})

	t.Run("the tree gate is the default way", func(t *testing.T) {
		t.Parallel()
		what := "the head h1 of s1-1 fails the tree gate: go vet ./...: exit status 1"
		a := answerOn(t, conflictCover(t, "", what), on(), NConflict, StreamSubject("s1"))
		require.Equal(t, ActReturn, a.Act, a.Why)
		assert.Equal(t, RefusedGate, a.set[FieldRuleRefused])
		assert.Contains(t, a.Why, "the merged tree fails the tree gate")
	})

	t.Run("the same refusal twice is a brief defect", func(t *testing.T) {
		t.Parallel()
		w := conflictCover(t, "file", file)
		w.s.Work.Placed("s1-1").Fields[FieldRuleRefused] = RefusedConflict
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, RuleBriefDefect, a.Rule)
		assert.Equal(t, ActMark, a.Act, a.Why)
	})

	t.Run("a conflict the lander cannot place is a mind's", func(t *testing.T) {
		t.Parallel()
		a := answerOn(t, conflictCover(t, "", ""), on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "did not say which files")
	})

	t.Run("a conflict in a ledger is a mind's, named with its paths", func(t *testing.T) {
		t.Parallel()
		w := conflictCover(t, "ledger", "the head h1 of s1-1 does not merge: the generated ledgers did not resolve")
		w.s.StreamCtl("s1").Fields[FieldConflictPaths] = "internal/ci/testdata/x.txt"
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "the conflict is in a ledger (internal/ci/testdata/x.txt)")
	})

	t.Run("a marked brief defect is a mind's", func(t *testing.T) {
		t.Parallel()
		w := conflictCover(t, "file", file)
		w.s.Work.Placed("s1-1").Fields[FieldBriefDefect] = stamp(t0)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "a brief defect")
	})

	t.Run("a returned card resumes its stream", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		ctlStopped(w, "s1-1", RefusedPaths, "")
		w.s.Work.Placed("s1-1").Fields[FieldRuleRedo] = "1"
		conflictNote(w, file)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActResume, a.Act, a.Why)
		assert.Contains(t, a.Why, "goes on")
	})

	t.Run("a card not where the rule left it is a mind's", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		ctlStopped(w, "s1-1", "file", "")
		conflictNote(w, file)
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "not where the rule left it")
	})
}

// boundCover is a world with s1-1 a pro card in review at its second identical failure,
// its second work card on the fleet table, and the NBound judgment open.
func boundCover(t *testing.T) *world {
	t.Helper()
	w := ruleCoverWorld(t, proBrief)
	pr := w.s.Work.Placed("s1-1")
	pr.Fields["attempt"] = "2"
	pr.Fields["result"] = "failed"
	pr.Fields[FieldIdenticalAt] = "2"
	pr.Fields[FieldFailure] = "cover-bound-flake"
	w.place(w.s.Work, "s1-1", "s1", Review)
	w.s.Fleet.Put(&Card{ID: "s1-1.w2", Row: "m1", Col: Withdrawn, Rev: 1, Fields: map[string]string{
		"kind": "work", "primary": "s1-1", "stream": "s1", "attempt": "2", "gen": "1"}})
	w.note(Note{Kind: Judgment, Type: NBound, Stream: "s1", Primaries: []string{"s1-1"}, At: t0})
	return w
}

func TestSprintRulesCoverBound(t *testing.T) {
	t.Parallel()

	t.Run("a card off the table is left", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		w.note(Note{Kind: Judgment, Type: NBound, Stream: "s1", Primaries: []string{"gone"}, At: t0})
		a := answerOn(t, w, on(), NBound, "gone")
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "off the table")
	})

	t.Run("a card at no bound is left", func(t *testing.T) {
		t.Parallel()
		w := ruleCoverWorld(t, proBrief)
		w.note(Note{Kind: Judgment, Type: NBound, Stream: "s1", Primaries: []string{"s1-1"}, At: t0})
		a := answerOn(t, w, on(), NBound, "s1-1")
		assert.Equal(t, ActLeft, a.Act, a.Why)
		assert.Contains(t, a.Why, "not at a bound now")
	})

	t.Run("a second identical failure past heavy is a friend's card", func(t *testing.T) {
		t.Parallel()
		w := boundCover(t)
		w.s.Routes = []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Enabled: true},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true},
		}
		a := answerOn(t, w, on(), NBound, "s1-1")
		require.Equal(t, ActFriend, a.Act, a.Why)
		assert.True(t, a.Friend)
		assert.Equal(t, WhoFriend, a.set[FieldRuleTier])
		assert.Equal(t, "0", a.set[FieldRuleFails])
	})

	t.Run("a heavy route climbs the gate to heavy", func(t *testing.T) {
		t.Parallel()
		w := boundCover(t)
		w.s.Routes = []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Enabled: true},
			{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true},
			{Name: "heavy-a", Tier: cardhdr.RouteHeavy, Enabled: true},
		}
		a := answerOn(t, w, on(), NBound, "s1-1")
		require.Equal(t, ActUp, a.Act, a.Why)
		assert.False(t, a.Friend)
		assert.Equal(t, cardhdr.RouteHeavy, a.Tier)
		assert.Equal(t, cardhdr.RouteHeavy, a.set[FieldRuleTier])
		assert.Equal(t, "0", a.set[FieldRuleFails])
	})
}

// failedCover is a world with s1-1 a flash card in review whose work failed once on flash,
// at its second failure, and the NWorkFailed judgment open.
func failedCover(t *testing.T) *world {
	t.Helper()
	w := ruleCoverWorld(t, "")
	pr := w.s.Work.Placed("s1-1")
	pr.Fields["attempt"] = "3"
	pr.Fields["result"] = "failed"
	pr.Fields[FieldRuleTier] = cardhdr.RouteFlash
	pr.Fields[FieldRuleFails] = "1"
	pr.Fields[FieldFailure] = "cover-failed-flake"
	w.place(w.s.Work, "s1-1", "s1", Review)
	w.note(Note{Kind: Judgment, Type: NWorkFailed, Stream: "s1", Primaries: []string{"s1-1"}, At: t0})
	return w
}

func TestSprintRulesCoverFailedEscalation(t *testing.T) {
	t.Parallel()

	t.Run("the next dealt tier, never frontier", func(t *testing.T) {
		t.Parallel()
		w := failedCover(t)
		w.s.Routes = []Route{{Name: "pro-a", Tier: cardhdr.RoutePro, Enabled: true}}
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		require.Equal(t, RuleFailed, a.Rule)
		require.Equal(t, ActUp, a.Act, a.Why)
		assert.Equal(t, cardhdr.RoutePro, a.Tier)
		assert.NotEqual(t, cardhdr.RouteFrontier, a.Tier)
		assert.Equal(t, cardhdr.RoutePro, a.set[FieldRuleTier])
		assert.Equal(t, "0", a.set[FieldRuleFails])
	})

	t.Run("no route at all takes the next tier", func(t *testing.T) {
		t.Parallel()
		w := failedCover(t)
		_, why := w.s.noRoute(withField(w.s.Work.Placed("s1-1"), FieldTier, cardhdr.RoutePro))
		require.Empty(t, why, "a store with no route at all serves every tier")
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		require.Equal(t, ActUp, a.Act, a.Why)
		assert.Equal(t, cardhdr.RoutePro, a.Tier)
	})
}

func TestSprintRulesCoverRuleOff(t *testing.T) {
	t.Parallel()

	t.Run("the bound rule off", func(t *testing.T) {
		t.Parallel()
		w := boundCover(t)
		w.s.RulesOff = []string{RuleBound}
		a := answerOn(t, w, on(), NBound, "s1-1")
		assert.Equal(t, ActOff, a.Act, a.Why)
		assert.Contains(t, a.Why, "turns the rule "+RuleBound+" off")
	})

	t.Run("the failed rule off", func(t *testing.T) {
		t.Parallel()
		w := failedCover(t)
		w.s.RulesOff = []string{RuleFailed}
		a := answerOn(t, w, on(), NWorkFailed, "s1-1")
		assert.Equal(t, ActOff, a.Act, a.Why)
	})

	t.Run("the conflict rule off", func(t *testing.T) {
		t.Parallel()
		w := conflictCover(t, "file", "the head h1 of s1-1 does not merge: CONFLICT")
		w.s.RulesOff = []string{RuleConflict}
		a := answerOn(t, w, on(), NConflict, StreamSubject("s1"))
		assert.Equal(t, ActOff, a.Act, a.Why)
	})
}
