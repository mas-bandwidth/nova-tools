package request

import "testing"

// wantInputClass is the classification of every lifecycle input type, written
// out independently of the code: forward progress is mechanical; a return to an
// earlier state, a reader's retry or rework, a queue rejection, a changed head, a
// cancellation, a dependency failure and a landing outside the review path are
// points where judgment may be required.
var wantInputClass = map[InputType]Class{
	InStart: Mechanical, InResult: Mechanical, InVerdictAccept: Mechanical, InLanding: Mechanical,
	InVerdictRetry: Judgment, InVerdictRework: Judgment, InHead: Judgment, InQueueRejected: Judgment,
	InCancel: Judgment, InExternalLanding: Judgment, InDependencyFailed: Judgment,
}

// The test that fails when a lifecycle input type is added without a
// classification, or a classification outlives its type.
func TestEveryLifecycleInputIsClassified(t *testing.T) {
	t.Parallel()
	for _, it := range InputTypes() {
		got, ok := ClassifyInput(it)
		want, listed := wantInputClass[it]
		if !ok || !listed {
			t.Errorf("input type %s has no classification (in code %v, in the test %v)", it, ok, listed)
		}
		if got != want {
			t.Errorf("ClassifyInput(%s) = %s, want %s", it, got, want)
		}
	}
	if len(transitions) != len(InputTypes()) || len(wantInputClass) != len(InputTypes()) {
		t.Errorf("%d classifications for %d input types (test lists %d)", len(transitions), len(InputTypes()), len(wantInputClass))
	}
	if _, ok := ClassifyInput("ci-red"); ok {
		t.Error("a CI result has an input classification")
	}
	// Every transition that returns a card to an earlier state is a judgment
	// point, whichever input makes it.
	order := map[State]int{}
	for i, s := range States() {
		order[s] = i
	}
	for _, it := range InputTypes() {
		for _, from := range SourceStates(it) {
			to, _ := Destination(it, from)
			if to != Unplaced && order[to] <= order[from] {
				if c, _ := ClassifyInput(it); c != Judgment {
					t.Errorf("%s@%s -> %s returns the card and is %s", it, from, to, c)
				}
			}
		}
	}
}

// The coordinator's decisions are never mechanical.
func TestTheCoordinatorsDecisionsAreJudgmentPoints(t *testing.T) {
	t.Parallel()
	for _, it := range []InputType{InCancel, InVerdictRework, InVerdictRetry} {
		if c, ok := ClassifyInput(it); !ok || c != Judgment {
			t.Errorf("%s is %s, %v", it, c, ok)
		}
		if !contains(reqOf(it), "reason") {
			t.Errorf("%s requires no reason", it)
		}
	}
}

func TestResultValuesAreClassified(t *testing.T) {
	t.Parallel()
	for v, want := range map[ResultValue]Class{ResultSuccess: Mechanical, ResultFailure: Judgment, ResultReturn: Judgment} {
		if got, ok := ClassifyResult(v); !ok || got != want {
			t.Errorf("ClassifyResult(%s) = %s, %v, want %s", v, got, ok, want)
		}
	}
	for _, v := range ResultValues() {
		if _, ok := ClassifyResult(v); !ok {
			t.Errorf("result value %s has no classification", v)
		}
	}
	if _, ok := ClassifyResult("meh"); ok {
		t.Error("an unknown result value is classified")
	}
}

func TestForcedMovesAreJudgmentPoints(t *testing.T) {
	t.Parallel()
	n := 0
	for _, k := range EvidenceKinds() {
		for _, d := range Dispositions() {
			for _, st := range States() {
				c, ok := ClassifyForcedMove(k, d, st)
				_, forced := ForcedMove(k, d, st)
				if ok != forced || (ok && c != Judgment) {
					t.Errorf("ClassifyForcedMove(%s, %s, %s) = %s, %v; forced %v", k, d, st, c, ok, forced)
				}
				if ok {
					n++
				}
			}
		}
	}
	if n != len(ForcedMoves()) {
		t.Errorf("%d classified for %d forced moves", n, len(ForcedMoves()))
	}
}

func TestEveryEvidenceObservationIsClassified(t *testing.T) {
	t.Parallel()
	want := map[string]Class{
		"read|accept": Mechanical, "read|reject": Judgment, "ci|green": Mechanical, "ci|red": Judgment,
		"sweep|clean": Mechanical, "sweep|negative": Judgment, "landing|landed": Mechanical, "queue|reject": Judgment,
	}
	n := 0
	for _, k := range EvidenceKinds() {
		if len(DispositionsOf(k)) == 0 {
			t.Errorf("kind %s takes no disposition", k)
		}
		for _, d := range DispositionsOf(k) {
			got, ok := ClassifyEvidence(k, d)
			w, listed := want[string(k)+"|"+string(d)]
			if !ok || !listed || got != w {
				t.Errorf("ClassifyEvidence(%s, %s) = %s, %v; test says %s, %v", k, d, got, ok, w, listed)
			}
			if (got == Judgment) != d.Negative() {
				t.Errorf("%s %s: negative %v but class %s", k, d, d.Negative(), got)
			}
			n++
		}
	}
	if n != len(want) {
		t.Errorf("%d observations, %d classified in the test", n, len(want))
	}
	if _, ok := ClassifyEvidence(KindRead, DispGreen); ok {
		t.Error("a disposition of another kind is classified")
	}
	// every disposition belongs to some kind
	for _, d := range Dispositions() {
		found := false
		for _, k := range EvidenceKinds() {
			for _, v := range DispositionsOf(k) {
				found = found || v == d
			}
		}
		if !found {
			t.Errorf("disposition %s belongs to no kind", d)
		}
	}
}

func TestEverySelectionOutcomeIsClassified(t *testing.T) {
	t.Parallel()
	want := map[SelectionOutcome]Class{
		OutcomeChanged: Mechanical, OutcomeBlocked: Judgment, OutcomeIneligible: Mechanical,
		OutcomeMissing: Judgment, OutcomeAlready: Mechanical, OutcomeInapplicable: Mechanical,
	}
	for _, o := range SelectionOutcomes() {
		got, ok := ClassifyOutcome(o)
		w, listed := want[o]
		if !ok || !listed || got != w || !o.Valid() {
			t.Errorf("ClassifyOutcome(%s) = %s, %v; test says %s, %v", o, got, ok, w, listed)
		}
	}
	if len(outcomeClass) != len(SelectionOutcomes()) || len(want) != len(SelectionOutcomes()) {
		t.Errorf("%d classifications for %d outcomes", len(outcomeClass), len(SelectionOutcomes()))
	}
	if _, ok := ClassifyOutcome("deferred"); ok || SelectionOutcome("deferred").Valid() {
		t.Error("an unknown outcome is classified")
	}
}

// Every notification kind has a judgment rule, and every closed set is a copy.
func TestNotificationKindsMarksAndDriftAreClosed(t *testing.T) {
	t.Parallel()
	for _, k := range NotificationKinds() {
		if _, ok := notificationRule[k]; !ok || !k.Valid() {
			t.Errorf("notification kind %s has no judgment rule", k)
		}
	}
	if len(notificationRule) != len(NotificationKinds()) {
		t.Errorf("%d rules for %d kinds", len(notificationRule), len(NotificationKinds()))
	}
	if NotificationKind("shout").Valid() || Mark("loud").Valid() || Drift("rot").Valid() {
		t.Error("an unknown name is valid")
	}
	for _, m := range Marks() {
		if !m.Valid() {
			t.Errorf("mark %s", m)
		}
	}
	for _, d := range Drifts() {
		if !d.Valid() {
			t.Errorf("drift %s", d)
		}
	}
	if len(Marks()) != 11 || len(Drifts()) != 11 || len(NotificationKinds()) != 24 {
		t.Errorf("closed sets changed size: %d marks, %d drifts, %d kinds", len(Marks()), len(Drifts()), len(NotificationKinds()))
	}
	// The notification for every judgment classification of an input exists.
	for it, row := range transitions {
		if row.class == Judgment {
			found := false
			for _, k := range NotificationKinds() {
				if notificationRule[k] == ruleAlways || notificationRule[k] == ruleEither {
					found = true
				}
			}
			if !found {
				t.Errorf("no judgment notification exists for %s", it)
			}
		}
	}
	ks := NotificationKinds()
	ks[0] = "x"
	if NotificationKinds()[0] != NoteAdmitted {
		t.Error("NotificationKinds is not a copy")
	}
}
