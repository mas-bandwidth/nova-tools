package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestS7EntryCountBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxEntries - 1, MaxEntries} {
		if _, err := EncodeStep(boundsRowsStep(n, 1, false)); err != nil {
			t.Errorf("EncodeStep with %d entries: %v", n, err)
		}
	}
	_, err := EncodeStep(boundsRowsStep(MaxEntries+1, 1, false))
	boundsWantRefusal(t, err, "LIMIT")
}

func TestS7DistinctTablesBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxTables - 1, MaxTables} {
		if _, err := EncodeStep(boundsTableCountStep(n)); err != nil {
			t.Errorf("EncodeStep with %d distinct tables: %v", n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsStep(boundsTableCountStep(MaxTables+1)), "LIMIT")
}

func TestS7ConfiguredTableCountBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxTables - 1, MaxTables} {
		m := NewMem()
		for i := 0; i < n; i++ {
			if err := m.DefineTable("bounds:", fmt.Sprintf("table-%d", i), boundsTableDefinition(i, 1)); err != nil {
				t.Errorf("DefineTable %d of %d: %v", i+1, n, err)
			}
		}
	}
	m := NewMem()
	for i := 0; i <= MaxTables; i++ {
		err := m.DefineTable("bounds:", fmt.Sprintf("table-%d", i), boundsTableDefinition(i, 1))
		if i < MaxTables && err != nil {
			t.Fatalf("DefineTable %d of %d: %v", i+1, MaxTables, err)
		}
		if i == MaxTables {
			boundsWantRefusal(t, err, "CONFIG")
		}
	}
}

func TestS7ColumnsPerTableBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{31, 32} {
		m := NewMem()
		if err := m.DefineTable("bounds:", "work", boundsTableDefinition(0, n)); err != nil {
			t.Errorf("DefineTable with %d columns: %v", n, err)
		}
	}
	m := NewMem()
	boundsWantRefusal(t, m.DefineTable("bounds:", "work", boundsTableDefinition(0, 33)), "CONFIG")
}

func TestS7IntentByteBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxIntentBytes - 1, MaxIntentBytes} {
		intent, op := strings.Repeat("i", n), "intent-boundary"
		step := Step{Epoch: "0", Space: "bounds:", Op: &op, Intent: &intent, Entries: []Entry{}}
		if _, err := EncodeStep(step); err != nil {
			t.Errorf("EncodeStep with %d-byte intent: %v", n, err)
		}
	}
	intent, op := strings.Repeat("i", MaxIntentBytes+1), "intent-boundary"
	step := Step{Epoch: "0", Space: "bounds:", Op: &op, Intent: &intent, Entries: []Entry{}}
	boundsWantRefusal(t, encodeBoundsStep(step), "LIMIT")
}

func TestS7IdentifierUTF8ByteBoundaries(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"stored_id", "row", "column", "field", "op", "space"} {
		for _, n := range []int{MaxIdentifierBytes - 1, MaxIdentifierBytes} {
			step := boundsIdentifierStep(kind, n)
			if _, err := EncodeStep(step); err != nil {
				t.Errorf("EncodeStep %s with %d UTF-8 bytes: %v", kind, n, err)
			}
		}
		boundsWantRefusal(t, encodeBoundsStep(boundsIdentifierStep(kind, MaxIdentifierBytes+1)), "LIMIT")
	}

	// Cell references have separately bounded row and column components; the
	// separator makes a valid maximum-size reference 513 bytes overall.
	maxCell := boundsIdentifierStep("column", MaxIdentifierBytes)
	maxCell.Entries[0].To = boundsUTF8Bytes(MaxIdentifierBytes) + ":" + strings.Repeat("c", MaxIdentifierBytes)
	if _, err := EncodeStep(maxCell); err != nil {
		t.Errorf("EncodeStep with 513-byte cell reference: %v", err)
	}

	// Invalid UTF-8 remains a syntax refusal even when its byte length exceeds
	// the identifier cap.
	badUTF8 := strings.Repeat("x", MaxIdentifierBytes) + string([]byte{0xff})
	malformed := boundsIdentifierStep("stored_id", MaxIdentifierBytes)
	malformed.Entries[0].IDs = []string{badUTF8}
	malformed.Entries[0].About = []string{badUTF8}
	boundsWantRefusal(t, encodeBoundsStep(malformed), "REQUEST")
}

func TestS7MemberCandidateAndGuardBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxMemberCandidates - 1, MaxMemberCandidates} {
		if _, err := EncodeStep(boundsMemberStep("create", n)); err != nil {
			t.Errorf("EncodeStep with %d changed candidates: %v", n, err)
		}
	}
	_, err := EncodeStep(boundsMemberStep("create", MaxMemberCandidates+1))
	boundsWantRefusal(t, err, "LIMIT")

	for _, n := range []int{MaxGuardMembers - 1, MaxGuardMembers} {
		if _, err := EncodeStep(boundsMemberStep("guard", n)); err != nil {
			t.Errorf("EncodeStep with %d guard-only members: %v", n, err)
		}
	}
	_, err = EncodeStep(boundsMemberStep("guard", MaxGuardMembers+1))
	boundsWantRefusal(t, err, "LIMIT")
}

func TestS7IDsPerEntryBoundary(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxIDsPerEntry - 1, MaxIDsPerEntry} {
		if _, err := EncodeStep(boundsMemberStep("create", n)); err != nil {
			t.Errorf("EncodeStep with %d IDs in one entry: %v", n, err)
		}
	}
	over := boundsMemberStep("create", MaxIDsPerEntry+1)
	if len(over.Entries) != 2 || len(over.Entries[0].IDs) != MaxIDsPerEntry || len(over.Entries[1].IDs) != 1 {
		t.Fatalf("helper did not split IDs at per-entry boundary: %#v", over.Entries)
	}
	over.Entries = over.Entries[:1]
	over.Entries[0].IDs = boundsIDs(MaxIDsPerEntry+1, "id-")
	over.Entries[0].Scores = boundsStrings(MaxIDsPerEntry+1, "1")
	over.Entries[0].About = append([]string(nil), over.Entries[0].IDs...)
	boundsWantRefusal(t, encodeBoundsStep(over), "LIMIT")
}

func TestS7RowCountBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxRowsPerStep - 1, MaxRowsPerStep} {
		if _, err := EncodeStep(boundsRowsStep(1, n, false)); err != nil {
			t.Errorf("EncodeStep with %d normal rows: %v", n, err)
		}
	}
	_, err := EncodeStep(boundsRowsStep(1, MaxRowsPerStep+1, false))
	boundsWantRefusal(t, err, "LIMIT")

	for _, n := range []int{MaxRowsWithAdvance - 1, MaxRowsWithAdvance} {
		if _, err := EncodeStep(boundsRowsStep(1, n, true)); err != nil {
			t.Errorf("EncodeStep with advance and %d rows: %v", n, err)
		}
	}
	_, err = EncodeStep(boundsRowsStep(1, MaxRowsWithAdvance+1, true))
	boundsWantRefusal(t, err, "LIMIT")
}

func TestS7FieldCountBoundaries(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"set", "each", "unset", "before_fields"} {
		for _, n := range []int{MaxFieldsPerMember - 1, MaxFieldsPerMember} {
			if err := boundsEncodeMoveWithFields(kind, n); err != nil {
				t.Errorf("EncodeStep %s with %d fields: %v", kind, n, err)
			}
		}
		// Each is a valid object whose cardinality exceeds a hard field
		// bound; it is not malformed input.
		boundsWantRefusal(t, boundsEncodeMoveWithFields(kind, MaxFieldsPerMember+1), "LIMIT")
	}
	for _, n := range []int{MaxFieldsPerMember - 1, MaxFieldsPerMember} {
		if _, err := EncodeReadPlan(boundsReadFieldsPlan(n)); err != nil {
			t.Errorf("EncodeReadPlan with %d projected fields: %v", n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsReadPlan(boundsReadFieldsPlan(MaxFieldsPerMember+1)), "LIMIT")

	// These array counts are hard bounds before any normalization or dedup.
	for _, kind := range []string{"unset", "before_fields"} {
		for _, n := range []int{MaxFieldsPerMember - 1, MaxFieldsPerMember} {
			if err := boundsEncodeRepeatedMoveFields(kind, n); err != nil {
				t.Errorf("EncodeStep %s with %d repeated names: %v", kind, n, err)
			}
		}
		boundsWantRefusal(t, boundsEncodeRepeatedMoveFields(kind, MaxFieldsPerMember+1), "LIMIT")
	}
}

func TestS7StoredFieldCountBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		initial int
		wantOK  bool
	}{
		{initial: MaxFieldsPerMember - 2, wantOK: true},
		{initial: MaxFieldsPerMember - 1, wantOK: true},
		{initial: MaxFieldsPerMember, wantOK: false},
	} {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		create := Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"}, Set: boundsFieldMap(tc.initial),
		}}}
		if _, err := m.Step(context.Background(), create); err != nil {
			t.Fatalf("create member with %d fields: %v", tc.initial, err)
		}
		update := Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: []string{"p"}, Set: map[string]string{"boundary": "v"},
		}}}
		if _, err := EncodeStep(update); err != nil {
			t.Fatalf("static update request at initial field count %d: %v", tc.initial, err)
		}
		before, err := m.Snapshot(s7Namespace)
		if err != nil {
			t.Fatalf("snapshot at initial field count %d: %v", tc.initial, err)
		}
		_, err = m.Step(context.Background(), update)
		if tc.wantOK {
			if err != nil {
				t.Fatalf("update from %d stored fields to %d: %v", tc.initial, tc.initial+1, err)
			}
			continue
		}
		boundsWantRefusal(t, err, "LIMIT")
		after, err := m.Snapshot(s7Namespace)
		if err != nil {
			t.Fatalf("snapshot after over-limit update: %v", err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatal("stored-field limit refusal changed state")
		}
	}
}

func TestS7FieldValueAndResultByteBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxFieldValueBytes - 1, MaxFieldValueBytes} {
		if _, err := EncodeStep(boundsValueStep(n)); err != nil {
			t.Errorf("EncodeStep with %d-byte field value: %v", n, err)
		}
	}
	// A field value over its documented byte bound is LIMIT, just like other
	// hard input caps.
	boundsWantRefusal(t, encodeBoundsStep(boundsValueStep(MaxFieldValueBytes+1)), "LIMIT")

	for _, n := range []int{MaxResultBytes - 1, MaxResultBytes} {
		if _, err := EncodeStep(Step{Epoch: "0", Space: "bounds:", Result: strings.Repeat("r", n), Entries: []Entry{}}); err != nil {
			t.Errorf("EncodeStep with %d-byte result: %v", n, err)
		}
	}
	resultOver := Step{Epoch: "0", Space: "bounds:", Result: strings.Repeat("r", MaxResultBytes+1), Entries: []Entry{}}
	boundsWantRefusal(t, encodeBoundsStep(resultOver), "LIMIT")
}

func TestS7NoteAndAboutCountBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxNotes - 1, MaxNotes} {
		if _, err := EncodeStep(boundsNotesStep(n, 1)); err != nil {
			t.Errorf("EncodeStep with %d notes: %v", n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsStep(boundsNotesStep(MaxNotes+1, 1)), "LIMIT")

	for _, n := range []int{MaxAboutBeforeDedup - 1, MaxAboutBeforeDedup} {
		if _, err := EncodeStep(boundsRepeatedAboutNotesStep(2, n)); err != nil {
			t.Errorf("EncodeStep with %d about IDs: %v", n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsStep(boundsRepeatedAboutNotesStep(3, MaxAboutBeforeDedup+1)), "LIMIT")

	for _, n := range []int{MaxAboutBeforeDedup - MaxMemberCandidates - 1, MaxAboutBeforeDedup - MaxMemberCandidates} {
		if _, err := EncodeStep(boundsMemberAndNoteAboutStep(MaxMemberCandidates, n)); err != nil {
			t.Errorf("EncodeStep with %d combined member/note about IDs: %v", MaxMemberCandidates+n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsStep(boundsMemberAndNoteAboutStep(MaxMemberCandidates, MaxAboutBeforeDedup-MaxMemberCandidates+1)), "LIMIT")
}

func TestS7DistinctAboutsPerNoteBoundary(t *testing.T) {
	t.Parallel()

	const maxDistinctAboutsPerNote = 2000
	for _, n := range []int{maxDistinctAboutsPerNote - 1, maxDistinctAboutsPerNote} {
		if _, err := EncodeStep(boundsNotesStep(1, n)); err != nil {
			t.Errorf("EncodeStep with %d distinct about IDs in one note: %v", n, err)
		}
	}
	boundsWantRefusal(t, encodeBoundsStep(boundsNotesStep(1, maxDistinctAboutsPerNote+1)), "LIMIT")
}

func TestS7NotesRequireStableOperationIdentity(t *testing.T) {
	t.Parallel()

	step := Step{Epoch: "0", Space: "bounds:", Entries: []Entry{}, Notes: []Note{{
		Line: NoteLine{Kind: "note", Meta: []byte(`{}`)}, About: []string{"primary"},
	}}}
	boundsWantRefusal(t, encodeBoundsStep(step), "REQUEST")
}

func TestS7EncodedWriteRequestByteBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{MaxWriteRequestBytes - 1, MaxWriteRequestBytes} {
		step := boundsWriteRequestBytesStep(t, n)
		raw, err := EncodeStep(step)
		if err != nil {
			t.Errorf("EncodeStep at %d serialized bytes: %v", n, err)
			continue
		}
		if len(raw) != n {
			t.Errorf("EncodeStep serialized %d bytes, want exact boundary %d", len(raw), n)
		}
	}
	step := boundsWriteRequestBytesStep(t, MaxWriteRequestBytes+1)
	boundsWantRefusal(t, encodeBoundsStep(step), "LIMIT")
}

func TestS7ExpandedSharedFieldBudgetRefusesBeforeMutation(t *testing.T) {
	t.Parallel()

	m := s7NewMem(t)
	s7SeedRow(t, m, "r", "0")
	step := Step{
		Epoch: "0", Space: s7Namespace,
		Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c",
			IDs:    boundsIDs(2000, "member-000000000000000000000000"),
			Scores: boundsStrings(2000, "1"),
			About:  boundsIDs(2000, "primary-00000000000000000000000"),
			Set:    map[string]string{"payload": strings.Repeat("x", 4096)},
		}},
	}
	encoded, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("compact 4 KiB shared-field request must pass wire bounds: %v", err)
	}
	if len(encoded) >= MaxWriteRequestBytes {
		t.Fatalf("request unexpectedly approaches its wire cap: %d bytes", len(encoded))
	}
	before, err := m.Snapshot(s7Namespace)
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	_, err = m.Step(context.Background(), step)
	refusal := boundsWantRefusal(t, err, "LIMIT")
	if refusal.Detail.Budget != "planned_argv_bytes" {
		t.Fatalf("refusal budget=%q, want planned_argv_bytes", refusal.Detail.Budget)
	}
	after, err := m.Snapshot(s7Namespace)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("expanded-command refusal changed state\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestS7ExactExpandedArgvBoundaryRefusesBeforeMutation(t *testing.T) {
	t.Parallel()

	// This exercises Mem's Go-side plan accounting only. Lua composition parity
	// remains a separate functional gate.
	ids := make([]string, MaxMemberCandidates)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%04d", i)
	}

	for _, tc := range []struct {
		name         string
		lastBytes    int
		wantAccepted bool
		wantBytes    int
	}{
		{name: "limit_minus_one", lastBytes: 4690, wantAccepted: true, wantBytes: MaxPlannedArgvBytes - 1},
		{name: "limit", lastBytes: 4691, wantAccepted: true, wantBytes: MaxPlannedArgvBytes},
		{name: "limit_plus_one", lastBytes: 4692},
	} {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		seed := Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c", IDs: ids, Scores: boundsStrings(len(ids), "1"),
		}}}
		if _, err := m.Step(context.Background(), seed); err != nil {
			t.Fatalf("%s seed %d candidate members: %v", tc.name, len(ids), err)
		}
		step := boundsExpandedFieldStep(ids, 4083, tc.lastBytes)
		if _, err := EncodeStep(step); err != nil {
			t.Fatalf("%s compact write request: %v", tc.name, err)
		}
		before, err := m.Snapshot(s7Namespace)
		if err != nil {
			t.Fatalf("%s snapshot before: %v", tc.name, err)
		}
		reply, err := m.Step(context.Background(), step)
		if !tc.wantAccepted {
			refusal := boundsWantRefusal(t, err, "LIMIT")
			if refusal.Detail.Budget != "planned_argv_bytes" || refusal.Detail.Actual == nil || *refusal.Detail.Actual != MaxPlannedArgvBytes+1 {
				t.Fatalf("%s refusal detail=%#v, want exact planned_argv_bytes overflow", tc.name, refusal.Detail)
			}
			after, snapErr := m.Snapshot(s7Namespace)
			if snapErr != nil {
				t.Fatalf("%s snapshot after: %v", tc.name, snapErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s expanded-command refusal changed state", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s exact argv boundary: %v", tc.name, err)
		}
		var counters struct {
			PlannedArgvBytes int `json:"planned_argv_bytes"`
		}
		if err := json.Unmarshal(reply.Counters, &counters); err != nil {
			t.Fatalf("%s decode counters: %v", tc.name, err)
		}
		if counters.PlannedArgvBytes != tc.wantBytes {
			t.Fatalf("%s planned argv bytes=%d, want %d", tc.name, counters.PlannedArgvBytes, tc.wantBytes)
		}
	}
}

func TestS7EffectiveFieldUnionBoundary(t *testing.T) {
	t.Parallel()

	m := s7NewMem(t)
	s7SeedRow(t, m, "r", "0")
	accepted := Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
		Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"},
		Set: boundsFieldMap(MaxFieldsPerMember - 1), Each: []map[string]string{{"extra": "x"}},
	}}}
	if _, err := EncodeStep(accepted); err != nil {
		t.Fatalf("effective union at %d fields should encode: %v", MaxFieldsPerMember, err)
	}
	if _, err := m.Step(context.Background(), accepted); err != nil {
		t.Fatalf("effective union at %d fields should commit: %v", MaxFieldsPerMember, err)
	}

	over := Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
		Kind: "create", Table: "work", To: "r:c", IDs: []string{"q"}, Scores: []string{"1"}, About: []string{"q"},
		Set: boundsFieldMap(MaxFieldsPerMember), Each: []map[string]string{{"extra": "x"}},
	}}}
	if _, err := EncodeStep(over); err == nil {
		t.Fatalf("EncodeStep accepted effective field union above %d", MaxFieldsPerMember)
	} else {
		boundsWantRefusal(t, err, "LIMIT")
	}
	before, err := m.Snapshot(s7Namespace)
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	_, err = m.Step(context.Background(), over)
	refusal := boundsWantRefusal(t, err, "LIMIT")
	if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 0 {
		t.Fatalf("effective-field refusal has no entry index: %#v", refusal.Detail)
	}
	after, err := m.Snapshot(s7Namespace)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("effective-field refusal changed state\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestS7MaximumLegalResultFitsReceipt(t *testing.T) {
	t.Parallel()

	m := s7NewMem(t)
	s7SeedRow(t, m, "r", "0")
	op, intent := "full-result", `{"verb":"create","part":"0"}`
	step := Step{
		Epoch: "0", Space: s7Namespace, Op: &op, Intent: &intent,
		Result:  strings.Repeat("r", MaxResultBytes),
		Entries: []Entry{{Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"}}},
	}
	first, err := m.Step(context.Background(), step)
	if err != nil {
		t.Fatalf("maximum legal caller result did not fit its receipt: %v", err)
	}
	if first.Result != step.Result || first.Replay {
		t.Fatalf("fresh maximum-result reply = replay:%t result_bytes:%d", first.Replay, len(first.Result))
	}

	replayed, err := m.Step(context.Background(), step)
	if err != nil {
		t.Fatalf("retry with matching maximum-result receipt: %v", err)
	}
	if !replayed.Replay || replayed.Result != step.Result || replayed.Changed != first.Changed {
		t.Fatalf("maximum-result replay = %#v, first reply %#v", replayed, first)
	}
}

func boundsRowsStep(entries, rows int, advance bool) Step {
	step := Step{Epoch: "0", Space: "bounds:", Entries: make([]Entry, 0, entries+1)}
	if advance {
		op, intent := "bounds-row-count-advance", "exercise row-count boundary with an advance"
		step.Op, step.Intent = &op, &intent
		step.Entries = append(step.Entries, Entry{Kind: "advance", AdvanceFrom: "0"})
	}
	for i := 0; i < entries; i++ {
		add := []string(nil)
		if i == 0 {
			add = boundsIDs(rows, "row-")
		} else {
			add = []string{"same-row"}
		}
		step.Entries = append(step.Entries, Entry{Kind: "rows", Table: "work", Add: add})
	}
	return step
}

func boundsTableCountStep(tables int) Step {
	step := Step{Epoch: "0", Space: "bounds:", Entries: make([]Entry, tables)}
	for i := range step.Entries {
		step.Entries[i] = Entry{Kind: "rows", Table: fmt.Sprintf("table-%d", i), Add: []string{"row"}}
	}
	return step
}

func boundsTableDefinition(index, columns int) TableDefinition {
	return TableDefinition{
		Columns: boundsIDs(columns, "c"), MemberPrefix: fmt.Sprintf("bounds:member:%d:", index),
		EpochKey: "bounds:epoch", EpochField: "current",
	}
}

func boundsIdentifierStep(kind string, sizeBytes int) Step {
	name := boundsUTF8Bytes(sizeBytes)
	step := Step{Epoch: "0", Space: "bounds:", Entries: []Entry{}}
	switch kind {
	case "stored_id":
		step.Entries = []Entry{{Kind: "create", Table: "work", To: "r:c", IDs: []string{name}, Scores: []string{"1"}, About: []string{name}}}
	case "row":
		step.Entries = []Entry{{Kind: "rows", Table: "work", Add: []string{name}}}
	case "column":
		step.Entries = []Entry{{Kind: "create", Table: "work", To: "r:" + strings.Repeat("c", sizeBytes), IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"}}}
	case "field":
		step.Entries = []Entry{{Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"}, Set: map[string]string{name: "v"}}}
	case "op":
		intent := "stable-intent"
		step.Op, step.Intent = &name, &intent
	case "space":
		step.Space = name
	default:
		panic("unknown identifier kind: " + kind)
	}
	return step
}

func boundsUTF8Bytes(sizeBytes int) string {
	value := strings.Repeat("é", sizeBytes/2)
	if sizeBytes%2 != 0 {
		value += "x"
	}
	return value
}

func boundsMemberStep(kind string, count int) Step {
	step := Step{Epoch: "0", Space: "bounds:"}
	ids := boundsIDs(count, "id-")
	for start := 0; start < count; start += MaxIDsPerEntry {
		end := start + MaxIDsPerEntry
		if end > count {
			end = count
		}
		chunk := ids[start:end]
		entry := Entry{Kind: kind, Table: "work", IDs: chunk}
		if kind == "guard" {
			entry.From = "row:col"
		} else {
			entry.To = "row:col"
			entry.Scores = boundsStrings(len(chunk), "1")
			entry.About = append([]string(nil), chunk...)
		}
		step.Entries = append(step.Entries, entry)
	}
	return step
}

func boundsEncodeMoveWithFields(kind string, count int) error {
	e := Entry{Kind: "move", Table: "work", From: "row:col", IDs: []string{"p"}, About: []string{"p"}}
	switch kind {
	case "set":
		e.Set = boundsFieldMap(count)
	case "each":
		e.Each = []map[string]string{boundsFieldMap(count)}
	case "unset":
		e.Unset = boundsIDs(count, "u-")
	case "before_fields":
		e.BeforeFields = boundsIDs(count, "b-")
	default:
		return fmt.Errorf("unknown field kind %q", kind)
	}
	_, err := EncodeStep(Step{Epoch: "0", Space: "bounds:", Entries: []Entry{e}})
	return err
}

func boundsEncodeRepeatedMoveFields(kind string, count int) error {
	e := Entry{Kind: "move", Table: "work", From: "row:col", IDs: []string{"p"}, About: []string{"p"}}
	fields := boundsStrings(count, "shared")
	switch kind {
	case "unset":
		e.Unset = fields
	case "before_fields":
		e.BeforeFields = fields
	default:
		return fmt.Errorf("unknown field kind %q", kind)
	}
	return encodeBoundsStep(Step{Epoch: "0", Space: "bounds:", Entries: []Entry{e}})
}

func boundsValueStep(size int) Step {
	return Step{Epoch: "0", Space: "bounds:", Entries: []Entry{{
		Kind: "create", Table: "work", To: "row:col", IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"},
		Set: map[string]string{"payload": strings.Repeat("v", size)},
	}}}
}

func boundsExpandedFieldStep(ids []string, sharedBytes, lastBytes int) Step {
	first := append([]string(nil), ids[:len(ids)-1]...)
	last := append([]string(nil), ids[len(ids)-1:]...)
	return Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{
		{Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: first, Set: map[string]string{"payload": strings.Repeat("x", sharedBytes)}},
		{Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: last, Set: map[string]string{"payload": strings.Repeat("x", lastBytes)}},
	}}
}

func boundsNotesStep(notes, abouts int) Step {
	op, intent := "bounds-notes", "bounds-notes-intent"
	step := Step{Epoch: "0", Space: "bounds:", Op: &op, Intent: &intent, Entries: []Entry{}, Notes: make([]Note, notes)}
	for i := range step.Notes {
		step.Notes[i] = Note{Line: NoteLine{Kind: "note", Meta: []byte(`{}`)}}
	}
	left := abouts
	for i := range step.Notes {
		remainingSlots := len(step.Notes) - i
		count := left / remainingSlots
		if left%remainingSlots != 0 {
			count++
		}
		step.Notes[i].About = boundsIDs(count, fmt.Sprintf("n%d-", i))
		left -= count
	}
	return step
}

func boundsRepeatedAboutNotesStep(notes, abouts int) Step {
	step := boundsNotesStep(notes, abouts)
	for i := range step.Notes {
		for j := range step.Notes[i].About {
			step.Notes[i].About[j] = "same-primary"
		}
	}
	return step
}

func boundsMemberAndNoteAboutStep(members, noteAbouts int) Step {
	step := boundsMemberStep("create", members)
	op, intent := "bounds-notes", "bounds-notes-intent"
	step.Op, step.Intent = &op, &intent
	step.Notes = []Note{{
		Line:  NoteLine{Kind: "note", Meta: []byte(`{}`)},
		About: boundsIDs(noteAbouts, "note-primary-"),
	}}
	return step
}

func boundsWriteRequestBytesStep(t *testing.T, target int) Step {
	t.Helper()
	op, intent := "bounds-request-size", "bounds-request-size-intent"
	step := Step{Epoch: "0", Space: "bounds:", Op: &op, Intent: &intent, Entries: []Entry{}, Notes: []Note{{
		Line: NoteLine{Kind: "note", Meta: []byte(`{"padding":""}`)}, About: []string{"primary"},
	}}}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("encode base request for size calculation: %v", err)
	}
	padding := target - len(raw)
	if padding < 0 {
		t.Fatalf("target request size %d is below base size %d", target, len(raw))
	}
	step.Notes[0].Line.Meta = []byte(`{"padding":"` + strings.Repeat("x", padding) + `"}`)
	return step
}

func boundsReadFieldsPlan(count int) ReadPlan {
	return ReadPlan{Epoch: "0", Space: "bounds:", Queries: []ReadQuery{{
		Kind: "ids", Table: "work", IDs: []string{"p"}, Fields: boundsIDs(count, "f-"),
	}}}
}

func boundsIDs(count int, prefix string) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s%06d", prefix, i)
	}
	return ids
}

func boundsStrings(count int, value string) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = value
	}
	return values
}

func boundsFieldMap(count int) map[string]string {
	fields := make(map[string]string, count)
	for i := 0; i < count; i++ {
		fields[fmt.Sprintf("f%03d", i)] = "v"
	}
	return fields
}

func encodeBoundsStep(step Step) error {
	_, err := EncodeStep(step)
	return err
}

func encodeBoundsReadPlan(plan ReadPlan) error {
	_, err := EncodeReadPlan(plan)
	return err
}

func boundsWantRefusal(t *testing.T, err error, code string) *Refusal {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != code || refusal.Status != "refused" {
		t.Fatalf("refusal = %#v, want status refused code %s", err, code)
	}
	return refusal
}
