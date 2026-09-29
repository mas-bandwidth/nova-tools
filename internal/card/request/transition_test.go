package request

import (
	"strings"
	"testing"
)

// wantDestination is the transition table, written out by hand per (input type @
// source state), independently of the code's table: the spec's list, and the
// owner's rulings (2026-09-29). A pair not listed has no transition. A change to
// the table in lifecycle.go without a change here is a red test, and the other way
// round. "off" is a card that leaves the table (unplaced) with an outcome.
//
//	ready -> working                     a recorded start
//	working -> review                    a bound result, including failure or return
//	review -> ready                      a reasoned retry or rework verdict
//	review -> merging                    an accepting verdict
//	merging -> landed                    landing, observed from the repository
//	merging -> review                    a queue rejection or a changed code head
//	review -> review                     a changed code head in review
//	waiting, ready, working -> landed    an external landing, observed
//	nonterminal -> off (cancelled)       cancellation
//	waiting -> off (dependency-failed)   a failed prerequisite
//
// Landed is final: nothing leaves it. A CI result is not a lifecycle input: it is
// evidence.
var wantDestination = map[string]string{
	"start@ready":               "working",
	"result@working":            "review",
	"verdict-accept@review":     "merging",
	"verdict-retry@review":      "ready",
	"verdict-rework@review":     "ready",
	"head@merging":              "review",
	"head@review":               "review",
	"queue-rejected@merging":    "review",
	"cancel@waiting":            "off",
	"cancel@ready":              "off",
	"cancel@working":            "off",
	"cancel@review":             "off",
	"cancel@merging":            "off",
	"landing@merging":           "landed",
	"external-landing@waiting":  "landed",
	"external-landing@ready":    "landed",
	"external-landing@working":  "landed",
	"dependency-failed@waiting": "off",
}

// wantOutcome is the outcome of each input that takes a card off the table. There
// is no outcome completed, and no done state.
var wantOutcome = map[InputType]Outcome{
	InCancel:           Cancelled,
	InDependencyFailed: DependencyFailed,
}

func destinationText(s State) string {
	if s == Unplaced {
		return "off"
	}
	return string(s)
}

func TestDestinationMatchesTheSpecList(t *testing.T) {
	t.Parallel()
	if len(InputTypes()) != 11 || len(States()) != 6 {
		t.Fatalf("closed sets changed: %d input types, %d states", len(InputTypes()), len(States()))
	}
	seen := 0
	for _, it := range InputTypes() {
		for _, st := range States() {
			key := string(it) + "@" + string(st)
			want, listed := wantDestination[key]
			got, ok := Destination(it, st)
			if ok != listed || (ok && destinationText(got) != want) {
				t.Errorf("Destination(%s, %s) = (%q, %v), want (%q, %v)", it, st, destinationText(got), ok, want, listed)
			}
			if m, ok2 := Transition(it, st); ok2 != ok || m.To != got || (ok2 && m.Leaves()) != (ok && got == Unplaced) {
				t.Errorf("Transition(%s, %s) = %+v, %v disagrees with Destination", it, st, m, ok2)
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

// Landed is the one terminal state: the code is on the development branch, and
// nothing moves the card out of it. There is no done state.
func TestOnlyLandedIsTerminal(t *testing.T) {
	t.Parallel()
	for _, it := range InputTypes() {
		if _, ok := Destination(it, Landed); ok {
			t.Errorf("%s leaves landed", it)
		}
		if _, ok := Destination(it, "done"); ok {
			t.Errorf("%s has a transition from a done state", it)
		}
	}
	for _, st := range States() {
		if want := st == Landed; IsTerminal(st) != want {
			t.Errorf("IsTerminal(%s) = %v", st, IsTerminal(st))
		}
	}
	if State("done").Valid() || len(States()) != 6 {
		t.Error("there is a done state")
	}
	if IsTerminal("limbo") != true {
		t.Error("an unknown state has a transition")
	}
}

// A card that stopped leaves the table: it is unplaced, never done.
func TestStoppedCardsLeaveTheTable(t *testing.T) {
	t.Parallel()
	for _, it := range []InputType{InCancel, InDependencyFailed} {
		for _, st := range SourceStates(it) {
			m, ok := Transition(it, st)
			if !ok || !m.Leaves() || m.To != Unplaced || !m.Outcome.Valid() {
				t.Errorf("%s@%s = %+v, %v: a stopped card leaves the table with an outcome", it, st, m, ok)
			}
		}
	}
	if ReplaceOutcome() != Replaced || len(ReplaceableStates()) != 2 || !Replaceable(Waiting) || !Replaceable(Ready) || Replaceable(Working) || Replaceable(Review) || Replaceable(Merging) || Replaceable(Landed) {
		t.Errorf("replace: %v leaves as %s", ReplaceableStates(), ReplaceOutcome())
	}
	if from, to := ResolveMove(); from != Waiting || to != Ready {
		t.Errorf("resolve moves %s to %s", from, to)
	}
	if Initial != Waiting {
		t.Errorf("Initial = %s", Initial)
	}
	// no outcome completed, on any input or in the closed set
	if Outcome("completed").Valid() || len(Outcomes()) != 3 {
		t.Errorf("outcomes = %v", Outcomes())
	}
	// only a landing observed from the repository puts a card in landed, and only
	// done, reopen and cancel-free moves leave it
	for _, it := range InputTypes() {
		for _, st := range SourceStates(it) {
			if dst, _ := Destination(it, st); dst == Landed && !contains(transitions[it].required, "landing") {
				t.Errorf("%s reaches landed without a landing identity", it)
			}
		}
	}
	if !HoldsLanding(Landed) || HoldsLanding(Merging) || HoldsLanding(Unplaced) {
		t.Error("HoldsLanding")
	}
	// no input ends a card that has no code to land: that is an open question
	for _, it := range InputTypes() {
		if string(it) == "completed" || string(it) == "reopen" {
			t.Errorf("input %s exists", it)
		}
	}
	if !Replaced.HasSuccessor() || Cancelled.HasSuccessor() {
		t.Error("HasSuccessor")
	}
}

func TestOutcomeOfInputsThatLeaveTheTable(t *testing.T) {
	t.Parallel()
	for _, it := range InputTypes() {
		want, has := wantOutcome[it]
		got, ok := OutcomeOf(it)
		if ok != has || got != want {
			t.Errorf("OutcomeOf(%s) = (%q, %v), want (%q, %v)", it, got, ok, want, has)
		}
		for _, st := range States() {
			dst, ok := Destination(it, st)
			if ok && (dst == Unplaced) != has {
				t.Errorf("%s@%s goes to %q but its outcome is %v", it, st, dst, has)
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
	if Outcome("replaced") != Replaced || Outcome("x").Valid() || Outcome("").Valid() {
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
	if States()[0] != Waiting || Outcomes()[0] != Cancelled || Operations()[0] != OpAdmit || InputTypes()[0] != InStart || EvidenceKinds()[0] != KindRead || Dispositions()[0] != DispAccept {
		t.Fatal("a closed set was changed through its copy")
	}
	if !strings.Contains(string(Operations()[6]), "check") {
		t.Fatal("check is the seventh operation")
	}
	if len(Outcomes()) != 3 {
		t.Fatal("outcomes")
	}
}
