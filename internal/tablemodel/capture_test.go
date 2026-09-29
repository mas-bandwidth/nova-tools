package tablemodel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// stateBefore returns the state each step of the recorded trace started from.
func stateBefore(tr Trace, i int) State {
	if i == 0 {
		return tr.Initial
	}
	return tr.Steps[i-1].State
}

func TestEveryRecordedReceiptAgreesWithTheStatesAroundIt(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	receipts, refusals, silent := 0, 0, 0
	revisions := map[string]int{}
	for i, s := range tr.Steps {
		switch {
		case s.Receipt != nil:
			receipts++
			if err := validateDelta(s.Receipt, stateBefore(tr, i), s.State); err != nil {
				t.Errorf("step %d (%s): %v", i, s.Verb, err)
			}
			// Revisions continue one at a time, per table.
			table := s.Args[0]
			if got := atoi(s.Receipt["rev_before"]); got != revisions[table] && revisions[table] != 0 {
				t.Errorf("step %d: receipt starts at %d, the table was at %d", i, got, revisions[table])
			}
			revisions[table] = atoi(s.Receipt["rev_after"])
		case s.Refused != nil && *s.Refused:
			refusals++
		default:
			silent++
		}
	}
	// The trace is only worth having if it holds changes, refusals and the two
	// model-only steps.
	if receipts < 10 || refusals < 2 || silent < 3 {
		t.Fatalf("receipts=%d refusals=%d silent=%d", receipts, refusals, silent)
	}
}

func TestValidateDeltaRefusesEachKindOfDisagreement(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	// Step 0 is the first move: m1 goes from c1 to c2 of t1 at epoch 1.
	first := tr.Steps[0]
	before, after := tr.Initial, first.State
	mutate := func(edit func(Event)) Event {
		e := first.Receipt.clone()
		edit(e)
		return e
	}
	if err := validateDelta(first.Receipt, before, after); err != nil {
		t.Fatalf("the recorded receipt is refused: %v", err)
	}
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{"a member change with the wrong destination", mutate(func(e Event) {
			e["members"] = `[{"id":"m1","score":"1","to":"","from":"r1:c1"}]`
		}), "receipt member delta disagrees with store"},
		{"a member change with the wrong score", mutate(func(e Event) {
			e["members"] = `[{"id":"m1","score":"9","to":"r1:c2","from":"r1:c1"}]`
		}), "receipt member delta disagrees with store"},
		{"no member change for a move", mutate(func(e Event) { e["members"] = `[]` }), "receipt member delta disagrees with store"},
		{"a member change nobody made", mutate(func(e Event) {
			e["members"] = `[{"id":"m1","score":"1","to":"r1:c2","from":"r1:c1"},{"id":"m3","score":"1","to":"r1:c1","from":""}]`
		}), "receipt member delta disagrees with store"},
		{"an omitted affected cell", mutate(func(e Event) { e["cells"] = `["r1:c1"]` }), "receipt omits an affected cell"},
		{"a duplicated affected cell", mutate(func(e Event) { e["cells"] = `["r1:c1","r1:c1","r1:c2"]` }), "duplicate affected cell"},
		{"a change called a noop", mutate(func(e Event) { e["outcome"] = "noop" }), "receipt calls a state change a noop"},
		{"members that are not a list", mutate(func(e Event) { e["members"] = `{}` }), "receipt members are not a list of objects"},
		{"arguments that are not a list", mutate(func(e Event) { e["args"] = `{}` }), "receipt arguments are not a list"},
	}
	for _, tc := range tests {
		err := validateDelta(tc.event, before, after)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	// A noop that really changed nothing is fine.
	noop := first.Receipt.clone()
	noop["outcome"], noop["members"], noop["cells"] = "noop", "[]", "[]"
	if err := validateDelta(noop, after, after); err != nil {
		t.Errorf("a true noop was refused: %v", err)
	}
}

func TestContinuityWantsTheRevisionsToFollowOnAnother(t *testing.T) {
	t.Parallel()
	e := Event{"rev_before": "4", "rev_after": "5"}
	if err := continuity(e, 4); err != nil {
		t.Fatal(err)
	}
	for _, before := range []int{3, 5} {
		err := continuity(e, before)
		if err == nil || !strings.HasPrefix(err.Error(), "GAP:") {
			t.Errorf("store at %d: %v", before, err)
		}
	}
	if err := continuity(Event{"rev_before": "4", "rev_after": "6"}, 4); err == nil {
		t.Error("a receipt that skips a revision was accepted")
	}
}

func TestTheMutationControlsFailOnTheRecordedTrace(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	if err := guard(func() { mutationControls(tr) }); err != nil {
		t.Fatalf("the controls do not hold on the recorded trace: %v", err)
	}
	// A control that cannot fail is not a control: with nothing to corrupt the
	// check itself says so.
	empty := tr
	empty.Steps = append([]TraceStep(nil), tr.Steps...)
	empty.Steps[0].Receipt = nil
	if err := guard(func() { mutationControls(empty) }); err == nil {
		t.Error("a trace whose first step has no receipt passed the controls")
	}
	noMembers := empty
	noMembers.Steps = append([]TraceStep(nil), tr.Steps...)
	r := tr.Steps[0].Receipt.clone()
	r["members"] = "[]"
	noMembers.Steps[0].Receipt = r
	if err := guard(func() { mutationControls(noMembers) }); err == nil {
		t.Error("a receipt with no member change passed the member control")
	}
}

func TestEveryStepModelMatchesItsAction(t *testing.T) {
	t.Parallel()
	tr := loadTrace(t)
	epoch := 1
	for i, a := range Actions() {
		got, err := ActionTLA(a, epoch)
		if err != nil || got != tr.Steps[i].Model {
			t.Errorf("step %d (%s at epoch %d): %q, %v; recorded %q", i, a.Verb, epoch, got, err, tr.Steps[i].Model)
		}
		if a.Verb == "advance" {
			epoch++
		}
	}
	if epoch != 2 {
		t.Fatalf("the trace advances to epoch %d, want 2", epoch)
	}
}

func TestStateCloneSharesNothing(t *testing.T) {
	t.Parallel()
	s := loadTrace(t).Initial
	c := s.Clone()
	if !reflect.DeepEqual(s, c) {
		t.Fatal("a clone differs from its original")
	}
	c.Live[0].Table = "changed"
	c.Rows[0].Row = "changed"
	c.Data[0].Member = "changed"
	c.Place[0].Locations[0].Cell = NoPlace
	c.Seen["w1"] = 99
	if reflect.DeepEqual(s, c) || s.Live[0].Table == "changed" || s.Rows[0].Row == "changed" || s.Data[0].Member == "changed" ||
		s.Place[0].Locations[0].Cell == NoPlace || s.Seen["w1"] == 99 {
		t.Fatal("editing a clone changed its original")
	}
}

func TestStateJSONKeepsTheModelsTupleShape(t *testing.T) {
	t.Parallel()
	c := mkCell("t1", 2, "r1", "c3")
	raw, _ := json.Marshal(Datum{c, "m1", 4})
	if string(raw) != `[[["t1",2],"r1","c3"],"m1",4]` {
		t.Fatalf("datum = %s", raw)
	}
	var back Datum
	if err := json.Unmarshal(raw, &back); err != nil || back != (Datum{c, "m1", 4}) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	for _, bad := range []string{`{}`, `[1]`, `[["t1"],"r","c"]`} {
		var cell Cell
		if err := json.Unmarshal([]byte(bad), &cell); err == nil {
			t.Errorf("%s was read as a cell", bad)
		}
	}
}
