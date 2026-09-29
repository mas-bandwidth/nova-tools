package batchmodel

import (
	"context"
	"strings"
	"testing"
)

func fixture() Step {
	before := Member{Exists: true, Epoch: "1", Revision: "7", Place: &Place{"r1", "c1", "1"}, Fields: map[string]string{"status": ""}}
	after := Member{Exists: true, Epoch: "1", Revision: "8", Place: &Place{"r2", "c1", "1"}, Fields: map[string]string{"status": "done"}}
	guard := Member{Exists: true, Epoch: "1", Revision: "2", Place: &Place{"r1", "c2", "2"}, Fields: map[string]string{"token": "permit"}}
	canonical := []byte(`{"schema":1,"operation_id":"op1"}`)
	r := &Receipt{StreamID: "1-0", OperationID: "op1", Digest: "digest", Actor: "w1", Table: "t1", Epoch: "1", BeforeRevision: "12", AfterRevision: "13", Kind: "changed", SelectedCount: 2, GuardCount: 1, ChangedCount: 1, Members: []Delta{{"m1", cloneMember(before), cloneMember(after)}, {"m3", cloneMember(guard), cloneMember(guard)}}}
	var state ModelState
	for i := range state.Values {
		state.Values[i] = String("state")
	}
	step := Step{
		Request: Request{Canonical: canonical, OperationID: "op1", Digest: "digest", Actor: "w1", Table: "t1", Epoch: "1", Revision: "12", Selected: []string{"m1", "m3"}, ExpectedRevision: map[string]*string{"m1": strptr("7"), "m3": strptr("2")}},
		Result:  Accepted,
		Before:  Snapshot{Image: map[string][]byte{"table:t1:revision": []byte("before")}, TableRevision: "12", Epoch: "1", EpochFields: map[string]string{"n": "1"}, RevisionFields: map[string]string{"n": "12"}, Definitions: map[string]map[string]string{"1": {"_present": "1", "_revision": "12"}, "2": {}}, Members: map[string]Member{"m1": before, "m3": guard}, Operations: 0, Receipts: 0, StreamInfo: StreamInfo{LastGeneratedID: "0-0", MaxDeletedID: "0-0"}},
		After: Snapshot{Image: map[string][]byte{"table:t1:revision": []byte("after")}, TableRevision: "13", Epoch: "1", EpochFields: map[string]string{"n": "1"}, RevisionFields: map[string]string{"n": "13"}, Definitions: map[string]map[string]string{"1": {"_present": "1", "_revision": "13"}, "2": {}}, Members: map[string]Member{"m1": after, "m3": guard}, Operations: 1, Receipts: 1, StreamInfo: StreamInfo{EntriesAdded: 1, LastGeneratedID: "1-0", MaxDeletedID: "0-0"}, Events: []StreamEvent{{ID: "1-0", Fields: map[string]string{"verb": "apply"}}},
			Recorded: &OperationRecord{Table: "t1", Epoch: "1", OperationID: "op1", Digest: "digest", ReceiptID: "1-0", BeforeRevision: "12", AfterRevision: "13", Outcome: "changed", Canonical: canonical}, LastReceipt: r},
		Receipt: r, BeforeModel: state, AfterModel: state,
		TableBaseline: "12", MemberBaseline: map[string]string{"m1": "7", "m3": "2"},
		Action: BatchAction{Table: "t1", Epoch: "1", OperationID: "op1", Actor: "w1", Digest: "digest", Canonical: canonical, ProjectedRevision: 0, Members: []MemberAction{
			{ID: "m1", Revision: ptr(0), Source: Cell{"t1", "1", "r1", "c1"}, Target: Cell{"t1", "1", "r2", "c1"}, GuardField: "status", GuardKind: "equals", GuardValues: []string{""}, SetField: "status", SetValue: "done", Score: 1, Change: true},
			{ID: "m3", Revision: ptr(0), Source: Cell{"t1", "1", "r1", "c2"}, Target: Cell{"t1", "1", "r1", "c2"}, GuardField: "token", GuardKind: "equals", GuardValues: []string{"permit"}, SetField: "none", Score: 2},
		}},
	}
	step.Request.Entries = cloneAction(step.Action).Members
	return step
}

func ptr(v uint64) *uint64    { return &v }
func strptr(v string) *string { return &v }

func TestReceiptCheckedAgainstIndependentSnapshots(t *testing.T) {
	t.Parallel()
	s := fixture()
	if err := ValidateStep(s); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name   string
		mutate func(*Step)
		want   string
	}{
		{"omitted selected member", func(s *Step) { s.Receipt.Members = s.Receipt.Members[:1]; s.After.LastReceipt = s.Receipt }, "omits selected member"},
		{"present empty changed to absent", func(s *Step) { delete(s.Receipt.Members[0].Before.Fields, "status") }, "disagrees"},
		{"wrong score", func(s *Step) { s.Receipt.Members[0].After.Place.Score = "2" }, "disagrees"},
		{"wrong selection count", func(s *Step) { s.Receipt.SelectedCount = 1 }, "count"},
		{"wrong actor", func(s *Step) { s.Receipt.Actor = "other" }, "identity"},
		{"wrong revision", func(s *Step) { s.Receipt.AfterRevision = "14" }, "revision"},
		{"wrong model member revision guard", func(s *Step) { s.Action.Members[0].Revision = ptr(1); s.Request.Entries[0].Revision = ptr(1) }, "model revision differs"},
		{"omitted guard became comparison", func(s *Step) { s.Request.ExpectedRevision["m1"] = nil }, "omitted guard"},
		{"missing stream append", func(s *Step) { s.After.Receipts = 0 }, "exactly one"},
		{"unchanged member advanced", func(s *Step) { m := s.After.Members["m3"]; m.Revision = "3"; s.After.Members["m3"] = m }, "unchanged member"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			tc.mutate(&s)
			if err := ValidateStep(s); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRefusalAndReplayRequireCompleteImageAndOriginalReceipt(t *testing.T) {
	t.Parallel()
	s := fixture()
	s.Result = Refused
	s.Receipt = nil
	s.After = s.Before
	if err := ValidateStep(s); err != nil {
		t.Fatal(err)
	}
	s.After.Image = map[string][]byte{"table:t1:revision": []byte("changed")}
	if err := ValidateStep(s); err == nil || !strings.Contains(err.Error(), "complete store image") {
		t.Fatalf("error = %v", err)
	}
	s = fixture()
	s.Result = Replayed
	s.After = s.Before
	s.PriorReceipt = s.Receipt
	s.Before.Recorded = &OperationRecord{Table: "t1", Epoch: "1", OperationID: "op1", Digest: "digest", ReceiptID: "1-0", BeforeRevision: "12", AfterRevision: "13", Outcome: "changed", Canonical: s.Request.Canonical}
	s.After.Recorded = s.Before.Recorded
	if err := ValidateStep(s); err != nil {
		t.Fatal(err)
	}
	r := *s.Receipt
	r.StreamID = "2-0"
	s.Receipt = &r
	if err := ValidateStep(s); err == nil || !strings.Contains(err.Error(), "original receipt") {
		t.Fatalf("error = %v", err)
	}
}

func TestHarnessUsesActualBatchActionAndObservedState(t *testing.T) {
	t.Parallel()
	s := fixture()
	h, err := RenderHarness([]Step{s})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"ReplaySpec == BatchReplayInit", "ActualState == BatchReplayState", "MatchesExecution == ActualState=Observed[step+1]", "step=0 -> Apply(", "6f7031"} {
		if !strings.Contains(h, part) {
			t.Errorf("harness omits %q", part)
		}
	}
	s.Action.Canonical = []byte("different")
	if _, err := RenderHarness([]Step{s}); err == nil || !strings.Contains(err.Error(), "identity differs") {
		t.Fatalf("action identity error = %v", err)
	}
}

func TestTLALiteralCannotInjectSource(t *testing.T) {
	t.Parallel()
	v, err := String("x\" INVARIANT FALSE").TLA()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v, `\"`) {
		t.Fatalf("quote not escaped: %q", v)
	}
	if _, err := String("x\nINVARIANT FALSE").TLA(); err == nil {
		t.Fatal("unsupported control byte accepted")
	}
	if _, err := Record(map[string]Expr{"bad-name": String("x")}).TLA(); err == nil {
		t.Fatal("invalid record field accepted")
	}
	if _, err := Function([]Expr{String("k")}, nil).TLA(); err == nil {
		t.Fatal("mismatched function accepted")
	}
}

func TestActionCarriesFullModelEntryShape(t *testing.T) {
	t.Parallel()
	s := fixture()
	m := s.Action.Members[0]
	m.Revision = nil // explicit optional revision, not a zero comparison
	m.SetField = ""
	m.UnsetField = "status"
	m.Remove = true
	m.RemoveSupplied = true
	s.Action.Members[0] = m
	text, err := s.Action.TLA()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"hasRevision |-> FALSE", "revision |-> 0", "remove |-> TRUE", "removeSupplied |-> TRUE", `unsetField |-> "status"`, "setField |-> \"none\"", "setValue |-> NoValue"} {
		if !strings.Contains(text, part) {
			t.Errorf("action omits %q", part)
		}
	}
	if strings.Contains(text, "NoRevision") {
		t.Fatal("obsolete omitted-revision sentinel remains")
	}
}

func TestConfigRequiresThePinnedModelShape(t *testing.T) {
	t.Parallel()
	input := "SPECIFICATION BatchSpec\nCONSTANTS\n MaxSteps = 3\nINVARIANTS BatchTypeOK\n"
	cfg, err := RenderConfig(input, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"SPECIFICATION ReplaySpec", "MaxSteps = 2", "INVARIANT MatchesExecution"} {
		if !strings.Contains(cfg, part) {
			t.Errorf("config omits %q", part)
		}
	}
	if _, err := RenderConfig(strings.ReplaceAll(input, "BatchSpec", "Other"), 2); err == nil {
		t.Fatal("unexpected config accepted")
	}
}

func TestCaptureReadsBothSidesAndFreezesReceiptAndImage(t *testing.T) {
	t.Parallel()
	f := fixture()
	reads := 0
	calls := 0
	step, err := CaptureStep(context.Background(), 0, f.Request, f.Action, f.TableBaseline, f.MemberBaseline,
		func(context.Context, Request) (Snapshot, error) {
			reads++
			if reads == 1 {
				return f.Before, nil
			}
			return f.After, nil
		},
		func(s Snapshot, pc ProjectionContext) (ModelState, error) {
			if pc.Index != 0 || pc.Request.OperationID != "op1" {
				t.Fatalf("projection context = %+v", pc)
			}
			if (s.TableRevision == "12" && pc.Phase != "before") || (s.TableRevision == "13" && (pc.Phase != "after" || pc.Result != Accepted)) {
				t.Fatalf("projection phase = %+v", pc)
			}
			if s.TableRevision == "12" {
				return f.BeforeModel, nil
			}
			return f.AfterModel, nil
		},
		func(context.Context, Request, Snapshot) (Result, *Receipt, *Receipt, error) {
			calls++
			return Accepted, f.Receipt, nil, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 || calls != 1 {
		t.Fatalf("reads=%d calls=%d", reads, calls)
	}
	f.Receipt.Members[0].Before.Fields["status"] = "corrupt"
	f.After.Image["table:t1:revision"][0] = 'X'
	if err := ValidateStep(step); err != nil {
		t.Fatalf("retained evidence changed after source mutation: %v", err)
	}
}

func TestRevisionProjectionRejectsUnderflowAndNoncanonicalDecimal(t *testing.T) {
	t.Parallel()
	if n, err := ProjectRevision("18446744073709551615", "18446744073709551614"); err != nil || n != 1 {
		t.Fatalf("project = %d, %v", n, err)
	}
	for _, v := range []string{"11", "01", "-1", "18446744073709551616"} {
		if _, err := ProjectRevision(v, "12"); err == nil {
			t.Errorf("accepted bad projection %q", v)
		}
	}
}
