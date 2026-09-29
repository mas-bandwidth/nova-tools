package request

import (
	"strings"
	"testing"
)

// wantDestination is the spec's list of typed transitions, and the owner's
// rulings, written out per (input type @ source state), independently of the
// code's table. A pair not listed has no transition. A change to inputSpecs or
// to this table is a red test.
//
//	ready -> working                     a recorded start
//	working -> review                    a bound result, including failure or return
//	review -> ready                      a reasoned retry or rework verdict
//	review -> merging                    an accepting verdict
//	merging -> landed                    verified landing
//	merging -> review                    a queue rejection or a changed code head
//	review -> review                     a changed code head in review (ruling)
//	nonterminal -> done                  cancellation
//	waiting -> done                      dependency failure
//	review -> done                       verified non-code completion
//	waiting, ready, working -> landed    verified external landing
//
// A CI result is not a lifecycle input: it is evidence.
var wantDestination = map[string]State{
	"start@ready":               Working,
	"result@working":            Review,
	"verdict-accept@review":     Merging,
	"verdict-retry@review":      Ready,
	"verdict-rework@review":     Ready,
	"head@merging":              Review,
	"head@review":               Review,
	"queue-rejected@merging":    Review,
	"cancel@waiting":            Done,
	"cancel@ready":              Done,
	"cancel@working":            Done,
	"cancel@review":             Done,
	"cancel@merging":            Done,
	"landing@merging":           Landed,
	"external-landing@waiting":  Landed,
	"external-landing@ready":    Landed,
	"external-landing@working":  Landed,
	"dependency-failed@waiting": Done,
	"completed@review":          Done,
}

var wantOutcome = map[InputType]Outcome{
	InCancel:           Cancelled,
	InDependencyFailed: DependencyFailed,
	InCompleted:        Completed,
}

func TestDestinationMatchesTheSpecList(t *testing.T) {
	t.Parallel()
	if len(InputTypes()) != 12 || len(States()) != 7 {
		t.Fatalf("closed sets changed: %d input types, %d states", len(InputTypes()), len(States()))
	}
	seen := 0
	for _, it := range InputTypes() {
		for _, st := range States() {
			key := string(it) + "@" + string(st)
			want, listed := wantDestination[key]
			got, ok := Destination(it, st)
			if ok != listed || got != want {
				t.Errorf("Destination(%s, %s) = (%q, %v), want (%q, %v)", it, st, got, ok, want, listed)
			}
			if listed {
				seen++
			}
		}
	}
	if seen != len(wantDestination) {
		t.Errorf("table lists %d pairs but %d were reachable through the closed sets", len(wantDestination), seen)
	}
}

// CI results are evidence, never lifecycle inputs.
func TestCIIsNotALifecycleInput(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ci-green", "ci-red", "ci"} {
		if InputType(name).Valid() {
			t.Errorf("%s is a lifecycle input type", name)
		}
		for _, st := range States() {
			if _, ok := Destination(InputType(name), st); ok {
				t.Errorf("%s has a transition from %s", name, st)
			}
		}
	}
}

func TestDestinationRefusesUnknownTypeAndState(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		it InputType
		st State
	}{{"", Ready}, {"bogus", Ready}, {InStart, ""}, {InStart, "limbo"}, {"verdict", Review}, {InStart, State("Ready")}, {"ci-red", Merging}, {"ci-green", Review}} {
		if got, ok := Destination(c.it, c.st); ok || got != "" {
			t.Errorf("Destination(%q, %q) = (%q, %v), want no transition", c.it, c.st, got, ok)
		}
	}
}

func TestTerminalStatesHaveNoTransition(t *testing.T) {
	t.Parallel()
	for _, it := range InputTypes() {
		for _, st := range []State{Landed, Done} {
			if _, ok := Destination(it, st); ok {
				t.Errorf("%s leaves terminal state %s", it, st)
			}
		}
	}
}

func TestOutcomeOfDoneInputs(t *testing.T) {
	t.Parallel()
	for _, it := range InputTypes() {
		want, has := wantOutcome[it]
		got, ok := OutcomeOf(it)
		if ok != has || got != want {
			t.Errorf("OutcomeOf(%s) = (%q, %v), want (%q, %v)", it, got, ok, want, has)
		}
		for _, st := range States() {
			dst, ok := Destination(it, st)
			if ok && (dst == Done) != has {
				t.Errorf("%s@%s goes to %s but its outcome is %v", it, st, dst, has)
			}
		}
	}
	if _, ok := OutcomeOf("bogus"); ok {
		t.Error("an unknown type has an outcome")
	}
	for _, o := range Outcomes() {
		if !o.Valid() {
			t.Errorf("outcome %q not valid", o)
		}
	}
	if Outcome("replaced") != Replaced || Outcome("x").Valid() {
		t.Error("outcome set drifted")
	}
}

func TestSourceStatesAreLifecycleOrdered(t *testing.T) {
	t.Parallel()
	got := SourceStates(InExternalLanding)
	want := []State{Waiting, Ready, Working}
	if len(got) != len(want) {
		t.Fatalf("SourceStates = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SourceStates = %v, want %v", got, want)
		}
	}
	if got := SourceStates(InHead); len(got) != 2 || got[0] != Review || got[1] != Merging {
		t.Errorf("head moves from %v", got)
	}
	if len(SourceStates("ci-green")) != 0 || len(SourceStates("bogus")) != 0 {
		t.Error("a type with no transition lists a source")
	}
}

// wantForced is the forced moves, as the owner ruled them, independently of the
// code's table: a red CI result recorded for a card in merging returns it to
// review. Every other (kind, disposition, state) forces nothing.
var wantForced = map[string]State{"ci|red|merging": Review}

func TestForcedMoveExhaustively(t *testing.T) {
	t.Parallel()
	n := 0
	for _, k := range EvidenceKinds() {
		for _, d := range Dispositions() {
			for _, st := range States() {
				key := string(k) + "|" + string(d) + "|" + string(st)
				want, listed := wantForced[key]
				got, ok := ForcedMove(k, d, st)
				if ok != listed || got != want {
					t.Errorf("ForcedMove(%s, %s, %s) = (%q, %v), want (%q, %v)", k, d, st, got, ok, want, listed)
				}
				if ok {
					n++
				}
			}
		}
	}
	if n != len(wantForced) || len(ForcedMoves()) != 1 {
		t.Errorf("%d forced moves reachable, %d listed, %d in the data; the list has exactly one entry", n, len(wantForced), len(ForcedMoves()))
	}
	// A forced move is always backward and never leaves the open states.
	for _, f := range ForcedMoves() {
		if f.From != Merging || f.To != Review {
			t.Errorf("forced move %+v is not merging -> review", f)
		}
	}
	for _, c := range []struct {
		k EvidenceKind
		d Disposition
		s State
	}{{"", DispRed, Merging}, {KindCI, "", Merging}, {"bogus", DispRed, Merging}, {KindCI, DispRed, "limbo"}, {KindRead, DispReject, Merging}, {KindCI, DispGreen, Merging}, {KindCI, DispRed, Review}, {KindCI, DispRed, Working}, {KindCI, DispRed, Landed}} {
		if got, ok := ForcedMove(c.k, c.d, c.s); ok || got != "" {
			t.Errorf("ForcedMove(%q, %q, %q) = (%q, %v), want nothing", c.k, c.d, c.s, got, ok)
		}
	}
}

func TestForcedMovesReturnsACopy(t *testing.T) {
	t.Parallel()
	f := ForcedMoves()
	f[0].To = Landed
	if got, _ := ForcedMove(KindCI, DispRed, Merging); got != Review {
		t.Fatalf("mutating the copy changed the data: %s", got)
	}
}

// The closed sets are functions that return copies: a caller cannot change them.
func TestClosedSetsAreCopies(t *testing.T) {
	t.Parallel()
	s := States()
	s[0] = "x"
	o := Outcomes()
	o[0] = "x"
	op := Operations()
	op[0] = "x"
	it := InputTypes()
	it[0] = "x"
	k := EvidenceKinds()
	k[0] = "x"
	d := Dispositions()
	d[0] = "x"
	if States()[0] != Waiting || Outcomes()[0] != Completed || Operations()[0] != OpAdmit || InputTypes()[0] != InStart || EvidenceKinds()[0] != KindRead || Dispositions()[0] != DispAccept {
		t.Fatal("a closed set was changed through its copy")
	}
	if !strings.Contains(string(Operations()[6]), "check") {
		t.Fatal("check is the seventh operation")
	}
}
