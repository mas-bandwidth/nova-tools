package request

import "testing"

// wantDestination is the spec's list of typed transitions, written out per
// (event type @ source state). A pair not listed has no transition. A change to
// eventSpecs or to this table is a red test.
//
//	ready -> working                     a recorded start
//	working -> review                    a bound result, including failure or return
//	review -> ready                      a reasoned retry or rework verdict
//	review -> merging                    an accepting verdict
//	merging -> landed                    verified landing
//	merging -> review                    a queue rejection or a changed code head
//	nonterminal -> done                  cancellation
//	waiting -> done                      dependency failure
//	review -> done                       verified non-code completion
//	waiting, ready, working -> landed    verified external landing
var wantDestination = map[string]State{
	"start@ready":               Working,
	"result@working":            Review,
	"verdict-accept@review":     Merging,
	"verdict-retry@review":      Ready,
	"verdict-rework@review":     Ready,
	"head@merging":              Review,
	"ci-red@merging":            Review,
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

var wantOutcome = map[EventType]Outcome{
	EvCancel:           Cancelled,
	EvDependencyFailed: DependencyFailed,
	EvCompleted:        Completed,
}

func TestDestinationMatchesTheSpecList(t *testing.T) {
	t.Parallel()
	if len(EventTypes) != 13 || len(States) != 7 {
		t.Fatalf("closed sets changed: %d event types, %d states", len(EventTypes), len(States))
	}
	seen := 0
	for _, et := range EventTypes {
		for _, st := range States {
			key := string(et) + "@" + string(st)
			want, listed := wantDestination[key]
			got, ok := Destination(et, st)
			if ok != listed || got != want {
				t.Errorf("Destination(%s, %s) = (%q, %v), want (%q, %v)", et, st, got, ok, want, listed)
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

func TestDestinationRefusesUnknownTypeAndState(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		et EventType
		st State
	}{{"", Ready}, {"bogus", Ready}, {EvStart, ""}, {EvStart, "limbo"}, {"verdict", Review}, {EvStart, State("Ready")}} {
		if got, ok := Destination(c.et, c.st); ok || got != "" {
			t.Errorf("Destination(%q, %q) = (%q, %v), want no transition", c.et, c.st, got, ok)
		}
	}
}

func TestTerminalStatesHaveNoTransition(t *testing.T) {
	t.Parallel()
	for _, et := range EventTypes {
		for _, st := range []State{Landed, Done} {
			if _, ok := Destination(et, st); ok {
				t.Errorf("%s leaves terminal state %s", et, st)
			}
		}
	}
}

func TestOutcomeOfDoneEvents(t *testing.T) {
	t.Parallel()
	for _, et := range EventTypes {
		want, has := wantOutcome[et]
		got, ok := OutcomeOf(et)
		if ok != has || got != want {
			t.Errorf("OutcomeOf(%s) = (%q, %v), want (%q, %v)", et, got, ok, want, has)
		}
		for _, st := range States {
			dst, ok := Destination(et, st)
			if ok && (dst == Done) != has {
				t.Errorf("%s@%s goes to %s but its outcome is %v", et, st, dst, has)
			}
		}
	}
	if _, ok := OutcomeOf("bogus"); ok {
		t.Error("an unknown type has an outcome")
	}
	for _, o := range Outcomes {
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
	got := SourceStates(EvExternalLanding)
	want := []State{Waiting, Ready, Working}
	if len(got) != len(want) {
		t.Fatalf("SourceStates = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SourceStates = %v, want %v", got, want)
		}
	}
	if len(SourceStates(EvCIGreen)) != 0 || len(SourceStates("bogus")) != 0 {
		t.Error("a type with no transition lists a source")
	}
}
